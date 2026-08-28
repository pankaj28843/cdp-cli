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
	socket := &claudeTestSocket{reads: [][]byte{
		[]byte(`{"type":"TranscriptText","data":"direct claude transcript"}`),
		[]byte(`{"type":"TranscriptEndpoint"}`),
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
	if result.OK || dials != 1 || refreshes != 0 || result.Error == nil || result.Error.RetrySafe {
		t.Fatalf("result=%+v dials=%d refreshes=%d", result, dials, refreshes)
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
	writes []claudeTestWrite
	reads  [][]byte
	index  int
}

func (s *claudeTestSocket) Write(_ context.Context, messageType augloop.MessageType, payload []byte) error {
	s.writes = append(s.writes, claudeTestWrite{messageType: messageType, payload: bytes.Clone(payload)})
	return nil
}

func (s *claudeTestSocket) Read(context.Context) (augloop.MessageType, []byte, error) {
	if s.index >= len(s.reads) {
		return 0, nil, errors.New("no scripted frame")
	}
	payload := s.reads[s.index]
	s.index++
	return augloop.MessageText, payload, nil
}

func (*claudeTestSocket) Close(augloop.StatusCode, string) error { return nil }
