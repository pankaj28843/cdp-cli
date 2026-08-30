package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/spf13/cobra"
)

func TestEventStreamRuntimeRegistrationStatusPresent(t *testing.T) {
	stateDir := t.TempDir()
	expected := eventStreamRegistrationTestRuntime(stateDir, "proc:expected")
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", expected); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	status, err := eventStreamRuntimeRegistrationStatus(context.Background(), stateDir, "headless", expected)
	if err != nil || status != eventStreamRuntimeRegistrationPresent {
		t.Fatalf("registration status = %q, err=%v; want present", status, err)
	}
}

func TestEventStreamRuntimeRegistrationStatusRetiredWhenReadableReplacement(t *testing.T) {
	stateDir := t.TempDir()
	expected := eventStreamRegistrationTestRuntime(stateDir, "proc:expected")
	replacement := expected
	replacement.PID = expected.PID + 1
	replacement.ProcessStartTime = "proc:replacement"
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", replacement); err != nil {
		t.Fatalf("SaveRuntimeForMode replacement returned error: %v", err)
	}

	status, err := eventStreamRuntimeRegistrationStatus(context.Background(), stateDir, "headless", expected)
	if err != nil || status != eventStreamRuntimeRegistrationRetired {
		t.Fatalf("registration status = %q, err=%v; want retired replacement", status, err)
	}
}

func TestEventStreamRuntimeRegistrationStatusUnknownWhenStateIsAmbiguous(t *testing.T) {
	tests := []struct {
		name      string
		file      []byte
		writeFile bool
		current   *daemon.Runtime
	}{
		{name: "missing"},
		{name: "empty runtime", file: []byte{}, writeFile: true},
		{name: "zero runtime", file: []byte(`{}`), writeFile: true},
		{name: "corrupt", file: []byte(`{"pid":`), writeFile: true},
		{name: "strong identity missing", current: func() *daemon.Runtime {
			runtime := eventStreamRegistrationTestRuntime("", "")
			runtime.ProcessStartTime = ""
			return &runtime
		}()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			expected := eventStreamRegistrationTestRuntime(stateDir, "proc:expected")
			if test.writeFile {
				if err := os.MkdirAll(filepath.Dir(daemon.RuntimePathForMode(stateDir, "headless")), 0o700); err != nil {
					t.Fatalf("create runtime directory: %v", err)
				}
				if err := os.WriteFile(daemon.RuntimePathForMode(stateDir, "headless"), test.file, 0o600); err != nil {
					t.Fatalf("write runtime fixture: %v", err)
				}
			} else if test.current != nil {
				current := *test.current
				current.SocketPath = expected.SocketPath
				if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", current); err != nil {
					t.Fatalf("SaveRuntimeForMode ambiguous runtime returned error: %v", err)
				}
			}

			status, err := eventStreamRuntimeRegistrationStatus(context.Background(), stateDir, "headless", expected)
			if err != nil || status != eventStreamRuntimeUnknown {
				t.Fatalf("registration status = %q, err=%v; want unknown", status, err)
			}
		})
	}
}

func TestEventStreamRuntimeRegistrationStatusSupportsCompleteLegacyIdentity(t *testing.T) {
	stateDir := t.TempDir()
	expected := eventStreamRegistrationTestRuntime(stateDir, "")
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headed", expected); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	status, err := eventStreamRuntimeRegistrationStatus(context.Background(), stateDir, "headed", expected)
	if err != nil || status != eventStreamRuntimeRegistrationPresent {
		t.Fatalf("matching legacy registration status = %q, err=%v; want present", status, err)
	}

	replacement := expected
	replacement.StartedAt = "2026-08-30T17:00:01Z"
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headed", replacement); err != nil {
		t.Fatalf("SaveRuntimeForMode legacy replacement returned error: %v", err)
	}
	status, err = eventStreamRuntimeRegistrationStatus(context.Background(), stateDir, "headed", expected)
	if err != nil || status != eventStreamRuntimeRegistrationRetired {
		t.Fatalf("mismatched legacy registration status = %q, err=%v; want retired", status, err)
	}

	incomplete := expected
	incomplete.SocketPath = ""
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headed", incomplete); err != nil {
		t.Fatalf("SaveRuntimeForMode incomplete legacy returned error: %v", err)
	}
	status, err = eventStreamRuntimeRegistrationStatus(context.Background(), stateDir, "headed", expected)
	if err != nil || status != eventStreamRuntimeUnknown {
		t.Fatalf("incomplete legacy registration status = %q, err=%v; want unknown", status, err)
	}
}

