package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

func TestRunManagedCronTaskCapturesPassiveHeadedChildInBoundedLog(t *testing.T) {
	stateDir := t.TempDir()
	script := filepath.Join(t.TempDir(), "fake-cdp")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\n"), 0o700); err != nil {
		t.Fatalf("write fake cdp: %v", err)
	}
	previous := cronRunExecutable
	cronRunExecutable = func() (string, error) { return script, nil }
	t.Cleanup(func() { cronRunExecutable = previous })

	opts := defaultCronRenderOptions()
	task, ok := managedCronTaskByID(opts, cronTaskHeadedDaemonKeepalive)
	if !ok {
		t.Fatal("headed managed task is missing")
	}
	result, err := runManagedCronTask(context.Background(), stateDir, task, opts)
	if err != nil {
		t.Fatalf("run managed headed task: %v", err)
	}
	if result["log"] == nil {
		t.Fatalf("managed task result = %+v, want bounded log metadata", result)
	}
	logBytes, err := os.ReadFile(filepath.Join(stateDir, task.LogName))
	if err != nil {
		t.Fatalf("read managed log: %v", err)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, "daemon keepalive") || !strings.Contains(logText, "--probe passive") {
		t.Fatalf("managed log = %q, want passive headed child command", logText)
	}
	for _, forbidden := range []string{" login ", " consent ", " ask ", " click ", " type "} {
		if strings.Contains(" "+logText+" ", forbidden) {
			t.Fatalf("managed child contains human action %q: %s", forbidden, logText)
		}
	}
}

func TestCronRunReportsAlreadyRunningWithoutLaunchingChild(t *testing.T) {
	stateDir := t.TempDir()
	task, ok := managedCronTaskByID(defaultCronRenderOptions(), cronTaskHeadedDaemonKeepalive)
	if !ok {
		t.Fatal("headed managed task is missing")
	}
	lockPath := filepath.Join(stateDir, "locks", task.LockName+".lock")
	lock, acquired, err := artifacts.TryAcquireOwnerOnlyFileLock(lockPath)
	if err != nil || !acquired {
		t.Fatalf("hold cron lock = acquired %v, err %v", acquired, err)
	}
	defer lock.Release()

	var stdout, stderr bytes.Buffer
	code := Execute(
		context.Background(),
		[]string{"--state-dir", stateDir, "cron", "run", cronTaskHeadedDaemonKeepalive, "--json"},
		&stdout,
		&stderr,
		BuildInfo{},
	)
	if code != ExitOK {
		t.Fatalf("cron run busy exit = %d; stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK       bool   `json:"ok"`
		Task     string `json:"task"`
		State    string `json:"state"`
		Executed bool   `json:"executed"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode busy cron run: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.Task != cronTaskHeadedDaemonKeepalive || got.State != "already_running" || got.Executed {
		t.Fatalf("busy cron run = %+v, want non-mutating typed skip", got)
	}
}
