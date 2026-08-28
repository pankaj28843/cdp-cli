package chatgpt

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestTranscribeUsesDirectObservedHTTPMultipart(t *testing.T) {
	store := testTranscriptionStore(t)
	audio := []byte("synthetic-webm")
	filePath := testTranscriptionFile(t, audio)
	client := &http.Client{Transport: transcriptionRoundTripper(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		for _, want := range []string{
			`name="file"; filename="whisper.webm"`,
			"audio/webm;codecs=opus",
			`name="duration_ms"`,
			"1234",
			"synthetic-webm",
		} {
			if !bytes.Contains(body, []byte(want)) {
				t.Fatalf("multipart body missing %q", want)
			}
		}
		if request.Method != http.MethodPost ||
			request.URL.Path != "/backend-api/transcribe" ||
			request.Header.Get("Authorization") != "Bearer synthetic" ||
			request.Header.Get("Cookie") != "__Secure-next-auth.session-token=synthetic; _account=synthetic-account; session=synthetic" ||
			request.Header.Get("ChatGPT-Account-Id") != "synthetic-account" ||
			request.Header.Get("X-OpenAI-Target-Path") != "/backend-api/transcribe" ||
			request.Header.Get("Origin") != Origin ||
			request.Header.Get("Accept") != "*/*" ||
			request.Header.Get("Referer") != Origin+"/" ||
			request.Header.Get("Sec-Fetch-Dest") != "empty" ||
			request.Header.Get("Sec-Fetch-Mode") != "cors" ||
			request.Header.Get("Sec-Fetch-Site") != "same-origin" ||
			request.Header.Get("OAI-Client-Build-Number") != "" ||
			request.Header.Get("OAI-Client-Version") != "" ||
			request.Header.Get("OAI-Session-Id") != "" ||
			request.Header.Get("X-OAI-Is-Client-Observation") != "" {
			t.Fatalf("request shape = method=%s path=%s headers=%v", request.Method, request.URL.Path, request.Header)
		}
		return makeTranscriptionHTTPResponse(http.StatusOK, `{"result":{"text":"direct works"}}`), nil
	})}

	result := Transcribe(context.Background(), TranscribeConfig{
		Store:       store,
		BuildCommit: "test",
		HTTPClient:  client,
		MaxAttempts: 1,
	}, filePath, 1234)
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
	if !result.OK || result.Operation != webagent.OperationTranscribe {
		t.Fatalf("result = %+v", result)
	}
	data, ok := result.Data.(TranscriptionData)
	if !ok || data.Transcript != "direct works" || data.Attempts != 1 || data.AudioBytes != int64(len(audio)) {
		t.Fatalf("transcription data = %+v", result.Data)
	}
}

func TestTranscribeSubstitutesEachNewMultipartAudioPayload(t *testing.T) {
	store := testTranscriptionStore(t)
	payloads := [][]byte{
		[]byte("first-distinct-webm"),
		[]byte("second-distinct-webm"),
	}
	type capturedRequest struct {
		audio       []byte
		duration    string
		contentType string
	}
	captured := make([]capturedRequest, 0, len(payloads))
	client := &http.Client{Transport: transcriptionRoundTripper(func(request *http.Request) (*http.Response, error) {
		reader, err := request.MultipartReader()
		if err != nil {
			t.Fatalf("multipart reader: %v", err)
		}
		got := capturedRequest{contentType: request.Header.Get("Content-Type")}
		parts := 0
		for {
			part, partErr := reader.NextPart()
			if partErr == io.EOF {
				break
			}
			if partErr != nil {
				t.Fatalf("next multipart part: %v", partErr)
			}
			parts++
			value, readErr := io.ReadAll(part)
			if readErr != nil {
				t.Fatalf("read multipart part %q: %v", part.FormName(), readErr)
			}
			switch part.FormName() {
			case "file":
				got.audio = value
			case "duration_ms":
				got.duration = string(value)
			default:
				t.Fatalf("unexpected multipart part %q", part.FormName())
			}
		}
		if parts != 2 {
			t.Fatalf("multipart parts = %d, want file and duration only", parts)
		}
		captured = append(captured, got)
		return makeTranscriptionHTTPResponse(http.StatusOK, `{"text":"direct works"}`), nil
	})}

	for index, audio := range payloads {
		result := Transcribe(context.Background(), TranscribeConfig{
			Store: store, HTTPClient: client, MaxAttempts: 1,
		}, testTranscriptionFile(t, audio), int64(index+1)*100)
		if !result.OK {
			t.Fatalf("Transcribe(%d) = %+v", index, result)
		}
	}
	for index, request := range captured {
		if !bytes.Equal(request.audio, payloads[index]) {
			t.Fatalf("request %d audio = %q, want only %q", index, request.audio, payloads[index])
		}
		if wantDuration := []string{"100", "200"}[index]; request.duration != wantDuration {
			t.Fatalf("request %d duration = %q, want %q", index, request.duration, wantDuration)
		}
	}
	if captured[0].contentType == "" || captured[0].contentType == captured[1].contentType {
		t.Fatalf("multipart content types = %q, want a fresh boundary per request", []string{captured[0].contentType, captured[1].contentType})
	}
}

