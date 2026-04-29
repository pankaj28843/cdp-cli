package cdp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if protocol.Source != server.URL {
		t.Fatalf("protocol source = %q, want server URL", protocol.Source)
	}
}

func TestFetchProtocolSnapshotMergesDomains(t *testing.T) {
	browserServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": map[string]string{"major": "1", "minor": "3"},
			"domains": []map[string]any{
				{"domain": "Page", "commands": []map[string]any{{"name": "navigate"}}},
			},
		})
	}))
	defer browserServer.Close()
	jsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": map[string]string{"major": "1", "minor": "3"},
			"domains": []map[string]any{
				{"domain": "Runtime", "commands": []map[string]any{{"name": "evaluate"}}},
			},
		})
	}))
	defer jsServer.Close()

	protocol, err := cdp.FetchProtocolSnapshot(context.Background(), browserServer.URL, jsServer.URL)
	if err != nil {
		t.Fatalf("FetchProtocolSnapshot returned error: %v", err)
	}
	if protocol.Version.Major != "1" || len(protocol.Domains) != 2 || protocol.Domains[0].Domain != "Page" || protocol.Domains[1].Domain != "Runtime" {
		t.Fatalf("protocol = %+v, want merged Page and Runtime domains", protocol)
	}
	if !strings.Contains(protocol.Source, browserServer.URL) || !strings.Contains(protocol.Source, jsServer.URL) {
		t.Fatalf("protocol source = %q, want both URLs", protocol.Source)
	}
}

func TestSearchProtocol(t *testing.T) {
	protocol := cdp.Protocol{
		Domains: []cdp.Domain{
			{
				Domain: "Page",
				Commands: mustRawMessage(t, []map[string]any{
					{"name": "navigate", "description": "Navigate the page"},
					{"name": "captureScreenshot", "description": "Capture page pixels"},
				}),
			},
			{
				Domain: "Runtime",
				Events: mustRawMessage(t, []map[string]any{
					{"name": "consoleAPICalled", "description": "Issued when console API was called"},
				}),
			},
		},
	}

	got := cdp.SearchProtocol(protocol, "page capture", 10)
	if len(got) != 1 || got[0].Path != "Page.captureScreenshot" || got[0].Kind != "command" {
		t.Fatalf("SearchProtocol() = %+v, want Page.captureScreenshot command", got)
	}
}

func TestFilterSearchResultsByKind(t *testing.T) {
	results := []cdp.SearchResult{
		{Kind: "command", Path: "Page.navigate"},
		{Kind: "event", Path: "Runtime.consoleAPICalled"},
	}
	got := cdp.FilterSearchResultsByKind(results, "event")
	if len(got) != 1 || got[0].Path != "Runtime.consoleAPICalled" {
		t.Fatalf("FilterSearchResultsByKind() = %+v, want event only", got)
	}
}

func TestDescribeEntity(t *testing.T) {
	protocol := cdp.Protocol{
		Domains: []cdp.Domain{
			{
				Domain: "Page",
				Commands: mustRawMessage(t, []map[string]any{
					{"name": "captureScreenshot", "description": "Capture page pixels", "returns": []map[string]any{{"name": "data", "type": "string"}}},
				}),
			},
		},
	}

	got, ok := cdp.DescribeEntity(protocol, "Page.captureScreenshot")
	if !ok || got.Kind != "command" || got.Path != "Page.captureScreenshot" || len(got.Schema) == 0 {
		t.Fatalf("DescribeEntity() = %+v ok=%v, want command schema", got, ok)
	}
}

func mustRawMessage(t *testing.T, value any) json.RawMessage {
	t.Helper()

	b, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	return b
}
