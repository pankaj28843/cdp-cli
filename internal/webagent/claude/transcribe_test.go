package claude

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
)

func TestTranscribeReplaysClaudeDictationWebSocket(t *testing.T) {
	store := testDirectTranscriptionStore(t)
	filePath := testClaudeAudioFile(t, []byte("synthetic-webm"))
	readGate := make(chan struct{})
	socket := &claudeTestSocket{reads: [][]byte{
		[]byte(`{"type":"TranscriptText","data":"direct claude transcript"}`),
		[]byte(`{"type":"TranscriptEndpoint"}`),
	}, readGate: readGate, onWrite: func(messageType augloop.MessageType, payload []byte) {
		if messageType == augloop.MessageText && string(payload) == `{"type":"CloseStream"}` {
			close(readGate)
		}
	}}
	dials := 0
	result := Transcribe(context.Background(), TranscribeConfig{
		Store: store,
		Dial: func(_ context.Context, rawURL string, header http.Header) (augloop.Socket, int, error) {
			dials++
			parsed, err := url.Parse(rawURL)
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			if parsed.Path != claudeVoicePath || query.Get("encoding") != "linear16" ||
				query.Get("sample_rate") != "16000" || query.Get("channels") != "1" ||
				query.Get("endpointing_ms") != "300" || query.Get("utterance_end_ms") != "1000" ||
				query.Get("language") != "en-US" || query.Get("use_conversation_engine") != "true" ||
				query.Get("stt_provider") != "deepgram-nova3" || query.Get("client_platform") != "web_claude_ai" ||
				query.Get("organization_uuid") != "org-1" {
				t.Fatalf("Claude voice URL = %s", rawURL)
			}
			if header.Get("Origin") != Origin || header.Get("User-Agent") != "Browser/Test" ||
				!strings.Contains(header.Get("Cookie"), "sessionKey=private-session-cookie") {
				t.Fatalf("Claude voice headers omitted observed auth: %#v", header)
			}
			return socket, http.StatusSwitchingProtocols, nil
		},
		DecodePCM: func(context.Context, string) ([]byte, error) {
			return bytes.Repeat([]byte{0x01, 0x02}, 2_000), nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	}, filePath, 125)
	if !result.OK || dials != 1 {
		t.Fatalf("result=%+v dials=%d", result, dials)
	}
	data := result.Data.(TranscriptionData)
	if data.Transcript != "direct claude transcript" || data.Transport != "direct_websocket" ||
		data.Attempts != 1 || data.Frames != 2 || data.PCMBytes != 4_000 {
		t.Fatalf("data = %+v", data)
	}
	if len(socket.writes) != 3 || socket.writes[0].messageType != augloop.MessageBinary ||
		socket.writes[1].messageType != augloop.MessageBinary ||
		socket.writes[2].messageType != augloop.MessageText ||
		string(socket.writes[2].payload) != `{"type":"CloseStream"}` {
		t.Fatalf("writes = %+v", socket.writes)
	}
}

func TestTranscribeReplaysEachClaudeAudioInputIndependently(t *testing.T) {
	store := testDirectTranscriptionStore(t)
	inputs := [][]byte{[]byte("first-webm"), []byte("second-webm")}
	pcmByInput := map[string][]byte{
		"first-webm":  {0x01, 0x02, 0x03, 0x04},
		"second-webm": {0x11, 0x12, 0x13, 0x14, 0x15, 0x16},
	}
	sockets := make([]*claudeTestSocket, len(inputs))
	for index := range sockets {
		readGate := make(chan struct{})
		sockets[index] = &claudeTestSocket{
			reads: [][]byte{
				[]byte(`{"type":"TranscriptText","data":"direct"}`),
				[]byte(`{"type":"TranscriptEndpoint"}`),
			},
			readGate: readGate,
			onWrite: func(messageType augloop.MessageType, payload []byte) {
				if messageType == augloop.MessageText && string(payload) == `{"type":"CloseStream"}` {
					close(readGate)
				}
			},
		}
	}
	dials := 0
	refreshes := 0
	config := TranscribeConfig{
		Store: store,
		Dial: func(context.Context, string, http.Header) (augloop.Socket, int, error) {
			socket := sockets[dials]
			dials++
			return socket, http.StatusSwitchingProtocols, nil
		},
		DecodePCM: func(_ context.Context, path string) ([]byte, error) {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			return bytes.Clone(pcmByInput[string(raw)]), nil
		},
		RefreshAuth: func(context.Context, string) error {
			refreshes++
			return nil
		},
	}

	for _, input := range inputs {
		result := Transcribe(context.Background(), config, testClaudeAudioFile(t, input), 100)
		if !result.OK {
			t.Fatalf("Transcribe(%q) = %+v", input, result)
		}
	}
	if dials != 2 || refreshes != 0 {
		t.Fatalf("dials=%d refreshes=%d, want two fresh sockets and cached auth", dials, refreshes)
	}
	for index, socket := range sockets {
		if len(socket.writes) != 2 || socket.writes[0].messageType != augloop.MessageBinary ||
			!bytes.Equal(socket.writes[0].payload, pcmByInput[string(inputs[index])]) ||
			socket.writes[1].messageType != augloop.MessageText {
			t.Fatalf("request %d writes = %+v", index+1, socket.writes)
		}
	}
}

func TestTranscribeRefreshesExpiredTemplateOnce(t *testing.T) {
	store := testDirectTranscriptionStore(t)
	expired := validAuthTemplate(time.Now().UTC().Add(-2 * DefaultAuthTTL))
	if err := store.Save(context.Background(), expired); err != nil {
		t.Fatal(err)
	}
	filePath := testClaudeAudioFile(t, []byte("synthetic-webm"))
	refreshes := 0
	dials := 0
	result := Transcribe(context.Background(), TranscribeConfig{
		Store: store,
		Dial: func(context.Context, string, http.Header) (augloop.Socket, int, error) {
			dials++
			return &claudeTestSocket{reads: [][]byte{
				[]byte(`{"type":"TranscriptText","data":"after stale refresh"}`),
				[]byte(`{"type":"TranscriptEndpoint"}`),
			}}, http.StatusSwitchingProtocols, nil
		},
		DecodePCM: func(context.Context, string) ([]byte, error) { return []byte{0, 0}, nil },
		RefreshAuth: func(_ context.Context, rejectedGeneration string) error {
			refreshes++
			if rejectedGeneration != expired.CapturedAt {
				t.Fatalf("rejected generation = %q", rejectedGeneration)
			}
			return store.Save(context.Background(), validAuthTemplate(time.Now().UTC()))
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	}, filePath, 100)
	if !result.OK || refreshes != 1 || dials != 1 {
		t.Fatalf("result=%+v refreshes=%d dials=%d", result, refreshes, dials)
	}
	if data := result.Data.(TranscriptionData); data.Attempts != 2 || data.Transcript != "after stale refresh" {
		t.Fatalf("data = %+v", data)
	}
}

func TestTranscribeStopsAfterSecondAuthRejection(t *testing.T) {
	store := testDirectTranscriptionStore(t)
	filePath := testClaudeAudioFile(t, []byte("synthetic-webm"))
	dials := 0
	refreshes := 0
	result := Transcribe(context.Background(), TranscribeConfig{
		Store: store,
		Dial: func(context.Context, string, http.Header) (augloop.Socket, int, error) {
			dials++
			return nil, http.StatusUnauthorized, errors.New("expired")
		},
		DecodePCM: func(context.Context, string) ([]byte, error) { return []byte{0, 0}, nil },
		RefreshAuth: func(context.Context, string) error {
			refreshes++
			return nil
		},
	}, filePath, 100)
	if result.OK || dials != 2 || refreshes != 1 || result.Error == nil || result.Error.RetrySafe {
		t.Fatalf("result=%+v dials=%d refreshes=%d", result, dials, refreshes)
	}
}

func TestTranscribeRefreshesAuthOnceAfterRejectedHandshake(t *testing.T) {
	store := testDirectTranscriptionStore(t)
	filePath := testClaudeAudioFile(t, []byte("synthetic-webm"))
	dials := 0
	refreshes := 0
	result := Transcribe(context.Background(), TranscribeConfig{
		Store: store,
		Dial: func(context.Context, string, http.Header) (augloop.Socket, int, error) {
			dials++
			if dials == 1 {
				return nil, http.StatusUnauthorized, errors.New("expired")
			}
			return &claudeTestSocket{reads: [][]byte{
				[]byte(`{"type":"TranscriptText","data":"after refresh"}`),
				[]byte(`{"type":"TranscriptEndpoint"}`),
			}}, http.StatusSwitchingProtocols, nil
		},
		DecodePCM: func(context.Context, string) ([]byte, error) { return []byte{0, 0}, nil },
		RefreshAuth: func(context.Context, string) error {
			refreshes++
			return nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	}, filePath, 100)
	if !result.OK || dials != 2 || refreshes != 1 {
		t.Fatalf("result=%+v dials=%d refreshes=%d", result, dials, refreshes)
	}
	data := result.Data.(TranscriptionData)
	if data.Attempts != 2 || data.Transcript != "after refresh" {
		t.Fatalf("data = %+v", data)
	}
}

func TestTranscribeDoesNotRetryNonAuthSocketFailure(t *testing.T) {
	store := testDirectTranscriptionStore(t)
	filePath := testClaudeAudioFile(t, []byte("synthetic-webm"))
	dials := 0
	refreshes := 0
	result := Transcribe(context.Background(), TranscribeConfig{
		Store: store,
		Dial: func(context.Context, string, http.Header) (augloop.Socket, int, error) {
			dials++
			return nil, 0, errors.New("offline")
		},
		DecodePCM: func(context.Context, string) ([]byte, error) { return []byte{0, 0}, nil },
		RefreshAuth: func(context.Context, string) error {
			refreshes++
			return nil
		},
	}, filePath, 100)
	if result.OK || dials != 1 || refreshes != 0 || result.Error == nil ||
		result.Error.Code != "claude_voice_unavailable" || result.Error.ErrClass != "connection" || result.Error.RetrySafe {
		t.Fatalf("result=%+v dials=%d refreshes=%d", result, dials, refreshes)
	}
}

func TestTranscribeReturnsTypedAuthRefreshFailure(t *testing.T) {
	result := Transcribe(context.Background(), TranscribeConfig{
		Store: testDirectTranscriptionStore(t),
		Dial: func(context.Context, string, http.Header) (augloop.Socket, int, error) {
			return nil, http.StatusUnauthorized, errors.New("expired")
		},
		DecodePCM: func(context.Context, string) ([]byte, error) { return []byte{0, 0}, nil },
		RefreshAuth: func(context.Context, string) error {
			return errors.New("refresh unavailable")
		},
	}, testClaudeAudioFile(t, []byte("synthetic-webm")), 100)
	if result.OK || result.Error == nil || result.Error.Code != "claude_auth_refresh_failed" ||
		result.Error.ErrClass != "auth" || result.Error.RetrySafe {
		t.Fatalf("result = %+v", result)
	}
}

func TestTranscribeReturnsTypedReplayFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	socket := &claudeTestSocket{blockRead: true, writeErr: errors.New("write failed")}
	result := Transcribe(ctx, TranscribeConfig{
		Store: testDirectTranscriptionStore(t),
		Dial: func(context.Context, string, http.Header) (augloop.Socket, int, error) {
			return socket, http.StatusSwitchingProtocols, nil
		},
		DecodePCM: func(context.Context, string) ([]byte, error) { return []byte{0, 0}, nil },
	}, testClaudeAudioFile(t, []byte("synthetic-webm")), 100)
	cancel()
	if result.OK || len(socket.writes) != 1 || result.Error == nil ||
		result.Error.Code != "claude_voice_websocket_failed" || result.Error.ErrClass != "connection" || result.Error.RetrySafe {
		t.Fatalf("result=%+v writes=%d", result, len(socket.writes))
	}
}

func TestReadClaudeVoiceReturnsTypedResponseShapeFailure(t *testing.T) {
	result := readClaudeVoice(context.Background(), &claudeTestSocket{reads: [][]byte{[]byte("{")}}, make(chan struct{}))
	if result.failure == nil || result.failure.code != "claude_voice_response_changed" || result.failure.errClass != "provider" {
		t.Fatalf("result = %+v", result)
	}
}

func TestTranscriptionFailureResultPreservesTypedTimeout(t *testing.T) {
	result := transcriptionFailureResult("run-test", "test", TranscriptionData{}, transcribeFailure{
		code: "claude_voice_timeout", errClass: "timeout", message: "Claude dictation did not return a final transcript",
	})
	if result.OK || result.Error == nil || result.Error.Code != "claude_voice_timeout" ||
		result.Error.ErrClass != "timeout" || result.Error.RetrySafe {
		t.Fatalf("result = %+v", result)
	}
}

func TestReadClaudeVoiceKeepsFinalEndpointWhenProviderCloses(t *testing.T) {
	socket := &claudeTestSocket{reads: [][]byte{
		[]byte(`{"type":"TranscriptText","data":"complete before close"}`),
		[]byte(`{"type":"TranscriptEndpoint"}`),
	}}
	result := readClaudeVoice(context.Background(), socket, make(chan struct{}))
	if result.failure != nil || result.transcript != "complete before close" {
		t.Fatalf("result = %+v", result)
	}
}

func TestClaudeCookieHeaderPreservesObservedValues(t *testing.T) {
	template := validAuthTemplate(time.Now().UTC())
	template.Cookies["quoted"] = `"provider-value"`
	if got := claudeCookieHeader(template.Cookies); !strings.Contains(got, `quoted="provider-value"`) {
		t.Fatalf("cookie header = %q", got)
	}
}

func testDirectTranscriptionStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), validAuthTemplate(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	return store
}

func testClaudeAudioFile(t *testing.T, audio []byte) string {
	t.Helper()
	path := t.TempDir() + "/audio.webm"
	if err := os.WriteFile(path, audio, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type claudeTestWrite struct {
	messageType augloop.MessageType
	payload     []byte
}

type claudeTestSocket struct {
	writes    []claudeTestWrite
	reads     [][]byte
	index     int
	blockRead bool
	readGate  <-chan struct{}
	onWrite   func(augloop.MessageType, []byte)
	writeErr  error
}

func (s *claudeTestSocket) Write(_ context.Context, messageType augloop.MessageType, payload []byte) error {
	s.writes = append(s.writes, claudeTestWrite{messageType: messageType, payload: bytes.Clone(payload)})
	if s.onWrite != nil {
		s.onWrite(messageType, payload)
	}
	return s.writeErr
}

func (s *claudeTestSocket) Read(ctx context.Context) (augloop.MessageType, []byte, error) {
	if s.blockRead {
		<-ctx.Done()
		return 0, nil, ctx.Err()
	}
	if s.readGate != nil {
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-s.readGate:
		}
	}
	if s.index >= len(s.reads) {
		return 0, nil, errors.New("no scripted frame")
	}
	payload := s.reads[s.index]
	s.index++
	return augloop.MessageText, payload, nil
}

func (*claudeTestSocket) Close(augloop.StatusCode, string) error { return nil }
