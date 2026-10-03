package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// This opt-in gate runs the production JavaScript through installed cdp and an
// isolated managed browser. It exercises native input without provider traffic.
func TestGrokComposerInstalled(t *testing.T) {
	binary := os.Getenv("CDP_GROK_COMPOSER_INSTALLED_BINARY")
	if binary == "" {
		t.Skip("set CDP_GROK_COMPOSER_INSTALLED_BINARY to the installed cdp executable")
	}
	source, err := os.ReadFile("ask.go")
	if err != nil {
		t.Fatal(err)
	}
	observerStart := strings.Index(string(source), "func observeComposer(")
	selectionStart := strings.Index(string(source), "func prepareExactPrompt(")
	selectionEnd := strings.Index(string(source), "func prepareVerifiedPrompt(")
	if observerStart < 0 || selectionStart <= observerStart || selectionEnd <= selectionStart {
		t.Fatal("production composer function boundaries changed")
	}
	observer := regexp.MustCompile("expression := fmt.Sprintf\\(`([\\s\\S]*?)`, promptJSON\\)").FindSubmatch(source[observerStart:selectionStart])
	selection := regexp.MustCompile("evaluateInto\\(ctx, session, `([\\s\\S]*?)`, &selected\\)").FindSubmatch(source[selectionStart:selectionEnd])
	if len(observer) != 2 || len(selection) != 2 {
		t.Fatal("production composer script extraction contract changed")
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"resource_budget":{"min_free_memory_mb":1,"min_free_disk_mb":1,"max_load_per_cpu":999999}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	base := []string{"--config", configPath, "--state-dir", filepath.Join(dir, "state"), "--browser-mode", "headless"}
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CDP_") {
			env = append(env, entry)
		}
	}
	call := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, append(append(append([]string{}, base...), args...), "--timeout", "45s", "--json")...)
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	mustCall := func(tb testing.TB, args ...string) []byte {
		tb.Helper()
		output, err := call(args...)
		if err != nil {
			tb.Fatalf("synthetic composer command %s: %v: %s", args[0], err, output)
		}
		return output
	}
	t.Cleanup(func() {
		if output, err := call("daemon", "stop", "--force-managed"); err != nil {
			t.Errorf("stop owned synthetic browser: %v: %s", err, output)
		}
	})
	mustCall(t, "daemon", "keepalive", "--repair")
	var opened struct {
		Page struct {
			ID string `json:"id"`
		} `json:"page"`
	}
	if err := json.Unmarshal(mustCall(t, "open", "data:text/html,<html><body></body></html>", "--created-by", "grok-composer-fixture"), &opened); err != nil || opened.Page.ID == "" {
		t.Fatalf("open synthetic page: %v", err)
	}
	t.Cleanup(func() { mustCall(t, "page", "close", "--target", opened.Page.ID) })
	evaluate := func(tb testing.TB, expression string, value any) {
		tb.Helper()
		var result struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
		}
		if err := json.Unmarshal(mustCall(tb, "eval", expression, "--target", opened.Page.ID), &result); err != nil {
			tb.Fatal(err)
		}
		if value != nil {
			if err := json.Unmarshal(result.Result.Value, value); err != nil {
				tb.Fatal(err)
			}
		}
	}
	const textarea = `<textarea aria-label="Ask Grok anything" style="width:400px;height:60px">old text</textarea>`
	const editable = `<div contenteditable="true" role="textbox" aria-label="Ask Grok anything" style="width:400px;min-height:60px">old text</div>`
	const transition = `document.querySelector('textarea').addEventListener('input', e => {const next=document.createElement('div'); next.contentEditable='true'; next.setAttribute('role','textbox'); next.setAttribute('aria-label','Ask Grok anything'); next.style.cssText='width:400px;min-height:60px'; next.innerText=e.target.value; e.target.replaceWith(next); next.focus();});`
	cases := []struct {
		name, body, setup string
		count             int
		ready             bool
	}{
		{"textarea", textarea, "", 1, true},
		{"editable replacement", editable, "", 1, true},
		{"transition after first input", textarea, transition, 1, true},
		{"ambiguous current editors", textarea + editable, "", 2, false},
		{"readonly textarea", strings.Replace(textarea, "<textarea ", "<textarea readonly ", 1), "", 1, false},
		{"readonly editable", strings.Replace(editable, "<div ", `<div aria-readonly="true" `, 1), "", 1, false},
		{"disabled editable", strings.Replace(editable, "<div ", `<div aria-disabled="true" `, 1), "", 1, false},
		{"hidden duplicate", textarea + strings.Replace(editable, "<div ", `<div hidden `, 1), "", 1, true},
		{"unrelated textbox", strings.Replace(editable, "Ask Grok anything", "Other input", 1), "", 0, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(test.body + `<button aria-label="Model select">Fast</button><button aria-label="Submit" onclick="window.sendCount++">Send</button>`)
			evaluate(t, fmt.Sprintf("document.body.innerHTML=%s; window.sendCount=0; %s true", body, test.setup), nil)
			var selected struct {
				OK bool `json:"ok"`
			}
			evaluate(t, string(selection[1]), &selected)
			if selected.OK != test.ready {
				t.Fatalf("selection allowed=%v want %v", selected.OK, test.ready)
			}
			const prompt = "Καλημέρα 🌍\nReplacement üñicode"
			if selected.OK {
				params, _ := json.Marshal(map[string]string{"text": prompt})
				mustCall(t, "protocol", "exec", "Input.insertText", string(params), "--target", opened.Page.ID)
			}
			promptJSON, _ := json.Marshal(prompt)
			var observed struct {
				Count   int  `json:"editor_count"`
				Ready   bool `json:"editor_ready"`
				Matches bool `json:"prompt_matches"`
			}
			evaluate(t, fmt.Sprintf(string(observer[1]), promptJSON), &observed)
			if observed.Count != test.count || observed.Ready != test.ready || observed.Matches != test.ready {
				t.Fatalf("composer contract=%+v want count=%d ready/matches=%v", observed, test.count, test.ready)
			}
			var sends int
			evaluate(t, "window.sendCount", &sends)
			if sends != 0 {
				t.Fatal("synthetic input unexpectedly dispatched Send")
			}
		})
	}
}
