package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/config"
)

func TestResolveBrowserModeRootPrecedence(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"mode":"headless"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	tests := []struct {
		name       string
		flagName   string
		flagValue  string
		envValue   string
		configPath string
		wantMode   config.BrowserMode
		wantSource config.BrowserModeSource
	}{
		{"flag beats env and config", "browser-mode", "headed", "headless", configPath, config.BrowserModeHeaded, config.BrowserModeSourceFlag},
		{"camel flag beats env and config", "browserMode", "headed", "headless", configPath, config.BrowserModeHeaded, config.BrowserModeSourceFlag},
		{"env beats config", "", "", "headed", configPath, config.BrowserModeHeaded, config.BrowserModeSourceEnv},
		{"config beats default", "", "", "", configPath, config.BrowserModeHeadless, config.BrowserModeSourceConfig},
		{"default headed", "", "", "", filepath.Join(t.TempDir(), "missing.json"), config.BrowserModeHeaded, config.BrowserModeSourceDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CDP_BROWSER_MODE", tt.envValue)
			a := &app{opts: options{profile: config.DefaultProfile}}
			root := a.newRoot()
			if err := root.PersistentFlags().Set("config", tt.configPath); err != nil {
				t.Fatalf("set config flag: %v", err)
			}
			if tt.flagName != "" {
				if err := root.PersistentFlags().Set(tt.flagName, tt.flagValue); err != nil {
					t.Fatalf("set flag: %v", err)
				}
			}

			got, err := a.resolveBrowserMode(root)
			if err != nil {
				t.Fatalf("resolveBrowserMode returned error: %v", err)
			}
			if got.Mode != tt.wantMode || got.Source != tt.wantSource {
				t.Fatalf("resolveBrowserMode() = %s/%s, want %s/%s", got.Mode, got.Source, tt.wantMode, tt.wantSource)
			}
			if got.ConfigPath != tt.configPath {
				t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, tt.configPath)
			}
			if len(got.NextCommands) == 0 {
				t.Fatalf("NextCommands is empty")
			}
		})
	}
}

