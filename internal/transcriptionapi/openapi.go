package transcriptionapi

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed openapi.json
var openAPISpec []byte

//go:embed demo.html
var demoPage []byte

// OpenAPISpec returns a copy of the checked-in OpenAPI document.
func OpenAPISpec() []byte {
	return append([]byte(nil), openAPISpec...)
}

// DemoHTML returns a copy of the human-facing API dogfood page.
func DemoHTML() []byte {
	return append([]byte(nil), demoPage...)
}

// ValidateOpenAPISpec performs the structural checks that must hold before
// the document is served. Full schema validation belongs in the API contract
// test, but a malformed or incomplete embedded document must never boot.
func ValidateOpenAPISpec() error {
	var document struct {
		OpenAPI    string                     `json:"openapi"`
		Info       map[string]json.RawMessage `json:"info"`
		Paths      map[string]json.RawMessage `json:"paths"`
		Components map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(openAPISpec, &document); err != nil {
		return fmt.Errorf("parse embedded OpenAPI document: %w", err)
	}
	if document.OpenAPI != "3.1.0" {
		return fmt.Errorf("unsupported OpenAPI version %q", document.OpenAPI)
	}
	if len(document.Info) == 0 {
		return fmt.Errorf("OpenAPI info is required")
	}
	for _, path := range []string{
		"/healthz",
		"/openapi.json",
		"/v1/models",
		"/v1/audio/transcriptions",
		"/v1/audio/translations",
		"/v1/realtime",
	} {
		if _, ok := document.Paths[path]; !ok {
			return fmt.Errorf("OpenAPI path %q is missing", path)
		}
	}
	if _, ok := document.Components["schemas"]; !ok {
		return fmt.Errorf("OpenAPI component schemas are missing")
	}
	return nil
}
