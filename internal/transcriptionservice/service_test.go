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
		Address:              "0.0.0.0:8765",
		Provider:             "chatgpt-web",
		LocalBaseURL:         "http://example.test:9000/v1",
		LocalRealtimeBaseURL: "ws://example.test:9001/v1",
		LocalAPIKey:          "local-key",
		MaxAudioBytes:        512 << 20,
		AuthRefreshInterval:  10 * time.Minute,
		Path:                 "/opt/homebrew/bin:/usr/bin:/bin",
	}
	config.Token = "demo-token"
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
		"0.0.0.0:8765",
		"CDP_TRANSCRIPTION_API_TOKEN",
		"demo-token",
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
		"EnvironmentFile=-\"" + paths.Environment + "\"",
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, unit)
		}
	}
	if !strings.Contains(environment, `CDP_TRANSCRIPTION_API_TOKEN="demo-token"`) {
		t.Fatalf("environment file does not contain quoted token: %s", environment)
	}
	if artifacts[1].Mode != 0o600 {
		t.Fatalf("environment mode = %o, want 600", artifacts[1].Mode)
	}
}
