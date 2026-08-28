package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTranscribeReplaysGeminiDictationWebChannel(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := testTranscriptionStore(t, now)
	audio := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte(strings.Repeat("webm-opus-byte-stream-", 2_000))...)
	audioPath := writeGeminiAudio(t, audio)
	transport := &fakeGeminiWebChannel{transcript: "synthetic transcript"}

	result := Transcribe(context.Background(), TranscribeConfig{
		Store:       store,
		BuildCommit: "test",
		HTTPClient:  &http.Client{Transport: transport},
		Now:         func() time.Time { return now },
	}, audioPath, 2_000)
	if !result.OK {
		t.Fatalf("Transcribe() = %+v", result)
	}
	data, ok := result.Data.(TranscriptionData)
	if !ok {
		t.Fatalf("data = %T, want TranscriptionData", result.Data)
	}
	if data.Transcript != "synthetic transcript" || data.Attempts != 1 {
		t.Fatalf("transcription data = %+v", data)
	}
	if result.Evidence.BrowserMode != "none" || result.Evidence.ReadMode != "direct_http_webchannel" || result.Evidence.Target != nil {
		t.Fatalf("evidence = %+v, want browser-free direct replay", result.Evidence)
	}
	if got := transport.audioBytes(); string(got) != string(audio) {
		t.Fatalf("uploaded audio bytes = %d, want %d byte-for-byte", len(got), len(audio))
	}
	if !transport.sawInitial || !transport.sawStop {
		t.Fatalf("WebChannel messages: initial=%v stop=%v", transport.sawInitial, transport.sawStop)
	}
}

func TestTranscribeSubstitutesEachNewAudioPayload(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := testTranscriptionStore(t, now)
	payloads := [][]byte{
		append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte("first-audio")...),
		append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte("second-distinct-audio")...),
	}
	nonces := make([]string, 0, len(payloads))
	for _, audio := range payloads {
		transport := &fakeGeminiWebChannel{transcript: "synthetic transcript"}
		result := Transcribe(context.Background(), TranscribeConfig{
			Store: store, HTTPClient: &http.Client{Transport: transport},
			Now: func() time.Time { return now },
		}, writeGeminiAudio(t, audio), 100)
		if !result.OK {
			t.Fatalf("Transcribe() = %+v", result)
		}
		if got := transport.audioBytes(); string(got) != string(audio) {
			t.Fatalf("uploaded audio = %q, want only %q", got, audio)
		}
		nonces = append(nonces, transport.bootstrapNonce())
	}
	if nonces[0] == "" || nonces[0] == nonces[1] {
		t.Fatalf("bootstrap nonces = %q, want fresh dynamic value per request", nonces)
	}
}

