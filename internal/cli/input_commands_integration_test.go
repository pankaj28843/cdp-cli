package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestClickTargetIndexJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "first-page", "type": "page", "title": "First", "url": "https://example.test/first", "attached": false},
		{"targetId": "second-page", "type": "page", "title": "Second", "url": "https://example.test/second", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "main", "--target-index", "2", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("indexed click exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	var got struct {
		OK          bool `json:"ok"`
		TargetIndex int  `json:"target_index"`
		Target      struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("indexed click output is invalid JSON: %v", err)
	}
	if !got.OK || got.TargetIndex != 2 || got.Target.ID != "second-page" {
		t.Fatalf("indexed click output = %+v, want second page and index evidence", got)
	}
}

func TestClickRejectsInvalidTargetIndexSelection(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "zero", args: []string{"click", "main", "--target-index", "0", "--json"}, want: "invalid_target_index"},
		{name: "combined", args: []string{"click", "main", "--target-index", "1", "--target", "page-1", "--json"}, want: "invalid_target_selector"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), test.args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitUsage {
				t.Fatalf("%s exit code = %d, want %d; stdout=%s stderr=%s", test.name, code, cli.ExitUsage, out.String(), errOut.String())
			}
			var got struct {
				OK   bool   `json:"ok"`
				Code string `json:"code"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("%s error is invalid JSON: %v", test.name, err)
			}
			if got.OK || got.Code != test.want {
				t.Fatalf("%s error = %+v, want code %q", test.name, got, test.want)
			}
		})
	}
}

func TestClickWaitURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false, "afterURL": "https://example.test/results?q=agent", "afterTitle": "Results"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "button#search", "--target", "page-1", "--wait-url", "https://example.test/results?q=agent", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click wait url exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Target struct {
			URL string `json:"url"`
		} `json:"target"`
		Before struct {
			URL string `json:"url"`
		} `json:"before_target"`
		After struct {
			URL string `json:"url"`
		} `json:"after_target"`
		Final struct {
			URL string `json:"url"`
		} `json:"final_target"`
		PageState struct {
			URLChanged bool `json:"url_changed"`
		} `json:"page_state"`
		Click struct {
			Clicked    bool   `json:"clicked"`
			Verified   *bool  `json:"verified"`
			FinalURL   string `json:"final_url"`
			FinalTitle string `json:"final_title"`
		} `json:"click"`
		Verification struct {
			Kind         string `json:"kind"`
			Needle       string `json:"needle"`
			Condition    string `json:"condition"`
			URL          string `json:"url"`
			Title        string `json:"title"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click wait url output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "clicked" || !got.Click.Clicked || got.Click.Verified == nil || !*got.Click.Verified {
		t.Fatalf("click wait url action = %+v, want clicked and verified", got)
	}
	if got.Before.URL != "https://example.test/app" || got.After.URL != "https://example.test/results?q=agent" || got.Final.URL != got.After.URL || got.Target.URL != got.After.URL || got.Click.FinalURL != got.After.URL || got.Click.FinalTitle != "Results" || !got.PageState.URLChanged {
		t.Fatalf("click wait url target state = %+v, want refreshed final URL evidence", got)
	}
	if got.Verification.Kind != "url" || got.Verification.Needle != "https://example.test/results?q=agent" || got.Verification.Condition != "exact" || got.Verification.URL != "https://example.test/results?q=agent" || got.Verification.Title != "Example App" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "250ms" {
		t.Fatalf("click wait url verification = %+v, want matched URL evidence", got.Verification)
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
	guidPath := filepath.Join(downloadDir, "click-download-1")
	if err := os.WriteFile(guidPath, []byte("click report bytes"), 0o600); err != nil {
		t.Fatalf("write GUID download fixture: %v", err)
	}
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
	wantPath := filepath.Join(downloadDir, "click-report.csv")
	if got.DownloadProgress.State != "completed" || got.DownloadProgress.TotalBytes != 24 || got.DownloadProgress.ReceivedBytes != 24 || got.DownloadProgress.FilePath != wantPath {
		t.Fatalf("click wait download progress = %+v, want completed progress", got.DownloadProgress)
	}
	if got.Download.GUID != "click-download-1" || got.Download.SuggestedFilename != "click-report.csv" || got.Download.State != "completed" || !got.Download.Completed || got.Download.ReceivedBytes != 24 || strings.Contains(got.Download.URL, "token=abc") {
		t.Fatalf("click wait download summary = %+v, want completed redacted download", got.Download)
	}
	if len(got.NextCommands) == 0 || !strings.HasPrefix(got.NextCommands[0], "ls -lah ") {
		t.Fatalf("click wait download next commands = %+v, want download directory listing", got.NextCommands)
	}
	if content, err := os.ReadFile(wantPath); err != nil {
		t.Fatalf("read retained click download: %v", err)
	} else if string(content) != "click report bytes" {
		t.Errorf("retained click download = %q, want click report bytes", content)
	}
	if _, err := os.Lstat(guidPath); !os.IsNotExist(err) {
		t.Errorf("GUID path still exists after click download finalization: %v", err)
	}
}

func TestClickWaitDownloadRetainsSafeSuggestedFilenameJSON(t *testing.T) {
	tests := []struct {
		name              string
		suggestedFilename string
		wantFilename      string
		existingFilename  string
	}{
		{
			name:              "real ChatGPT image filename",
			suggestedFilename: "ChatGPT Image Aug 4, 2026, 04_14_48 AM.png",
			wantFilename:      "ChatGPT Image Aug 4, 2026, 04_14_48 AM.png",
		},
		{
			name:              "existing filename is not overwritten",
			suggestedFilename: "report.png",
			wantFilename:      "report (1).png",
			existingFilename:  "report.png",
		},
		{
			name:              "path traversal is reduced to a plain filename",
			suggestedFilename: `../../nested\escape.png`,
			wantFilename:      "escape.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			downloadDir := t.TempDir()
			guid := "click-download-guid"
			guidPath := filepath.Join(downloadDir, guid)
			if err := os.WriteFile(guidPath, []byte("new image bytes"), 0o600); err != nil {
				t.Fatalf("write GUID download fixture: %v", err)
			}
			if tt.existingFilename != "" {
				if err := os.WriteFile(filepath.Join(downloadDir, tt.existingFilename), []byte("existing image bytes"), 0o600); err != nil {
					t.Fatalf("write collision fixture: %v", err)
				}
			}

			server := newFakeCDPServer(t, []map[string]any{
				{
					"targetId":         "click-download-finalize-page",
					"type":             "page",
					"title":            "Download App",
					"url":              "https://example.test/downloads",
					"attached":         false,
					"downloadOnClick":  true,
					"downloadGUID":     guid,
					"downloadURL":      "https://example.test/download/image.png",
					"downloadFilename": tt.suggestedFilename,
					"downloadFilePath": guidPath,
				},
			})
			t.Cleanup(server.Close)
			startFakeDaemon(t, server, "browser_url")

			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), []string{
				"click", "a#download",
				"--target", "click-download-finalize-page",
				"--wait-download",
				"--download-dir", downloadDir,
				"--json",
			}, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("click wait download exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
			}

			var got struct {
				DownloadWait struct {
					Progress struct {
						FilePath string `json:"file_path"`
					} `json:"progress"`
					LastEvent struct {
						FilePath string `json:"file_path"`
					} `json:"last_event"`
				} `json:"download_wait"`
				DownloadProgress struct {
					FilePath string `json:"file_path"`
				} `json:"download_progress"`
				Download struct {
					FilePath string `json:"file_path"`
				} `json:"download"`
				LastDownloadEvent struct {
					FilePath string `json:"file_path"`
				} `json:"last_download_event"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("click wait download output is invalid JSON: %v", err)
			}

			wantPath := filepath.Join(downloadDir, tt.wantFilename)
			paths := []struct {
				field string
				path  string
			}{
				{field: "download_wait.progress.file_path", path: got.DownloadWait.Progress.FilePath},
				{field: "download_wait.last_event.file_path", path: got.DownloadWait.LastEvent.FilePath},
				{field: "download_progress.file_path", path: got.DownloadProgress.FilePath},
				{field: "download.file_path", path: got.Download.FilePath},
				{field: "last_download_event.file_path", path: got.LastDownloadEvent.FilePath},
			}
			for _, reported := range paths {
				if reported.path != wantPath {
					t.Errorf("%s = %q, want finalized path %q", reported.field, reported.path, wantPath)
				}
				if filepath.Dir(filepath.Clean(reported.path)) != filepath.Clean(downloadDir) {
					t.Errorf("%s = %q escaped download dir %q", reported.field, reported.path, downloadDir)
				}
			}
			if _, err := os.Stat(guidPath); !os.IsNotExist(err) {
				t.Errorf("GUID path still exists after finalization: %v", err)
			}
			content, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatalf("read finalized download: %v", err)
			}
			if string(content) != "new image bytes" {
				t.Errorf("finalized content = %q, want new image bytes", content)
			}
			if tt.existingFilename != "" {
				existing, err := os.ReadFile(filepath.Join(downloadDir, tt.existingFilename))
				if err != nil {
					t.Fatalf("read collision fixture: %v", err)
				}
				if string(existing) != "existing image bytes" {
					t.Errorf("existing collision file was replaced: %q", existing)
				}
			}
		})
	}
}

func TestClickWaitDialogJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "dialog-page", "type": "page", "title": "Dialog App", "url": "https://example.test/dialog", "attached": false, "dialogOnClick": true, "dialogMessage": "Delete item?", "dialogType": "confirm", "dialogURL": "https://example.test/dialog?token=abc"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "button#delete", "--target", "dialog-page", "--wait-dialog", "--wait-dialog-type", "confirm", "--wait-dialog-message-contains", "Delete", "--wait-dialog-action", "dismiss", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click wait dialog exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Click struct {
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
			Verified *bool  `json:"verified"`
		} `json:"click"`
		DialogWait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			EventCount    int    `json:"event_count"`
			ObservedCount int    `json:"observed_count"`
			Criteria      struct {
				Type            string `json:"type"`
				MessageContains string `json:"message_contains"`
			} `json:"criteria"`
		} `json:"dialog_wait"`
		Dialog struct {
			Type        string `json:"type"`
			Message     string `json:"message"`
			URL         string `json:"url"`
			CDPMethod   string `json:"cdp_method"`
			Action      string `json:"action"`
			Handled     bool   `json:"handled"`
			Accepted    bool   `json:"accepted"`
			PromptGiven bool   `json:"prompt_text_supplied"`
		} `json:"dialog"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click wait dialog output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Click.Clicked || got.Click.Strategy != "raw-input" || got.Click.Verified == nil || !*got.Click.Verified {
		t.Fatalf("click wait dialog action = %+v, want raw-input clicked and verified", got)
	}
	if got.DialogWait.Kind != "dialog" || !got.DialogWait.Matched || got.DialogWait.EventCount == 0 || got.DialogWait.ObservedCount != 1 || got.DialogWait.Criteria.Type != "confirm" || got.DialogWait.Criteria.MessageContains != "Delete" {
		t.Fatalf("click wait dialog wait = %+v, want matched dialog evidence", got.DialogWait)
	}
	if got.Dialog.Type != "confirm" || got.Dialog.Message != "Delete item?" || got.Dialog.CDPMethod != "Page.javascriptDialogOpening" || got.Dialog.Action != "dismiss" || !got.Dialog.Handled || got.Dialog.Accepted || got.Dialog.PromptGiven || strings.Contains(got.Dialog.URL, "token=abc") {
		t.Fatalf("click wait dialog event = %+v, want dismissed redacted confirm dialog", got.Dialog)
	}
	if !containsString(got.NextCommands, "cdp snapshot --json") {
		t.Fatalf("click wait dialog next commands = %+v, want snapshot follow-up", got.NextCommands)
	}
}

func TestClickWaitFileChooserJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "upload-page", "type": "page", "title": "Upload App", "url": "https://example.test/upload", "attached": false, "fileChooserOnClick": true, "fileChooserMode": "selectMultiple", "fileChooserBackendNodeID": 77},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "label#upload", "--target", "upload-page", "--wait-file-chooser", "--wait-file-chooser-mode", "multiple", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click wait file chooser exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Click struct {
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
			Verified *bool  `json:"verified"`
		} `json:"click"`
		FileChooserWait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			EventCount    int    `json:"event_count"`
			ObservedCount int    `json:"observed_count"`
			Intercepted   bool   `json:"intercepted"`
			Criteria      struct {
				Mode string `json:"mode"`
			} `json:"criteria"`
		} `json:"file_chooser_wait"`
		FileChooser struct {
			FrameID       string `json:"frame_id"`
			Mode          string `json:"mode"`
			Multiple      bool   `json:"multiple"`
			BackendNodeID int    `json:"backend_node_id"`
			CDPMethod     string `json:"cdp_method"`
		} `json:"file_chooser"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click wait file chooser output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Click.Clicked || got.Click.Strategy != "raw-input" || got.Click.Verified == nil || !*got.Click.Verified {
		t.Fatalf("click wait file chooser action = %+v, want raw-input clicked and verified", got)
	}
	if got.FileChooserWait.Kind != "file-chooser" || !got.FileChooserWait.Matched || got.FileChooserWait.EventCount == 0 || got.FileChooserWait.ObservedCount != 1 || got.FileChooserWait.Criteria.Mode != "selectMultiple" || !got.FileChooserWait.Intercepted {
		t.Fatalf("click wait file chooser wait = %+v, want matched intercepted chooser evidence", got.FileChooserWait)
	}
	if got.FileChooser.FrameID != "frame-upload" || got.FileChooser.Mode != "selectMultiple" || !got.FileChooser.Multiple || got.FileChooser.BackendNodeID != 77 || got.FileChooser.CDPMethod != "Page.fileChooserOpened" {
		t.Fatalf("click wait file chooser event = %+v, want multiple chooser metadata", got.FileChooser)
	}
	if !containsString(got.NextCommands, "cdp file input[type=file] tmp/upload.txt --json") {
		t.Fatalf("click wait file chooser next commands = %+v, want cdp file follow-up", got.NextCommands)
	}
}

func TestClickWaitRequestJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "network-page", "type": "page", "title": "Network App", "url": "https://example.test/network", "attached": false, "networkOnClick": true, "networkURL": "https://example.test/api/click?token=abc", "networkMethod": "POST", "networkResourceType": "Fetch"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "button#save", "--target", "network-page", "--wait-request", "--wait-request-match-url", "/api/click", "--wait-request-method", "POST", "--wait-request-resource-type", "Fetch", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click wait request exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Click struct {
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
			Verified *bool  `json:"verified"`
		} `json:"click"`
		RequestWait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			CDPMethod     string `json:"cdp_method"`
			EventCount    int    `json:"event_count"`
			ObservedCount int    `json:"observed_count"`
			Criteria      struct {
				URLContains  string `json:"url_contains"`
				Method       string `json:"method"`
				ResourceType string `json:"resource_type"`
			} `json:"criteria"`
		} `json:"request_wait"`
		Request struct {
			Kind         string `json:"kind"`
			CDPMethod    string `json:"cdp_method"`
			RequestID    string `json:"request_id"`
			URL          string `json:"url"`
			Method       string `json:"method"`
			ResourceType string `json:"resource_type"`
		} `json:"request"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click wait request output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Click.Clicked || got.Click.Strategy != "raw-input" || got.Click.Verified == nil || !*got.Click.Verified {
		t.Fatalf("click wait request action = %+v, want raw-input clicked and verified", got)
	}
	if got.RequestWait.Kind != "request" || !got.RequestWait.Matched || got.RequestWait.CDPMethod != "Network.requestWillBeSent" || got.RequestWait.EventCount == 0 || got.RequestWait.ObservedCount < 1 || got.RequestWait.Criteria.URLContains != "/api/click" || got.RequestWait.Criteria.Method != "POST" || got.RequestWait.Criteria.ResourceType != "Fetch" {
		t.Fatalf("click wait request wait = %+v, want matched request criteria", got.RequestWait)
	}
	if got.Request.Kind != "request" || got.Request.CDPMethod != "Network.requestWillBeSent" || got.Request.RequestID != "click-request-1" || got.Request.Method != "POST" || got.Request.ResourceType != "Fetch" || strings.Contains(got.Request.URL, "token=abc") {
		t.Fatalf("click wait request event = %+v, want redacted request evidence", got.Request)
	}
}

func TestClickWaitResponseJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "network-page", "type": "page", "title": "Network App", "url": "https://example.test/network", "attached": false, "networkOnClick": true, "networkURL": "https://example.test/api/save?token=abc", "networkMethod": "POST", "networkResourceType": "Fetch", "networkStatus": 201, "networkStatusText": "Created", "networkMimeType": "application/json"},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "button#save", "--target", "network-page", "--wait-response", "--wait-response-match-url", "/api/save", "--wait-response-method", "POST", "--wait-response-status", "201", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("click wait response exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Click struct {
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
			Verified *bool  `json:"verified"`
		} `json:"click"`
		ResponseWait struct {
			Kind          string `json:"kind"`
			Matched       bool   `json:"matched"`
			CDPMethod     string `json:"cdp_method"`
			EventCount    int    `json:"event_count"`
			ObservedCount int    `json:"observed_count"`
			Criteria      struct {
				URLContains string `json:"url_contains"`
				Method      string `json:"method"`
				Status      int    `json:"status"`
			} `json:"criteria"`
		} `json:"response_wait"`
		Response struct {
			Kind         string `json:"kind"`
			CDPMethod    string `json:"cdp_method"`
			RequestID    string `json:"request_id"`
			URL          string `json:"url"`
			Method       string `json:"method"`
			ResourceType string `json:"resource_type"`
			Status       int    `json:"status"`
			StatusText   string `json:"status_text"`
			MimeType     string `json:"mime_type"`
		} `json:"response"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("click wait response output is invalid JSON: %v", err)
	}
	if !got.OK || !got.Click.Clicked || got.Click.Strategy != "raw-input" || got.Click.Verified == nil || !*got.Click.Verified {
		t.Fatalf("click wait response action = %+v, want raw-input clicked and verified", got)
	}
	if got.ResponseWait.Kind != "response" || !got.ResponseWait.Matched || got.ResponseWait.CDPMethod != "Network.responseReceived" || got.ResponseWait.EventCount == 0 || got.ResponseWait.ObservedCount < 1 || got.ResponseWait.Criteria.URLContains != "/api/save" || got.ResponseWait.Criteria.Method != "POST" || got.ResponseWait.Criteria.Status != 201 {
		t.Fatalf("click wait response wait = %+v, want matched response criteria", got.ResponseWait)
	}
	if got.Response.Kind != "response" || got.Response.CDPMethod != "Network.responseReceived" || got.Response.RequestID != "click-request-1" || got.Response.Method != "POST" || got.Response.ResourceType != "Fetch" || got.Response.Status != 201 || got.Response.StatusText != "Created" || got.Response.MimeType != "application/json" || strings.Contains(got.Response.URL, "token=abc") {
		t.Fatalf("click wait response event = %+v, want redacted response evidence", got.Response)
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

func TestClickByUniqueSemanticLocatorUsesResolvedNodeWhenCSSHintIsAmbiguous(t *testing.T) {
	for _, trial := range []bool{true, false} {
		t.Run(fmt.Sprintf("trial=%t", trial), func(t *testing.T) {
			server := newFakeCDPServer(t, []map[string]any{
				{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
			})
			defer server.Close()
			startFakeDaemon(t, server, "browser_url")

			args := []string{"click", "Delete Chat", "--by", "role", "--role", "menuitem", "--exact", "--strategy", "raw-input", "--json"}
			if trial {
				args = append(args, "--trial")
			} else {
				args = append(args, "--wait-url", "https://example.test/app")
			}
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
			if code != cli.ExitOK {
				t.Fatalf("semantic click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
			}

			var got struct {
				OK               bool   `json:"ok"`
				ResolvedSelector string `json:"resolved_selector"`
				Locator          struct {
					Count   int `json:"count"`
					Matches []struct {
						SelectorHint      string `json:"selector_hint"`
						SelectorAmbiguous bool   `json:"selector_ambiguous"`
					} `json:"matches"`
				} `json:"locator"`
				Click struct {
					Selector string `json:"selector"`
					Clicked  bool   `json:"clicked"`
					Trial    bool   `json:"trial"`
					Strategy string `json:"strategy"`
					Verified *bool  `json:"verified"`
				} `json:"click"`
				Actionability struct {
					Actionable bool `json:"actionable"`
					Checks     struct {
						ReceivesEvents struct {
							Passed bool `json:"passed"`
						} `json:"receives_events"`
					} `json:"checks"`
				} `json:"actionability"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("semantic click output is invalid JSON: %v", err)
			}
			var raw map[string]any
			if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
				t.Fatalf("semantic click raw output is invalid JSON: %v", err)
			}
			locatorPayload := raw["locator"].(map[string]any)
			matchPayload := locatorPayload["matches"].([]any)[0].(map[string]any)
			if _, exposed := matchPayload["resolved_node_selector"]; exposed {
				t.Fatal("internal resolved-node selector leaked into the public locator schema")
			}
			if strings.Contains(out.String(), ":nth-of-type(") {
				t.Fatalf("semantic click leaked private node selector: %s", out.String())
			}
			if !got.OK || got.ResolvedSelector != "" || got.Locator.Count != 1 || len(got.Locator.Matches) != 1 {
				t.Fatalf("semantic resolution = %+v, want one resolved node", got)
			}
			if got.Locator.Matches[0].SelectorHint != `div[role="menuitem"]` || !got.Locator.Matches[0].SelectorAmbiguous {
				t.Fatalf("semantic locator evidence = %+v, want preserved ambiguous hint", got.Locator)
			}
			if got.Click.Selector != "Delete Chat" || got.Click.Strategy != "raw-input" || got.Click.Trial != trial || got.Click.Clicked == trial {
				t.Fatalf("semantic click = %+v, want raw-input trial=%t", got.Click, trial)
			}
			if !trial && (got.Click.Verified == nil || !*got.Click.Verified) {
				t.Fatalf("semantic raw-input click = %+v, want verified postcondition", got.Click)
			}
			if !got.Actionability.Actionable || !got.Actionability.Checks.ReceivesEvents.Passed {
				t.Fatalf("semantic actionability = %+v, want actionable receiving target", got.Actionability)
			}
		})
	}
}

