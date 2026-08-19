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
		Paths map[string]map[string]json.RawMessage `json:"paths"`
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
}
