//go:build unix

package meta

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRunJSONCommandCancellationLetsChildCleanup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "provider-state")
	t.Setenv("GO_WANT_META_COMMAND_HELPER", "wait-for-interrupt")
	t.Setenv("META_COMMAND_HELPER_MARKER", marker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultChannel := make(chan CommandResult, 1)
	go func() {
		resultChannel <- runJSONCommand(
			ctx,
			[]string{os.Args[0], "-test.run=^TestMetaCommandHelper$"},
			5*time.Second,
		)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, err := os.ReadFile(marker)
		if err == nil && string(raw) == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider helper did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	result := <-resultChannel
	if result.OK || result.ErrorType != "interrupted" {
		t.Fatalf("canceled command result = %+v", result)
	}
	if result.ReturnCode == nil || *result.ReturnCode != 9 {
		t.Fatalf("canceled command return code = %v, want 9", result.ReturnCode)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "clean" {
		t.Fatalf("provider cleanup marker = %q, want clean", raw)
	}
}

func TestSafeOwnedCommandGroupRejectsUnsafeIdentifiers(t *testing.T) {
	tests := []struct {
		name               string
		pid                int
		processGroup       int
		runnerProcessGroup int
		safe               bool
	}{
		{
			name:               "owned group",
			pid:                41,
			processGroup:       41,
			runnerProcessGroup: 7,
			safe:               true,
		},
		{
			name:               "zero pid",
			pid:                0,
			processGroup:       0,
			runnerProcessGroup: 7,
		},
		{
			name:               "init group",
			pid:                1,
			processGroup:       1,
			runnerProcessGroup: 7,
		},
		{
			name:               "different group leader",
			pid:                41,
			processGroup:       42,
			runnerProcessGroup: 7,
		},
		{
			name:               "runner group",
			pid:                41,
			processGroup:       41,
			runnerProcessGroup: 41,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := safeOwnedCommandGroup(
				test.pid,
				test.processGroup,
				test.runnerProcessGroup,
			); got != test.safe {
				t.Fatalf(
					"safeOwnedCommandGroup() = %v, want %v",
					got,
					test.safe,
				)
			}
		})
	}
}

func TestOwnedCommandGroupIsCapturedImmediatelyAfterStart(t *testing.T) {
	command := exec.Command("sleep", "60")
	prepareOwnedCommand(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	group := ownedCommandGroup(command)
	if group != command.Process.Pid ||
		!safeOwnedCommandGroup(
			command.Process.Pid,
			group,
			syscall.Getpgrp(),
		) {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf(
			"owned process group = %d, pid = %d",
			group,
			command.Process.Pid,
		)
	}
	_ = syscall.Kill(-group, syscall.SIGKILL)
	if err := command.Wait(); err == nil {
		t.Fatal("killed owned command exited successfully")
	}
}