func TestTranscribeRefreshesAuthOnceAndRetries(t *testing.T) {
	store := testTranscriptionStore(t)
	filePath := testTranscriptionFile(t, []byte("synthetic-webm"))
	requests := 0
	refreshes := 0
	client := &http.Client{Transport: transcriptionRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return makeTranscriptionHTTPResponse(http.StatusForbidden, "challenge"), nil
		}
		return makeTranscriptionHTTPResponse(http.StatusOK, `{"text":"after refresh"}`), nil
	})}
	result := Transcribe(context.Background(), TranscribeConfig{
		Store:       store,
		BuildCommit: "test",
		HTTPClient:  client,
		RefreshAuth: func(context.Context) error {
			refreshes++
			return nil
		},
		MaxAttempts: 3,
		Backoff:     []time.Duration{0, 0},
	}, filePath, 500)
	if !result.OK || requests != 2 || refreshes != 1 {
		t.Fatalf("result=%+v requests=%d refreshes=%d", result, requests, refreshes)
	}
	data, ok := result.Data.(TranscriptionData)
	if !ok || data.Attempts != 2 || data.Transcript != "after refresh" {
		t.Fatalf("transcription data = %+v", result.Data)
	}
}

func TestTranscribeReturnsTypedFailureWithoutBrowserFallback(t *testing.T) {
	store := testTranscriptionStore(t)
	filePath := testTranscriptionFile(t, []byte("synthetic-webm"))
	directRequests := 0
	client := &http.Client{Transport: transcriptionRoundTripper(func(request *http.Request) (*http.Response, error) {
		directRequests++
		return makeTranscriptionHTTPResponse(http.StatusUnauthorized, `{"error":{"message":"rejected"}}`), nil
	})}
	result := Transcribe(context.Background(), TranscribeConfig{
		Store:       store,
		BuildCommit: "test",
		HTTPClient:  client,
		MaxAttempts: 1,
	}, filePath, 500)
	if result.OK || directRequests != 1 {
		t.Fatalf("result=%+v directRequests=%d", result, directRequests)
	}
	if result.Error == nil || result.Error.ErrClass != "auth" {
		t.Fatalf("error = %+v, want typed auth failure", result.Error)
	}
}

func TestTranscribeStopsAfterOneAuthRefreshAndOneRetry(t *testing.T) {
	store := testTranscriptionStore(t)
	filePath := testTranscriptionFile(t, []byte("synthetic-webm"))
	requests := 0
	refreshes := 0
	client := &http.Client{Transport: transcriptionRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		return makeTranscriptionHTTPResponse(http.StatusUnauthorized, `{"error":{"message":"rejected"}}`), nil
	})}
	result := Transcribe(context.Background(), TranscribeConfig{
		Store:       store,
		BuildCommit: "test",
		HTTPClient:  client,
		RefreshAuth: func(context.Context) error {
			refreshes++
			return nil
		},
		MaxAttempts: 2,
		Backoff:     []time.Duration{0},
	}, filePath, 500)
	if result.OK || requests != 2 || refreshes != 1 {
		t.Fatalf("result=%+v requests=%d refreshes=%d", result, requests, refreshes)
	}
}

