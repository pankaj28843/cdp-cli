package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	AppName        = "cdp-cli"
	DefaultProfile = "default"
)

type BrowserMode string

const (
	BrowserModeHeaded   BrowserMode = "headed"
	BrowserModeHeadless BrowserMode = "headless"
)

type BrowserModeSource string

const (
	BrowserModeSourceFlag    BrowserModeSource = "flag"
	BrowserModeSourceEnv     BrowserModeSource = "env"
	BrowserModeSourceConfig  BrowserModeSource = "config"
	BrowserModeSourceDefault BrowserModeSource = "default"
)

type Config struct {
	Path    string        `json:"path,omitempty"`
	Profile string        `json:"profile,omitempty"`
	Timeout time.Duration `json:"timeout,omitempty"`
	Browser BrowserConfig `json:"browser,omitempty"`

	browserModeSet bool
}

type BrowserConfig struct {
	Mode           BrowserMode          `json:"mode,omitempty"`
	Headed         HeadedConfig         `json:"headed,omitempty"`
	Headless       HeadlessConfig       `json:"headless,omitempty"`
	ResourceBudget ResourceBudgetConfig `json:"resource_budget,omitempty"`
}

type HeadedConfig struct{}

type ResourceBudgetConfig struct {
	MaxTabs         int     `json:"max_tabs,omitempty"`
	MinFreeMemoryMB int     `json:"min_free_memory_mb,omitempty"`
	MinFreeDiskMB   int     `json:"min_free_disk_mb,omitempty"`
	MaxLoadPerCPU   float64 `json:"max_load_per_cpu,omitempty"`
}

type HeadlessConfig struct {
	ProfileSeedStrategy string        `json:"profile_seed_strategy,omitempty"`
	ProfileRefreshAfter time.Duration `json:"profile_refresh_after,omitempty"`
}

