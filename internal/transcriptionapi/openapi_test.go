package transcriptionapi

import (
	"encoding/json"
	"testing"
)

func TestOpenAPISpecIsEmbeddedAndComplete(t *testing.T) {
	if err := ValidateOpenAPISpec(); err != nil {
		t.Fatal(err)
	}

	var document struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(OpenAPISpec(), &document); err != nil {
		t.Fatal(err)
	}
	for path, method := range map[string]string{
		"/healthz":                 "get",
		"/openapi.json":            "get",
		"/v1/models":               "get",
		"/v1/audio/transcriptions": "post",
		"/v1/audio/translations":   "post",
		"/v1/realtime":             "get",
	} {
		if _, ok := document.Paths[path][method]; !ok {
			t.Fatalf("OpenAPI operation %s %s is missing", method, path)
		}
	}
	if _, ok := document.Components.Schemas["AvailabilitySummary"]; !ok {
		t.Fatal("OpenAPI AvailabilitySummary schema is missing")
	}
	var health struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(document.Components.Schemas["Health"], &health); err != nil {
		t.Fatal(err)
	}
	foundUptime := false
	for _, field := range health.Required {
		foundUptime = foundUptime || field == "uptime"
	}
	if !foundUptime {
		t.Fatal("OpenAPI Health schema does not require uptime")
	}
}
