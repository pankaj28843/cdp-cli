package availability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckAllowsOnlineEnvironmentAndPersistsObservation(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	result, err := Check(context.Background(), Options{
		StateDir: stateDir,
		Now:      func() time.Time { return now },
		InternetProbe: func(context.Context) ProbeResult {
			return ProbeResult{Online: true, Reason: "connectivity_probe_ok"}
		},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.Allowed || result.State != "ready" || result.Network != "online" {
		t.Fatalf("result = %+v, want ready online", result)
	}
	if result.LastObservedAt == "" {
		t.Fatalf("result = %+v, want persisted observation timestamp", result)
	}
	if _, err := os.Stat(filepath.Join(stateDir, stateDirectoryName, stateFileName)); err != nil {
		t.Fatalf("state file was not written: %v", err)
	}
}

func TestCheckBlocksOfflineWithoutSleepGap(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	check := func(online bool) Result {
		result, err := Check(context.Background(), Options{
			StateDir: stateDir,
			Now:      func() time.Time { return now },
			InternetProbe: func(context.Context) ProbeResult {
				if online {
					return ProbeResult{Online: true}
				}
				return ProbeResult{Reason: "connectivity_probe_failed"}
			},
		})
		if err != nil {
			t.Fatalf("Check returned error: %v", err)
		}
		return result
	}

	if result := check(true); !result.Allowed {
		t.Fatalf("initial result = %+v, want allowed", result)
	}
	now = now.Add(30 * time.Second)
	result := check(false)
	if result.Allowed || result.State != "offline" || result.Network != "offline" || result.SleepGapDetected {
		t.Fatalf("offline result = %+v, want ordinary offline block", result)
	}
}

func TestCheckDetectsWakeGapAndStartsCooldown(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	options := Options{
		StateDir:          stateDir,
		Now:               func() time.Time { return now },
		SleepGapThreshold: time.Minute,
		Cooldown:          3 * time.Minute,
		InternetProbe: func(context.Context) ProbeResult {
			t.Fatalf("internet probe must not run on the first post-wake tick")
			return ProbeResult{Online: true}
		},
	}
	first, err := Check(context.Background(), Options{
		StateDir: stateDir,
		Now:      func() time.Time { return now },
		InternetProbe: func(context.Context) ProbeResult {
			return ProbeResult{Online: true}
		},
	})
	if err != nil || !first.Allowed {
		t.Fatalf("initial result = %+v, err=%v", first, err)
	}
	now = now.Add(10 * time.Minute)
	wake, err := Check(context.Background(), options)
	if err != nil {
		t.Fatalf("wake Check returned error: %v", err)
	}
	if wake.Allowed || wake.State != "suspended" || !wake.SleepGapDetected || wake.Network != "not_checked" {
		t.Fatalf("wake result = %+v, want suspended without network probe", wake)
	}
	now = now.Add(30 * time.Second)
	cooldown, err := Check(context.Background(), Options{
		StateDir: stateDir,
		Now:      func() time.Time { return now },
		InternetProbe: func(context.Context) ProbeResult {
			return ProbeResult{Online: true}
		},
	})
	if err != nil {
		t.Fatalf("cooldown Check returned error: %v", err)
	}
	if cooldown.Allowed || cooldown.State != "cooldown" || cooldown.Reason != "post_wake_cooldown" {
		t.Fatalf("cooldown result = %+v, want cooldown", cooldown)
	}
}

func TestCheckFailsClosedForCorruptState(t *testing.T) {
	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, stateDirectoryName, stateFileName)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Check(context.Background(), Options{StateDir: stateDir})
	if err == nil || result.Allowed || result.State != "unknown" || result.Reason != "availability_state_unreadable" {
		t.Fatalf("result = %+v, err=%v, want fail-closed state error", result, err)
	}
}

func TestCheckUsesConfiguredProbeTimeout(t *testing.T) {
	stateDir := t.TempDir()
	result, err := Check(context.Background(), Options{
		StateDir:     stateDir,
		ProbeTimeout: time.Nanosecond,
		InternetProbe: func(ctx context.Context) ProbeResult {
			<-ctx.Done()
			return ProbeResult{Reason: "connectivity_probe_timeout"}
		},
	})
	if err != nil || result.Allowed || result.State != "offline" {
		t.Fatalf("result = %+v, err=%v, want bounded offline result", result, err)
	}
}

func TestTryAcquireRepairLockSerializesAutoHealWork(t *testing.T) {
	stateDir := t.TempDir()
	first, acquired, err := TryAcquireRepairLock(context.Background(), stateDir)
	if err != nil || !acquired || first == nil {
		t.Fatalf("first repair lock = acquired %v, lock %v, err %v", acquired, first, err)
	}
	defer first.Release()

	second, acquired, err := TryAcquireRepairLock(context.Background(), stateDir)
	if err != nil || acquired || second != nil {
		t.Fatalf("contending repair lock = acquired %v, lock %v, err %v; want busy", acquired, second, err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release first repair lock: %v", err)
	}
	third, acquired, err := TryAcquireRepairLock(context.Background(), stateDir)
	if err != nil || !acquired || third == nil {
		t.Fatalf("third repair lock = acquired %v, lock %v, err %v; want reacquired", acquired, third, err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("release third repair lock: %v", err)
	}
}

func TestCheckConnectivityURLUsesSmallHeadProbeAndBlocksBadStatus(t *testing.T) {
	var method string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	result, err := Check(context.Background(), Options{StateDir: t.TempDir(), ConnectivityURL: server.URL})
	if err != nil || !result.Allowed || result.Network != "online" || method != http.MethodHead {
		t.Fatalf("result = %+v, method=%q, err=%v; want online HEAD probe", result, method, err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	result, err = Check(context.Background(), Options{StateDir: t.TempDir(), ConnectivityURL: bad.URL})
	if err != nil || result.Allowed || result.State != "offline" || result.Reason != "connectivity_http_status" {
		t.Fatalf("bad-status result = %+v, err=%v; want offline block", result, err)
	}
}
