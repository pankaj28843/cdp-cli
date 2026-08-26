package bing

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
)

func TestRunSessionSendsAudioAndReturnsSpeechPhrase(t *testing.T) {
	fake := newFakeSocket(augloop.MessageText, []byte("Content-Type:application/json; charset=utf-8\r\nPath:speech.phrase\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\"hello from Bing\"}"))
	pcm := bytes.Repeat([]byte{0x01, 0x02}, 1_600)
	attempt, err := runSession(context.Background(), pcm, TranscribeConfig{
		Dial: func(_ context.Context, rawURL, _ string) (Socket, error) {
			if !bytes.Contains([]byte(rawURL), []byte("Ocp-Apim-Subscription-Key=key")) {
				t.Fatalf("raw URL does not contain the public routing key")
			}
			return fake, nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.transcript != "hello from Bing" {
		t.Fatalf("transcript = %q", attempt.transcript)
	}
	if attempt.frames != 3 {
		t.Fatalf("frames = %d, want initial WAV + PCM + end", attempt.frames)
	}
	writes := fake.writesSnapshot()
	if len(writes) != 5 {
		t.Fatalf("write count = %d, want config, context, initial WAV, PCM, end", len(writes))
	}
	if writes[0].messageType != augloop.MessageText || writes[1].messageType != augloop.MessageText {
		t.Fatalf("initial writes = %#v", writes[:2])
	}
	if writes[2].messageType != augloop.MessageBinary || writes[2].payload[0] != 0 || writes[2].payload[1] != '~' {
		t.Fatalf("initial audio frame prefix = %#v", writes[2].payload[:2])
	}
	if writes[3].messageType != augloop.MessageBinary || writes[3].payload[0] != 0 || writes[3].payload[1] != 'c' {
		t.Fatalf("PCM audio frame prefix = %#v", writes[3].payload[:2])
	}
}

type fakeSocket struct {
	mu     sync.Mutex
	reads  []fakeRead
	writes []fakeWrite
}

type fakeRead struct {
	messageType augloop.MessageType
	payload     []byte
}

type fakeWrite struct {
	messageType augloop.MessageType
	payload     []byte
}

func newFakeSocket(messageType augloop.MessageType, payload []byte) *fakeSocket {
	return &fakeSocket{reads: []fakeRead{{messageType: messageType, payload: payload}}}
}

func (f *fakeSocket) Write(_ context.Context, messageType augloop.MessageType, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, fakeWrite{messageType: messageType, payload: append([]byte(nil), payload...)})
	return nil
}

func (f *fakeSocket) Read(ctx context.Context) (augloop.MessageType, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reads) > 0 {
		read := f.reads[0]
		f.reads = f.reads[1:]
		return read.messageType, read.payload, nil
	}
	return 0, nil, ctx.Err()
}

func (f *fakeSocket) Close(augloop.StatusCode, string) error { return nil }

func (f *fakeSocket) writesSnapshot() []fakeWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeWrite(nil), f.writes...)
}