func TestClickSemanticResolvedNodeDriftFailsBeforeDispatch(t *testing.T) {
	fakeSemanticDriftActionabilityAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "Drifting menuitem", "--by", "role", "--role", "menuitem", "--exact", "--strategy", "raw-input", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("drifting semantic click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Action string `json:"action"`
			Click  struct {
				Clicked bool `json:"clicked"`
			} `json:"click"`
			Actionability struct {
				Checks map[string]struct {
					Passed  bool   `json:"passed"`
					Message string `json:"message"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("drifting semantic click output is invalid JSON: %v", err)
	}
	if strings.Contains(out.String(), ":nth-of-type(") {
		t.Fatalf("blocked semantic click leaked private node selector: %s", out.String())
	}
	identity := got.Data.Actionability.Checks["semantic_identity"]
	if got.OK || got.Code != "actionability_failed" || got.Data.Action != "blocked" || got.Data.Click.Clicked || identity.Passed || identity.Message == "" {
		t.Fatalf("drifting semantic click = %+v, want pre-dispatch identity failure", got)
	}
}

func TestClickIdenticalSemanticNodeReplacementFailsBackendIdentity(t *testing.T) {
	fakeSemanticReplacementDescribeAttempts.Store(0)
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "Replacing menuitem", "--by", "role", "--role", "menuitem", "--exact", "--strategy", "raw-input", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("replaced semantic click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), ":nth-of-type(") {
		t.Fatalf("replacement failure leaked private node selector: %s", out.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Click struct {
				Clicked bool `json:"clicked"`
			} `json:"click"`
			Actionability struct {
				Checks map[string]struct {
					Passed bool `json:"passed"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("replacement failure output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "actionability_failed" || got.Data.Click.Clicked || got.Data.Actionability.Checks["semantic_backend_identity"].Passed {
		t.Fatalf("replacement failure = %+v, want backend identity to fail closed", got)
	}
}

func TestClickStillRejectsMultipleSemanticMatches(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "Duplicate menuitem", "--by", "role", "--role", "menuitem", "--exact", "--strategy", "raw-input", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("ambiguous semantic click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("ambiguous semantic click output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "ambiguous_locator" {
		t.Fatalf("ambiguous semantic click = %+v, want strict rejection", got)
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

func TestClickDOMStrategyAcceptsRelatedSplitControlJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "button#split-control", "--strategy", "dom", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("DOM split-control click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Click  struct {
			Clicked  bool   `json:"clicked"`
			Strategy string `json:"strategy"`
		} `json:"click"`
		Actionability struct {
			Actionable bool     `json:"actionable"`
			Required   []string `json:"required_checks"`
			Checks     map[string]struct {
				Required bool   `json:"required"`
				Passed   bool   `json:"passed"`
				Message  string `json:"message"`
			} `json:"checks"`
			Point struct {
				TargetMatches  bool `json:"target_matches"`
				PseudoElements map[string]struct {
					PointerEvents string `json:"pointer_events"`
					HitMatches    bool   `json:"hit_matches"`
					Rect          struct {
						Width  float64 `json:"width"`
						Height float64 `json:"height"`
					} `json:"rect"`
				} `json:"pseudo_elements"`
			} `json:"point"`
		} `json:"actionability"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("DOM split-control output is invalid JSON: %v", err)
	}
	domSafe := got.Actionability.Checks["dom_dispatch_safe"]
	if !got.OK || got.Action != "clicked" || !got.Click.Clicked || got.Click.Strategy != "dom" || !got.Actionability.Actionable {
		t.Fatalf("DOM split-control action = %+v, want successful same-target DOM click", got)
	}
	if !containsString(got.Actionability.Required, "dom_dispatch_safe") || containsString(got.Actionability.Required, "receives_events") || !domSafe.Required || !domSafe.Passed || got.Actionability.Point.TargetMatches {
		t.Fatalf("DOM split-control actionability = %+v, want safe DOM fallback without center hit requirement", got.Actionability)
	}
	after := got.Actionability.Point.PseudoElements["after"]
	if after.PointerEvents != "auto" || !after.HitMatches || after.Rect.Width <= 0 || after.Rect.Height <= 0 {
		t.Fatalf("DOM split-control pseudo evidence = %+v, want measured interactive pseudo geometry", got.Actionability.Point.PseudoElements)
	}
}

func TestClickDOMStrategyRejectsUnrelatedOcclusionJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"click", "button#occluded", "--strategy", "dom", "--force", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitCheckFailed {
		t.Fatalf("DOM occluded click exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitCheckFailed, out.String(), errOut.String())
	}

	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Data struct {
			Click struct {
				Clicked bool `json:"clicked"`
			} `json:"click"`
			Actionability struct {
				Checks map[string]struct {
					Required bool   `json:"required"`
					Passed   bool   `json:"passed"`
					Message  string `json:"message"`
				} `json:"checks"`
			} `json:"actionability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("DOM occluded output is invalid JSON: %v", err)
	}
	domSafe := got.Data.Actionability.Checks["dom_dispatch_safe"]
	if got.OK || got.Code != "actionability_failed" || got.Data.Click.Clicked || !domSafe.Required || domSafe.Passed || domSafe.Message == "" {
		t.Fatalf("DOM occluded actionability = %+v, want safe rejection with explicit DOM-fallback failure", got)
	}
	if got.Data.Actionability.Checks["receives_events"].Required {
		t.Fatalf("DOM occluded actionability should report receives_events as diagnostic, not its blocker: %+v", got.Data.Actionability.Checks)
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

func TestFillWaitTextByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"fill", "Search", "typed value", "--by", "label", "--wait-text", "Example", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("fill wait text exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Fill             struct {
			Selector string `json:"selector"`
			Filled   bool   `json:"filled"`
			Verified *bool  `json:"verified"`
			Value    string `json:"value"`
		} `json:"fill"`
		Verification struct {
			Kind         string `json:"kind"`
			Needle       string `json:"needle"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
		Locator struct {
			Strict bool `json:"strict"`
		} `json:"locator"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("fill wait text output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "filled" || got.ResolvedSelector != "input#q" || got.Fill.Selector != "input#q" || !got.Fill.Filled || got.Fill.Verified == nil || !*got.Fill.Verified || got.Fill.Value != "typed value" || got.Verification.Kind != "text" || got.Verification.Needle != "Example" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "250ms" || !got.Locator.Strict {
		t.Fatalf("fill wait text = %+v, want filled value with matched text verification", got)
	}
}

func TestFillWaitSelectorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"fill", "input#q", "typed value", "--wait-selector", "main", "--poll", "100ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("fill wait selector exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Fill struct {
			Selector string `json:"selector"`
			Filled   bool   `json:"filled"`
			Verified *bool  `json:"verified"`
		} `json:"fill"`
		Verification struct {
			Kind         string `json:"kind"`
			Selector     string `json:"selector"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("fill wait selector output is invalid JSON: %v", err)
	}
	if !got.OK || got.Fill.Selector != "input#q" || !got.Fill.Filled || got.Fill.Verified == nil || !*got.Fill.Verified || got.Verification.Kind != "selector" || got.Verification.Selector != "main" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "100ms" {
		t.Fatalf("fill wait selector = %+v, want matched selector verification", got)
	}
}

func TestFillWaitURLByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"fill", "Search", "typed value", "--by", "label", "--wait-url-contains", "results", "--poll", "100ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("fill wait url exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Target           struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		Fill struct {
			Selector string `json:"selector"`
			URL      string `json:"url"`
			Title    string `json:"title"`
			Filled   bool   `json:"filled"`
			Verified *bool  `json:"verified"`
			Value    string `json:"value"`
		} `json:"fill"`
		Verification struct {
			Kind         string `json:"kind"`
			Needle       string `json:"needle"`
			Condition    string `json:"condition"`
			URL          string `json:"url"`
			Title        string `json:"title"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
		Locator struct {
			Strict bool `json:"strict"`
		} `json:"locator"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("fill wait url output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "filled" || got.ResolvedSelector != "input#q" || got.Fill.Selector != "input#q" || !got.Fill.Filled || got.Fill.Verified == nil || !*got.Fill.Verified || got.Fill.Value != "typed value" || got.Verification.Kind != "url" || got.Verification.Needle != "results" || got.Verification.Condition != "contains" || !strings.Contains(got.Verification.URL, "results") || got.Verification.Title != "Example App" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "100ms" || !got.Locator.Strict {
		t.Fatalf("fill wait url = %+v, want filled value with matched URL verification", got)
	}
	if got.Target.URL != got.Verification.URL || got.Target.Title != "Example App" || got.Fill.URL != got.Verification.URL || got.Fill.Title != "Example App" {
		t.Fatalf("fill wait url target/result = %+v, want final URL/title evidence", got)
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

func TestSelectWaitTextByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"select", "Plan", "pro", "--by", "label", "--wait-text", "Example", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("select wait text exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Select           struct {
			Selector string `json:"selector"`
			Selected bool   `json:"selected"`
			Verified *bool  `json:"verified"`
			Value    string `json:"value"`
		} `json:"select"`
		Verification struct {
			Kind         string `json:"kind"`
			Needle       string `json:"needle"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
		Locator struct {
			Strict bool `json:"strict"`
		} `json:"locator"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("select wait text output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "selected" || got.ResolvedSelector != "select#plan" || got.Select.Selector != "select#plan" || !got.Select.Selected || got.Select.Verified == nil || !*got.Select.Verified || got.Select.Value != "pro" || got.Verification.Kind != "text" || got.Verification.Needle != "Example" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "250ms" || !got.Locator.Strict {
		t.Fatalf("select wait text = %+v, want selected value with matched text verification", got)
	}
}

func TestSelectWaitSelectorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"select", "select#plan", "pro", "--wait-selector", "main", "--poll", "100ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("select wait selector exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Select struct {
			Selector string `json:"selector"`
			Selected bool   `json:"selected"`
			Verified *bool  `json:"verified"`
		} `json:"select"`
		Verification struct {
			Kind         string `json:"kind"`
			Selector     string `json:"selector"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("select wait selector output is invalid JSON: %v", err)
	}
	if !got.OK || got.Select.Selector != "select#plan" || !got.Select.Selected || got.Select.Verified == nil || !*got.Select.Verified || got.Verification.Kind != "selector" || got.Verification.Selector != "main" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "100ms" {
		t.Fatalf("select wait selector = %+v, want matched selector verification", got)
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

func TestFileChooserTrialByBackendNodeJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Detached Upload", "url": "https://example.test/upload", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	firstPath := filepath.Join(t.TempDir(), "first.epub")
	secondPath := filepath.Join(t.TempDir(), "second.epub")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("synthetic upload"), 0o600); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", path, err)
		}
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"file", "chooser", "247", firstPath, secondPath, "--target", "page-1", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("file chooser trial exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
		FileChooser struct {
			BackendNodeID  int      `json:"backend_node_id"`
			Tag            string   `json:"tag"`
			Type           string   `json:"type"`
			Multiple       bool     `json:"multiple"`
			Accept         string   `json:"accept"`
			Trial          bool     `json:"trial"`
			FilesSet       bool     `json:"files_set"`
			FileCount      int      `json:"file_count"`
			FileNames      []string `json:"file_names"`
			ContentOmitted bool     `json:"content_omitted"`
		} `json:"file_chooser"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("file chooser trial output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "trial" || got.Target.ID != "page-1" || got.FileChooser.BackendNodeID != 247 || got.FileChooser.Tag != "input" || got.FileChooser.Type != "file" || !got.FileChooser.Multiple || got.FileChooser.Accept != ".epub,application/epub+zip" || !got.FileChooser.Trial || got.FileChooser.FilesSet || got.FileChooser.FileCount != 2 || !got.FileChooser.ContentOmitted {
		t.Fatalf("file chooser trial = %+v, want detached multiple-input evidence", got)
	}
	if len(got.FileChooser.FileNames) != 2 || got.FileChooser.FileNames[0] != "first.epub" || got.FileChooser.FileNames[1] != "second.epub" {
		t.Fatalf("file chooser names = %#v, want both basenames", got.FileChooser.FileNames)
	}
}

func TestFileChooserSetsMultipleFilesByBackendNodeJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Detached Upload", "url": "https://example.test/upload", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	firstPath := filepath.Join(t.TempDir(), "first.epub")
	secondPath := filepath.Join(t.TempDir(), "second.epub")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("synthetic upload"), 0o600); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", path, err)
		}
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"file", "chooser", "247", firstPath, secondPath, "--target", "page-1", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("file chooser set exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK          bool   `json:"ok"`
		Action      string `json:"action"`
		FileChooser struct {
			BackendNodeID  int      `json:"backend_node_id"`
			Trial          bool     `json:"trial"`
			FilesSet       bool     `json:"files_set"`
			FileCount      int      `json:"file_count"`
			FileNames      []string `json:"file_names"`
			ContentOmitted bool     `json:"content_omitted"`
		} `json:"file_chooser"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("file chooser set output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "files_set" || got.FileChooser.BackendNodeID != 247 || got.FileChooser.Trial || !got.FileChooser.FilesSet || got.FileChooser.FileCount != 2 || len(got.FileChooser.FileNames) != 2 || !got.FileChooser.ContentOmitted {
		t.Fatalf("file chooser set = %+v, want two assigned files without contents", got)
	}
}

func TestFileChooserRejectsMultipleFilesForSingleInput(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Detached Upload", "url": "https://example.test/upload", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	firstPath := filepath.Join(t.TempDir(), "first.epub")
	secondPath := filepath.Join(t.TempDir(), "second.epub")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("synthetic upload"), 0o600); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", path, err)
		}
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"file", "chooser", "248", firstPath, secondPath, "--target", "page-1", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("single file chooser exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("single file chooser output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "file_multiplicity" {
		t.Fatalf("single file chooser = %+v, want file_multiplicity", got)
	}
}

func TestFileChooserRejectsNonFileBackendNode(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Detached Upload", "url": "https://example.test/upload", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	uploadPath := filepath.Join(t.TempDir(), "upload.epub")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"file", "chooser", "249", uploadPath, "--target", "page-1", "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("non-file chooser exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("non-file chooser output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "invalid_target" {
		t.Fatalf("non-file chooser = %+v, want invalid_target", got)
	}
}

func TestFileChooserRequiresExplicitTarget(t *testing.T) {
	uploadPath := filepath.Join(t.TempDir(), "upload.epub")
	if err := os.WriteFile(uploadPath, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatalf("WriteFile upload returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"file", "chooser", "247", uploadPath, "--trial", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("file chooser without target exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("file chooser without target output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "target_required" {
		t.Fatalf("file chooser without target = %+v, want target_required", got)
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

func TestTypeWaitTextByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"type", "Search", "typed value", "--by", "label", "--wait-text", "Example", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("type wait text exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Type             struct {
			Selector string `json:"selector"`
			Typing   bool   `json:"typing"`
			Verified *bool  `json:"verified"`
			Typed    string `json:"typed"`
			Value    string `json:"value"`
		} `json:"type"`
		Verification struct {
			Kind         string `json:"kind"`
			Needle       string `json:"needle"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
		Locator struct {
			Strict bool `json:"strict"`
		} `json:"locator"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("type wait text output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "typed" || got.ResolvedSelector != "input#q" || got.Type.Selector != "input#q" || !got.Type.Typing || got.Type.Verified == nil || !*got.Type.Verified || got.Type.Typed != "typed value" || got.Type.Value != "beforetyped value" || got.Verification.Kind != "text" || got.Verification.Needle != "Example" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "250ms" || !got.Locator.Strict {
		t.Fatalf("type wait text = %+v, want typed value with matched text verification", got)
	}
}

func TestTypeWaitSelectorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"type", "input#q", "typed value", "--wait-selector", "main", "--poll", "100ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("type wait selector exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Type struct {
			Selector string `json:"selector"`
			Typing   bool   `json:"typing"`
			Verified *bool  `json:"verified"`
		} `json:"type"`
		Verification struct {
			Kind         string `json:"kind"`
			Selector     string `json:"selector"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("type wait selector output is invalid JSON: %v", err)
	}
	if !got.OK || got.Type.Selector != "input#q" || !got.Type.Typing || got.Type.Verified == nil || !*got.Type.Verified || got.Verification.Kind != "selector" || got.Verification.Selector != "main" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "100ms" {
		t.Fatalf("type wait selector = %+v, want matched selector verification", got)
	}
}

func TestTypeWaitURLByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"type", "Search", "typed value", "--by", "label", "--wait-url-contains", "results", "--poll", "100ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("type wait url exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Target           struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		Type struct {
			Selector string `json:"selector"`
			URL      string `json:"url"`
			Title    string `json:"title"`
			Typing   bool   `json:"typing"`
			Verified *bool  `json:"verified"`
			Typed    string `json:"typed"`
			Value    string `json:"value"`
		} `json:"type"`
		Verification struct {
			Kind         string `json:"kind"`
			Needle       string `json:"needle"`
			Condition    string `json:"condition"`
			URL          string `json:"url"`
			Title        string `json:"title"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
		Locator struct {
			Strict bool `json:"strict"`
		} `json:"locator"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("type wait url output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "typed" || got.ResolvedSelector != "input#q" || got.Type.Selector != "input#q" || !got.Type.Typing || got.Type.Verified == nil || !*got.Type.Verified || got.Type.Typed != "typed value" || got.Type.Value != "beforetyped value" || got.Verification.Kind != "url" || got.Verification.Needle != "results" || got.Verification.Condition != "contains" || !strings.Contains(got.Verification.URL, "results") || got.Verification.Title != "Example App" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "100ms" || !got.Locator.Strict {
		t.Fatalf("type wait url = %+v, want typed value with matched URL verification", got)
	}
	if got.Target.URL != got.Verification.URL || got.Target.Title != "Example App" || got.Type.URL != got.Verification.URL || got.Type.Title != "Example App" {
		t.Fatalf("type wait url target/result = %+v, want final URL/title evidence", got)
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

func TestPressWaitTextByLabelLocatorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"press", "Enter", "Search", "--by", "label", "--wait-text", "Example", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("press wait text exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK               bool   `json:"ok"`
		Action           string `json:"action"`
		ResolvedSelector string `json:"resolved_selector"`
		Press            struct {
			Selector   string `json:"selector"`
			Key        string `json:"key"`
			Dispatched bool   `json:"dispatched"`
			Verified   *bool  `json:"verified"`
		} `json:"press"`
		Verification struct {
			Kind         string `json:"kind"`
			Needle       string `json:"needle"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
		Locator struct {
			Strict bool `json:"strict"`
		} `json:"locator"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("press wait text output is invalid JSON: %v", err)
	}
	if !got.OK || got.Action != "pressed" || got.ResolvedSelector != "input#q" || got.Press.Selector != "input#q" || got.Press.Key != "Enter" || !got.Press.Dispatched || got.Press.Verified == nil || !*got.Press.Verified || got.Verification.Kind != "text" || got.Verification.Needle != "Example" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "250ms" || !got.Locator.Strict {
		t.Fatalf("press wait text = %+v, want dispatched press with matched text verification", got)
	}
}

func TestPressWaitSelectorJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"press", "Enter", "--selector", "input#q", "--wait-selector", "main", "--poll", "100ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("press wait selector exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Press struct {
			Selector   string `json:"selector"`
			Dispatched bool   `json:"dispatched"`
			Verified   *bool  `json:"verified"`
		} `json:"press"`
		Verification struct {
			Kind         string `json:"kind"`
			Selector     string `json:"selector"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("press wait selector output is invalid JSON: %v", err)
	}
	if !got.OK || got.Press.Selector != "input#q" || !got.Press.Dispatched || got.Press.Verified == nil || !*got.Press.Verified || got.Verification.Kind != "selector" || got.Verification.Selector != "main" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "100ms" {
		t.Fatalf("press wait selector = %+v, want matched selector verification", got)
	}
}

func TestPressWaitURLJSON(t *testing.T) {
	server := newFakeCDPServer(t, []map[string]any{
		{"targetId": "page-1", "type": "page", "title": "Example App", "url": "https://example.test/app", "attached": false},
	})
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"press", "Enter", "--selector", "input#q", "--wait-url-contains", "results", "--poll", "100ms", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("press wait url exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Target struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"target"`
		Press struct {
			Selector   string `json:"selector"`
			URL        string `json:"url"`
			Title      string `json:"title"`
			Dispatched bool   `json:"dispatched"`
			Verified   *bool  `json:"verified"`
		} `json:"press"`
		Verification struct {
			Kind         string `json:"kind"`
			Needle       string `json:"needle"`
			Condition    string `json:"condition"`
			URL          string `json:"url"`
			Title        string `json:"title"`
			Matched      bool   `json:"matched"`
			Count        int    `json:"count"`
			PollInterval string `json:"poll_interval"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("press wait url output is invalid JSON: %v", err)
	}
	if !got.OK || got.Press.Selector != "input#q" || !got.Press.Dispatched || got.Press.Verified == nil || !*got.Press.Verified || got.Verification.Kind != "url" || got.Verification.Needle != "results" || got.Verification.Condition != "contains" || !strings.Contains(got.Verification.URL, "results") || got.Verification.Title != "Example App" || !got.Verification.Matched || got.Verification.Count != 1 || got.Verification.PollInterval != "100ms" {
		t.Fatalf("press wait url = %+v, want matched URL verification", got)
	}
	if got.Target.URL != got.Verification.URL || got.Target.Title != "Example App" || got.Press.URL != got.Verification.URL || got.Press.Title != "Example App" {
		t.Fatalf("press wait url target/result = %+v, want final URL/title evidence", got)
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
