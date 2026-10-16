package transcriptionapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

type delayedUpload struct {
	io.ReadCloser
	delayed bool
}

func (r *delayedUpload) Read(p []byte) (int, error) {
	if !r.delayed {
		time.Sleep(80 * time.Millisecond)
		r.delayed = true
	}
	return r.ReadCloser.Read(p)
}

type delayedResponse struct{ *httptest.ResponseRecorder }

func (w delayedResponse) Write(p []byte) (int, error) {
	time.Sleep(80 * time.Millisecond)
	return w.ResponseRecorder.Write(p)
}

func TestFileTraceIncludesUploadAndResponseWrite(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			provider := &fakeProvider{id: ProviderChatGPT, result: Result{Text: "synthetic result"}}
			if failed {
				provider.err = providerError(502, "provider", "synthetic_failure", "synthetic failure", false)
			}
			server, store := newTestServer(t, provider)
			request := newMultipartRequest(t, "/v1/audio/transcriptions", "timed-request", "speech.webm", []byte("fake-webm"), map[string]string{"model": DefaultModel, "duration_ms": "4000"})
			request.Body = &delayedUpload{ReadCloser: request.Body}
			server.Handler().ServeHTTP(delayedResponse{httptest.NewRecorder()}, request)
			raw, err := os.ReadFile(store.TracePath())
			if err != nil {
				t.Fatal(err)
			}
			lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
			var first, last TraceEvent
			if err := json.Unmarshal(lines[0], &first); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
				t.Fatal(err)
			}
			if first.ElapsedMS < 80 || last.ElapsedMS < 160 {
				t.Fatalf("trace excludes upload or response write: first=%+v last=%+v", first, last)
			}
		})
	}
}
