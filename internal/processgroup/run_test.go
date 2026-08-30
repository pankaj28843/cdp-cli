package processgroup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReturnsOutputAndProcessFailure(t *testing.T) {
	bin := writeProcessGroupFixture(t, `#!/bin/sh
set -eu
printf 'synthetic-output'
exit 7
`)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), bin, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run returned nil error for failed process")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("Run error = %v, want exit status 7", err)
	}
	if stdout.String() != "synthetic-output" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunRejectsCanceledContextBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, "missing-processgroup-fixture", nil, io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context cancellation", err)
	}
}

func TestRunWithOptionsPreservesManagedEnvironment(t *testing.T) {
	bin := writeProcessGroupFixture(t, `#!/bin/sh
printf '%s' "$CDP_PROCESSGROUP_TEST_VALUE"
`)
	var stdout bytes.Buffer
	env := append(os.Environ(), "CDP_PROCESSGROUP_TEST_VALUE=managed-value")
	err := RunWithOptions(context.Background(), bin, nil, Options{Env: env}, &stdout, io.Discard)
	if err != nil {
		t.Fatalf("RunWithOptions returned error: %v", err)
	}
	if stdout.String() != "managed-value" {
		t.Fatalf("managed environment output = %q, want managed-value", stdout.String())
	}
}

func TestRunWithOptionsProvidesManagedStdin(t *testing.T) {
	bin := writeProcessGroupFixture(t, `#!/bin/sh
cat
`)
	var stdout bytes.Buffer
	err := RunWithOptions(context.Background(), bin, nil, Options{
		Stdin: strings.NewReader("managed-input"),
	}, &stdout, io.Discard)
	if err != nil {
		t.Fatalf("RunWithOptions returned error: %v", err)
	}
	if stdout.String() != "managed-input" {
		t.Fatalf("managed stdin output = %q, want managed-input", stdout.String())
	}
}

func writeProcessGroupFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "owned-process")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
