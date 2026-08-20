// Package transcriptionservice renders user-scoped service-manager artifacts
// for the cdp transcription API. It deliberately contains no process-control
// code so the CLI can test rendering without starting launchd or systemd.
package transcriptionservice

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Platform string

const (
	PlatformMacOS Platform = "macos"
	PlatformLinux Platform = "linux"

	LaunchAgentLabel = "dev.pankaj.cdp.transcription"
	SystemdUnitName  = "cdp-transcription.service"
)

type Config struct {
	BinaryPath           string
	StateDir             string
	Address              string
	HTTPAddress          string
	Token                string
	Provider             string
	LocalBaseURL         string
	LocalRealtimeBaseURL string
	LocalAPIKey          string
	MaxAudioBytes        int64
	AuthRefreshInterval  time.Duration
	PersistAudio         bool
	TLSCertFile          string
	TLSKeyFile           string
	Path                 string
}

type Paths struct {
	LaunchAgent  string
	SystemdUnit  string
	Environment  string
	LogDirectory string
}

type Artifact struct {
	Path string
	Data []byte
	Mode os.FileMode
}

func CurrentPlatform() (Platform, error) {
	switch runtime.GOOS {
	case "darwin":
		return PlatformMacOS, nil
	case "linux":
		return PlatformLinux, nil
	default:
		return "", fmt.Errorf("transcription user service is unsupported on %s", runtime.GOOS)
	}
}

func PathsForHome(home string) (Paths, error) {
	if strings.TrimSpace(home) == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve home directory for transcription service: %w", err)
		}
	}
	home = filepath.Clean(home)
	return Paths{
		LaunchAgent:  filepath.Join(home, "Library", "LaunchAgents", LaunchAgentLabel+".plist"),
		SystemdUnit:  filepath.Join(home, ".config", "systemd", "user", SystemdUnitName),
		Environment:  filepath.Join(home, ".config", "cdp-cli", "transcription.env"),
		LogDirectory: filepath.Join(home, ".cdp-cli", "logs"),
	}, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.BinaryPath) == "" {
		return fmt.Errorf("transcription service binary path is required")
	}
	if strings.TrimSpace(c.StateDir) == "" {
		return fmt.Errorf("transcription service state directory is required")
	}
	if strings.TrimSpace(c.Address) == "" {
		return fmt.Errorf("transcription service listen address is required")
	}
	if strings.TrimSpace(c.HTTPAddress) == strings.TrimSpace(c.Address) && strings.TrimSpace(c.HTTPAddress) != "" {
		return fmt.Errorf("transcription service HTTP address must differ from the primary address")
	}
	if strings.TrimSpace(c.Provider) == "" {
		return fmt.Errorf("transcription service provider is required")
	}
	if c.MaxAudioBytes <= 0 {
		return fmt.Errorf("transcription service max audio bytes must be positive")
	}
	if c.AuthRefreshInterval < 0 {
		return fmt.Errorf("transcription service auth refresh interval must be zero or positive")
	}
	if (strings.TrimSpace(c.TLSCertFile) == "") != (strings.TrimSpace(c.TLSKeyFile) == "") {
		return fmt.Errorf("transcription service TLS certificate and key must be provided together")
	}
	return nil
}

func (c Config) Environment() map[string]string {
	environment := map[string]string{
		"CDP_STATE_DIR":                             c.StateDir,
		"CDP_TRANSCRIPTION_ADDRESS":                 c.Address,
		"CDP_TRANSCRIPTION_HTTP_ADDRESS":            c.HTTPAddress,
		"CDP_TRANSCRIPTION_API_TOKEN":               c.Token,
		"CDP_TRANSCRIPTION_PROVIDER":                c.Provider,
		"CDP_TRANSCRIPTION_LOCAL_BASE_URL":          c.LocalBaseURL,
		"CDP_TRANSCRIPTION_LOCAL_REALTIME_BASE_URL": c.LocalRealtimeBaseURL,
		"CDP_TRANSCRIPTION_LOCAL_API_KEY":           c.LocalAPIKey,
		"CDP_TRANSCRIPTION_MAX_AUDIO_BYTES":         strconv.FormatInt(c.MaxAudioBytes, 10),
		"CDP_TRANSCRIPTION_AUTH_REFRESH_INTERVAL":   c.AuthRefreshInterval.String(),
		"CDP_TRANSCRIPTION_PERSIST_AUDIO":           strconv.FormatBool(c.PersistAudio),
		"CDP_TRANSCRIPTION_TLS_CERT":                c.TLSCertFile,
		"CDP_TRANSCRIPTION_TLS_KEY":                 c.TLSKeyFile,
	}
	if strings.TrimSpace(c.Path) != "" {
		environment["PATH"] = c.Path
	}
	return environment
}

