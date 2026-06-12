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

func TestAssertAriaSnapshotJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	expected := "- button \"Submit\"\n- heading \"Welcome\" [level=1]"
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "aria-snapshot", "--expected", expected, "--selector", "body", "--depth", "4", "--limit", "10", "--timeout", "1s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("assert aria-snapshot exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		Assertion struct {
			Selector      string   `json:"selector"`
			Expected      string   `json:"expected"`
			Actual        string   `json:"actual"`
			Mode          string   `json:"mode"`
			Passed        bool     `json:"passed"`
			LineCount     int      `json:"line_count"`
			ExpectedLines []string `json:"expected_lines"`
			ActualLines   []string `json:"actual_lines"`
			Snapshot      struct {
				Selector  string   `json:"selector"`
				LineCount int      `json:"line_count"`
				Lines     []string `json:"lines"`
				Source    string   `json:"source"`
			} `json:"snapshot"`
			Attempts     int    `json:"attempts"`
			PollInterval string `json:"poll_interval"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert aria-snapshot output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Assertion.Passed || got.Assertion.Selector != "body" || got.Assertion.Mode != "contains" || got.Assertion.Expected != expected || got.Assertion.LineCount != 3 || got.Assertion.Snapshot.LineCount != 3 || got.Assertion.Snapshot.Source != "cdp-accessibility-tree" || got.Assertion.Attempts < 1 || got.Assertion.PollInterval != "10ms" {
		t.Fatalf("assert aria-snapshot = %+v, want passing bounded snapshot assertion", got)
	}
	if strings.Join(got.Assertion.ExpectedLines, "\n") != expected || !containsString(got.Assertion.ActualLines, "- button \"Submit\"") || !containsString(got.Assertion.ActualLines, "- heading \"Welcome\" [level=1]") || strings.Join(got.Assertion.Snapshot.Lines, "\n") != "- textbox: Editor\n- button \"Submit\"\n- heading \"Welcome\" [level=1]" {
		t.Fatalf("assert aria-snapshot lines = %+v, want expected subset and actual snapshot", got.Assertion)
	}
}

func TestAssertAriaSnapshotTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"assert", "aria-snapshot", "--expected", "- button \"Missing\"", "--selector", "body", "--timeout", "3s", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("assert aria-snapshot timeout exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}

	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		RemediationCommands []string `json:"remediation_commands"`
		Data                struct {
			Assertion struct {
				Selector     string   `json:"selector"`
				Expected     string   `json:"expected"`
				ActualLines  []string `json:"actual_lines"`
				Passed       bool     `json:"passed"`
				LineCount    int      `json:"line_count"`
				Attempts     int      `json:"attempts"`
				PollInterval string   `json:"poll_interval"`
				Diff         *struct {
					Mode              string `json:"mode"`
					Reason            string `json:"reason"`
					ExpectedIndex     int    `json:"expected_index"`
					ActualIndex       int    `json:"actual_index"`
					ExpectedLine      string `json:"expected_line"`
					ExpectedLineCount int    `json:"expected_line_count"`
					ActualLineCount   int    `json:"actual_line_count"`
				} `json:"diff"`
			} `json:"assertion"`
			Snapshot struct {
				Selector  string   `json:"selector"`
				LineCount int      `json:"line_count"`
				Lines     []string `json:"lines"`
			} `json:"snapshot"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("assert aria-snapshot timeout output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "timeout" || got.Data.Assertion.Selector != "body" || got.Data.Assertion.Expected != "- button \"Missing\"" || got.Data.Assertion.Passed || got.Data.Assertion.LineCount != 3 || got.Data.Assertion.Attempts < 1 || got.Data.Assertion.PollInterval != "10ms" || got.Data.Assertion.Diff == nil || got.Data.Assertion.Diff.Mode != "contains" || got.Data.Assertion.Diff.Reason != "missing_line" || got.Data.Assertion.Diff.ExpectedIndex != 0 || got.Data.Assertion.Diff.ActualIndex != -1 || got.Data.Assertion.Diff.ExpectedLine != "- button \"Missing\"" || got.Data.Assertion.Diff.ExpectedLineCount != 1 || got.Data.Assertion.Diff.ActualLineCount != 3 || !containsString(got.Data.Assertion.ActualLines, "- button \"Submit\"") || got.Data.Snapshot.Selector != "body" || got.Data.Snapshot.LineCount != 3 || !containsString(got.Data.Snapshot.Lines, "- heading \"Welcome\" [level=1]") || !containsString(got.RemediationCommands, "cdp a11y snapshot --selector body --depth 4 --limit 100 --json") {
		t.Fatalf("assert aria-snapshot timeout = %+v, want timeout with snapshot diff diagnostics", got)
	}
}
