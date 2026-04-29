package output_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/output"
)

func TestRenderHuman(t *testing.T) {
	var buf bytes.Buffer
	err := output.Render(context.Background(), &buf, output.Options{}, "hello", map[string]string{"ignored": "true"})
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "hello" {
		t.Fatalf("Render() = %q, want %q", got, "hello")
	}
}

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]string{"version": "dev"}

	err := output.Render(context.Background(), &buf, output.Options{JSON: true}, "ignored", data)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Render produced invalid JSON: %v", err)
	}
	if got["version"] != "dev" {
		t.Fatalf("version = %q, want %q", got["version"], "dev")
	}
}

func TestRenderCompactJSON(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]string{"version": "dev"}

	err := output.Render(context.Background(), &buf, output.Options{JSON: true, Compact: true}, "ignored", data)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != `{"version":"dev"}` {
		t.Fatalf("compact JSON = %q, want minified object", got)
	}
}
