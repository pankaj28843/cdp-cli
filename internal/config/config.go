package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
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
	Path      string         `json:"path,omitempty"`
	Profile   string         `json:"profile,omitempty"`
	Timeout   time.Duration  `json:"timeout,omitempty"`
	Browser   BrowserConfig  `json:"browser,omitempty"`
	Artifacts ArtifactConfig `json:"artifacts,omitempty"`
	Agents    AgentConfig    `json:"agents,omitempty"`

	browserModeSet           bool
	googleExclusiveAIModeSet bool
}

type BrowserConfig struct {
	Mode           BrowserMode          `json:"mode,omitempty"`
	Headed         HeadedConfig         `json:"headed,omitempty"`
	Headless       HeadlessConfig       `json:"headless,omitempty"`
	ResourceBudget ResourceBudgetConfig `json:"resource_budget,omitempty"`
}

type HeadedConfig struct{}

type ResourceBudgetConfig struct {
	MaxTabs              int     `json:"max_tabs,omitempty"`
	MaxRendererProcesses int     `json:"max_renderer_processes,omitempty"`
	MinFreeMemoryMB      int     `json:"min_free_memory_mb,omitempty"`
	MinFreeDiskMB        int     `json:"min_free_disk_mb,omitempty"`
	MaxLoadPerCPU        float64 `json:"max_load_per_cpu,omitempty"`
}

type HeadlessConfig struct {
	ProfileSeedStrategy string        `json:"profile_seed_strategy,omitempty"`
	ProfileRefreshAfter time.Duration `json:"profile_refresh_after,omitempty"`
}

type ArtifactConfig struct {
	Retention       time.Duration `json:"retention,omitempty"`
	MaxLogSizeBytes int64         `json:"max_log_size_bytes,omitempty"`
}

type AgentConfig struct {
	Google  GoogleAgentConfig `json:"google,omitempty"`
	ChatGPT ChatGPTConfig     `json:"chatgpt,omitempty"`
}

// GoogleAgentConfig contains machine-specific Google rendered-agent
// preferences. Exclusive AI Mode is opt-in because managed corporate browsers
// may isolate or block the separate Google AI Mode route.
type GoogleAgentConfig struct {
	ExclusiveAIMode bool `json:"exclusive_ai_mode,omitempty"`
}

