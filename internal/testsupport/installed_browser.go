package testsupport

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// InstalledBrowser owns an isolated daemon, Chrome profile and synthetic page.
// It exercises production JavaScript through the installed CLI without accounts.
type InstalledBrowser struct {
	binary    string
	base, env []string
	target    string
}

func NewInstalledBrowser(t testing.TB, binary string) *InstalledBrowser {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"resource_budget":{"min_free_memory_mb":1,"min_free_disk_mb":1,"max_load_per_cpu":999999}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	b := &InstalledBrowser{binary: binary, base: []string{"--config", configPath, "--state-dir", filepath.Join(dir, "state"), "--browser-mode", "headless"}}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CDP_") {
			b.env = append(b.env, entry)
		}
	}
	t.Cleanup(func() { b.Call(t, "daemon", "stop", "--force-managed") })
	b.Call(t, "daemon", "keepalive", "--repair")
	var opened struct {
		Page struct {
			ID string `json:"id"`
		} `json:"page"`
	}
	if err := json.Unmarshal(b.Call(t, "open", "data:text/html,<html><body></body></html>", "--created-by", "installed-js-fixture"), &opened); err != nil || opened.Page.ID == "" {
		t.Fatalf("open synthetic page: %v", err)
	}
	b.target = opened.Page.ID
	t.Cleanup(func() { b.Call(t, "page", "close", "--target", b.target) })
	return b
}

func (b *InstalledBrowser) Call(t testing.TB, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := append(append(append([]string{}, b.base...), args...), "--timeout", "45s", "--json")
	cmd := exec.CommandContext(ctx, b.binary, command...)
	cmd.Env = b.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic browser command %s: %v: %s", args[0], err, output)
	}
	return output
}

func (b *InstalledBrowser) Eval(t testing.TB, expression string, value any) {
	t.Helper()
	var result struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b.Call(t, "eval", expression, "--target", b.target), &result); err != nil {
		t.Fatal(err)
	}
	if value != nil {
		if err := json.Unmarshal(result.Result.Value, value); err != nil {
			t.Fatal(err)
		}
	}
}

func (b *InstalledBrowser) InsertText(t testing.TB, text string) {
	t.Helper()
	params, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	b.Call(t, "protocol", "exec", "Input.insertText", string(params), "--target", b.target)
}