func TestTranscribeRetriesResponseDriftOnlyThroughDirectHTTP(t *testing.T) {
	store := testTranscriptionStore(t)
	filePath := testTranscriptionFile(t, []byte("synthetic-webm"))
	directRequests := 0
	client := &http.Client{Transport: transcriptionRoundTripper(func(request *http.Request) (*http.Response, error) {
		directRequests++
		return makeTranscriptionHTTPResponse(http.StatusOK, `{}`), nil
	})}
	result := Transcribe(context.Background(), TranscribeConfig{
		Store:       store,
		BuildCommit: "test",
		HTTPClient:  client,
		MaxAttempts: 2,
		Backoff:     []time.Duration{0, 0},
	}, filePath, 500)
	if result.OK || directRequests != 2 {
		t.Fatalf("result=%+v directRequests=%d", result, directRequests)
	}
	if result.Error == nil || result.Error.Code != "chatgpt_transcription_response_changed" {
		t.Fatalf("error = %+v, want response-shape failure", result.Error)
	}
}

func TestTranscribePreservesIncomingAudioMetadata(t *testing.T) {
	store := testTranscriptionStore(t)
	filePath := testTranscriptionFile(t, []byte("synthetic-wav"))
	client := &http.Client{Transport: transcriptionRoundTripper(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		for _, want := range []string{
			`name="file"; filename="recording.wav"`,
			"audio/wav",
			"synthetic-wav",
		} {
			if !bytes.Contains(body, []byte(want)) {
				t.Fatalf("multipart body missing %q", want)
			}
		}
		return makeTranscriptionHTTPResponse(http.StatusOK, `{"text":"wav works"}`), nil
	})}

	result := Transcribe(context.Background(), TranscribeConfig{
		Store:         store,
		BuildCommit:   "test",
		HTTPClient:    client,
		AudioFileName: "recording.wav",
		AudioMIMEType: "audio/wav",
		MaxAttempts:   1,
	}, filePath, 1234)
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
}

type transcriptionRoundTripper func(*http.Request) (*http.Response, error)

func (f transcriptionRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func makeTranscriptionHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testTranscriptionStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.SaveTemplate(context.Background(), RequestTemplate{
		SchemaVersion: AuthTemplateSchemaVersion,
		Method:        http.MethodGet,
		URL:           Origin + ConversationListPath,
		Headers: map[string]string{
			"authorization":               "Bearer synthetic",
			"chatgpt-account-id":          "synthetic-account",
			"oai-client-build-number":     "9999999",
			"oai-client-version":          "synthetic-version",
			"oai-session-id":              "synthetic-session",
			"sec-fetch-dest":              "empty",
			"sec-fetch-mode":              "cors",
			"sec-fetch-site":              "same-origin",
			"user-agent":                  "synthetic-agent",
			"x-oai-is-client-observation": "synthetic-observation",
		},
		Cookies: map[string]string{
			"__Secure-next-auth.session-token": "synthetic",
			"_account":                         "synthetic-account",
			"session":                          "synthetic",
		},
		CookieHeader:     "__Secure-next-auth.session-token=synthetic; _account=synthetic-account; session=synthetic",
		BrowserUserAgent: "synthetic-agent",
		CapturedAt:       now,
		Source:           "headed-cdp-retained-read-shape",
	}); err != nil {
		t.Fatalf("save template: %v", err)
	}
	return store
}

func testTranscriptionFile(t *testing.T, body []byte) string {
	t.Helper()
	path := t.TempDir() + "/whisper.webm"
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	return path
}
