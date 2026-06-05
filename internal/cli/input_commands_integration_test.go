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

func TestClickWaitPopupJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "opener-page", "type": "page", "title": "Login App", "url": "https://example.test/login", "attached": false, "popupOnClick": true, "popupTargetId": "click-popup-page", "popupTitle": "Click Popup", "popupURL": "https://example.test/click-popup"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "a#oauth", "--target", "opener-page", "--wait-popup", "--wait-popup-url", "/click-popup", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click wait popup exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Click  struct {
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
			Verified *bool  `json:"verified"`
		} `json:"click"`
		PopupWait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			BaselineCount int    `json:"baseline_count"`
			EventCount    int    `json:"event_count"`
			ObservedCount int    `json:"observed_count"`
			Criteria      struct {
				OpenerID    string `json:"opener_id"`
				URLContains string `json:"url_contains"`
			} `json:"criteria"`
		} `json:"popup_wait"`
		Popup struct {
			Target struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				URL      string `json:"url"`
				OpenerID string `json:"opener_id"`
			} `json:"target"`
			NewTarget     bool `json:"new_target"`
			OpenerMatched bool `json:"opener_matched"`
		} `json:"popup"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click wait popup output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "clicked" || !got.Click.Clicked || got.Click.Strategy != "raw-input" || got.Click.Verified == nil || !*got.Click.Verified {
		t.Fatalf("click wait popup action = %+v, want raw-input clicked and verified", got)
	}
	if got.PopupWait.Kind != "popup" || !got.PopupWait.Matched || got.PopupWait.BaselineCount != 1 || got.PopupWait.EventCount == 0 || got.PopupWait.ObservedCount == 0 || got.PopupWait.Criteria.OpenerID != "opener-page" || got.PopupWait.Criteria.URLContains != "/click-popup" {
		t.Fatalf("click wait popup wait = %+v, want matched popup wait evidence", got.PopupWait)
	}
	if got.Popup.Target.ID != "click-popup-page" || got.Popup.Target.Title != "Click Popup" || got.Popup.Target.URL != "https://example.test/click-popup" || got.Popup.Target.OpenerID != "opener-page" || !got.Popup.NewTarget || !got.Popup.OpenerMatched {
		t.Fatalf("click wait popup event = %+v, want click-created popup target", got.Popup)
	}
	if !containsString(got.NextCommands, "cdp page select --target click-popup-page --json") {
		t.Fatalf("click wait popup next commands = %+v, want popup follow-up commands", got.NextCommands)
	}
}

func TestClickWaitDownloadJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "download-page", "type": "page", "title": "Download App", "url": "https://example.test/downloads", "attached": false, "downloadOnClick": true, "downloadURL": "https://example.test/download/click-report.csv?token=abc", "downloadFilename": "click-report.csv", "downloadFilePath": "/tmp/cdp-downloads/click-download-1"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	downloadDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "a#download", "--target", "download-page", "--wait-download", "--wait-download-url", "/download/click-report.csv", "--wait-download-filename", "click-report", "--download-dir", downloadDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click wait download exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Click struct {
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
			Verified *bool  `json:"verified"`
		} `json:"click"`
		DownloadWait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			DownloadDir   string `json:"download_dir"`
			EventCount    int    `json:"event_count"`
			ObservedCount int    `json:"observed_count"`
			Criteria      struct {
				URLContains      string `json:"url_contains"`
				FilenameContains string `json:"filename_contains"`
				State            string `json:"state"`
			} `json:"criteria"`
		} `json:"download_wait"`
		DownloadEvent struct {
			Kind              string `json:"kind"`
			GUID              string `json:"guid"`
			URL               string `json:"url"`
			SuggestedFilename string `json:"suggested_filename"`
		} `json:"download_event"`
		DownloadProgress struct {
			State         string  `json:"state"`
			TotalBytes    float64 `json:"total_bytes"`
			ReceivedBytes float64 `json:"received_bytes"`
			FilePath      string  `json:"file_path"`
		} `json:"download_progress"`
		Download struct {
			GUID              string  `json:"guid"`
			URL               string  `json:"url"`
			SuggestedFilename string  `json:"suggested_filename"`
			State             string  `json:"state"`
			Completed         bool    `json:"completed"`
			ReceivedBytes     float64 `json:"received_bytes"`
		} `json:"download"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click wait download output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Click.Clicked || got.Click.Strategy != "raw-input" || got.Click.Verified == nil || !*got.Click.Verified {
		t.Fatalf("click wait download action = %+v, want raw-input clicked and verified", got)
	}
	if got.DownloadWait.Kind != "download" || !got.DownloadWait.Matched || got.DownloadWait.DownloadDir != downloadDir || got.DownloadWait.EventCount == 0 || got.DownloadWait.ObservedCount != 1 || got.DownloadWait.Criteria.URLContains != "/download/click-report.csv" || got.DownloadWait.Criteria.FilenameContains != "click-report" || got.DownloadWait.Criteria.State != "completed" {
		t.Fatalf("click wait download wait = %+v, want matched download evidence", got.DownloadWait)
	}
	if got.DownloadEvent.Kind != "will-begin" || got.DownloadEvent.GUID != "click-download-1" || got.DownloadEvent.SuggestedFilename != "click-report.csv" || strings.Contains(got.DownloadEvent.URL, "token=abc") {
		t.Fatalf("click wait download event = %+v, want redacted will-begin event", got.DownloadEvent)
	}
	if got.DownloadProgress.State != "completed" || got.DownloadProgress.TotalBytes != 24 || got.DownloadProgress.ReceivedBytes != 24 || got.DownloadProgress.FilePath != "/tmp/cdp-downloads/click-download-1" {
		t.Fatalf("click wait download progress = %+v, want completed progress", got.DownloadProgress)
	}
	if got.Download.GUID != "click-download-1" || got.Download.SuggestedFilename != "click-report.csv" || got.Download.State != "completed" || !got.Download.Completed || got.Download.ReceivedBytes != 24 || strings.Contains(got.Download.URL, "token=abc") {
		t.Fatalf("click wait download summary = %+v, want completed redacted download", got.Download)
	}
	if len(got.NextCommands) == 0 || !strings.HasPrefix(got.NextCommands[0], "ls -lah ") {
		t.Fatalf("click wait download next commands = %+v, want download directory listing", got.NextCommands)
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

func TestClickActionabilityFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "button#covered", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("covered click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Click  struct {
				Clicked bool `json:"clicked"`
				Force   bool `json:"force"`
			} `json:"click"`
			Actionability struct {
				Actionable bool `json:"actionable"`
				Force      bool `json:"force"`
				Checks     struct {
					ReceivesEvents struct {
						Required bool   `json:"required"`
						Passed   bool   `json:"passed"`
						Skipped  bool   `json:"skipped"`
						Message  string `json:"message"`
					} `json:"receives_events"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("covered click output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "blocked" || got.Data.Click.Clicked || got.Data.Click.Force || got.Data.Actionability.Actionable || got.Data.Actionability.Force || !got.Data.Actionability.Checks.ReceivesEvents.Required || got.Data.Actionability.Checks.ReceivesEvents.Passed || got.Data.Actionability.Checks.ReceivesEvents.Skipped || got.Data.Actionability.Checks.ReceivesEvents.Message == "" {
		t.Fatalf("covered click = %+v, want failed receives-events actionability", got)
	}
}

func TestClickAutoScrollsOffscreenTargetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "scroll-target", "--by", "test-id", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("offscreen click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Click            struct {
			Selector string `json:"selector"`
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
		} `json:"click"`
		AutoScroll struct {
			Selector string `json:"selector"`
			Scrolled bool   `json:"scrolled"`
			Changed  bool   `json:"changed"`
			Block    string `json:"block"`
			Inline   string `json:"inline"`
			Before   struct {
				InViewport bool `json:"in_viewport"`
			} `json:"before"`
			After struct {
				InViewport bool `json:"in_viewport"`
			} `json:"after"`
		} `json:"auto_scroll"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Checks     struct {
				ReceivesEvents struct {
					Passed bool `json:"passed"`
				} `json:"receives_events"`
				InViewport struct {
					Passed bool `json:"passed"`
				} `json:"in_viewport"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("offscreen click output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "clicked" || got.ResolvedSelector != "div#scroll-target" || got.Click.Selector != "div#scroll-target" || !got.Click.Clicked || got.Click.Strategy != "dom" {
		t.Fatalf("offscreen click action = %+v, want clicked resolved target", got)
	}
	if got.AutoScroll.Selector != "div#scroll-target" || !got.AutoScroll.Scrolled || !got.AutoScroll.Changed || got.AutoScroll.Block != "center" || got.AutoScroll.Inline != "nearest" || got.AutoScroll.Before.InViewport || !got.AutoScroll.After.InViewport {
		t.Fatalf("offscreen click auto_scroll = %+v, want before/after scroll evidence", got.AutoScroll)
	}
	if !got.Actionability.Actionable || !got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.InViewport.Passed {
		t.Fatalf("offscreen click actionability = %+v, want rechecked actionability after auto-scroll", got.Actionability)
	}
}

func TestClickForceSkipsReceivesEventsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "button#covered", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("force covered click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Click  struct {
			Clicked bool `json:"clicked"`
			Force   bool `json:"force"`
		} `json:"click"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Force      bool     `json:"force"`
			Skipped    []string `json:"skipped_checks"`
			Checks     struct {
				ReceivesEvents struct {
					Required bool   `json:"required"`
					Passed   bool   `json:"passed"`
					Skipped  bool   `json:"skipped"`
					Message  string `json:"message"`
				} `json:"receives_events"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("force covered click output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "clicked" || !got.Click.Clicked || !got.Click.Force || !got.Actionability.Actionable || !got.Actionability.Force || !containsString(got.Actionability.Skipped, "receives_events") || got.Actionability.Checks.ReceivesEvents.Required || got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.ReceivesEvents.Skipped || !strings.Contains(got.Actionability.Checks.ReceivesEvents.Message, "--force") {
		t.Fatalf("force covered click = %+v, want receives-events skipped by force", got)
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

func TestFillActionabilityFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"fill", "Read-only notes", "typed value", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("fill readonly exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Fill   struct {
				Filled bool `json:"filled"`
				Force  bool `json:"force"`
			} `json:"fill"`
			Actionability struct {
				Actionable bool `json:"actionable"`
				Force      bool `json:"force"`
				Checks     struct {
					Editable struct {
						Required bool   `json:"required"`
						Passed   bool   `json:"passed"`
						Message  string `json:"message"`
					} `json:"editable"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("fill readonly output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "blocked" || got.Data.Fill.Filled || got.Data.Fill.Force || got.Data.Actionability.Actionable || got.Data.Actionability.Force || !got.Data.Actionability.Checks.Editable.Required || got.Data.Actionability.Checks.Editable.Passed || got.Data.Actionability.Checks.Editable.Message == "" {
		t.Fatalf("fill readonly = %+v, want blocked editable actionability", got)
	}
}

func TestFillForceSkipsVisibleJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"fill", "input#hidden-field", "forced value", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("force hidden fill exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Fill   struct {
			Filled bool `json:"filled"`
			Force  bool `json:"force"`
		} `json:"fill"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Force      bool     `json:"force"`
			Skipped    []string `json:"skipped_checks"`
			Checks     struct {
				Visible struct {
					Required bool   `json:"required"`
					Passed   bool   `json:"passed"`
					Skipped  bool   `json:"skipped"`
					Message  string `json:"message"`
				} `json:"visible"`
				Editable struct {
					Passed bool `json:"passed"`
				} `json:"editable"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("force hidden fill output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "filled" || !got.Fill.Filled || !got.Fill.Force || !got.Actionability.Actionable || !got.Actionability.Force || !containsString(got.Actionability.Skipped, "visible") || got.Actionability.Checks.Visible.Required || got.Actionability.Checks.Visible.Passed || !got.Actionability.Checks.Visible.Skipped || !strings.Contains(got.Actionability.Checks.Visible.Message, "--force") || !got.Actionability.Checks.Editable.Passed {
		t.Fatalf("force hidden fill = %+v, want visible skipped and editable enforced", got)
	}
}

func TestFillForceReadonlyStillFailsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"fill", "Read-only notes", "typed value", "--by", "label", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("force readonly fill exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Actionability struct {
				Actionable bool     `json:"actionable"`
				Force      bool     `json:"force"`
				Skipped    []string `json:"skipped_checks"`
				Checks     struct {
					Editable struct {
						Required bool `json:"required"`
						Passed   bool `json:"passed"`
						Skipped  bool `json:"skipped"`
					} `json:"editable"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("force readonly fill output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Actionability.Actionable || !got.Data.Actionability.Force || !containsString(got.Data.Actionability.Skipped, "visible") || !got.Data.Actionability.Checks.Editable.Required || got.Data.Actionability.Checks.Editable.Passed || got.Data.Actionability.Checks.Editable.Skipped {
		t.Fatalf("force readonly fill = %+v, want editable still enforced", got)
	}
}

func TestSelectByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"select", "Plan", "pro", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("select by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
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
		Select struct {
			Selector string `json:"selector"`
			Selected bool   `json:"selected"`
			Value    string `json:"value"`
			Previous string `json:"previous"`
		} `json:"select"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Required   []string `json:"required_checks"`
			Checks     struct {
				Stable struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"stable"`
				ReceivesEvents struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"receives_events"`
				Enabled struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("select by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "selected" || got.ResolvedSelector != "select#plan" || got.Locator.By != "label" || got.Locator.Query != "Plan" || !got.Locator.Strict || len(got.Locator.Matches) != 1 {
		t.Fatalf("select by label locator = %+v, want strict label locator", got)
	}
	if got.Locator.Matches[0].SelectorHint != "select#plan" || got.Locator.Matches[0].Tag != "select" || got.Select.Selector != "select#plan" || !got.Select.Selected || got.Select.Value != "pro" || got.Select.Previous != "free" || !got.Actionability.Actionable || len(got.Actionability.Required) != 3 || containsString(got.Actionability.Required, "stable") || containsString(got.Actionability.Required, "receives_events") || got.Actionability.Checks.Stable.Required || !got.Actionability.Checks.Stable.Skipped || got.Actionability.Checks.ReceivesEvents.Required || !got.Actionability.Checks.ReceivesEvents.Skipped || !got.Actionability.Checks.Enabled.Required || !got.Actionability.Checks.Enabled.Passed {
		t.Fatalf("select by label action = %+v, want select with visible/enabled actionability", got)
	}
}

func TestSelectTrialByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"select", "Plan", "pro", "--by", "label", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("select trial by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Select struct {
			Selector string `json:"selector"`
			Selected bool   `json:"selected"`
			Trial    bool   `json:"trial"`
			Value    string `json:"value"`
		} `json:"select"`
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
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("select trial by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.Select.Selector != "select#plan" || got.Select.Selected || !got.Select.Trial || got.Select.Value != "pro" || !got.Actionability.Actionable || !got.Actionability.Trial || !got.Actionability.Checks.Visible.Passed || !got.Actionability.Checks.Enabled.Passed {
		t.Fatalf("select trial = %+v, want non-mutating select trial", got)
	}
}

func TestSelectActionabilityFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"select", "select#disabled-plan", "pro", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("disabled select exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Select struct {
				Selected bool `json:"selected"`
				Force    bool `json:"force"`
			} `json:"select"`
			Actionability struct {
				Actionable bool `json:"actionable"`
				Force      bool `json:"force"`
				Checks     struct {
					Enabled struct {
						Required bool   `json:"required"`
						Passed   bool   `json:"passed"`
						Message  string `json:"message"`
					} `json:"enabled"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("disabled select output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "blocked" || got.Data.Select.Selected || got.Data.Select.Force || got.Data.Actionability.Actionable || got.Data.Actionability.Force || !got.Data.Actionability.Checks.Enabled.Required || got.Data.Actionability.Checks.Enabled.Passed || got.Data.Actionability.Checks.Enabled.Message == "" {
		t.Fatalf("disabled select = %+v, want blocked enabled actionability", got)
	}
}

func TestSelectForceSkipsVisibleJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"select", "select#hidden-plan", "pro", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("force hidden select exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Select struct {
			Selected bool `json:"selected"`
			Force    bool `json:"force"`
		} `json:"select"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Force      bool     `json:"force"`
			Skipped    []string `json:"skipped_checks"`
			Checks     struct {
				Visible struct {
					Required bool   `json:"required"`
					Passed   bool   `json:"passed"`
					Skipped  bool   `json:"skipped"`
					Message  string `json:"message"`
				} `json:"visible"`
				Enabled struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("force hidden select output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "selected" || !got.Select.Selected || !got.Select.Force || !got.Actionability.Actionable || !got.Actionability.Force || !containsString(got.Actionability.Skipped, "visible") || got.Actionability.Checks.Visible.Required || got.Actionability.Checks.Visible.Passed || !got.Actionability.Checks.Visible.Skipped || !strings.Contains(got.Actionability.Checks.Visible.Message, "--force") || !got.Actionability.Checks.Enabled.Required || !got.Actionability.Checks.Enabled.Passed {
		t.Fatalf("force hidden select = %+v, want visible skipped and enabled enforced", got)
	}
}

func TestFileByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	uploadPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"file", "Upload file", uploadPath, "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("file by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
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
				Type         string `json:"type"`
			} `json:"matches"`
		} `json:"locator"`
		File struct {
			Selector       string `json:"selector"`
			Accepted       bool   `json:"accepted"`
			FileSet        bool   `json:"file_set"`
			Trial          bool   `json:"trial"`
			Path           string `json:"path"`
			FileName       string `json:"file_name"`
			ContentOmitted bool   `json:"content_omitted"`
			Tag            string `json:"tag"`
			Type           string `json:"type"`
		} `json:"file"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Required   []string `json:"required_checks"`
			Checks     struct {
				Attached struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"attached"`
				Visible struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"visible"`
				Enabled struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("file by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "file_set" || got.ResolvedSelector != "input#upload" || got.Locator.By != "label" || got.Locator.Query != "Upload file" || !got.Locator.Strict || len(got.Locator.Matches) != 1 {
		t.Fatalf("file by label locator = %+v, want strict upload label locator", got)
	}
	if got.Locator.Matches[0].SelectorHint != "input#upload" || got.Locator.Matches[0].Tag != "input" || got.Locator.Matches[0].Type != "file" || got.File.Selector != "input#upload" || !got.File.Accepted || !got.File.FileSet || got.File.Trial || got.File.Path != uploadPath || got.File.FileName != "upload.txt" || !got.File.ContentOmitted || got.File.Tag != "input" || got.File.Type != "file" {
		t.Fatalf("file result = %+v, want accepted file input without content", got.File)
	}
	if !got.Actionability.Actionable || len(got.Actionability.Required) != 1 || got.Actionability.Required[0] != "attached" || !got.Actionability.Checks.Attached.Required || !got.Actionability.Checks.Attached.Passed || got.Actionability.Checks.Visible.Required || !got.Actionability.Checks.Visible.Skipped || got.Actionability.Checks.Enabled.Required || !got.Actionability.Checks.Enabled.Skipped {
		t.Fatalf("file actionability = %+v, want attached-only evidence", got.Actionability)
	}
}

func TestFileTrialByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	uploadPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"file", "Upload file", uploadPath, "--by", "label", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("file trial exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		File   struct {
			Selector string `json:"selector"`
			Accepted bool   `json:"accepted"`
			FileSet  bool   `json:"file_set"`
			Trial    bool   `json:"trial"`
		} `json:"file"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Trial      bool `json:"trial"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("file trial output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.File.Selector != "input#upload" || !got.File.Accepted || got.File.FileSet || !got.File.Trial || !got.Actionability.Actionable || !got.Actionability.Trial {
		t.Fatalf("file trial = %+v, want non-mutating file-input evidence", got)
	}
}

func TestFileInvalidTargetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	uploadPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"file", "button#submit", uploadPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("invalid file target exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	var got struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		Class string `json:"err_class"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid file target output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "invalid_target" || got.Class != "usage" {
		t.Fatalf("invalid file target = %+v, want invalid_target usage", got)
	}
}

func TestFileHiddenInputUsesAttachedOnlyActionability(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	uploadPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"file", "#hidden-upload", uploadPath, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("hidden file input exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		File   struct {
			FileSet bool `json:"file_set"`
		} `json:"file"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Checks     struct {
				Visible struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
					Skipped  bool `json:"skipped"`
				} `json:"visible"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("hidden file input output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "file_set" || !got.File.FileSet || !got.Actionability.Actionable || got.Actionability.Checks.Visible.Required || got.Actionability.Checks.Visible.Passed || !got.Actionability.Checks.Visible.Skipped {
		t.Fatalf("hidden file input = %+v, want attached-only file actionability", got)
	}
}

func TestScrollTrialByTestIDLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"scroll", "scroll-target", "--by", "test-id", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("scroll trial exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
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
			} `json:"matches"`
		} `json:"locator"`
		Scroll struct {
			Selector string `json:"selector"`
			Scrolled bool   `json:"scrolled"`
			Changed  bool   `json:"changed"`
			Trial    bool   `json:"trial"`
			Block    string `json:"block"`
			Inline   string `json:"inline"`
			Before   struct {
				InViewport bool `json:"in_viewport"`
			} `json:"before"`
			After struct {
				InViewport bool `json:"in_viewport"`
			} `json:"after"`
		} `json:"scroll"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Trial      bool     `json:"trial"`
			Required   []string `json:"required_checks"`
			Checks     struct {
				Stable struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"stable"`
				Visible struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"visible"`
				InViewport struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
					Skipped  bool `json:"skipped"`
				} `json:"in_viewport"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("scroll trial output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.ResolvedSelector != "div#scroll-target" || got.Locator.By != "test-id" || got.Locator.Query != "scroll-target" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || got.Locator.Matches[0].SelectorHint != "div#scroll-target" {
		t.Fatalf("scroll trial locator = %+v, want strict test-id locator", got)
	}
	if got.Scroll.Selector != "div#scroll-target" || got.Scroll.Scrolled || got.Scroll.Changed || !got.Scroll.Trial || got.Scroll.Block != "center" || got.Scroll.Inline != "nearest" || got.Scroll.Before.InViewport || got.Scroll.After.InViewport || !got.Actionability.Actionable || !got.Actionability.Trial || len(got.Actionability.Required) != 2 || !containsString(got.Actionability.Required, "stable") || !got.Actionability.Checks.Stable.Required || !got.Actionability.Checks.Stable.Passed || got.Actionability.Checks.Visible.Required || !got.Actionability.Checks.Visible.Skipped || got.Actionability.Checks.InViewport.Required || got.Actionability.Checks.InViewport.Passed || !got.Actionability.Checks.InViewport.Skipped {
		t.Fatalf("scroll trial = %+v, want non-mutating stable target evidence", got)
	}
}

func TestScrollByTestIDLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"scroll", "scroll-target", "--by", "test-id", "--block", "end", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("scroll exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Scroll struct {
			Selector string `json:"selector"`
			Scrolled bool   `json:"scrolled"`
			Changed  bool   `json:"changed"`
			Trial    bool   `json:"trial"`
			Block    string `json:"block"`
			Inline   string `json:"inline"`
			Before   struct {
				InViewport bool    `json:"in_viewport"`
				ScrollY    float64 `json:"scroll_y"`
			} `json:"before"`
			After struct {
				InViewport      bool    `json:"in_viewport"`
				FullyInViewport bool    `json:"fully_in_viewport"`
				ScrollY         float64 `json:"scroll_y"`
			} `json:"after"`
		} `json:"scroll"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Required   []string `json:"required_checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("scroll output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "scrolled" || got.Scroll.Selector != "div#scroll-target" || !got.Scroll.Scrolled || !got.Scroll.Changed || got.Scroll.Trial || got.Scroll.Block != "end" || got.Scroll.Inline != "nearest" || got.Scroll.Before.InViewport || got.Scroll.Before.ScrollY != 0 || !got.Scroll.After.InViewport || !got.Scroll.After.FullyInViewport || got.Scroll.After.ScrollY <= got.Scroll.Before.ScrollY || !got.Actionability.Actionable || len(got.Actionability.Required) != 2 || !containsString(got.Actionability.Required, "stable") {
		t.Fatalf("scroll = %+v, want viewport-changing scroll evidence", got)
	}
}

func TestScrollActionabilityFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"scroll", "#moving-target", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("moving scroll exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Scroll struct {
				Scrolled bool `json:"scrolled"`
				Trial    bool `json:"trial"`
			} `json:"scroll"`
			Actionability struct {
				Actionable bool `json:"actionable"`
				Checks     struct {
					Stable struct {
						Required bool   `json:"required"`
						Passed   bool   `json:"passed"`
						Message  string `json:"message"`
					} `json:"stable"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("moving scroll output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "blocked" || got.Data.Scroll.Scrolled || got.Data.Scroll.Trial || got.Data.Actionability.Actionable || !got.Data.Actionability.Checks.Stable.Required || got.Data.Actionability.Checks.Stable.Passed || got.Data.Actionability.Checks.Stable.Message == "" {
		t.Fatalf("moving scroll = %+v, want failed stable actionability", got)
	}
}

func TestCheckByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"check", "Subscribe to newsletter", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("check by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
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
				Role         string `json:"role"`
			} `json:"matches"`
		} `json:"locator"`
		Check struct {
			Selector       string `json:"selector"`
			Checked        bool   `json:"checked"`
			DesiredChecked bool   `json:"desired_checked"`
			Previous       bool   `json:"previous_checked"`
			Changed        bool   `json:"changed"`
			Type           string `json:"type"`
			Role           string `json:"role"`
		} `json:"check"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Required   []string `json:"required_checks"`
			Checks     struct {
				Stable struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"stable"`
				ReceivesEvents struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"receives_events"`
				Enabled struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("check by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "checked" || got.ResolvedSelector != "input#subscribe" || got.Locator.By != "label" || got.Locator.Query != "Subscribe to newsletter" || !got.Locator.Strict || len(got.Locator.Matches) != 1 {
		t.Fatalf("check by label locator = %+v, want strict label locator", got)
	}
	if got.Locator.Matches[0].SelectorHint != "input#subscribe" || got.Locator.Matches[0].Role != "checkbox" || got.Check.Selector != "input#subscribe" || !got.Check.Checked || !got.Check.DesiredChecked || got.Check.Previous || !got.Check.Changed || got.Check.Type != "checkbox" || got.Check.Role != "checkbox" {
		t.Fatalf("check by label action = %+v, want checked checkbox", got.Check)
	}
	if !got.Actionability.Actionable || len(got.Actionability.Required) != 5 || !containsString(got.Actionability.Required, "stable") || !containsString(got.Actionability.Required, "receives_events") || !got.Actionability.Checks.Stable.Required || !got.Actionability.Checks.Stable.Passed || !got.Actionability.Checks.ReceivesEvents.Required || !got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.Enabled.Required || !got.Actionability.Checks.Enabled.Passed {
		t.Fatalf("check actionability = %+v, want click-like actionability", got.Actionability)
	}
}

func TestUncheckByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"uncheck", "Subscribe to newsletter", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("uncheck by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Uncheck          struct {
			Selector       string `json:"selector"`
			Checked        bool   `json:"checked"`
			DesiredChecked bool   `json:"desired_checked"`
			Previous       bool   `json:"previous_checked"`
			Changed        bool   `json:"changed"`
		} `json:"uncheck"`
		Actionability struct {
			Actionable bool `json:"actionable"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("uncheck by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "unchecked" || got.ResolvedSelector != "input#subscribe" || got.Uncheck.Selector != "input#subscribe" || got.Uncheck.Checked || got.Uncheck.DesiredChecked || !got.Uncheck.Previous || !got.Uncheck.Changed || !got.Actionability.Actionable {
		t.Fatalf("uncheck by label = %+v, want unchecked checkbox", got)
	}
}

func TestCheckAutoScrollsOffscreenTargetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"check", "Below fold checkbox", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("offscreen check exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Check            struct {
			Selector       string `json:"selector"`
			Checked        bool   `json:"checked"`
			DesiredChecked bool   `json:"desired_checked"`
			Previous       bool   `json:"previous_checked"`
			Changed        bool   `json:"changed"`
		} `json:"check"`
		AutoScroll struct {
			Selector string `json:"selector"`
			Scrolled bool   `json:"scrolled"`
			Changed  bool   `json:"changed"`
			Block    string `json:"block"`
			Inline   string `json:"inline"`
			Before   struct {
				InViewport bool `json:"in_viewport"`
			} `json:"before"`
			After struct {
				InViewport bool `json:"in_viewport"`
			} `json:"after"`
		} `json:"auto_scroll"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Checks     struct {
				ReceivesEvents struct {
					Passed bool `json:"passed"`
				} `json:"receives_events"`
				InViewport struct {
					Passed bool `json:"passed"`
				} `json:"in_viewport"`
				Enabled struct {
					Passed bool `json:"passed"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("offscreen check output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "checked" || got.ResolvedSelector != "input#below-fold-checkbox" || got.Check.Selector != "input#below-fold-checkbox" || !got.Check.Checked || !got.Check.DesiredChecked || got.Check.Previous || !got.Check.Changed {
		t.Fatalf("offscreen check action = %+v, want checked resolved checkbox", got)
	}
	if got.AutoScroll.Selector != "input#below-fold-checkbox" || !got.AutoScroll.Scrolled || !got.AutoScroll.Changed || got.AutoScroll.Block != "center" || got.AutoScroll.Inline != "nearest" || got.AutoScroll.Before.InViewport || !got.AutoScroll.After.InViewport {
		t.Fatalf("offscreen check auto_scroll = %+v, want before/after scroll evidence", got.AutoScroll)
	}
	if !got.Actionability.Actionable || !got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.InViewport.Passed || !got.Actionability.Checks.Enabled.Passed {
		t.Fatalf("offscreen check actionability = %+v, want rechecked actionability after auto-scroll", got.Actionability)
	}
}

func TestUncheckAutoScrollsOffscreenTargetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"uncheck", "Below fold checkbox", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("offscreen uncheck exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Uncheck          struct {
			Selector       string `json:"selector"`
			Checked        bool   `json:"checked"`
			DesiredChecked bool   `json:"desired_checked"`
			Previous       bool   `json:"previous_checked"`
			Changed        bool   `json:"changed"`
		} `json:"uncheck"`
		AutoScroll struct {
			Selector string `json:"selector"`
			Scrolled bool   `json:"scrolled"`
			Changed  bool   `json:"changed"`
			Block    string `json:"block"`
			Inline   string `json:"inline"`
			Before   struct {
				InViewport bool `json:"in_viewport"`
			} `json:"before"`
			After struct {
				InViewport bool `json:"in_viewport"`
			} `json:"after"`
		} `json:"auto_scroll"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Checks     struct {
				ReceivesEvents struct {
					Passed bool `json:"passed"`
				} `json:"receives_events"`
				InViewport struct {
					Passed bool `json:"passed"`
				} `json:"in_viewport"`
				Enabled struct {
					Passed bool `json:"passed"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("offscreen uncheck output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "unchecked" || got.ResolvedSelector != "input#below-fold-checkbox" || got.Uncheck.Selector != "input#below-fold-checkbox" || got.Uncheck.Checked || got.Uncheck.DesiredChecked || !got.Uncheck.Previous || !got.Uncheck.Changed {
		t.Fatalf("offscreen uncheck action = %+v, want unchecked resolved checkbox", got)
	}
	if got.AutoScroll.Selector != "input#below-fold-checkbox" || !got.AutoScroll.Scrolled || !got.AutoScroll.Changed || got.AutoScroll.Block != "center" || got.AutoScroll.Inline != "nearest" || got.AutoScroll.Before.InViewport || !got.AutoScroll.After.InViewport {
		t.Fatalf("offscreen uncheck auto_scroll = %+v, want before/after scroll evidence", got.AutoScroll)
	}
	if !got.Actionability.Actionable || !got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.InViewport.Passed || !got.Actionability.Checks.Enabled.Passed {
		t.Fatalf("offscreen uncheck actionability = %+v, want rechecked actionability after auto-scroll", got.Actionability)
	}
}

func TestCheckTrialByRoleLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"check", "Subscribe", "--by", "role", "--role", "checkbox", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("check trial by role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Check  struct {
			Selector       string `json:"selector"`
			Checked        bool   `json:"checked"`
			DesiredChecked bool   `json:"desired_checked"`
			Trial          bool   `json:"trial"`
			Changed        bool   `json:"changed"`
		} `json:"check"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Trial      bool `json:"trial"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("check trial by role output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.Check.Selector != "input#subscribe" || got.Check.Checked || !got.Check.DesiredChecked || !got.Check.Trial || got.Check.Changed || !got.Actionability.Actionable || !got.Actionability.Trial {
		t.Fatalf("check trial = %+v, want non-mutating checkbox trial", got)
	}
}

func TestCheckActionabilityFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"check", "input#disabled-checkbox", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("disabled check exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Check  struct {
				Checked bool `json:"checked"`
				Force   bool `json:"force"`
			} `json:"check"`
			Actionability struct {
				Actionable bool `json:"actionable"`
				Force      bool `json:"force"`
				Checks     struct {
					Enabled struct {
						Required bool   `json:"required"`
						Passed   bool   `json:"passed"`
						Message  string `json:"message"`
					} `json:"enabled"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("disabled check output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "blocked" || got.Data.Check.Checked || got.Data.Check.Force || got.Data.Actionability.Actionable || got.Data.Actionability.Force || !got.Data.Actionability.Checks.Enabled.Required || got.Data.Actionability.Checks.Enabled.Passed || got.Data.Actionability.Checks.Enabled.Message == "" {
		t.Fatalf("disabled check = %+v, want blocked enabled actionability", got)
	}
}

func TestCheckForceSkipsReceivesEventsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"check", "input#covered-checkbox", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("force covered check exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Check  struct {
			Checked bool `json:"checked"`
			Force   bool `json:"force"`
		} `json:"check"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Force      bool     `json:"force"`
			Skipped    []string `json:"skipped_checks"`
			Checks     struct {
				ReceivesEvents struct {
					Required bool   `json:"required"`
					Passed   bool   `json:"passed"`
					Skipped  bool   `json:"skipped"`
					Message  string `json:"message"`
				} `json:"receives_events"`
				Enabled struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("force covered check output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "checked" || !got.Check.Checked || !got.Check.Force || !got.Actionability.Actionable || !got.Actionability.Force || !containsString(got.Actionability.Skipped, "receives_events") || got.Actionability.Checks.ReceivesEvents.Required || got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.ReceivesEvents.Skipped || !strings.Contains(got.Actionability.Checks.ReceivesEvents.Message, "--force") || !got.Actionability.Checks.Enabled.Required || !got.Actionability.Checks.Enabled.Passed {
		t.Fatalf("force covered check = %+v, want receives-events skipped and enabled enforced", got)
	}
}

func TestHoverByRoleLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"hover", "Search", "--by", "role", "--role", "button", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("hover by role exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
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
		Hover struct {
			Selector string `json:"selector"`
			Hovered  bool   `json:"hovered"`
			Force    bool   `json:"force"`
		} `json:"hover"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Required   []string `json:"required_checks"`
			Checks     struct {
				Enabled struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("hover by role output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "hovered" || got.ResolvedSelector != "button#submit" || got.Locator.By != "role" || got.Locator.Query != "Search" || got.Locator.Role != "button" || !got.Locator.Strict || len(got.Locator.Matches) != 1 {
		t.Fatalf("hover by role locator = %+v, want strict button locator", got)
	}
	if got.Locator.Matches[0].SelectorHint != "button#submit" || got.Locator.Matches[0].Role != "button" || got.Hover.Selector != "button#submit" || !got.Hover.Hovered || got.Hover.Force || !got.Actionability.Actionable || len(got.Actionability.Required) != 4 || containsString(got.Actionability.Required, "enabled") || got.Actionability.Checks.Enabled.Required || !got.Actionability.Checks.Enabled.Skipped {
		t.Fatalf("hover by role action = %+v, want hover with pointer-only actionability checks", got)
	}
}

func TestHoverAutoScrollsOffscreenTargetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"hover", "scroll-target", "--by", "test-id", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("offscreen hover exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Hover            struct {
			Selector string `json:"selector"`
			Hovered  bool   `json:"hovered"`
		} `json:"hover"`
		AutoScroll struct {
			Selector string `json:"selector"`
			Scrolled bool   `json:"scrolled"`
			Changed  bool   `json:"changed"`
			Block    string `json:"block"`
			Inline   string `json:"inline"`
			Before   struct {
				InViewport bool `json:"in_viewport"`
			} `json:"before"`
			After struct {
				InViewport bool `json:"in_viewport"`
			} `json:"after"`
		} `json:"auto_scroll"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Checks     struct {
				ReceivesEvents struct {
					Passed bool `json:"passed"`
				} `json:"receives_events"`
				InViewport struct {
					Passed bool `json:"passed"`
				} `json:"in_viewport"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("offscreen hover output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "hovered" || got.ResolvedSelector != "div#scroll-target" || got.Hover.Selector != "div#scroll-target" || !got.Hover.Hovered {
		t.Fatalf("offscreen hover action = %+v, want hovered resolved target", got)
	}
	if got.AutoScroll.Selector != "div#scroll-target" || !got.AutoScroll.Scrolled || !got.AutoScroll.Changed || got.AutoScroll.Block != "center" || got.AutoScroll.Inline != "nearest" || got.AutoScroll.Before.InViewport || !got.AutoScroll.After.InViewport {
		t.Fatalf("offscreen hover auto_scroll = %+v, want before/after scroll evidence", got.AutoScroll)
	}
	if !got.Actionability.Actionable || !got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.InViewport.Passed {
		t.Fatalf("offscreen hover actionability = %+v, want rechecked actionability after auto-scroll", got.Actionability)
	}
}

func TestHoverActionabilityFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"hover", "button#covered", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("covered hover exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Hover  struct {
				Hovered bool `json:"hovered"`
				Force   bool `json:"force"`
			} `json:"hover"`
			Actionability struct {
				Actionable bool `json:"actionable"`
				Force      bool `json:"force"`
				Checks     struct {
					ReceivesEvents struct {
						Required bool   `json:"required"`
						Passed   bool   `json:"passed"`
						Skipped  bool   `json:"skipped"`
						Message  string `json:"message"`
					} `json:"receives_events"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("covered hover output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "blocked" || got.Data.Hover.Hovered || got.Data.Hover.Force || got.Data.Actionability.Actionable || got.Data.Actionability.Force || !got.Data.Actionability.Checks.ReceivesEvents.Required || got.Data.Actionability.Checks.ReceivesEvents.Passed || got.Data.Actionability.Checks.ReceivesEvents.Skipped || got.Data.Actionability.Checks.ReceivesEvents.Message == "" {
		t.Fatalf("covered hover = %+v, want failed receives-events actionability", got)
	}
}

func TestHoverForceSkipsReceivesEventsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"hover", "button#covered", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("force covered hover exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Hover  struct {
			Hovered bool `json:"hovered"`
			Force   bool `json:"force"`
		} `json:"hover"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Force      bool     `json:"force"`
			Skipped    []string `json:"skipped_checks"`
			Checks     struct {
				ReceivesEvents struct {
					Required bool   `json:"required"`
					Passed   bool   `json:"passed"`
					Skipped  bool   `json:"skipped"`
					Message  string `json:"message"`
				} `json:"receives_events"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("force covered hover output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "hovered" || !got.Hover.Hovered || !got.Hover.Force || !got.Actionability.Actionable || !got.Actionability.Force || !containsString(got.Actionability.Skipped, "receives_events") || got.Actionability.Checks.ReceivesEvents.Required || got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.ReceivesEvents.Skipped || !strings.Contains(got.Actionability.Checks.ReceivesEvents.Message, "--force") {
		t.Fatalf("force covered hover = %+v, want receives-events skipped by force", got)
	}
}

func TestDragTrialByTestIDLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"drag", "drag-target", "8", "12", "--by", "test-id", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("drag trial by test-id exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
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
			} `json:"matches"`
		} `json:"locator"`
		Drag struct {
			Selector string  `json:"selector"`
			Dragged  bool    `json:"dragged"`
			Trial    bool    `json:"trial"`
			DeltaX   int     `json:"delta_x"`
			DeltaY   int     `json:"delta_y"`
			StartX   float64 `json:"start_x"`
			StartY   float64 `json:"start_y"`
			EndX     float64 `json:"end_x"`
			EndY     float64 `json:"end_y"`
		} `json:"drag"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Trial      bool     `json:"trial"`
			Required   []string `json:"required_checks"`
			Checks     struct {
				Enabled struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("drag trial by test-id output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.ResolvedSelector != "div#drag-target" || got.Locator.By != "test-id" || got.Locator.Query != "drag-target" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || got.Locator.Matches[0].SelectorHint != "div#drag-target" {
		t.Fatalf("drag trial locator = %+v, want strict test-id locator", got)
	}
	if got.Drag.Selector != "div#drag-target" || got.Drag.Dragged || !got.Drag.Trial || got.Drag.DeltaX != 8 || got.Drag.DeltaY != 12 || got.Drag.StartX != 90 || got.Drag.StartY != 70 || got.Drag.EndX != 98 || got.Drag.EndY != 82 || !got.Actionability.Actionable || !got.Actionability.Trial || len(got.Actionability.Required) != 4 || containsString(got.Actionability.Required, "enabled") || got.Actionability.Checks.Enabled.Required || !got.Actionability.Checks.Enabled.Skipped {
		t.Fatalf("drag trial action = %+v, want non-dispatching pointer-only actionability checks", got)
	}
}

func TestDragAutoScrollsOffscreenTargetJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"drag", "scroll-target", "8", "12", "--by", "test-id", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("offscreen drag exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Drag             struct {
			Selector string `json:"selector"`
			Dragged  bool   `json:"dragged"`
			DeltaX   int    `json:"delta_x"`
			DeltaY   int    `json:"delta_y"`
		} `json:"drag"`
		AutoScroll struct {
			Selector string `json:"selector"`
			Scrolled bool   `json:"scrolled"`
			Changed  bool   `json:"changed"`
			Block    string `json:"block"`
			Inline   string `json:"inline"`
			Before   struct {
				InViewport bool `json:"in_viewport"`
			} `json:"before"`
			After struct {
				InViewport bool `json:"in_viewport"`
			} `json:"after"`
		} `json:"auto_scroll"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Checks     struct {
				ReceivesEvents struct {
					Passed bool `json:"passed"`
				} `json:"receives_events"`
				InViewport struct {
					Passed bool `json:"passed"`
				} `json:"in_viewport"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("offscreen drag output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "dragged" || got.ResolvedSelector != "div#scroll-target" || got.Drag.Selector != "div#scroll-target" || !got.Drag.Dragged || got.Drag.DeltaX != 8 || got.Drag.DeltaY != 12 {
		t.Fatalf("offscreen drag action = %+v, want dragged resolved target", got)
	}
	if got.AutoScroll.Selector != "div#scroll-target" || !got.AutoScroll.Scrolled || !got.AutoScroll.Changed || got.AutoScroll.Block != "center" || got.AutoScroll.Inline != "nearest" || got.AutoScroll.Before.InViewport || !got.AutoScroll.After.InViewport {
		t.Fatalf("offscreen drag auto_scroll = %+v, want before/after scroll evidence", got.AutoScroll)
	}
	if !got.Actionability.Actionable || !got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.InViewport.Passed {
		t.Fatalf("offscreen drag actionability = %+v, want rechecked actionability after auto-scroll", got.Actionability)
	}
}

func TestDragActionabilityFailureJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"drag", "button#covered", "8", "12", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("covered drag exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Drag   struct {
				Dragged bool `json:"dragged"`
				Force   bool `json:"force"`
			} `json:"drag"`
			Actionability struct {
				Actionable bool `json:"actionable"`
				Checks     struct {
					ReceivesEvents struct {
						Required bool   `json:"required"`
						Passed   bool   `json:"passed"`
						Skipped  bool   `json:"skipped"`
						Message  string `json:"message"`
					} `json:"receives_events"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("covered drag output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "blocked" || got.Data.Drag.Dragged || got.Data.Drag.Force || got.Data.Actionability.Actionable || !got.Data.Actionability.Checks.ReceivesEvents.Required || got.Data.Actionability.Checks.ReceivesEvents.Passed || got.Data.Actionability.Checks.ReceivesEvents.Skipped || got.Data.Actionability.Checks.ReceivesEvents.Message == "" {
		t.Fatalf("covered drag = %+v, want failed receives-events actionability", got)
	}
}

func TestDragForceSkipsReceivesEventsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"drag", "button#covered", "8", "12", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("force covered drag exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Drag   struct {
			Dragged bool `json:"dragged"`
			Force   bool `json:"force"`
			DeltaX  int  `json:"delta_x"`
			DeltaY  int  `json:"delta_y"`
		} `json:"drag"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Force      bool     `json:"force"`
			Skipped    []string `json:"skipped_checks"`
			Checks     struct {
				ReceivesEvents struct {
					Required bool   `json:"required"`
					Passed   bool   `json:"passed"`
					Skipped  bool   `json:"skipped"`
					Message  string `json:"message"`
				} `json:"receives_events"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("force covered drag output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "dragged" || !got.Drag.Dragged || !got.Drag.Force || got.Drag.DeltaX != 8 || got.Drag.DeltaY != 12 || !got.Actionability.Actionable || !got.Actionability.Force || !containsString(got.Actionability.Skipped, "receives_events") || got.Actionability.Checks.ReceivesEvents.Required || got.Actionability.Checks.ReceivesEvents.Passed || !got.Actionability.Checks.ReceivesEvents.Skipped || !strings.Contains(got.Actionability.Checks.ReceivesEvents.Message, "--force") {
		t.Fatalf("force covered drag = %+v, want receives-events skipped by force", got)
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

func TestTypeByLabelLocatorTrialJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"type", "Search", "trial value", "--by", "label", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("type trial by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
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
			} `json:"matches"`
		} `json:"locator"`
		Type struct {
			Selector string `json:"selector"`
			Typing   bool   `json:"typing"`
			Trial    bool   `json:"trial"`
			Typed    string `json:"typed"`
			Strategy string `json:"strategy"`
		} `json:"type"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Trial      bool     `json:"trial"`
			Required   []string `json:"required_checks"`
			Checks     struct {
				Visible struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"visible"`
				Stable struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"stable"`
				ReceivesEvents struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"receives_events"`
				Enabled struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"enabled"`
				Editable struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"editable"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("type trial by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.ResolvedSelector != "input#q" || got.Locator.By != "label" || got.Locator.Query != "Search" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || got.Locator.Matches[0].SelectorHint != "input#q" {
		t.Fatalf("type trial locator = %+v, want strict label locator", got)
	}
	if got.Type.Selector != "input#q" || got.Type.Typing || !got.Type.Trial || got.Type.Typed != "trial value" || got.Type.Strategy != "auto" {
		t.Fatalf("type trial action = %+v, want non-mutating type trial", got.Type)
	}
	if !got.Actionability.Actionable || !got.Actionability.Trial || len(got.Actionability.Required) != 4 || containsString(got.Actionability.Required, "stable") || containsString(got.Actionability.Required, "receives_events") || !got.Actionability.Checks.Visible.Required || !got.Actionability.Checks.Visible.Passed || got.Actionability.Checks.Stable.Required || !got.Actionability.Checks.Stable.Skipped || got.Actionability.Checks.ReceivesEvents.Required || !got.Actionability.Checks.ReceivesEvents.Skipped || !got.Actionability.Checks.Enabled.Required || !got.Actionability.Checks.Enabled.Passed || !got.Actionability.Checks.Editable.Required || !got.Actionability.Checks.Editable.Passed {
		t.Fatalf("type trial actionability = %+v, want fill-like editable checks", got.Actionability)
	}
}

func TestTypeByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"type", "Search", "typed value", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("type by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Type             struct {
			Selector string `json:"selector"`
			Typing   bool   `json:"typing"`
			Typed    string `json:"typed"`
			Value    string `json:"value"`
			Kind     string `json:"kind"`
			Strategy string `json:"strategy"`
		} `json:"type"`
		Actionability struct {
			Actionable bool `json:"actionable"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("type by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "typed" || got.ResolvedSelector != "input#q" || got.Type.Selector != "input#q" || !got.Type.Typing || got.Type.Typed != "typed value" || got.Type.Value != "beforetyped value" || got.Type.Kind != "input" || got.Type.Strategy != "dom" || !got.Actionability.Actionable {
		t.Fatalf("type by label = %+v, want typed value on resolved input", got)
	}
}

func TestTypeForceSkipsVisibleJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"type", "input#hidden-field", "forced value", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("force hidden type exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Type   struct {
			Selector string `json:"selector"`
			Typing   bool   `json:"typing"`
			Force    bool   `json:"force"`
			Value    string `json:"value"`
		} `json:"type"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Force      bool     `json:"force"`
			Skipped    []string `json:"skipped_checks"`
			Checks     struct {
				Visible struct {
					Required bool   `json:"required"`
					Passed   bool   `json:"passed"`
					Skipped  bool   `json:"skipped"`
					Message  string `json:"message"`
				} `json:"visible"`
				Editable struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"editable"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("force hidden type output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "typed" || got.Type.Selector != "input#hidden-field" || !got.Type.Typing || !got.Type.Force || got.Type.Value != "beforeforced value" || !got.Actionability.Actionable || !got.Actionability.Force || !containsString(got.Actionability.Skipped, "visible") || got.Actionability.Checks.Visible.Required || got.Actionability.Checks.Visible.Passed || !got.Actionability.Checks.Visible.Skipped || !strings.Contains(got.Actionability.Checks.Visible.Message, "--force") || !got.Actionability.Checks.Editable.Required || !got.Actionability.Checks.Editable.Passed {
		t.Fatalf("force hidden type = %+v, want visible skipped and editable enforced", got)
	}
}

func TestTypeForceReadonlyStillFailsJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"type", "Read-only notes", "typed value", "--by", "label", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("force readonly type exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Type   struct {
				Selector string `json:"selector"`
				Typing   bool   `json:"typing"`
				Force    bool   `json:"force"`
			} `json:"type"`
			Actionability struct {
				Actionable bool     `json:"actionable"`
				Force      bool     `json:"force"`
				Skipped    []string `json:"skipped_checks"`
				Checks     struct {
					Editable struct {
						Required bool `json:"required"`
						Passed   bool `json:"passed"`
						Skipped  bool `json:"skipped"`
					} `json:"editable"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("force readonly type output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "blocked" || got.Data.Type.Selector != "textarea#readonly-notes" || got.Data.Type.Typing || !got.Data.Type.Force || got.Data.Actionability.Actionable || !got.Data.Actionability.Force || !containsString(got.Data.Actionability.Skipped, "visible") || !got.Data.Actionability.Checks.Editable.Required || got.Data.Actionability.Checks.Editable.Passed || got.Data.Actionability.Checks.Editable.Skipped {
		t.Fatalf("force readonly type = %+v, want editable still enforced", got)
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

func TestPressByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"press", "Enter", "Search", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("press by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
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
			} `json:"matches"`
		} `json:"locator"`
		Press struct {
			Selector   string `json:"selector"`
			Key        string `json:"key"`
			Dispatched bool   `json:"dispatched"`
			Trial      bool   `json:"trial"`
			Count      int    `json:"count"`
		} `json:"press"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Trial      bool     `json:"trial"`
			Required   []string `json:"required_checks"`
			Checks     struct {
				Attached struct {
					Required bool `json:"required"`
					Passed   bool `json:"passed"`
				} `json:"attached"`
				Visible struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"visible"`
				Enabled struct {
					Required bool `json:"required"`
					Skipped  bool `json:"skipped"`
				} `json:"enabled"`
			} `json:"checks"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("press by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "pressed" || got.ResolvedSelector != "input#q" || got.Locator.By != "label" || got.Locator.Query != "Search" || !got.Locator.Strict || len(got.Locator.Matches) != 1 || got.Locator.Matches[0].SelectorHint != "input#q" {
		t.Fatalf("press by label locator = %+v, want strict label locator", got)
	}
	if got.Press.Selector != "input#q" || got.Press.Key != "Enter" || !got.Press.Dispatched || got.Press.Trial || got.Press.Count != 1 {
		t.Fatalf("press by label action = %+v, want dispatched Enter on resolved selector", got.Press)
	}
	if !got.Actionability.Actionable || got.Actionability.Trial || len(got.Actionability.Required) != 1 || !containsString(got.Actionability.Required, "attached") || !got.Actionability.Checks.Attached.Required || !got.Actionability.Checks.Attached.Passed || got.Actionability.Checks.Visible.Required || !got.Actionability.Checks.Visible.Skipped || got.Actionability.Checks.Enabled.Required || !got.Actionability.Checks.Enabled.Skipped {
		t.Fatalf("press actionability = %+v, want locator attached evidence only", got.Actionability)
	}
}

func TestPressTrialByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"press", "Enter", "Search", "--by", "label", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("press trial by label exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Press  struct {
			Selector   string `json:"selector"`
			Key        string `json:"key"`
			Dispatched bool   `json:"dispatched"`
			Trial      bool   `json:"trial"`
		} `json:"press"`
		Actionability struct {
			Actionable bool `json:"actionable"`
			Trial      bool `json:"trial"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("press trial by label output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.Press.Selector != "input#q" || got.Press.Key != "Enter" || got.Press.Dispatched || !got.Press.Trial || !got.Actionability.Actionable || !got.Actionability.Trial {
		t.Fatalf("press trial = %+v, want non-dispatching locator press trial", got)
	}
}

func TestPressByLabelRequiresLocatorQuery(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"press", "Enter", "--by", "label", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("press missing locator query exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "requires a selector-or-locator argument") {
		t.Fatalf("press missing locator query output = %s, want usage guidance", out.String())
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
