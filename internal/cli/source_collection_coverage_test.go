package cli

import (
	"encoding/json"
	"testing"
)

func TestStaticSourceCoverageEncodesEmptyKindsAsArrays(t *testing.T) {
	coverage := staticSourceCoverage([]string{"story"}, nil, "exhausted", "")
	payload, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if kinds, ok := decoded["possibly_missing_record_kinds"].([]any); !ok || len(kinds) != 0 {
		t.Fatalf("possibly_missing_record_kinds = %#v, want empty array", decoded["possibly_missing_record_kinds"])
	}
}
