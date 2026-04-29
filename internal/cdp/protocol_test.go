package cdp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestFetchProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": map[string]string{"major": "1", "minor": "3"},
			"domains": []map[string]any{
				{
					"domain": "Page",
					"commands": []map[string]any{
						{"name": "navigate"},
						{"name": "captureScreenshot"},
					},
					"events": []map[string]any{
						{"name": "loadEventFired"},
					},
				},
			},
		})
	}))
	defer server.Close()

	protocol, err := cdp.FetchProtocol(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("FetchProtocol returned error: %v", err)
	}
	summaries := cdp.SummarizeDomains(protocol.Domains)
	if protocol.Version.Major != "1" || len(summaries) != 1 || summaries[0].CommandCount != 2 || summaries[0].EventCount != 1 {
		t.Fatalf("protocol summary = %+v version=%+v, want Page counts", summaries, protocol.Version)
	}
}
