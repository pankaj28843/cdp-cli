package config_test

import (
	"os"
	"path/filepath"
	"runtime"
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
    "headless": {
      "profile_seed_strategy": "managed",
      "profile_refresh_after": "24h"
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
}

func TestLoadRejectsMalformedConfig(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"bad json", `{`},
		{"bad mode", `{"browser":{"mode":"hidden"}}`},
		{"bad timeout", `{"timeout":"soon"}`},
		{"bad refresh duration", `{"browser":{"headless":{"profile_refresh_after":"daily"}}}`},
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
}

func TestSaveRejectsInvalidBrowserMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{Browser: config.BrowserConfig{Mode: config.BrowserMode("hidden")}}
	if err := config.Save(path, cfg); err == nil {
		t.Fatalf("Save returned nil error, want invalid mode failure")
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
