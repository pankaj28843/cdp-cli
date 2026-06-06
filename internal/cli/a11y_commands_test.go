package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestA11ySnapshotJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"a11y", "snapshot", "--selector", "body", "--depth", "4", "--limit", "10", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("a11y snapshot exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		Snapshot struct {
			Selector  string   `json:"selector"`
			LineCount int      `json:"line_count"`
			Lines     []string `json:"lines"`
			Text      string   `json:"text"`
			Truncated bool     `json:"truncated"`
			Depth     int      `json:"depth"`
			Limit     int      `json:"limit"`
			Source    string   `json:"source"`
		} `json:"snapshot"`
		Lines []string `json:"lines"`
		Text  string   `json:"text"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("a11y snapshot output is invalid JSON: %v", err)
	}
	wantLines := []string{
		"- textbox: Editor",
		"- button \"Submit\"",
		"- heading \"Welcome\" [level=1]",
	}
	if !got.OK || got.Target.URL != "https://example.test/app" || got.Target.Title != "Example App" || got.Snapshot.Selector != "body" || got.Snapshot.Source != "cdp-accessibility-tree" || got.Snapshot.Depth != 4 || got.Snapshot.Limit != 10 || got.Snapshot.Truncated || got.Snapshot.LineCount != len(wantLines) {
		t.Fatalf("a11y snapshot metadata = %+v target=%+v, want bounded snapshot metadata", got.Snapshot, got.Target)
	}
	if strings.Join(got.Snapshot.Lines, "\n") != strings.Join(wantLines, "\n") || strings.Join(got.Lines, "\n") != strings.Join(wantLines, "\n") || got.Snapshot.Text != strings.Join(wantLines, "\n")+"\n" || got.Text != got.Snapshot.Text {
		t.Fatalf("a11y snapshot lines = snapshot=%q top=%q text=%q, want %q", got.Snapshot.Lines, got.Lines, got.Snapshot.Text, wantLines)
	}
}
