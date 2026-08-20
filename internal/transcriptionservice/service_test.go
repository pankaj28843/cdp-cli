package transcriptionservice

import (
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	config := Config{
		BinaryPath:           "/Users/test/.local/bin/cdp",
		StateDir:             "/Users/test/.cdp-cli",
		Address:              "0.0.0.0:28765",
		HTTPAddress:          "0.0.0.0:28766",
		Provider:             "chatgpt-web",
		AllowedProviders:     []string{"chatgpt-web"},
		BrowserMode:          "headed",
		Display:              ":0",
		XAuthority:           "/Users/test/.Xauthority",
		AllowOverBudget:      true,
		LocalBaseURL:         "http://example.test:9000/v1",
		LocalRealtimeBaseURL: "ws://example.test:9001/v1",
		LocalAPIKey:          "local-key",
		MaxAudioBytes:        512 << 20,
		AuthRefreshInterval:  10 * time.Minute,
		Path:                 "/opt/homebrew/bin:/usr/bin:/bin",
	}
	return config
}

func TestRenderLaunchAgentIsOwnerScopedAndRestartable(t *testing.T) {
	paths, err := PathsForHome("/Users/test")
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := Render(PlatformMacOS, testConfig(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Path != paths.LaunchAgent {
		t.Fatalf("artifacts = %+v, want one LaunchAgent artifact", artifacts)
	}
	text := string(artifacts[0].Data)
	for _, want := range []string{
		"dev.pankaj.cdp.transcription",
		"/Users/test/.local/bin/cdp",
		"0.0.0.0:28765",
		"CDP_TRANSCRIPTION_HTTP_ADDRESS",
		"0.0.0.0:28766",
		"CDP_TRANSCRIPTION_PROVIDERS",
		"chatgpt-web",
		"CDP_BROWSER_MODE",
		"headed",
		"DISPLAY",
		":0",
		"XAUTHORITY",
		"/Users/test/.Xauthority",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"transcription.error.log",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("LaunchAgent missing %q:\n%s", want, text)
		}
	}
	if artifacts[0].Mode != 0o600 {
		t.Fatalf("LaunchAgent mode = %o, want 600", artifacts[0].Mode)
	}
}

func TestRenderSystemdUnitSeparatesOwnerOnlyEnvironment(t *testing.T) {
	paths, err := PathsForHome("/synthetic-user")
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := Render(PlatformLinux, testConfig(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(artifacts))
	}
	unit := string(artifacts[0].Data)
	environment := string(artifacts[1].Data)
	for _, want := range []string{
		"ExecStart=\"/Users/test/.local/bin/cdp\" transcription serve",
		"EnvironmentFile=-" + paths.Environment,
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, unit)
		}
	}
	if strings.Contains(environment, "CDP_TRANSCRIPTION_API_TOKEN") {
		t.Fatalf("environment file contains removed bearer-token configuration: %s", environment)
	}
	for _, want := range []string{
		`CDP_TRANSCRIPTION_PROVIDERS="chatgpt-web"`,
		`CDP_BROWSER_MODE="headed"`,
		`CDP_ALLOW_OVER_BUDGET="true"`,
		`DISPLAY=":0"`,
		`XAUTHORITY="/Users/test/.Xauthority"`,
	} {
		if !strings.Contains(environment, want) {
			t.Fatalf("environment file missing %q: %s", want, environment)
		}
	}
	if artifacts[1].Mode != 0o600 {
		t.Fatalf("environment mode = %o, want 600", artifacts[1].Mode)
	}
}

func TestRenderPropagatesTLSFilesToNativeServiceEnvironment(t *testing.T) {
	paths, err := PathsForHome("/synthetic-user")
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig()
	config.TLSCertFile = "/synthetic-user/.cdp-cli/tls/transcription.crt"
	config.TLSKeyFile = "/synthetic-user/.cdp-cli/tls/transcription.key"

	macArtifacts, err := Render(PlatformMacOS, config, paths)
	if err != nil {
		t.Fatal(err)
	}
	macText := string(macArtifacts[0].Data)
	for _, want := range []string{config.TLSCertFile, config.TLSKeyFile, "CDP_TRANSCRIPTION_TLS_CERT", "CDP_TRANSCRIPTION_TLS_KEY"} {
		if !strings.Contains(macText, want) {
			t.Fatalf("LaunchAgent missing TLS value %q:\n%s", want, macText)
		}
	}

	linuxArtifacts, err := Render(PlatformLinux, config, paths)
	if err != nil {
		t.Fatal(err)
	}
	linuxEnvironment := string(linuxArtifacts[1].Data)
	for _, want := range []string{`CDP_TRANSCRIPTION_TLS_CERT="` + config.TLSCertFile + `"`, `CDP_TRANSCRIPTION_TLS_KEY="` + config.TLSKeyFile + `"`} {
		if !strings.Contains(linuxEnvironment, want) {
			t.Fatalf("systemd environment missing TLS value %q:\n%s", want, linuxEnvironment)
		}
	}
}
