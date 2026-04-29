package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	AppName        = "cdp-cli"
	DefaultProfile = "default"
)

type Config struct {
	Path    string        `json:"path,omitempty"`
	Profile string        `json:"profile"`
	Timeout time.Duration `json:"timeout,omitempty"`
}

func Defaults() Config {
	return Config{
		Profile: DefaultProfile,
	}
}

func ResolvePath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config directory: %w", err)
	}

	return filepath.Join(dir, AppName, "config.json"), nil
}
