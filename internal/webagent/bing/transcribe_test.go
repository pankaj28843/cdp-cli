package bing

import (
	"bytes"
	"context"
	"net/url"
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

func TestRunSessionReplaysOnlyEachAudioWithFreshRequestData(t *testing.T) {
	firstPCM := bytes.Repeat([]byte{0x11, 0x12}, 64)
	secondPCM := bytes.Repeat([]byte{0x21, 0x22}, 64)
	run := func(pcm []byte, transcript string) (*fakeSocket, *url.URL) {
		fake := newFakeSocket(augloop.MessageText, []byte("Content-Type:application/json; charset=utf-8\r\nPath:speech.phrase\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\""+transcript+"\"}"))
		var rawURL string
		attempt, err := runSession(context.Background(), pcm, TranscribeConfig{
			Dial: func(_ context.Context, dialURL, _ string) (Socket, error) {
				rawURL = dialURL
				return fake, nil
			},
			Sleep: func(context.Context, time.Duration) error { return nil },
		})
		if err != nil || attempt.transcript != transcript {
			t.Fatalf("Bing session = %+v, %v", attempt, err)
		}
		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		return fake, parsed
	}

	firstSocket, firstParsed := run(firstPCM, "first")
	secondSocket, secondParsed := run(secondPCM, "second")
	firstRequestID := firstParsed.Query().Get("uqurequestid")
	secondRequestID := secondParsed.Query().Get("uqurequestid")
	if firstRequestID == "" || secondRequestID == "" || firstRequestID == secondRequestID ||
		firstParsed.Query().Get("X-ConnectionId") == secondParsed.Query().Get("X-ConnectionId") {
		t.Fatal("sequential Bing requests reused dynamic request data")
	}

	for requestIndex, proof := range []struct {
		writes    []fakeWrite
		pcm       []byte
		requestID string
	}{
		{writes: firstSocket.writesSnapshot(), pcm: firstPCM, requestID: firstRequestID},
		{writes: secondSocket.writesSnapshot(), pcm: secondPCM, requestID: secondRequestID},
	} {
		if len(proof.writes) != 5 {
			t.Fatalf("request %d write count = %d, want 5", requestIndex+1, len(proof.writes))
		}
		audio := proof.writes[3].payload
		headerEnd := bytes.LastIndex(audio, []byte("\r\n"))
		if headerEnd < 0 || !bytes.Equal(audio[headerEnd+2:], proof.pcm) {
			t.Fatalf("request %d did not replay only its own audio", requestIndex+1)
		}
		for _, write := range proof.writes {
			if !bytes.Contains(write.payload, []byte(proof.requestID)) {
				t.Fatalf("request %d write omitted its fresh request ID", requestIndex+1)
			}
		}
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
