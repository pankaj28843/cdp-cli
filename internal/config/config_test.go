package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/config"
)

func TestDefaults(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Profile != "default" {
		t.Fatalf("Profile = %q, want %q", cfg.Profile, "default")
	}
}

func TestResolvePathExplicit(t *testing.T) {
	got, err := config.ResolvePath("custom.json")
	if err != nil {
		t.Fatalf("ResolvePath returned error: %v", err)
	}
	if got != "custom.json" {
		t.Fatalf("ResolvePath() = %q, want %q", got, "custom.json")
	}
}

func TestResolvePathDefault(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/test-config")

	got, err := config.ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath returned error: %v", err)
	}

	want := filepath.Join("/tmp/test-config", "cdp-cli", "config.json")
	if runtime.GOOS == "darwin" {
		want = filepath.Join("/tmp/test-home", "Library", "Application Support", "cdp-cli", "config.json")
	}
	if got != want {
		t.Fatalf("ResolvePath() = %q, want %q", got, want)
	}
}

func TestLoadMissingConfigDefaultsToHeaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Path != path {
		t.Fatalf("Path = %q, want %q", cfg.Path, path)
	}
	if cfg.Profile != config.DefaultProfile {
		t.Fatalf("Profile = %q, want %q", cfg.Profile, config.DefaultProfile)
	}

	resolution, err := config.ResolveBrowserMode("", "", cfg)
	if err != nil {
		t.Fatalf("ResolveBrowserMode returned error: %v", err)
	}
	if resolution.Mode != config.BrowserModeHeaded || resolution.Source != config.BrowserModeSourceDefault {
		t.Fatalf("ResolveBrowserMode() = %#v, want headed/default", resolution)
	}
}

func TestLoadParsesBrowserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
  "profile": "work",
  "timeout": "3s",
  "browser": {
    "mode": "headless",
    "resource_budget": {
      "max_tabs": 33,
      "min_free_memory_mb": 2048,
      "min_free_disk_mb": 4096,
      "max_load_per_cpu": 1.5
    },
    "headless": {
      "profile_seed_strategy": "managed",
      "profile_refresh_after": "24h"
    }
  },
  "agents": {
    "google": {
      "exclusive_ai_mode": true
    },
    "chatgpt": {
      "thinking": "highest",
      "minimum_thinking": "extra-high",
      "model": "highest"
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Profile != "work" {
		t.Fatalf("Profile = %q, want work", cfg.Profile)
	}
	if cfg.Timeout != 3*time.Second {
		t.Fatalf("Timeout = %v, want 3s", cfg.Timeout)
	}
	if cfg.Browser.Mode != config.BrowserModeHeadless {
		t.Fatalf("Browser.Mode = %q, want headless", cfg.Browser.Mode)
	}
	if !cfg.BrowserModeConfigured() {
		t.Fatalf("BrowserModeConfigured() = false, want true")
	}
	if got := cfg.Browser.Headless.ProfileSeedStrategy; got != "managed" {
		t.Fatalf("ProfileSeedStrategy = %q, want managed", got)
	}
	if got := cfg.Browser.Headless.ProfileRefreshAfter; got != 24*time.Hour {
		t.Fatalf("ProfileRefreshAfter = %v, want 24h", got)
	}
	if got := cfg.Browser.ResourceBudget.MaxTabs; got != 33 {
		t.Fatalf("ResourceBudget.MaxTabs = %d, want 33", got)
	}
	if got := cfg.Browser.ResourceBudget.MinFreeMemoryMB; got != 2048 {
		t.Fatalf("ResourceBudget.MinFreeMemoryMB = %d, want 2048", got)
	}
	if got := cfg.Browser.ResourceBudget.MinFreeDiskMB; got != 4096 {
		t.Fatalf("ResourceBudget.MinFreeDiskMB = %d, want 4096", got)
	}
	if got := cfg.Browser.ResourceBudget.MaxLoadPerCPU; got != 1.5 {
		t.Fatalf("ResourceBudget.MaxLoadPerCPU = %v, want 1.5", got)
	}
	if got := cfg.Agents.ChatGPT.Thinking; got != "highest" {
		t.Fatalf("ChatGPT.Thinking = %q, want highest", got)
	}
	if !cfg.Agents.Google.ExclusiveAIMode {
		t.Fatalf("Google.ExclusiveAIMode = false, want true")
	}
	if got := cfg.Agents.ChatGPT.MinimumThinking; got != "extra-high" {
		t.Fatalf(
			"ChatGPT.MinimumThinking = %q, want extra-high",
			got,
		)
	}
	if got := cfg.Agents.ChatGPT.Model; got != "highest" {
		t.Fatalf("ChatGPT.Model = %q, want highest", got)
	}
}

func TestLoadParsesArtifactRetentionConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
  "artifacts": {
    "retention": "240h",
    "max_log_size": "32MiB"
  }
}`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Artifacts.Retention != 240*time.Hour || cfg.Artifacts.MaxLogSizeBytes != 32<<20 {
		t.Fatalf("artifact config = %+v", cfg.Artifacts)
	}
}

func TestLoadAndSavePreserveExplicitGoogleExclusiveAIModeFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agents":{"google":{"exclusive_ai_mode":false}}}`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.GoogleExclusiveAIModeConfigured() || cfg.Agents.Google.ExclusiveAIMode {
		t.Fatalf("loaded explicit false config = %+v, want configured false", cfg.Agents.Google)
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(data), `"exclusive_ai_mode": false`) {
		t.Fatalf("saved config omitted explicit false: %s", data)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload saved config: %v", err)
	}
	if !loaded.GoogleExclusiveAIModeConfigured() || loaded.Agents.Google.ExclusiveAIMode {
		t.Fatalf("reloaded explicit false config = %+v, want configured false", loaded.Agents.Google)
	}
}

