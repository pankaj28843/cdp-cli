package transcriptionapi

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestStoreAppendTraceWritesBoundedMetadataWithoutSecrets(t *testing.T) {
	store, err := NewStore(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTrace(context.Background(), TraceEvent{
		Event:        "realtime.commit_failed",
		Transport:    "websocket",
		RequestID:    "tr-test",
		Provider:     ProviderM365,
		Phase:        PhaseFailed,
		AudioBytes:   8192,
		AudioChunks:  2,
		ErrorType:    "provider_error",
		ErrorCode:    "m365_stream_done_before_final",
		ErrorMessage: "Bearer secret token=another-secret\nprovider stopped",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.TracePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "another-secret") {
		t.Fatalf("trace leaked a secret: %s", raw)
	}
	var event TraceEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "realtime.commit_failed" || event.AudioBytes != 8192 || event.AudioChunks != 2 {
		t.Fatalf("trace event = %+v", event)
	}
}
