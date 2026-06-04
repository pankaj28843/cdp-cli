package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestClickJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "main", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		Click struct {
			Selector string `json:"selector"`
			Count    int    `json:"count"`
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
		} `json:"click"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "clicked" || got.Target.ID != "page-1" || got.Click.Selector != "main" || got.Click.Count != 1 || !got.Click.Clicked || got.Click.Strategy != "dom" {
		t.Fatalf("click output = %+v, want DOM clicked main", got)
	}
}

func TestClickByRoleLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "Search", "--by", "role", "--role", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click by role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Role    string `json:"role"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Role         string `json:"role"`
			} `json:"matches"`
		} `json:"locator"`
		Click struct {
			Selector string `json:"selector"`
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
		} `json:"click"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click by role output is invalid JSON: %v", err)
	}
	if !got.OK || got.ResolvedSelector != "button#submit" || got.Locator.By != "role" || got.Locator.Query != "Search" || got.Locator.Role != "button" || !got.Locator.Strict || len(got.Locator.Matches) != 1 {
		t.Fatalf("click by role locator = %+v, want strict button locator", got)
	}
	if got.Locator.Matches[0].SelectorHint != "button#submit" || got.Locator.Matches[0].Role != "button" || got.Click.Selector != "button#submit" || !got.Click.Clicked || got.Click.Strategy != "dom" {
		t.Fatalf("click by role action = %+v, want DOM click on resolved selector", got)
	}
}

func TestClickTrialByRoleLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "Search", "--by", "role", "--role", "button", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click trial by role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Click  struct {
			Selector string `json:"selector"`
			Clicked  bool   `json:"clicked"`
			Trial    bool   `json:"trial"`
			Strategy string `json:"strategy"`
		} `json:"click"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Trial      bool     `json:"trial"`
			Required   []string `json:"required_checks"`
			Checks     struct {
				Visible struct {
					Passed bool `json:"passed"`
				} `json:"visible"`
				Stable struct {
					Passed bool `json:"passed"`
				} `json:"stable"`
				ReceivesEvents struct {
					Passed bool `json:"passed"`
				} `json:"receives_events"`
				Enabled struct {
					Passed bool `json:"passed"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click trial by role output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.Click.Selector != "button#submit" || got.Click.Clicked || !got.Click.Trial || got.Click.Strategy != "auto" {
		t.Fatalf("click trial action = %+v, want non-dispatching trial click", got)
	}
	if !got.Actionability.Actionable || !got.Actionability.Trial || len(got.Actionability.Required) != 5 || !got.Actionability.Checks.Visible.Passed || !got.Actionability.Checks.Stable.Passed || !got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.Enabled.Passed {
		t.Fatalf("click trial actionability = %+v, want passing click checks", got.Actionability)
	}
}

func TestClickRawInputVerifiedJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false, "afterTitle": "Ready Page", "afterURL": "https://example.test/ready"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "main", "--strategy", "raw-input", "--activate", "--wait-text", "Ready", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("raw click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"target"`
		BeforeTarget struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"before_target"`
		AfterTarget struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"after_target"`
		FinalTarget struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"final_target"`
		PageState struct {
			SameTarget   bool `json:"same_target"`
			URLChanged   bool `json:"url_changed"`
			TitleChanged bool `json:"title_changed"`
		} `json:"page_state"`
		Click struct {
			Clicked    bool    `json:"clicked"`
			Strategy   string  `json:"strategy"`
			X          float64 `json:"x"`
			Y          float64 `json:"y"`
			Verified   *bool   `json:"verified"`
			FinalTitle string  `json:"final_title"`
			FinalURL   string  `json:"final_url"`
		} `json:"click"`
		Verification struct {
			Matched bool `json:"matched"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("raw click output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Click.Clicked || got.Click.Strategy != "raw-input" || got.Click.X != 310 || got.Click.Y != 120 || got.Click.Verified == nil || !*got.Click.Verified || !got.Verification.Matched {
		t.Fatalf("raw click = %+v, want verified raw-input click", got)
	}
	if got.Target.Title != "Ready Page" || got.Target.URL != "https://example.test/ready" || got.BeforeTarget.URL != "https://example.test/app" || got.AfterTarget.URL != got.Target.URL || got.FinalTarget.URL != got.Target.URL || got.Click.FinalURL != got.Target.URL || got.Click.FinalTitle != got.Target.Title {
		t.Fatalf("raw click final target = %+v, want refreshed final metadata", got)
	}
	if !got.PageState.SameTarget || !got.PageState.URLChanged || !got.PageState.TitleChanged {
		t.Fatalf("raw click page state = %+v, want same target with changed url/title", got.PageState)
	}
}

func TestClickVerificationTimeoutJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--timeout", "500ms", "click", "main", "--strategy", "raw-input", "--wait-text", "Never Ready", "--poll", "10ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("unverified click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Click struct {
			Clicked  bool  `json:"clicked"`
			Verified *bool `json:"verified"`
		} `json:"click"`
		Verification struct {
			Matched bool `json:"matched"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unverified click output is invalid JSON: %v", err)
	}
	if got.OK || !got.Click.Clicked || got.Click.Verified == nil || *got.Click.Verified || got.Verification.Matched {
		t.Fatalf("unverified click = %+v, want clicked but not verified", got)
	}
}

func TestClickRawInputZeroRectJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "zero", "--strategy", "raw-input", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("zero rect click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "zero width or height") {
		t.Fatalf("zero rect stdout = %s, want zero rect error", out.String())
	}
}

func TestClickDiagnosticsArtifactJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	outPath := filepath.Join(t.TempDir(), "click.local.json")
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "main", "--strategy", "raw-input", "--wait-selector", "main", "--diagnostics-out", outPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("diagnostic click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
		Diagnostics struct {
			Selector    string `json:"selector"`
			Strategy    string `json:"strategy"`
			AfterTarget struct {
				URL string `json:"url"`
			} `json:"after_target"`
			PageState struct {
				SameTarget bool `json:"same_target"`
			} `json:"page_state"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("diagnostic click output is invalid JSON: %v", err)
	}
	if !got.OK || got.Artifact.Path != outPath || got.Diagnostics.Selector != "main" || got.Diagnostics.Strategy != "raw-input" || got.Diagnostics.AfterTarget.URL != "https://example.test/app" || !got.Diagnostics.PageState.SameTarget {
		t.Fatalf("diagnostic click = %+v, want artifact metadata", got)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("click diagnostics artifact was not written: %v", err)
	}
}

func TestFillByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"fill", "Search", "typed value", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("fill by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Locator          struct {
			By      string `json:"by"`
			Query   string `json:"query"`
			Strict  bool   `json:"strict"`
			Matches []struct {
				SelectorHint string `json:"selector_hint"`
				Tag          string `json:"tag"`
			} `json:"matches"`
		} `json:"locator"`
		Fill struct {
			Selector string `json:"selector"`
			Filled   bool   `json:"filled"`
			Value    string `json:"value"`
			Previous string `json:"previous"`
		} `json:"fill"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("fill by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "filled" || got.ResolvedSelector != "input#q" || got.Locator.By != "label" || got.Locator.Query != "Search" || !got.Locator.Strict || len(got.Locator.Matches) != 1 {
		t.Fatalf("fill by label locator = %+v, want strict label locator", got)
	}
	if got.Locator.Matches[0].SelectorHint != "input#q" || got.Locator.Matches[0].Tag != "input" || got.Fill.Selector != "input#q" || !got.Fill.Filled || got.Fill.Value != "typed value" || got.Fill.Previous != "before" {
		t.Fatalf("fill by label action = %+v, want fill on resolved selector", got)
	}
}

func TestFillTrialByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"fill", "Search", "trial value", "--by", "label", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("fill trial by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Fill   struct {
			Selector string `json:"selector"`
			Filled   bool   `json:"filled"`
			Trial    bool   `json:"trial"`
			Value    string `json:"value"`
		} `json:"fill"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Trial      bool `json:"trial"`
			Checks     struct {
				Visible struct {
					Passed bool `json:"passed"`
				} `json:"visible"`
				Enabled struct {
					Passed bool `json:"passed"`
				} `json:"enabled"`
				Editable struct {
					Passed bool `json:"passed"`
				} `json:"editable"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("fill trial by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.Fill.Selector != "input#q" || got.Fill.Filled || !got.Fill.Trial || got.Fill.Value != "trial value" {
		t.Fatalf("fill trial action = %+v, want non-mutating trial fill", got)
	}
	if !got.Actionability.Actionable || !got.Actionability.Trial || !got.Actionability.Checks.Visible.Passed || !got.Actionability.Checks.Enabled.Passed || !got.Actionability.Checks.Editable.Passed {
		t.Fatalf("fill trial actionability = %+v, want passing fill checks", got.Actionability)
	}
}

func TestFillTrialReadonlyFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"fill", "Read-only notes", "trial value", "--by", "label", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("fill trial readonly exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Fill   struct {
				Filled bool `json:"filled"`
				Trial  bool `json:"trial"`
			} `json:"fill"`
			Actionability struct {
				Actionable bool `json:"actionable"`
				Trial      bool `json:"trial"`
				Checks     struct {
					Editable struct {
						Passed  bool   `json:"passed"`
						Message string `json:"message"`
					} `json:"editable"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("fill trial readonly output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "trial" || got.Data.Fill.Filled || !got.Data.Fill.Trial || got.Data.Actionability.Actionable || !got.Data.Actionability.Trial || got.Data.Actionability.Checks.Editable.Passed || got.Data.Actionability.Checks.Editable.Message == "" {
		t.Fatalf("fill trial readonly = %+v, want failed editable actionability", got)
	}
}

func TestActionLocatorRoleRequiresRoleFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "Submit", "--by", "role", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("click role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "--role is required") {
		t.Fatalf("click role error = %s, want --role guidance", out.String())
	}
}

func TestTypeContentEditableUsesInsertTextJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"type", "[contenteditable=true]", "hello rich editor", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("type contenteditable exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Type struct {
			Selector string `json:"selector"`
			Typing   bool   `json:"typing"`
			Typed    string `json:"typed"`
			Value    string `json:"value"`
			Kind     string `json:"kind"`
			Strategy string `json:"strategy"`
		} `json:"type"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("type contenteditable output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Type.Typing || got.Type.Strategy != "insert-text" || got.Type.Kind != "contenteditable" || got.Type.Value != "beforehello rich editor" {
		t.Fatalf("type contenteditable = %+v, want insert-text strategy and resulting text", got)
	}
}

func TestInsertTextCommandJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"insert-text", "[contenteditable=true]", " inserted", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("insert-text exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK         bool `json:"ok"`
		InsertText struct {
			Typing   bool   `json:"typing"`
			Value    string `json:"value"`
			Strategy string `json:"strategy"`
		} `json:"insert_text"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("insert-text output is invalid JSON: %v", err)
	}
	if !got.OK || !got.InsertText.Typing || got.InsertText.Strategy != "insert-text" || got.InsertText.Value != "before inserted" {
		t.Fatalf("insert-text = %+v, want inserted rich text", got)
	}
}
