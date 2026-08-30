package processgroup

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestParseProcStatStartTimeHandlesParenthesesInCommandName(t *testing.T) {
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "x"
	}
	fields[18] = "987654"
	stat := "42 (worker ) with ) parens) S " + strings.Join(fields, " ")

	got, err := parseProcStatStartTime(stat)
	if err != nil {
		t.Fatalf("parseProcStatStartTime returned error: %v", err)
	}
	if got != "987654" {
		t.Fatalf("parseProcStatStartTime = %q, want opaque start token 987654", got)
	}
}

func TestParseProcStatStartTimeRejectsMalformedStat(t *testing.T) {
	if _, err := parseProcStatStartTime("42 missing-command-name"); err == nil {
		t.Fatal("parseProcStatStartTime accepted malformed stat data")
	}
}

func TestProcessStartTimeReturnsOpaqueIdentityForOwnProcess(t *testing.T) {
	token, err := ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("ProcessStartTime returned error: %v", err)
	}
	if token == "" || (!strings.HasPrefix(token, "proc:") && !strings.HasPrefix(token, "ps:")) {
		t.Fatalf("ProcessStartTime = %q, want prefixed opaque identity", token)
	}
	if strings.ContainsAny(token, "\r\n") {
		t.Fatalf("ProcessStartTime retained line breaks: %q", token)
	}
}

func TestProcessStartTimeRejectsInvalidPID(t *testing.T) {
	_, err := ProcessStartTime(context.Background(), 0)
	if err == nil {
		t.Fatal("ProcessStartTime accepted pid 0")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("invalid PID was misclassified as context cancellation: %v", err)
	}
}

func TestProcessStartTimeHonorsCanceledContextBeforeProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ProcessStartTime(ctx, os.Getpid())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessStartTime error = %v, want context cancellation", err)
	}
}

func TestRunProcessIdentityProbeBoundsOutputAndCancelsOwnedTree(t *testing.T) {
	bin := writeProcessGroupFixture(t, `#!/bin/sh
set -eu
printf '%0100000d' 1
while :; do sleep 1; done
`)
	_, err := runProcessIdentityProbe(context.Background(), bin, nil)
	if !errors.Is(err, ErrProcessIdentityOutputTooLarge) {
		t.Fatalf("runProcessIdentityProbe error = %v, want bounded-output error", err)
	}
}