func Render(platform Platform, c Config, paths Paths) ([]Artifact, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	environment := c.Environment()
	switch platform {
	case PlatformMacOS:
		data, err := renderLaunchAgent(c, environment, paths)
		if err != nil {
			return nil, err
		}
		return []Artifact{{Path: paths.LaunchAgent, Data: data, Mode: 0o600}}, nil
	case PlatformLinux:
		unit, err := renderSystemdUnit(c, paths)
		if err != nil {
			return nil, err
		}
		env := renderEnvironmentFile(environment)
		return []Artifact{
			{Path: paths.SystemdUnit, Data: unit, Mode: 0o644},
			{Path: paths.Environment, Data: env, Mode: 0o600},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported transcription service platform %q", platform)
	}
}

func renderLaunchAgent(c Config, environment map[string]string, paths Paths) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	plistKeyString(&b, "Label", LaunchAgentLabel)
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, argument := range []string{c.BinaryPath, "transcription", "serve"} {
		b.WriteString("    <string>")
		xmlEscape(&b, argument)
		b.WriteString("</string>\n")
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
	keys := sortedKeys(environment)
	for _, key := range keys {
		plistKeyString(&b, key, environment[key])
	}
	b.WriteString("  </dict>\n")
	plistKeyString(&b, "WorkingDirectory", c.StateDir)
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n  <true/>\n")
	b.WriteString("  <key>ThrottleInterval</key>\n  <integer>5</integer>\n")
	b.WriteString("  <key>ProcessType</key>\n  <string>Interactive</string>\n")
	plistKeyString(&b, "StandardOutPath", filepath.Join(paths.LogDirectory, "transcription.log"))
	plistKeyString(&b, "StandardErrorPath", filepath.Join(paths.LogDirectory, "transcription.error.log"))
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes(), nil
}

func renderSystemdUnit(c Config, paths Paths) ([]byte, error) {
	if strings.TrimSpace(paths.Environment) == "" {
		return nil, fmt.Errorf("systemd environment path is required")
	}
	var b bytes.Buffer
	b.WriteString("[Unit]\n")
	b.WriteString("Description=cdp provider-neutral transcription API\n")
	b.WriteString("After=default.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("ExecStart=")
	b.WriteString(systemdQuote(c.BinaryPath))
	b.WriteString(" transcription serve\n")
	// Keep systemd's optional-file marker inside the quoted path. A marker
	// placed before the opening quote is parsed as part of the directive's
	// value and makes systemd ignore the absolute path.
	b.WriteString("EnvironmentFile=")
	b.WriteString(systemdQuote("-" + paths.Environment))
	b.WriteString("\nRestart=on-failure\nRestartSec=2\n")
	b.WriteString("KillSignal=SIGINT\nTimeoutStopSec=10\nUMask=0077\n")
	b.WriteString("NoNewPrivileges=true\nPrivateTmp=true\n\n")
	b.WriteString("[Install]\nWantedBy=default.target\n")
	return b.Bytes(), nil
}

func renderEnvironmentFile(environment map[string]string) []byte {
	var b bytes.Buffer
	for _, key := range sortedKeys(environment) {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(systemdQuote(environment[key]))
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func plistKeyString(b *bytes.Buffer, key, value string) {
	b.WriteString("  <key>")
	xmlEscape(b, key)
	b.WriteString("</key>\n  <string>")
	xmlEscape(b, value)
	b.WriteString("</string>\n")
}

func xmlEscape(b *bytes.Buffer, value string) {
	_ = xml.EscapeText(b, []byte(value))
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return `"` + value + `"`
}