func TestTranscribeConvertsNonWebMAudioBeforeReplay(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := testTranscriptionStore(t, now)
	converted := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, []byte("converted-webm")...)
	audioPath := t.TempDir() + "/recording.wav"
	if err := os.WriteFile(audioPath, []byte("wav-audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	transport := &fakeGeminiWebChannel{transcript: "converted transcript"}
	called := false
	result := Transcribe(context.Background(), TranscribeConfig{
		Store:      store,
		HTTPClient: &http.Client{Transport: transport},
		EncodeWebM: func(_ context.Context, path string) ([]byte, error) {
			called = path == audioPath
			return converted, nil
		},
		Now: func() time.Time { return now },
	}, audioPath, 100)
	if !result.OK || !called {
		t.Fatalf("Transcribe() = %+v, encoder called=%v", result, called)
	}
	if got := string(transport.audioBytes()); got != string(converted) {
		t.Fatalf("uploaded audio = %q, want converted WebM", got)
	}
}

func TestGeminiInitialMessageMatchesObservedStableBytes(t *testing.T) {
	const observed = "ChViZXlvbmQtYTJhLXJlY29nbml6ZXIQAcKIjwEGEgQKAmVu4o6PAQkVAAB6RhgLIAGCx48BGBIRYmFyZC13ZWItZnJvbnRlbmRCA1dlYqLmjwEOCgRSAmVuKAHAAgGgAwE="
	message := geminiInitialMessage("en")
	if got := base64.StdEncoding.EncodeToString(message); got != observed {
		t.Fatalf("initial message = %q, want observed stable 98-byte configuration", got)
	}
	if len(message) != 98 {
		t.Fatalf("initial message bytes = %d, want 98", len(message))
	}
}

func TestParseGeminiFinalFrameDoesNotTreatProtobufLengthsAsText(t *testing.T) {
	const transcript = "Could you open the latest project notes before the meeting starts?"
	got, final, _, err := parseGeminiReceivePayload([]byte(finalGeminiFrame(transcript)))
	if err != nil || !final || got != transcript {
		t.Fatalf("parseGeminiReceivePayload() = (%q, %v, %v)", got, final, err)
	}
}

func TestTranscribeRefreshesOnceOnTypedAuthRejection(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := testTranscriptionStore(t, now)
	transport := &fakeGeminiWebChannel{transcript: "after refresh", rejectBootstraps: 1}
	refreshes := 0

	result := Transcribe(context.Background(), TranscribeConfig{
		Store:       store,
		BuildCommit: "test",
		HTTPClient:  &http.Client{Transport: transport},
		Now:         func() time.Time { return now },
		RefreshAuth: func(ctx context.Context, generation string) error {
			refreshes++
			if generation != now.Format(time.RFC3339Nano) {
				t.Fatalf("refresh generation = %q", generation)
			}
			template, err := store.LoadTemplate(ctx)
			if err != nil {
				return err
			}
			template.CapturedAt = now.Add(time.Second).Format(time.RFC3339Nano)
			return store.SaveTemplate(ctx, template)
		},
	}, writeGeminiAudio(t, []byte("webm")), 100)
	if !result.OK {
		t.Fatalf("Transcribe() = %+v", result)
	}
	data := result.Data.(TranscriptionData)
	if refreshes != 1 || data.Attempts != 2 || data.Transcript != "after refresh" {
		t.Fatalf("refreshes=%d data=%+v", refreshes, data)
	}
}

func TestTranscribeDoesNotExposeRetryAfterBoundedAuthRepair(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := testTranscriptionStore(t, now)
	transport := &fakeGeminiWebChannel{rejectBootstraps: 2}
	refreshes := 0
	result := Transcribe(context.Background(), TranscribeConfig{
		Store:      store,
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return now },
		RefreshAuth: func(context.Context, string) error {
			refreshes++
			return nil
		},
	}, writeGeminiAudio(t, []byte("webm")), 100)
	if result.OK || result.Error == nil {
		t.Fatalf("Transcribe() = %+v, want failure", result)
	}
	data := result.Data.(TranscriptionData)
	if refreshes != 1 || data.Attempts != 2 || result.Error.Code != "gemini_auth_rejected" || result.Error.RetrySafe {
		t.Fatalf("refreshes=%d data=%+v error=%+v", refreshes, data, result.Error)
	}
}

func TestTranscribeReportsAuthRefreshFailure(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	result := Transcribe(context.Background(), TranscribeConfig{
		Store:      testTranscriptionStore(t, now),
		HTTPClient: &http.Client{Transport: &fakeGeminiWebChannel{rejectBootstraps: 1}},
		Now:        func() time.Time { return now },
		RefreshAuth: func(context.Context, string) error {
			return errors.New("refresh unavailable")
		},
	}, writeGeminiAudio(t, []byte("webm")), 100)
	if result.OK || result.Error == nil || result.Error.Code != "gemini_auth_refresh_failed" || result.Error.ErrClass != "auth" {
		t.Fatalf("Transcribe() = %+v", result)
	}
}

func TestGeminiTranscriptionFailureCodesAreStable(t *testing.T) {
	tests := []struct {
		name, code, class string
		failure           *transcribeFailure
	}{
		{"timeout", "gemini_transcription_timeout", "timeout", &transcribeFailure{code: "gemini_transcription_timeout", errClass: "timeout"}},
		{"transport", "gemini_dictation_unavailable", "connection", connectionTranscriptionFailure("unavailable")},
		{"response shape", "gemini_dictation_response_changed", "provider", providerTranscriptionFailure("changed")},
		{"replay status", "gemini_dictation_http_failed", "provider", geminiHTTPFailure(http.StatusBadGateway)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := transcriptionFailureResult("run", "test", TranscriptionData{}, *test.failure)
			if result.Error == nil || result.Error.Code != test.code || result.Error.ErrClass != test.class {
				t.Fatalf("error = %+v, want code=%q class=%q", result.Error, test.code, test.class)
			}
		})
	}
}

type fakeGeminiWebChannel struct {
	mu               sync.Mutex
	transcript       string
	rejectBootstraps int
	bootstraps       int
	sawInitial       bool
	sawStop          bool
	audio            []byte
	bootstrapZX      string
	finalReady       chan struct{}
	finalOnce        sync.Once
}