// ChatGPTConfig contains owner-specific selection preferences. Empty values
// preserve the provider's current selection and make no entitlement assumption.
type ChatGPTConfig struct {
	Thinking        string `json:"thinking,omitempty"`
	MinimumThinking string `json:"minimum_thinking,omitempty"`
	Model           string `json:"model,omitempty"`
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

func (c Config) GoogleExclusiveAIModeConfigured() bool {
	return c.googleExclusiveAIModeSet || c.Agents.Google.ExclusiveAIMode
}

type fileConfig struct {
	Profile   string              `json:"profile,omitempty"`
	Timeout   string              `json:"timeout,omitempty"`
	Browser   *fileBrowserConfig  `json:"browser,omitempty"`
	Artifacts *fileArtifactConfig `json:"artifacts,omitempty"`
	Agents    *fileAgentConfig    `json:"agents,omitempty"`
}

type fileAgentConfig struct {
	Google  *fileGoogleAgentConfig `json:"google,omitempty"`
	ChatGPT *fileChatGPTConfig     `json:"chatgpt,omitempty"`
}

type fileGoogleAgentConfig struct {
	ExclusiveAIMode bool `json:"exclusive_ai_mode"`
}

type fileChatGPTConfig struct {
	Thinking        string `json:"thinking,omitempty"`
	MinimumThinking string `json:"minimum_thinking,omitempty"`
	Model           string `json:"model,omitempty"`
}

type fileArtifactConfig struct {
	Retention  string `json:"retention,omitempty"`
	MaxLogSize string `json:"max_log_size,omitempty"`
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
	MaxTabs              int     `json:"max_tabs,omitempty"`
	MaxRendererProcesses int     `json:"max_renderer_processes,omitempty"`
	MinFreeMemoryMB      int     `json:"min_free_memory_mb,omitempty"`
	MinFreeDiskMB        int     `json:"min_free_disk_mb,omitempty"`
	MaxLoadPerCPU        float64 `json:"max_load_per_cpu,omitempty"`
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
			if raw.Browser.ResourceBudget.MaxRendererProcesses < 0 {
				return Config{}, fmt.Errorf("browser.resource_budget.max_renderer_processes must be non-negative")
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
			cfg.Browser.ResourceBudget.MaxRendererProcesses = raw.Browser.ResourceBudget.MaxRendererProcesses
			cfg.Browser.ResourceBudget.MinFreeMemoryMB = raw.Browser.ResourceBudget.MinFreeMemoryMB
			cfg.Browser.ResourceBudget.MinFreeDiskMB = raw.Browser.ResourceBudget.MinFreeDiskMB
			cfg.Browser.ResourceBudget.MaxLoadPerCPU = raw.Browser.ResourceBudget.MaxLoadPerCPU
		}
	}
	if raw.Artifacts != nil {
		if raw.Artifacts.Retention != "" {
			d, err := time.ParseDuration(raw.Artifacts.Retention)
			if err != nil {
				return Config{}, fmt.Errorf("parse artifacts.retention: %w", err)
			}
			if d <= 0 {
				return Config{}, fmt.Errorf("artifacts.retention must be positive")
			}
			cfg.Artifacts.Retention = d
		}
		if raw.Artifacts.MaxLogSize != "" {
			size, err := artifacts.ParseByteSize(raw.Artifacts.MaxLogSize)
			if err != nil {
				return Config{}, fmt.Errorf("parse artifacts.max_log_size: %w", err)
			}
			cfg.Artifacts.MaxLogSizeBytes = size
		}
	}
	if raw.Agents != nil {
		if raw.Agents.Google != nil {
			cfg.Agents.Google.ExclusiveAIMode = raw.Agents.Google.ExclusiveAIMode
			cfg.googleExclusiveAIModeSet = true
		}
		if raw.Agents.ChatGPT != nil {
			cfg.Agents.ChatGPT = ChatGPTConfig{
				Thinking:        strings.TrimSpace(raw.Agents.ChatGPT.Thinking),
				MinimumThinking: strings.TrimSpace(raw.Agents.ChatGPT.MinimumThinking),
				Model:           strings.TrimSpace(raw.Agents.ChatGPT.Model),
			}
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
	if cfg.Browser.ResourceBudget.MaxRendererProcesses < 0 {
		return nil, fmt.Errorf("browser.resource_budget.max_renderer_processes must be non-negative")
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
	if cfg.Browser.Mode != "" || cfg.Browser.Headless.ProfileSeedStrategy != "" || cfg.Browser.Headless.ProfileRefreshAfter > 0 || cfg.Browser.ResourceBudget.MaxTabs > 0 || cfg.Browser.ResourceBudget.MaxRendererProcesses > 0 || cfg.Browser.ResourceBudget.MinFreeMemoryMB > 0 || cfg.Browser.ResourceBudget.MinFreeDiskMB > 0 || cfg.Browser.ResourceBudget.MaxLoadPerCPU > 0 {
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
		if cfg.Browser.ResourceBudget.MaxTabs > 0 || cfg.Browser.ResourceBudget.MaxRendererProcesses > 0 || cfg.Browser.ResourceBudget.MinFreeMemoryMB > 0 || cfg.Browser.ResourceBudget.MinFreeDiskMB > 0 || cfg.Browser.ResourceBudget.MaxLoadPerCPU > 0 {
			raw.Browser.ResourceBudget = &fileResourceBudgetConfig{
				MaxTabs:              cfg.Browser.ResourceBudget.MaxTabs,
				MaxRendererProcesses: cfg.Browser.ResourceBudget.MaxRendererProcesses,
				MinFreeMemoryMB:      cfg.Browser.ResourceBudget.MinFreeMemoryMB,
				MinFreeDiskMB:        cfg.Browser.ResourceBudget.MinFreeDiskMB,
				MaxLoadPerCPU:        cfg.Browser.ResourceBudget.MaxLoadPerCPU,
			}
		}
	}
	if cfg.Artifacts.Retention < 0 || cfg.Artifacts.MaxLogSizeBytes < 0 {
		return nil, fmt.Errorf("artifact retention and max log size must be non-negative")
	}
	if cfg.Artifacts.Retention > 0 || cfg.Artifacts.MaxLogSizeBytes > 0 {
		raw.Artifacts = &fileArtifactConfig{}
		if cfg.Artifacts.Retention > 0 {
			raw.Artifacts.Retention = cfg.Artifacts.Retention.String()
		}
		if cfg.Artifacts.MaxLogSizeBytes > 0 {
			raw.Artifacts.MaxLogSize = artifacts.FormatByteSize(cfg.Artifacts.MaxLogSizeBytes)
		}
	}
	if cfg.googleExclusiveAIModeSet ||
		cfg.Agents.Google.ExclusiveAIMode ||
		cfg.Agents.ChatGPT.Thinking != "" ||
		cfg.Agents.ChatGPT.MinimumThinking != "" ||
		cfg.Agents.ChatGPT.Model != "" {
		raw.Agents = &fileAgentConfig{}
		if cfg.googleExclusiveAIModeSet || cfg.Agents.Google.ExclusiveAIMode {
			raw.Agents.Google = &fileGoogleAgentConfig{ExclusiveAIMode: cfg.Agents.Google.ExclusiveAIMode}
		}
		if cfg.Agents.ChatGPT.Thinking != "" ||
			cfg.Agents.ChatGPT.MinimumThinking != "" ||
			cfg.Agents.ChatGPT.Model != "" {
			raw.Agents.ChatGPT = &fileChatGPTConfig{
				Thinking: strings.TrimSpace(
					cfg.Agents.ChatGPT.Thinking,
				),
				MinimumThinking: strings.TrimSpace(
					cfg.Agents.ChatGPT.MinimumThinking,
				),
				Model: strings.TrimSpace(cfg.Agents.ChatGPT.Model),
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
