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
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestEventsWaitFindsHistoricalNestedEventAndAllContent(t *testing.T) {
	path := writeEventWaitFixture(t, []string{
		`{"ok":true,"type":"ready"}`,
		`{"ok":true,"type":"event","event":{"method":"Runtime.consoleAPICalled","params":{"text":"decoy"}}}`,
		`{"ok":true,"type":"event","event":{"method":"Page.loadEventFired","params":{"marker":"history-target","phase":"ready"}}}`,
	})

	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "wait", "--file", path,
		"--method", "Page.loadEventFired",
		"--contains", "history-target",
		"--contains", "\"phase\":\"ready\"",
		"--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("events wait exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("events wait output is invalid JSON: %v; output=%s", err, out.String())
	}
	if result["ok"] != true {
		t.Fatalf("events wait result = %#v, want success", result)
	}
	event, ok := result["event"].(map[string]any)
	if !ok || event["method"] != "Page.loadEventFired" {
		t.Fatalf("matched event = %#v, want Page.loadEventFired", result["event"])
	}
	wait, ok := result["wait"].(map[string]any)
	if !ok || wait["matched_method"] != "Page.loadEventFired" {
		t.Fatalf("wait metadata = %#v, want matched method", result["wait"])
	}
	wantOffset := len([]byte(strings.Join([]string{
		`{"ok":true,"type":"ready"}`,
		`{"ok":true,"type":"event","event":{"method":"Runtime.consoleAPICalled","params":{"text":"decoy"}}}`,
		`{"ok":true,"type":"event","event":{"method":"Page.loadEventFired","params":{"marker":"history-target","phase":"ready"}}}`,
	}, "\n") + "\n"))
	if got, ok := result["offset"].(float64); !ok || int(got) != wantOffset {
		t.Fatalf("matched offset = %#v, want %d", result["offset"], wantOffset)
	}
}

func TestEventsWaitOffsetChainingDoesNotRematchHistory(t *testing.T) {
	first := `{"ok":true,"type":"event","event":{"method":"Page.frameNavigated","params":{"url":"https://example.test/first"}}}`
	second := `{"ok":true,"type":"event","event":{"method":"Page.frameNavigated","params":{"url":"https://example.test/second"}}}`
	path := writeEventWaitFixture(t, []string{first, second})

	run := func(offset string) map[string]any {
		var out, errOut bytes.Buffer
		args := []string{"events", "wait", "--file", path, "--method", "Page.frameNavigated", "--json"}
		if offset != "" {
			args = append(args, "--from-offset", offset)
		}
		code := cli.ExecuteWithInput(context.Background(), args, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("events wait offset=%q exit=%d stdout=%s stderr=%s", offset, code, out.String(), errOut.String())
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("events wait offset=%q output is invalid JSON: %v", offset, err)
		}
		return result
	}

	firstResult := run("")
	firstEvent := firstResult["event"].(map[string]any)
	if firstEvent["params"].(map[string]any)["url"] != "https://example.test/first" {
		t.Fatalf("first event = %#v, want first record", firstEvent)
	}
	firstOffset := fmt.Sprintf("%d", int(firstResult["offset"].(float64)))
	secondResult := run(firstOffset)
	secondEvent := secondResult["event"].(map[string]any)
	if secondEvent["params"].(map[string]any)["url"] != "https://example.test/second" {
		t.Fatalf("second event = %#v, want second record after offset %s", secondEvent, firstOffset)
	}
}

func TestEventsWaitMatchesRawCDPRecord(t *testing.T) {
	path := writeEventWaitFixture(t, []string{
		`{"method":"Runtime.exceptionThrown","params":{"text":"synthetic-failure"}}`,
	})
	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "wait", "--file", path,
		"--method", "Runtime.exceptionThrown",
		"--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("raw events wait exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("raw events wait output is invalid JSON: %v", err)
	}
	if result["event"] != nil || result["record"].(map[string]any)["method"] != "Runtime.exceptionThrown" {
		t.Fatalf("raw events wait result = %#v, want top-level raw record without inner event", result)
	}
}

func TestEventsWaitWakesForCompleteAppendedLineAfterPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- cli.ExecuteWithInput(ctx, []string{
			"events", "wait", "--file", path,
			"--method", "Runtime.consoleAPICalled",
			"--contains", "append-target",
			"--json",
		}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	}()

	time.Sleep(80 * time.Millisecond)
	partial := `{"ok":true,"type":"event","event":{"method":"Runtime.consoleAPICalled","params":{"text":"append-target"}}}`
	if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-done:
		if code != cli.ExitOK {
			t.Fatalf("events wait append exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
	case <-time.After(time.Second):
		t.Fatal("events wait did not wake after complete appended line")
	}
}

func TestEventsWaitTimeoutIsBoundedAndBrowserFree(t *testing.T) {
	t.Setenv("CDP_BROWSER_MODE", "not-a-real-mode")
	path := writeEventWaitFixture(t, []string{`{"ok":true,"type":"ready"}`})
	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{
		"events", "wait", "--file", path,
		"--method", "Network.loadingFailed",
		"--timeout", "60ms", "--json",
	}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitTimeout {
		t.Fatalf("events wait timeout exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitTimeout, out.String(), errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("timeout output is invalid JSON: %v", err)
	}
	if result["ok"] != false || result["code"] != "event_wait_timeout" {
		t.Fatalf("timeout result = %#v, want typed event_wait_timeout", result)
	}
}

func TestEventsWaitRejectsMissingPredicateAndUnknownFlag(t *testing.T) {
	path := writeEventWaitFixture(t, []string{`{"ok":true,"type":"ready"}`})
	var out, errOut bytes.Buffer
	code := cli.ExecuteWithInput(context.Background(), []string{"events", "wait", "--file", path, "--json"}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("events wait without predicate exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.ExecuteWithInput(context.Background(), []string{"events", "wait", "--file", path, "--method", "Page.loadEventFired", "--unknown", "--json"}, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
	if code == cli.ExitOK {
		t.Fatalf("events wait unknown flag unexpectedly succeeded: stdout=%s stderr=%s", out.String(), errOut.String())
	}
	for _, args := range [][]string{
		{"events", "wait", "--file", path, "--method", "Page.loadEventFired", "--contains", ""},
		{"events", "wait", "--file", path, "--method", "not-a-method"},
	} {
		var out, errOut bytes.Buffer
		code := cli.ExecuteWithInput(context.Background(), args, strings.NewReader(""), &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitUsage {
			t.Fatalf("events wait args=%v exit=%d, want %d; stdout=%s stderr=%s", args, code, cli.ExitUsage, out.String(), errOut.String())
		}
	}
}

func writeEventWaitFixture(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	data := []byte(strings.Join(lines, "\n") + "\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
