package config_test

import (
	"path/filepath"
	"testing"

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
	t.Setenv("XDG_CONFIG_HOME", "/tmp/test-config")

	got, err := config.ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath returned error: %v", err)
	}

	want := filepath.Join("/tmp/test-config", "cdp-cli", "config.json")
	if got != want {
		t.Fatalf("ResolvePath() = %q, want %q", got, want)
	}
}
