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

func TestWorkflowYouTubeCookiesWritesOwnerOnlyFileAndClosesTarget(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")
	cookieFile := filepath.Join(t.TempDir(), "yt-dlp", "cookies.txt")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"workflow", "youtube", "cookies", "--out", cookieFile, "--settle", "0", "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		OK          bool   `json:"ok"`
		CookieFile  string `json:"cookie_file"`
		CookieCount int    `json:"cookie_count"`
		Cleanup     struct {
			Closed   bool   `json:"closed"`
			TargetID string `json:"target_id"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	content, err := os.ReadFile(cookieFile)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cookieFile)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.CookieFile != cookieFile || got.CookieCount != 1 || !got.Cleanup.Closed || got.Cleanup.TargetID != "created-page" {
		t.Fatalf("result=%+v", got)
	}
	if info.Mode().Perm() != 0o600 || !strings.Contains(string(content), "SAPISID\tsynthetic-youtube-auth") {
		t.Fatalf("mode=%o content=%q", info.Mode().Perm(), content)
	}
	if strings.Contains(out.String(), "synthetic-youtube-auth") {
		t.Fatalf("command output leaked cookie value: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK || strings.Contains(out.String(), "created-page") {
		t.Fatalf("owned target leaked: exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}