func TestBrowserModeGetJSON(t *testing.T) {
	stateDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"browser":{"mode":"headless"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var addOut, addErr bytes.Buffer
	addCode := Execute(context.Background(), []string{
		"--state-dir", stateDir,
		"connection", "add", "local",
		"--browser-url", "http://localhost/devtools",
		"--json",
	}, &addOut, &addErr, BuildInfo{})
	if addCode != ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stdout=%s stderr=%s", addCode, ExitOK, addOut.String(), addErr.String())
	}

	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{
		"--state-dir", stateDir,
		"--config", configPath,
		"browser", "mode", "get",
		"--json",
	}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("browser mode get exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}

	var got struct {
		OK                bool                     `json:"ok"`
		BrowserMode       config.BrowserMode       `json:"browser_mode"`
		BrowserModeSource config.BrowserModeSource `json:"browser_mode_source"`
		ConfigPath        string                   `json:"config_path"`
		NextCommands      []string                 `json:"next_commands"`
		Selected          struct {
			Name           string `json:"name"`
			ConnectionMode string `json:"connection_mode"`
			Source         string `json:"source"`
		} `json:"selected_connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser mode get output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || got.BrowserMode != config.BrowserModeHeadless || got.BrowserModeSource != config.BrowserModeSourceConfig || got.ConfigPath != configPath {
		t.Fatalf("browser mode get = %+v, want headless config mode", got)
	}
	if len(got.NextCommands) == 0 {
		t.Fatalf("NextCommands is empty")
	}
	if got.Selected.Name != "local" || got.Selected.ConnectionMode != "browser_url" || got.Selected.Source != "selected" {
		t.Fatalf("Selected = %+v, want selected local browser_url", got.Selected)
	}
}

func TestBrowserModeGetDoesNotRequireConnection(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--state-dir", t.TempDir(), "browser", "mode", "get", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("browser mode get exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK                bool                     `json:"ok"`
		BrowserMode       config.BrowserMode       `json:"browser_mode"`
		BrowserModeSource config.BrowserModeSource `json:"browser_mode_source"`
		Selected          json.RawMessage          `json:"selected_connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser mode get output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || got.BrowserMode != config.BrowserModeHeaded || got.BrowserModeSource != config.BrowserModeSourceDefault {
		t.Fatalf("browser mode get = %+v, want headed default", got)
	}
	if len(got.Selected) != 0 {
		t.Fatalf("selected_connection present without configured connection: %s", string(got.Selected))
	}
}

func TestDescribeIncludesBrowserModeMetadata(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"describe", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("describe exit code = %d, want %d; stderr=%s", code, ExitOK, errOut.String())
	}
	var got struct {
		Globals []string `json:"globals"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe output is invalid JSON: %v", err)
	}
	if !containsTestString(got.Globals, "--browser-mode") || !containsTestString(got.Globals, "--browserMode") {
		t.Fatalf("globals = %+v, want browser mode flags", got.Globals)
	}

	out.Reset()
	errOut.Reset()
	code = Execute(context.Background(), []string{"describe", "--command", "browser mode get", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("describe browser mode get exit code = %d, want %d; stderr=%s", code, ExitOK, errOut.String())
	}
	var command struct {
		Commands struct {
			Name     string   `json:"name"`
			Examples []string `json:"examples"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &command); err != nil {
		t.Fatalf("describe browser mode get output is invalid JSON: %v", err)
	}
	if command.Commands.Name != "get" || !containsSubstring(command.Commands.Examples, "--browser-mode headless") {
		t.Fatalf("browser mode get metadata = %+v, want headless example", command.Commands)
	}

	out.Reset()
	errOut.Reset()
	code = Execute(context.Background(), []string{"describe", "--command", "browser profile seed", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("describe browser profile seed exit code = %d, want %d; stderr=%s", code, ExitOK, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &command); err != nil {
		t.Fatalf("describe browser profile seed output is invalid JSON: %v", err)
	}
	if command.Commands.Name != "seed" || !containsSubstring(command.Commands.Examples, "--strategy managed") || !containsSubstring(command.Commands.Examples, "--strategy copy-default") {
		t.Fatalf("browser profile seed metadata = %+v, want managed and copy-default seed examples", command.Commands)
	}
}

func TestBrowserModeSchemas(t *testing.T) {
	catalog := schemaCatalog()
	browserMode, ok := catalog["browser-mode"]
	if !ok {
		t.Fatalf("schemaCatalog missing browser-mode")
	}
	if !schemaHasField(browserMode, "browser_mode") || !schemaHasField(browserMode, "browser_mode_source") || !schemaHasField(browserMode, "next_commands") {
		t.Fatalf("browser-mode schema fields = %+v, want mode/source/next_commands", browserMode.Fields)
	}
	profileStatus, ok := catalog["browser-profile-status"]
	if !ok {
		t.Fatalf("schemaCatalog missing browser-profile-status")
	}
	if !schemaHasField(profileStatus, "managed_browser") || !schemaHasField(profileStatus, "next_commands") {
		t.Fatalf("browser-profile-status schema fields = %+v, want managed_browser/next_commands", profileStatus.Fields)
	}
	profileSeed, ok := catalog["browser-profile-seed"]
	if !ok {
		t.Fatalf("schemaCatalog missing browser-profile-seed")
	}
	if !schemaHasField(profileSeed, "seed_strategy") || !schemaHasField(profileSeed, "managed_browser") {
		t.Fatalf("browser-profile-seed schema fields = %+v, want seed_strategy/managed_browser", profileSeed.Fields)
	}
	connectionResolve := catalog["connection-resolve"]
	if !schemaHasField(connectionResolve, "browser_mode") || !schemaHasField(connectionResolve, "browser_mode_source") {
		t.Fatalf("connection-resolve schema fields = %+v, want browser mode fields", connectionResolve.Fields)
	}
}

func schemaHasField(info schemaInfo, name string) bool {
	for _, field := range info.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func containsTestString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func TestInvalidBrowserModeReturnsUsageEnvelope(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
	}{
		{"flag", []string{"--browser-mode", "hidden", "version", "--json"}, ""},
		{"env", []string{"version", "--json"}, "hidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CDP_BROWSER_MODE", tt.env)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			var out, errOut bytes.Buffer

			code := Execute(context.Background(), tt.args, &out, &errOut, BuildInfo{})
			if code != ExitUsage {
				t.Fatalf("Execute exit code = %d, want %d; stdout=%s stderr=%s", code, ExitUsage, out.String(), errOut.String())
			}

			var got struct {
				OK       bool   `json:"ok"`
				Code     string `json:"code"`
				ErrClass string `json:"err_class"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("error output is invalid JSON: %v; output=%s", err, out.String())
			}
			if got.OK || got.Code != "invalid_browser_mode" || got.ErrClass != "usage" {
				t.Fatalf("error envelope = %+v, want invalid_browser_mode usage", got)
			}
		})
	}
}

func TestBrowserProfileStatusAndSeedManaged(t *testing.T) {
	stateDir := t.TempDir()

	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--state-dir", stateDir, "browser", "profile", "status", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("browser profile status exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}
	var missing struct {
		OK           bool     `json:"ok"`
		BrowserMode  string   `json:"browser_mode"`
		State        string   `json:"state"`
		Exists       bool     `json:"exists"`
		Seeded       bool     `json:"seeded"`
		SeedStrategy string   `json:"seed_strategy"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &missing); err != nil {
		t.Fatalf("browser profile status output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !missing.OK || missing.BrowserMode != "headless" || missing.State != "missing" || missing.Exists || missing.Seeded || missing.SeedStrategy != "managed" {
		t.Fatalf("browser profile status = %+v, want missing managed headless profile", missing)
	}
	if !containsSubstring(missing.NextCommands, "browser profile seed --strategy managed") || !containsSubstring(missing.NextCommands, "browser profile seed --strategy copy-default") {
		t.Fatalf("profile status next commands = %+v, want managed and copy-default seed commands", missing.NextCommands)
	}

	out.Reset()
	errOut.Reset()
	code = Execute(context.Background(), []string{"--state-dir", stateDir, "browser", "profile", "seed", "--strategy", "managed", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("browser profile seed exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}
	var seeded struct {
		OK             bool `json:"ok"`
		Seeded         bool `json:"seeded"`
		Exists         bool `json:"exists"`
		ManagedBrowser struct {
			BrowserMode         string `json:"browser_mode"`
			UserDataDir         string `json:"user_data_dir"`
			ProfileSeedStrategy string `json:"profile_seed_strategy"`
			LastSeededAt        string `json:"last_seeded_at"`
			OwnedMarker         string `json:"ownership_token"`
			ProcessStartTime    string `json:"process_start_time"`
		} `json:"managed_browser"`
	}
	if err := json.Unmarshal(out.Bytes(), &seeded); err != nil {
		t.Fatalf("browser profile seed output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !seeded.OK || !seeded.Seeded || !seeded.Exists || seeded.ManagedBrowser.BrowserMode != "headless" || seeded.ManagedBrowser.ProfileSeedStrategy != "managed" {
		t.Fatalf("browser profile seed = %+v, want seeded managed profile", seeded)
	}
	if seeded.ManagedBrowser.OwnedMarker != "" || seeded.ManagedBrowser.ProcessStartTime != "" {
		t.Fatalf("browser profile seed leaked internal metadata: %+v", seeded.ManagedBrowser)
	}

	out.Reset()
	errOut.Reset()
	code = Execute(context.Background(), []string{"--state-dir", stateDir, "browser", "profile", "status", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("browser profile status after seed exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}
	var ready struct {
		OK           bool     `json:"ok"`
		State        string   `json:"state"`
		Exists       bool     `json:"exists"`
		Seeded       bool     `json:"seeded"`
		ProfilePerm  string   `json:"profile_perm"`
		MetadataPerm string   `json:"metadata_perm"`
		NextCommands []string `json:"next_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &ready); err != nil {
		t.Fatalf("browser profile ready status output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !ready.OK || ready.State != "ready" || !ready.Exists || !ready.Seeded || ready.ProfilePerm != "700" || ready.MetadataPerm != "600" {
		t.Fatalf("browser profile status after seed = %+v, want ready owner-only profile", ready)
	}
	if !containsSubstring(ready.NextCommands, "daemon keepalive --repair") {
		t.Fatalf("profile ready next commands = %+v, want headless keepalive", ready.NextCommands)
	}
}

func TestBrowserProfileSeedCopyDefaultUsesSyntheticProfile(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()
	sourceRoot := filepath.Join(homeDir, ".config", "google-chrome")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "Default"), 0o700); err != nil {
		t.Fatalf("create source profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "Local State"), []byte("local-state"), 0o600); err != nil {
		t.Fatalf("write local state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "Default", "Cookies"), []byte("cookie-db"), 0o600); err != nil {
		t.Fatalf("write cookies: %v", err)
	}

	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", homeDir)
	}

	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--state-dir", stateDir, "browser", "profile", "seed", "--strategy", "copy-default", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("browser profile seed copy-default exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK             bool   `json:"ok"`
		Seeded         bool   `json:"seeded"`
		Exists         bool   `json:"exists"`
		SeedStrategy   string `json:"seed_strategy"`
		ManagedBrowser struct {
			ProfileSeedStrategy  string `json:"profile_seed_strategy"`
			DefaultProfileCopied bool   `json:"default_profile_copied"`
			CopiedFileCount      int    `json:"copied_file_count"`
		} `json:"managed_browser"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("browser profile seed copy-default output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || !got.Seeded || !got.Exists || got.SeedStrategy != "copy-default" || got.ManagedBrowser.ProfileSeedStrategy != "copy-default" || !got.ManagedBrowser.DefaultProfileCopied || got.ManagedBrowser.CopiedFileCount == 0 {
		t.Fatalf("browser profile seed copy-default = %+v, want copied profile metadata", got)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "browser", "headless-profile", "Default", "Cookies")); err != nil {
		t.Fatalf("managed profile missing copied Cookies fixture: %v", err)
	}
	if strings.Contains(out.String(), "cookie-db") || strings.Contains(out.String(), "local-state") {
		t.Fatalf("browser profile seed leaked copied profile values: %s", out.String())
	}
}

func TestBrowserProfileSeedIfOlderThanSkipsRecentCopyDefault(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()
	sourceRoot := filepath.Join(homeDir, ".config", "google-chrome")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "Default"), 0o700); err != nil {
		t.Fatalf("create source profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "Local State"), []byte("local-state"), 0o600); err != nil {
		t.Fatalf("write local state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "Default", "Cookies"), []byte("cookie-db"), 0o600); err != nil {
		t.Fatalf("write cookies: %v", err)
	}

	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", homeDir)
	}

	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--state-dir", stateDir, "browser", "profile", "seed", "--strategy", "copy-default", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("initial copy-default seed exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}

	cookiePath := filepath.Join(stateDir, "browser", "headless-profile", "Default", "Cookies")
	if err := os.WriteFile(cookiePath, []byte("managed-cookie-db"), 0o600); err != nil {
		t.Fatalf("overwrite managed cookie fixture: %v", err)
	}

	out.Reset()
	errOut.Reset()
	code = Execute(context.Background(), []string{"--state-dir", stateDir, "browser", "profile", "seed", "--strategy", "copy-default", "--if-older-than", "6h", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("age-gated copy-default seed exit code = %d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK                  bool   `json:"ok"`
		SeedAction          string `json:"seed_action"`
		SeedAgeSeconds      int64  `json:"seed_age_seconds"`
		SeedIntervalSeconds int64  `json:"seed_interval_seconds"`
		ManagedBrowser      struct {
			ProfileSeedStrategy string `json:"profile_seed_strategy"`
		} `json:"managed_browser"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("age-gated seed output is invalid JSON: %v; output=%s", err, out.String())
	}
	if !got.OK || got.SeedAction != "skipped" || got.SeedIntervalSeconds != int64((6*time.Hour).Seconds()) || got.ManagedBrowser.ProfileSeedStrategy != "copy-default" {
		t.Fatalf("age-gated seed = %+v, want skipped copy-default", got)
	}
	content, err := os.ReadFile(cookiePath)
	if err != nil {
		t.Fatalf("read managed cookie fixture: %v", err)
	}
	if string(content) != "managed-cookie-db" {
		t.Fatalf("age-gated seed recopied profile; cookie fixture = %q", content)
	}
}

func TestBrowserProfileSeedRejectsNegativeIfOlderThan(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--state-dir", t.TempDir(), "browser", "profile", "seed", "--strategy", "copy-default", "--if-older-than", "-1s", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitUsage {
		t.Fatalf("browser profile seed negative age exit code = %d, want %d; stdout=%s stderr=%s", code, ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("error output is invalid JSON: %v; output=%s", err, out.String())
	}
	if got.OK || got.Code != "invalid_profile_seed_age" {
		t.Fatalf("error envelope = %+v, want invalid profile seed age", got)
	}
}

func TestBrowserProfileSeedRejectsUnsupportedStrategy(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--state-dir", t.TempDir(), "browser", "profile", "seed", "--strategy", "redacted", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitUsage {
		t.Fatalf("browser profile seed invalid strategy exit code = %d, want %d; stdout=%s stderr=%s", code, ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("error output is invalid JSON: %v; output=%s", err, out.String())
	}
	if got.OK || got.Code != "invalid_profile_seed_strategy" {
		t.Fatalf("error envelope = %+v, want invalid profile seed strategy", got)
	}
}
