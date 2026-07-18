package cdp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

type ioStreamFakeClient struct {
	chunks   []map[string]any
	methods  []string
	readErr  error
	closeErr error
}

func (f *ioStreamFakeClient) Call(context.Context, string, any, any) error { return nil }

func (f *ioStreamFakeClient) CallSession(_ context.Context, _ string, method string, _ any, result any) error {
	f.methods = append(f.methods, method)
	switch method {
	case "IO.read":
		if f.readErr != nil {
			return f.readErr
		}
		chunk := f.chunks[0]
		f.chunks = f.chunks[1:]
		encoded, _ := json.Marshal(chunk)
		return json.Unmarshal(encoded, result)
	case "IO.close":
		return f.closeErr
	default:
		return errors.New("unexpected method")
	}
}

func TestReadIOStreamSequentialAndCloses(t *testing.T) {
	fake := &ioStreamFakeClient{chunks: []map[string]any{
		{"data": "hello ", "eof": false},
		{"data": base64.StdEncoding.EncodeToString([]byte("world")), "base64Encoded": true, "eof": true},
	}}
	var dst bytes.Buffer
	result, err := ReadIOStream(context.Background(), fake, "session-1", "stream-1", 64, &dst)
	if err != nil {
		t.Fatalf("ReadIOStream() error = %v", err)
	}
	if dst.String() != "hello world" || result.BytesWritten != 11 || !result.EOF || result.Truncated || !result.CloseAttempted || !result.Closed {
		t.Fatalf("ReadIOStream() = result=%+v data=%q", result, dst.String())
	}
	if want := []string{"IO.read", "IO.read", "IO.close"}; !reflect.DeepEqual(fake.methods, want) {
		t.Fatalf("methods = %v, want %v", fake.methods, want)
	}
}

func TestReadIOStreamTruncatesAndCloses(t *testing.T) {
	fake := &ioStreamFakeClient{chunks: []map[string]any{{"data": "0123456789", "eof": false}}}
	var dst bytes.Buffer
	result, err := ReadIOStream(context.Background(), fake, "session-1", "stream-1", 5, &dst)
	if err != nil {
		t.Fatalf("ReadIOStream() error = %v", err)
	}
	if dst.String() != "01234" || !result.Truncated || result.EOF || !result.Closed {
		t.Fatalf("ReadIOStream() = result=%+v data=%q", result, dst.String())
	}
	if want := []string{"IO.read", "IO.close"}; !reflect.DeepEqual(fake.methods, want) {
		t.Fatalf("methods = %v, want %v", fake.methods, want)
	}
}

func TestReadIOStreamClosesAfterCanceledRead(t *testing.T) {
	fake := &ioStreamFakeClient{readErr: context.Canceled}
	var dst bytes.Buffer
	result, err := ReadIOStream(context.Background(), fake, "session-1", "stream-1", 5, &dst)
	if err == nil || !result.CloseAttempted || !result.Closed {
		t.Fatalf("ReadIOStream() = result=%+v err=%v, want read error with closed handle", result, err)
	}
	if want := []string{"IO.read", "IO.close"}; !reflect.DeepEqual(fake.methods, want) {
		t.Fatalf("methods = %v, want %v", fake.methods, want)
	}
}