func TestLoadRejectsMalformedConfig(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"bad json", `{`},
		{"bad mode", `{"browser":{"mode":"hidden"}}`},
		{"bad timeout", `{"timeout":"soon"}`},
		{"bad profile seed strategy", `{"browser":{"headless":{"profile_seed_strategy":"mirror-default"}}}`},
		{"bad refresh duration", `{"browser":{"headless":{"profile_refresh_after":"daily"}}}`},
		{"bad max tabs", `{"browser":{"resource_budget":{"max_tabs":-1}}}`},
		{"bad max renderer processes", `{"browser":{"resource_budget":{"max_renderer_processes":-1}}}`},
		{"bad min free memory", `{"browser":{"resource_budget":{"min_free_memory_mb":-1}}}`},
		{"bad min free disk", `{"browser":{"resource_budget":{"min_free_disk_mb":-1}}}`},
		{"bad max load", `{"browser":{"resource_budget":{"max_load_per_cpu":-1}}}`},
		{"bad artifact retention", `{"artifacts":{"retention":"weekly"}}`},
		{"zero artifact retention", `{"artifacts":{"retention":"0s"}}`},
		{"bad artifact max log size", `{"artifacts":{"max_log_size":"huge"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if _, err := config.Load(path); err == nil {
				t.Fatalf("Load returned nil error, want failure")
			}
		})
	}
}

func TestSaveWritesOwnerOnlyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg := config.Config{
		Profile: "work",
		Timeout: 5 * time.Second,
		Browser: config.BrowserConfig{
			Mode: config.BrowserModeHeadless,
			Headless: config.HeadlessConfig{
				ProfileSeedStrategy: "managed",
				ProfileRefreshAfter: 48 * time.Hour,
			},
			ResourceBudget: config.ResourceBudgetConfig{
				MaxTabs:              33,
				MaxRendererProcesses: 12,
				MinFreeMemoryMB:      2048,
				MinFreeDiskMB:        4096,
				MaxLoadPerCPU:        1.5,
			},
		},
		Artifacts: config.ArtifactConfig{
			Retention:       240 * time.Hour,
			MaxLogSizeBytes: 32 << 20,
		},
		Agents: config.AgentConfig{
			Google: config.GoogleAgentConfig{
				ExclusiveAIMode: true,
			},
			ChatGPT: config.ChatGPTConfig{
				Thinking:        "highest",
				MinimumThinking: "extra-high",
				Model:           "highest",
			},
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("config dir permissions = %o, want 700", got)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load saved config returned error: %v", err)
	}
	if loaded.Browser.Mode != config.BrowserModeHeadless {
		t.Fatalf("loaded Browser.Mode = %q, want headless", loaded.Browser.Mode)
	}
	if loaded.Browser.Headless.ProfileRefreshAfter != 48*time.Hour {
		t.Fatalf("loaded ProfileRefreshAfter = %v, want 48h", loaded.Browser.Headless.ProfileRefreshAfter)
	}
	if loaded.Browser.ResourceBudget.MaxTabs != 33 {
		t.Fatalf("loaded ResourceBudget.MaxTabs = %d, want 33", loaded.Browser.ResourceBudget.MaxTabs)
	}
	if loaded.Browser.ResourceBudget.MaxRendererProcesses != 12 {
		t.Fatalf("loaded ResourceBudget.MaxRendererProcesses = %d, want 12", loaded.Browser.ResourceBudget.MaxRendererProcesses)
	}
	if loaded.Browser.ResourceBudget.MinFreeMemoryMB != 2048 {
		t.Fatalf("loaded ResourceBudget.MinFreeMemoryMB = %d, want 2048", loaded.Browser.ResourceBudget.MinFreeMemoryMB)
	}
	if loaded.Browser.ResourceBudget.MinFreeDiskMB != 4096 {
		t.Fatalf("loaded ResourceBudget.MinFreeDiskMB = %d, want 4096", loaded.Browser.ResourceBudget.MinFreeDiskMB)
	}
	if loaded.Browser.ResourceBudget.MaxLoadPerCPU != 1.5 {
		t.Fatalf("loaded ResourceBudget.MaxLoadPerCPU = %v, want 1.5", loaded.Browser.ResourceBudget.MaxLoadPerCPU)
	}
	if loaded.Artifacts.Retention != 240*time.Hour || loaded.Artifacts.MaxLogSizeBytes != 32<<20 {
		t.Fatalf("loaded artifact config = %+v", loaded.Artifacts)
	}
	if !loaded.Agents.Google.ExclusiveAIMode {
		t.Fatalf("loaded Google.ExclusiveAIMode = false, want true")
	}
	if loaded.Agents.ChatGPT.Thinking != "highest" ||
		loaded.Agents.ChatGPT.MinimumThinking != "extra-high" ||
		loaded.Agents.ChatGPT.Model != "highest" {
		t.Fatalf("loaded ChatGPT config = %+v", loaded.Agents.ChatGPT)
	}
}

func TestSaveRejectsInvalidBrowserMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{Browser: config.BrowserConfig{Mode: config.BrowserMode("hidden")}}
	if err := config.Save(path, cfg); err == nil {
		t.Fatalf("Save returned nil error, want invalid mode failure")
	}
}

func TestSaveRejectsInvalidProfileSeedStrategy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{Browser: config.BrowserConfig{Headless: config.HeadlessConfig{ProfileSeedStrategy: "mirror-default"}}}
	if err := config.Save(path, cfg); err == nil {
		t.Fatalf("Save returned nil error, want invalid profile seed strategy failure")
	}
}

func TestSaveRejectsNegativeMaxTabs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{Browser: config.BrowserConfig{ResourceBudget: config.ResourceBudgetConfig{MaxTabs: -1}}}
	if err := config.Save(path, cfg); err == nil {
		t.Fatalf("Save returned nil error, want invalid max_tabs failure")
	}
}

func TestResolveBrowserModePrecedence(t *testing.T) {
	cfg := config.Config{Browser: config.BrowserConfig{Mode: config.BrowserModeHeadless}}
	tests := []struct {
		name     string
		flag     string
		env      string
		cfg      config.Config
		wantMode config.BrowserMode
		wantSrc  config.BrowserModeSource
	}{
		{"flag beats env and config", "headed", "headless", cfg, config.BrowserModeHeaded, config.BrowserModeSourceFlag},
		{"env beats config", "", "headed", cfg, config.BrowserModeHeaded, config.BrowserModeSourceEnv},
		{"config beats default", "", "", cfg, config.BrowserModeHeadless, config.BrowserModeSourceConfig},
		{"default headed", "", "", config.Config{}, config.BrowserModeHeaded, config.BrowserModeSourceDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ResolveBrowserMode(tt.flag, tt.env, tt.cfg)
			if err != nil {
				t.Fatalf("ResolveBrowserMode returned error: %v", err)
			}
			if got.Mode != tt.wantMode || got.Source != tt.wantSrc {
				t.Fatalf("ResolveBrowserMode() = %#v, want %s/%s", got, tt.wantMode, tt.wantSrc)
			}
		})
	}
}

func TestResolveBrowserModeRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		flag string
		env  string
		cfg  config.Config
	}{
		{"flag", "hidden", "", config.Config{}},
		{"env", "", "hidden", config.Config{}},
		{"config", "", "", config.Config{Browser: config.BrowserConfig{Mode: config.BrowserMode("hidden")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := config.ResolveBrowserMode(tt.flag, tt.env, tt.cfg); err == nil {
				t.Fatalf("ResolveBrowserMode returned nil error, want failure")
			}
		})
	}
}