func TestEventStreamRuntimeRegistrationStatusHonorsCancellation(t *testing.T) {
	stateDir := t.TempDir()
	expected := eventStreamRegistrationTestRuntime(stateDir, "proc:expected")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status, err := eventStreamRuntimeRegistrationStatus(ctx, stateDir, "headless", expected)
	if !errors.Is(err, context.Canceled) || status != eventStreamRuntimeUnknown {
		t.Fatalf("canceled registration status = %q, err=%v; want unknown/context canceled", status, err)
	}
}

func TestAppEventStreamRuntimeRegistrationCheckUsesDaemonClientRuntime(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("CDP_BROWSER_MODE", "headless")
	expected := eventStreamRegistrationTestRuntime(stateDir, "proc:expected")
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", expected); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	a := &app{opts: options{stateDir: stateDir}, root: &cobra.Command{}}
	check := a.eventStreamRuntimeRegistrationCheck(daemon.RuntimeClient{Runtime: expected})
	if check == nil {
		t.Fatal("event stream runtime registration check = nil, want daemon-backed check")
	}
	status, err := check(context.Background())
	if err != nil || status != eventStreamRuntimeRegistrationPresent {
		t.Fatalf("daemon-backed registration check = %q, err=%v; want present", status, err)
	}

	replacement := expected
	replacement.PID++
	replacement.ProcessStartTime = "proc:replacement"
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", replacement); err != nil {
		t.Fatalf("SaveRuntimeForMode replacement returned error: %v", err)
	}
	status, err = check(context.Background())
	if err != nil || status != eventStreamRuntimeRegistrationRetired {
		t.Fatalf("daemon-backed replacement check = %q, err=%v; want retired", status, err)
	}
}

