package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browser"
)

func TestEvaluateResourcePreflightDecisions(t *testing.T) {
	policy := resourcePreflightPolicy{MinFreeMemoryMB: 1000, MinFreeDiskMB: 1000, MaxLoadPerCPU: 2}
	passHost := hostResourceSnapshot{
		Memory: resourcePreflightCheck{Name: "memory", Status: "pass", AvailableMB: 4096, RequiredMB: 1000},
		Disk:   resourcePreflightCheck{Name: "disk", Status: "pass", AvailableMB: 8192, RequiredMB: 1000},
		Load:   resourcePreflightCheck{Name: "load", Status: "pass", LoadPerCPU: 0.5, MaxLoadPerCPU: 2},
	}
	healthyManaged := &browser.ManagedProcessReconcileResult{Checked: true, State: "healthy", LiveCount: 1}

	t.Run("pass", func(t *testing.T) {
		got := evaluateResourcePreflight(policy, "headless", passHost, healthyManaged, map[string]any{
			"resource_budget":    true,
			"tab_count":          2,
			"max_tabs":           25,
			"window_count":       1,
			"max_windows":        5,
			"window_count_known": true,
		})
		if got.Status != "pass" || got.State != "sufficient" || !got.HeavyWorkAllowed {
			t.Fatalf("resource preflight = %+v, want pass and allowed", got)
		}
	})

	t.Run("warn", func(t *testing.T) {
		host := passHost
		host.Memory = resourcePreflightCheck{Name: "memory", Status: "warn", Reason: "memory_near_minimum", AvailableMB: 1500, RequiredMB: 1000}
		got := evaluateResourcePreflight(policy, "headless", host, healthyManaged, nil)
		if got.Status != "warn" || got.State != "warning" || !got.HeavyWorkAllowed {
			t.Fatalf("resource preflight = %+v, want warning but allowed", got)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		got := evaluateResourcePreflight(policy, "headless", hostResourceSnapshot{
			Memory: unknownResourceCheck("memory", "unsupported"),
			Disk:   resourcePreflightCheck{Name: "disk", Status: "pass", AvailableMB: 8192, RequiredMB: 1000},
			Load:   resourcePreflightCheck{Name: "load", Status: "pass", LoadPerCPU: 0.5, MaxLoadPerCPU: 2},
		}, healthyManaged, nil)
		if got.Status != "unknown" || got.State != "unknown" || !got.HeavyWorkAllowed {
			t.Fatalf("resource preflight = %+v, want unknown but allowed", got)
		}
	})

	t.Run("skip host", func(t *testing.T) {
		host := passHost
		host.Disk = resourcePreflightCheck{Name: "disk", Status: "skip", Reason: "disk_below_minimum", AvailableMB: 10, RequiredMB: 1000}
		got := evaluateResourcePreflight(policy, "headless", host, healthyManaged, nil)
		if got.Status != "skip" || got.State != "blocked" || got.HeavyWorkAllowed {
			t.Fatalf("resource preflight = %+v, want blocked host resource", got)
		}
	})

	t.Run("skip managed process pressure", func(t *testing.T) {
		got := evaluateResourcePreflight(policy, "headless", passHost, &browser.ManagedProcessReconcileResult{
			Checked:      true,
			State:        "over_budget",
			LiveCount:    2,
			NextCommands: []string{"cdp --browser-mode headless daemon keepalive --managed-process-sweep --repair --force --json"},
		}, nil)
		if got.Status != "skip" || got.State != "blocked" || got.HeavyWorkAllowed {
			t.Fatalf("resource preflight = %+v, want blocked managed-process pressure", got)
		}
		if !resourcePreflightTestContains(got.NextCommands, "cdp --browser-mode headless daemon keepalive --managed-process-sweep --repair --force --json") {
			t.Fatalf("next_commands = %+v, want managed-process recovery", got.NextCommands)
		}
	})

	t.Run("skip browser budget", func(t *testing.T) {
		got := evaluateResourcePreflight(policy, "headless", passHost, healthyManaged, map[string]any{
			"resource_budget":    true,
			"tab_count":          25,
			"max_tabs":           25,
			"tabs_over_budget":   true,
			"window_count":       1,
			"max_windows":        5,
			"window_count_known": true,
		})
		if got.Status != "skip" || got.State != "blocked" || got.HeavyWorkAllowed {
			t.Fatalf("resource preflight = %+v, want blocked browser budget", got)
		}
	})

	t.Run("skip unknown renderer budget", func(t *testing.T) {
		got := evaluateResourcePreflight(policy, "headless", passHost, healthyManaged, map[string]any{
			"resource_budget":        true,
			"tab_count":              2,
			"max_tabs":               25,
			"window_count":           1,
			"max_windows":            5,
			"window_count_known":     true,
			"max_renderer_processes": 4,
			"renderer_count_known":   false,
		})
		if got.Status != "skip" || got.State != "blocked" || got.HeavyWorkAllowed {
			t.Fatalf("resource preflight = %+v, want blocked unknown renderer budget", got)
		}
		if len(got.Reasons) == 0 || got.Reasons[0] != "browser_budget_skip" {
			t.Fatalf("resource preflight reasons = %+v, want browser budget skip classification", got.Reasons)
		}
	})
}

func TestBrowserProfileSeedCopyDefaultSkipsWhenResourcePreflightBlocked(t *testing.T) {
	stateDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"resource_budget":{"min_free_disk_mb":999999999}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--browser-mode", "headless", "--config", configPath, "--state-dir", stateDir, "browser", "profile", "seed", "--strategy", "copy-default", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("browser profile seed exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK                bool   `json:"ok"`
		State             string `json:"state"`
		SeedAction        string `json:"seed_action"`
		SeedStatusPath    string `json:"seed_status_path"`
		ResourcePreflight struct {
			Status           string `json:"status"`
			HeavyWorkAllowed bool   `json:"heavy_work_allowed"`
		} `json:"resource_preflight"`
		LastSeed struct {
			OK                bool   `json:"ok"`
			Status            string `json:"status"`
			State             string `json:"state"`
			SeedAction        string `json:"seed_action"`
			ResourcePreflight struct {
				Status           string `json:"status"`
				HeavyWorkAllowed bool   `json:"heavy_work_allowed"`
			} `json:"resource_preflight"`
		} `json:"last_seed"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("profile seed output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || got.State != "resource_blocked" || got.SeedAction != "skipped_resource_preflight" || got.ResourcePreflight.Status != "skip" || got.ResourcePreflight.HeavyWorkAllowed {
		t.Fatalf("profile seed = %+v, want visible resource skip", got)
	}
	if got.SeedStatusPath != filepath.Join(stateDir, "profile-seed", "latest.json") || !got.LastSeed.OK || got.LastSeed.Status != "skip" || got.LastSeed.State != "resource_blocked" || got.LastSeed.SeedAction != "skipped_resource_preflight" || got.LastSeed.ResourcePreflight.Status != "skip" || got.LastSeed.ResourcePreflight.HeavyWorkAllowed {
		t.Fatalf("profile seed status artifact summary = %+v, path=%q, want visible resource skip artifact", got.LastSeed, got.SeedStatusPath)
	}
	artifactBytes, err := os.ReadFile(got.SeedStatusPath)
	if err != nil {
		t.Fatalf("read profile seed resource skip artifact: %v", err)
	}
	if !bytes.Contains(artifactBytes, []byte(`"seed_action": "skipped_resource_preflight"`)) || !bytes.Contains(artifactBytes, []byte(`"status": "skip"`)) {
		t.Fatalf("profile seed resource skip artifact = %s, want skip status", string(artifactBytes))
	}
}

func TestBrowserPreflightBlocksHeavyWorkWhenResourcePreflightBlocked(t *testing.T) {
	stateDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"resource_budget":{"min_free_disk_mb":999999999}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--browser-mode", "headless", "--config", configPath, "--state-dir", stateDir, "browser", "preflight", "--profile-seed", "copy-default", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitCheckFailed {
		t.Fatalf("browser preflight exit code = %d, want %d; stdout=%s stderr=%s", code, ExitCheckFailed, out.String(), errOut.String())
	}
	var got struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		State string `json:"state"`
		Data  struct {
			State             string `json:"state"`
			ResourcePreflight struct {
				Status           string `json:"status"`
				HeavyWorkAllowed bool   `json:"heavy_work_allowed"`
			} `json:"resource_preflight"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser preflight output is invalid JSON: %v; output=%s", err, out.String())
	}
	if got.OK || got.Code != "resource_preflight_blocked" || got.State != "resource_blocked" || got.Data.State != "resource_blocked" || got.Data.ResourcePreflight.Status != "skip" || got.Data.ResourcePreflight.HeavyWorkAllowed {
		t.Fatalf("browser preflight = %+v, want resource preflight error envelope", got)
	}
}

func resourcePreflightTestContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