func (f *fakeGeminiWebChannel) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != speechHost || request.URL.Path != speechChannelPath {
		return nil, fmt.Errorf("unexpected request URL %s", request.URL)
	}
	var body []byte
	if request.Body != nil {
		var err error
		body, err = io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
	}
	if request.Method == http.MethodPost && request.URL.Query().Get("X-HTTP-Session-Id") == "gsessionid" {
		f.mu.Lock()
		f.bootstraps++
		f.bootstrapZX = request.URL.Query().Get("zx")
		attempt := f.bootstraps
		f.mu.Unlock()
		if string(body) != "count=0" {
			return nil, fmt.Errorf("bootstrap body = %q", body)
		}
		if request.Header.Get("X-Goog-Api-Key") != "test-api-key" || !strings.Contains(request.Header.Get("Authorization"), "SAPISIDHASH") {
			return nil, fmt.Errorf("bootstrap auth headers are incomplete")
		}
		if attempt <= f.rejectBootstraps {
			return fakeHTTPResponse(http.StatusUnauthorized, "rejected", nil), nil
		}
		header := http.Header{"X-Http-Session-Id": []string{"test-gsession"}}
		return fakeHTTPResponse(http.StatusOK, webChannelFrame(`[[0,["c","test-sid","",8,14,30000]]]`), header), nil
	}
	if request.Method == http.MethodGet {
		reader, writer := io.Pipe()
		finalReady := f.finalSignal()
		go func() {
			_, _ = io.WriteString(writer, webChannelFrame(`[[1,["noop"]]]`))
			<-finalReady
			_, _ = io.WriteString(writer, webChannelFrame(finalGeminiFrame(f.transcript)))
			_ = writer.Close()
		}()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}}, Body: reader}, nil
	}
	if request.Method != http.MethodPost {
		return nil, fmt.Errorf("unexpected method %s", request.Method)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}
	encoded := values.Get("req0___data__")
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case encoded == base64.StdEncoding.EncodeToString(geminiInitialMessage("")):
		f.sawInitial = true
	case len(payload) == 2 && payload[0] == 0x18 && payload[1] == 0x01:
		f.sawStop = true
		f.finalOnce.Do(func() { close(f.finalReady) })
	default:
		audio, ok := geminiAudioField(payload)
		if !ok {
			return nil, fmt.Errorf("unexpected protobuf message")
		}
		f.audio = append(f.audio, audio...)
	}
	return fakeHTTPResponse(http.StatusOK, webChannelFrame(`[1,1,7]`), nil), nil
}

func (f *fakeGeminiWebChannel) finalSignal() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.finalReady == nil {
		f.finalReady = make(chan struct{})
	}
	return f.finalReady
}

func (f *fakeGeminiWebChannel) audioBytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.audio...)
}

func (f *fakeGeminiWebChannel) bootstrapNonce() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bootstrapZX
}

func fakeHTTPResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	header.Set("Content-Type", "text/plain; charset=utf-8")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func webChannelFrame(payload string) string {
	return fmt.Sprintf("%d\n%s\n", len(payload), payload)
}

func finalGeminiFrame(transcript string) string {
	segment := appendProtoVarint(nil, 1, 0)
	segment = appendProtoVarint(segment, 2, 1)
	segment = appendProtoBytes(segment, 3, appendProtoBytes(nil, 1, []byte(transcript)))
	recognition := appendProtoVarint(nil, 1, 1)
	recognition = appendProtoBytes(recognition, 3, segment)
	recognition = appendProtoBytes(recognition, 5, segment)
	provider := appendProtoBytes(nil, 1, recognition)
	message := appendProtoVarint(nil, 5, 2)
	message = appendProtoBytes(message, 1253625, provider)
	return fmt.Sprintf(`[[2,[%q]]]`, base64.StdEncoding.EncodeToString(message))
}

func testTranscriptionStore(t *testing.T, capturedAt time.Time) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTemplate(context.Background(), testGeminiTemplate(capturedAt)); err != nil {
		t.Fatal(err)
	}
	return store
}

func testGeminiTemplate(capturedAt time.Time) RequestTemplate {
	if capturedAt.IsZero() {
		capturedAt = time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	}
	return RequestTemplate{
		SchemaVersion:    RequestTemplateSchemaVersion,
		APIKey:           "test-api-key",
		AuthUser:         "0",
		Cookies:          map[string]string{"SAPISID": "test-sapisid", "__Secure-1PAPISID": "test-1p", "__Secure-3PAPISID": "test-3p"},
		BrowserUserAgent: "Browser/Test",
		CapturedAt:       capturedAt.Format(time.RFC3339Nano),
		Source:           "headed-cdp-observed-dictation-template",
	}
}

func writeGeminiAudio(t *testing.T, audio []byte) string {
	t.Helper()
	if len(audio) < 4 || audio[0] != 0x1a || audio[1] != 0x45 || audio[2] != 0xdf || audio[3] != 0xa3 {
		audio = append([]byte{0x1a, 0x45, 0xdf, 0xa3}, audio...)
	}
	path := t.TempDir() + "/fixture.webm"
	if err := os.WriteFile(path, audio, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