func TestPumpEventStreamLivenessRetiresOnRuntimeReplacementBeforeHeartbeat(t *testing.T) {
	fake := &eventStreamLivenessFakeClient{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := pumpEventStreamLivenessWithRegistration(ctx, fake, "session-1", time.Millisecond, 2, func(context.Context) (string, error) {
		return eventStreamRuntimeRegistrationRetired, nil
	})
	select {
	case result, ok := <-results:
		if !ok {
			t.Fatal("registration retirement channel closed without a result")
		}
		if !result.retired || result.reason != "runtime_retired" || result.source != "runtime_registration" {
			t.Fatalf("liveness result = %+v, want runtime-registration retirement", result)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("liveness pump did not retire after runtime replacement")
	}
	if count, _ := fake.snapshot(); count != 0 {
		t.Fatalf("heartbeat calls = %d, want no heartbeat before definitive retirement", count)
	}
}

func TestPumpEventStreamLivenessUnknownRegistrationStillUsesHeartbeat(t *testing.T) {
	fake := &eventStreamLivenessFakeClient{outcomes: []error{
		errors.New("first heartbeat failure"),
		errors.New("second heartbeat failure"),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := pumpEventStreamLivenessWithRegistration(ctx, fake, "session-1", time.Millisecond, 2, func(context.Context) (string, error) {
		return eventStreamRuntimeUnknown, nil
	})
	select {
	case result, ok := <-results:
		if !ok {
			t.Fatal("heartbeat retirement channel closed without a result")
		}
		if !result.retired || result.reason != "exact_session_unhealthy" || result.source != "" {
			t.Fatalf("liveness result = %+v, want heartbeat retirement", result)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("liveness pump did not retain the heartbeat path for unknown registration")
	}
	if count, _ := fake.snapshot(); count < 2 {
		t.Fatalf("heartbeat calls = %d, want two-strike heartbeat after unknown registration", count)
	}
}

func TestPumpEventStreamLivenessRegistrationErrorStillUsesHeartbeat(t *testing.T) {
	fake := &eventStreamLivenessFakeClient{outcomes: []error{
		errors.New("first heartbeat failure"),
		errors.New("second heartbeat failure"),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := pumpEventStreamLivenessWithRegistration(ctx, fake, "session-1", time.Millisecond, 2, func(context.Context) (string, error) {
		return eventStreamRuntimeUnknown, errors.New("ambiguous runtime state")
	})
	select {
	case result, ok := <-results:
		if !ok {
			t.Fatal("heartbeat retirement channel closed without a result")
		}
		if !result.retired || result.reason != "exact_session_unhealthy" {
			t.Fatalf("liveness result = %+v, want heartbeat retirement after registration error", result)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("liveness pump did not retain heartbeat after ambiguous registration error")
	}
	if count, _ := fake.snapshot(); count < 2 {
		t.Fatalf("heartbeat calls = %d, want two-strike heartbeat after registration error", count)
	}
}

func TestPumpEventStreamLivenessPreCanceledRegistrationDoesNotRunCheck(t *testing.T) {
	called := false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := pumpEventStreamLivenessWithRegistration(ctx, &eventStreamLivenessFakeClient{}, "session-1", time.Millisecond, 2, func(context.Context) (string, error) {
		called = true
		return eventStreamRuntimeRegistrationRetired, nil
	})

	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("pre-canceled liveness pump emitted a result")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("pre-canceled liveness pump did not close")
	}
	if called {
		t.Fatal("pre-canceled liveness pump ran the registration check")
	}
}

func TestPumpEventStreamLivenessCancellationDuringRegistrationDoesNotRetire(t *testing.T) {
	called := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	results := pumpEventStreamLivenessWithRegistration(ctx, &eventStreamLivenessFakeClient{}, "session-1", time.Millisecond, 2, func(ctx context.Context) (string, error) {
		close(called)
		<-ctx.Done()
		return eventStreamRuntimeRegistrationRetired, ctx.Err()
	})

	select {
	case <-called:
		cancel()
	case <-time.After(100 * time.Millisecond):
		cancel()
		t.Fatal("liveness pump did not start registration check")
	}
	select {
	case _, ok := <-results:
		if ok {
			t.Fatal("canceled registration check emitted a retirement result")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("liveness pump did not close after registration cancellation")
	}
}

func TestEventStreamLivenessMetadataIncludesOnlySemanticRegistrationSource(t *testing.T) {
	metadata := eventStreamLivenessMetadata(&eventStreamLivenessResult{
		retired:             true,
		consecutiveFailures: 0,
		reason:              "runtime_retired",
		source:              "runtime_registration",
	})
	if metadata["reason"] != "runtime_retired" || metadata["source"] != "runtime_registration" {
		t.Fatalf("registration retirement metadata = %#v, want semantic reason/source", metadata)
	}
	if _, ok := eventStreamLivenessMetadata(&eventStreamLivenessResult{reason: "exact_session_unhealthy"})["source"]; ok {
		t.Fatal("heartbeat retirement metadata unexpectedly exposed a source")
	}
}

func eventStreamRegistrationTestRuntime(stateDir, processStartTime string) daemon.Runtime {
	return daemon.Runtime{
		PID:              4242,
		StartedAt:        "2026-08-30T17:00:00Z",
		BrowserMode:      "headless",
		ConnectionMode:   "browser_url",
		SocketPath:       filepath.Join(stateDir, "headless", daemon.RuntimeSocketFileName),
		ProcessStartTime: processStartTime,
	}
}