type BrowserModeResolution struct {
	Mode   BrowserMode       `json:"browser_mode"`
	Source BrowserModeSource `json:"browser_mode_source"`
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

func Load(explicitPath string) (Config, error) {
	path, err := ResolvePath(explicitPath)
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := Defaults()
			cfg.Path = path
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg, err := decode(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.Path = path
	return cfg, nil
}

func Save(explicitPath string, cfg Config) error {
	path := explicitPath
	var err error
	if path == "" {
		if cfg.Path != "" {
			path = cfg.Path
		} else {
			path, err = ResolvePath("")
			if err != nil {
				return err
			}
		}
	}

	data, err := encode(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func ParseBrowserMode(value string) (BrowserMode, error) {
	switch BrowserMode(strings.TrimSpace(strings.ToLower(value))) {
	case BrowserModeHeaded:
		return BrowserModeHeaded, nil
	case BrowserModeHeadless:
		return BrowserModeHeadless, nil
	case "":
		return "", fmt.Errorf("browser mode is required")
	default:
		return "", fmt.Errorf("invalid browser mode %q", value)
	}
}

func (m BrowserMode) Valid() bool {
	return m == BrowserModeHeaded || m == BrowserModeHeadless
}

func ResolveBrowserMode(flagValue, envValue string, cfg Config) (BrowserModeResolution, error) {
	if flagValue != "" {
		mode, err := ParseBrowserMode(flagValue)
		if err != nil {
			return BrowserModeResolution{}, fmt.Errorf("resolve browser mode from flag: %w", err)
		}
		return BrowserModeResolution{Mode: mode, Source: BrowserModeSourceFlag}, nil
	}
	if envValue != "" {
		mode, err := ParseBrowserMode(envValue)
		if err != nil {
			return BrowserModeResolution{}, fmt.Errorf("resolve browser mode from env: %w", err)
		}
		return BrowserModeResolution{Mode: mode, Source: BrowserModeSourceEnv}, nil
	}
	if cfg.browserModeSet || cfg.Browser.Mode != "" {
		mode, err := ParseBrowserMode(string(cfg.Browser.Mode))
		if err != nil {
			return BrowserModeResolution{}, fmt.Errorf("resolve browser mode from config: %w", err)
		}
		return BrowserModeResolution{Mode: mode, Source: BrowserModeSourceConfig}, nil
	}
	return BrowserModeResolution{Mode: BrowserModeHeaded, Source: BrowserModeSourceDefault}, nil
}

func (c Config) BrowserModeConfigured() bool {
	return c.browserModeSet || c.Browser.Mode != ""
}

type fileConfig struct {
	Profile string             `json:"profile,omitempty"`
	Timeout string             `json:"timeout,omitempty"`
	Browser *fileBrowserConfig `json:"browser,omitempty"`
}

type fileBrowserConfig struct {
	Mode           string                    `json:"mode,omitempty"`
	Headless       *fileHeadlessConfig       `json:"headless,omitempty"`
	ResourceBudget *fileResourceBudgetConfig `json:"resource_budget,omitempty"`
}

type fileHeadlessConfig struct {
	ProfileSeedStrategy string `json:"profile_seed_strategy,omitempty"`
	ProfileRefreshAfter string `json:"profile_refresh_after,omitempty"`
}

type fileResourceBudgetConfig struct {
	MaxTabs         int     `json:"max_tabs,omitempty"`
	MinFreeMemoryMB int     `json:"min_free_memory_mb,omitempty"`
	MinFreeDiskMB   int     `json:"min_free_disk_mb,omitempty"`
	MaxLoadPerCPU   float64 `json:"max_load_per_cpu,omitempty"`
}

func decode(data []byte) (Config, error) {
	var raw fileConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, err
	}

	cfg := Defaults()
	cfg.Profile = raw.Profile
	if cfg.Profile == "" {
		cfg.Profile = DefaultProfile
	}
	if raw.Timeout != "" {
		d, err := time.ParseDuration(raw.Timeout)
		if err != nil {
			return Config{}, fmt.Errorf("parse timeout: %w", err)
		}
		cfg.Timeout = d
	}
	if raw.Browser != nil {
		if raw.Browser.Mode != "" {
			mode, err := ParseBrowserMode(raw.Browser.Mode)
			if err != nil {
				return Config{}, err
			}
			cfg.Browser.Mode = mode
			cfg.browserModeSet = true
		}
		if raw.Browser.Headless != nil {
			strategy, err := parseHeadlessProfileSeedStrategy(raw.Browser.Headless.ProfileSeedStrategy)
			if err != nil {
				return Config{}, err
			}
			cfg.Browser.Headless.ProfileSeedStrategy = strategy
			if raw.Browser.Headless.ProfileRefreshAfter != "" {
				d, err := time.ParseDuration(raw.Browser.Headless.ProfileRefreshAfter)
				if err != nil {
					return Config{}, fmt.Errorf("parse browser.headless.profile_refresh_after: %w", err)
				}
				cfg.Browser.Headless.ProfileRefreshAfter = d
			}
		}
		if raw.Browser.ResourceBudget != nil {
			if raw.Browser.ResourceBudget.MaxTabs < 0 {
				return Config{}, fmt.Errorf("browser.resource_budget.max_tabs must be non-negative")
			}
			if raw.Browser.ResourceBudget.MinFreeMemoryMB < 0 {
				return Config{}, fmt.Errorf("browser.resource_budget.min_free_memory_mb must be non-negative")
			}
			if raw.Browser.ResourceBudget.MinFreeDiskMB < 0 {
				return Config{}, fmt.Errorf("browser.resource_budget.min_free_disk_mb must be non-negative")
			}
			if raw.Browser.ResourceBudget.MaxLoadPerCPU < 0 {
				return Config{}, fmt.Errorf("browser.resource_budget.max_load_per_cpu must be non-negative")
			}
			cfg.Browser.ResourceBudget.MaxTabs = raw.Browser.ResourceBudget.MaxTabs
			cfg.Browser.ResourceBudget.MinFreeMemoryMB = raw.Browser.ResourceBudget.MinFreeMemoryMB
			cfg.Browser.ResourceBudget.MinFreeDiskMB = raw.Browser.ResourceBudget.MinFreeDiskMB
			cfg.Browser.ResourceBudget.MaxLoadPerCPU = raw.Browser.ResourceBudget.MaxLoadPerCPU
		}
	}
	return cfg, nil
}

func encode(cfg Config) ([]byte, error) {
	raw := fileConfig{
		Profile: cfg.Profile,
	}
	if raw.Profile == "" {
		raw.Profile = DefaultProfile
	}
	if cfg.Timeout > 0 {
		raw.Timeout = cfg.Timeout.String()
	}
	if cfg.Browser.ResourceBudget.MaxTabs < 0 {
		return nil, fmt.Errorf("browser.resource_budget.max_tabs must be non-negative")
	}
	if cfg.Browser.ResourceBudget.MinFreeMemoryMB < 0 {
		return nil, fmt.Errorf("browser.resource_budget.min_free_memory_mb must be non-negative")
	}
	if cfg.Browser.ResourceBudget.MinFreeDiskMB < 0 {
		return nil, fmt.Errorf("browser.resource_budget.min_free_disk_mb must be non-negative")
	}
	if cfg.Browser.ResourceBudget.MaxLoadPerCPU < 0 {
		return nil, fmt.Errorf("browser.resource_budget.max_load_per_cpu must be non-negative")
	}
	if cfg.Browser.Mode != "" || cfg.Browser.Headless.ProfileSeedStrategy != "" || cfg.Browser.Headless.ProfileRefreshAfter > 0 || cfg.Browser.ResourceBudget.MaxTabs > 0 || cfg.Browser.ResourceBudget.MinFreeMemoryMB > 0 || cfg.Browser.ResourceBudget.MinFreeDiskMB > 0 || cfg.Browser.ResourceBudget.MaxLoadPerCPU > 0 {
		raw.Browser = &fileBrowserConfig{}
		if cfg.Browser.Mode != "" {
			if !cfg.Browser.Mode.Valid() {
				return nil, fmt.Errorf("invalid browser mode %q", cfg.Browser.Mode)
			}
			raw.Browser.Mode = string(cfg.Browser.Mode)
		}
		if cfg.Browser.Headless.ProfileSeedStrategy != "" || cfg.Browser.Headless.ProfileRefreshAfter > 0 {
			strategy, err := parseHeadlessProfileSeedStrategy(cfg.Browser.Headless.ProfileSeedStrategy)
			if err != nil {
				return nil, err
			}
			raw.Browser.Headless = &fileHeadlessConfig{
				ProfileSeedStrategy: strategy,
			}
			if cfg.Browser.Headless.ProfileRefreshAfter > 0 {
				raw.Browser.Headless.ProfileRefreshAfter = cfg.Browser.Headless.ProfileRefreshAfter.String()
			}
		}
		if cfg.Browser.ResourceBudget.MaxTabs > 0 || cfg.Browser.ResourceBudget.MinFreeMemoryMB > 0 || cfg.Browser.ResourceBudget.MinFreeDiskMB > 0 || cfg.Browser.ResourceBudget.MaxLoadPerCPU > 0 {
			raw.Browser.ResourceBudget = &fileResourceBudgetConfig{
				MaxTabs:         cfg.Browser.ResourceBudget.MaxTabs,
				MinFreeMemoryMB: cfg.Browser.ResourceBudget.MinFreeMemoryMB,
				MinFreeDiskMB:   cfg.Browser.ResourceBudget.MinFreeDiskMB,
				MaxLoadPerCPU:   cfg.Browser.ResourceBudget.MaxLoadPerCPU,
			}
		}
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func parseHeadlessProfileSeedStrategy(strategy string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(strategy))
	if normalized == "" {
		return "", nil
	}
	switch normalized {
	case "managed", "copy-default":
		return normalized, nil
	default:
		return "", fmt.Errorf("browser.headless.profile_seed_strategy must be managed or copy-default")
	}
}
