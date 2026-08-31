package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestLoadManagedFingerprintProfile(t *testing.T) {
	path := writeManagedFingerprintProfile(t, map[string]any{
		"userAgent": "SyntheticAgent/1.0",
		"platform":  "SyntheticPlatform",
		"vendor":    "SyntheticVendor",
		"language":  "en-US",
		"timezone":  "UTC",
		"viewport":  map[string]any{"width": 1280, "height": 720},
	})

	profile, err := loadManagedFingerprintProfile(path)
	if err != nil {
		t.Fatalf("loadManagedFingerprintProfile returned error: %v", err)
	}
	if got, want := profile.chromeArgs(), []string{"--user-agent=SyntheticAgent/1.0", "--window-size=1280,720", "--lang=en-US", "--accept-lang=en-US"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("chromeArgs() = %#v, want %#v", got, want)
	}
	env := profile.childEnvironment([]string{"PATH=/bin", "TZ=Host/Local", "SYNTHETIC_TOKEN=kept"})
	if got := environmentValue(env, "TZ"); got != "UTC" {
		t.Fatalf("child TZ = %q, want UTC", got)
	}
	if got := environmentValue(env, "SYNTHETIC_TOKEN"); got != "kept" {
		t.Fatalf("unrelated child environment = %q, want kept", got)
	}
}

func TestLoadManagedFingerprintProfileRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"malformed JSON", `{`, "decode"},
		{"non-object", `[]`, "decode"},
		{"missing source field", `{"userAgent":"A","platform":"P","vendor":"V","language":"en-US","timezone":"UTC","viewport":{"width":1280}}`, "viewport.height"},
		{"unknown field", `{"userAgent":"A","platform":"P","vendor":"V","language":"en-US","timezone":"UTC","viewport":{"width":1280,"height":720},"script":"override"}`, "unknown field"},
		{"user agent control", `{"userAgent":"A\nB","platform":"P","vendor":"V","language":"en-US","timezone":"UTC","viewport":{"width":1280,"height":720}}`, "userAgent"},
		{"invalid language", `{"userAgent":"A","platform":"P","vendor":"V","language":"not a locale","timezone":"UTC","viewport":{"width":1280,"height":720}}`, "language"},
		{"invalid timezone", `{"userAgent":"A","platform":"P","vendor":"V","language":"en-US","timezone":"Mars/Olympus","viewport":{"width":1280,"height":720}}`, "timezone"},
		{"invalid viewport", `{"userAgent":"A","platform":"P","vendor":"V","language":"en-US","timezone":"UTC","viewport":{"width":0,"height":720}}`, "viewport.width"},
		{"trailing value", `{"userAgent":"A","platform":"P","vendor":"V","language":"en-US","timezone":"UTC","viewport":{"width":1280,"height":720}} {}`, "single JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profile.json")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write profile: %v", err)
			}
			_, err := loadManagedFingerprintProfile(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("loadManagedFingerprintProfile error = %v, want substring %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "override") {
				t.Fatalf("loadManagedFingerprintProfile exposed a profile value: %v", err)
			}
		})
	}
}

func TestLoadManagedFingerprintProfileDoesNotExposePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-profile-name.json")
	_, err := loadManagedFingerprintProfile(path)
	if err == nil || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "private-profile-name") {
		t.Fatalf("loadManagedFingerprintProfile error = %v, want pathless failure", err)
	}
}

func TestLoadManagedFingerprintProfileRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(`{"userAgent":"`+strings.Repeat("x", managedFingerprintMaxBytes)+`"}`), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	if _, err := loadManagedFingerprintProfile(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("loadManagedFingerprintProfile error = %v, want bounded-size rejection", err)
	}
}

func TestStartManagedChromeAppliesFingerprintOnlyToNewChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell Chrome fixture is Unix-only")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	captureDir := t.TempDir()
	argsPath := filepath.Join(captureDir, "args")
	tzPath := filepath.Join(captureDir, "tz")
	t.Setenv("CDP_SYNTHETIC_ARGS", argsPath)
	t.Setenv("CDP_SYNTHETIC_TZ", tzPath)
	t.Setenv("TZ", "Host/Local")
	profilePath := writeManagedFingerprintProfile(t, map[string]any{
		"userAgent": "SyntheticAgent/2.0",
		"platform":  "SyntheticPlatform",
		"vendor":    "SyntheticVendor",
		"language":  "da-DK",
		"timezone":  "Europe/Copenhagen",
		"viewport":  map[string]any{"width": 1440, "height": 900},
	})
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	script := `#!/usr/bin/env sh
set -eu
user_data_dir=
printf '%s\n' "$@" > "$CDP_SYNTHETIC_ARGS"
printf '%s' "${TZ-}" > "$CDP_SYNTHETIC_TZ"
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) user_data_dir="${arg#--user-data-dir=}" ;;
  esac
done
printf '12345\n/devtools/browser/synthetic\n' > "$user_data_dir/DevToolsActivePort"
sleep 30
`
	if err := os.WriteFile(chromePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake Chrome: %v", err)
	}

	launch, err := StartManagedChrome(context.Background(), ManagedOptions{StateDir: stateDir, Chrome: chromePath, FingerprintProfile: profilePath})
	if err != nil {
		t.Fatalf("StartManagedChrome returned error: %v", err)
	}
	t.Cleanup(func() {
		if process, findErr := os.FindProcess(launch.Metadata.ChromePID); findErr == nil {
			_ = process.Signal(syscall.SIGKILL)
		}
	})
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read captured arguments: %v", err)
	}
	for _, want := range []string{"--user-agent=SyntheticAgent/2.0", "--window-size=1440,900", "--lang=da-DK", "--accept-lang=da-DK"} {
		if !strings.Contains(string(args), want+"\n") {
			t.Fatalf("captured arguments missing %q: %s", want, args)
		}
	}
	childTZ, err := os.ReadFile(tzPath)
	if err != nil {
		t.Fatalf("read captured timezone: %v", err)
	}
	if string(childTZ) != "Europe/Copenhagen" || os.Getenv("TZ") != "Host/Local" {
		t.Fatalf("timezone child=%q parent=%q, want child-only override", childTZ, os.Getenv("TZ"))
	}
	wantFields := []string{"language", "timezone", "user_agent", "viewport"}
	status := ManagedMetadataStatus(launch.Metadata)
	if !status.FingerprintProfileApplied || !reflect.DeepEqual(status.FingerprintProfileFields, wantFields) {
		t.Fatalf("managed status = %+v, want metadata-only fingerprint summary", status)
	}
	metadata, err := os.ReadFile(ManagedMetadataPath(stateDir))
	if err != nil {
		t.Fatalf("read managed metadata: %v", err)
	}
	for _, privateValue := range []string{profilePath, "SyntheticAgent/2.0", "da-DK", "Europe/Copenhagen"} {
		if strings.Contains(string(metadata), privateValue) {
			t.Fatalf("managed metadata exposed fingerprint path or value %q: %s", privateValue, metadata)
		}
	}
}

func TestStartManagedChromeRejectsInvalidFingerprintBeforeLaunch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "launched")
	t.Setenv("CDP_SYNTHETIC_MARKER", marker)
	chromePath := filepath.Join(t.TempDir(), "fake-chrome")
	if err := os.WriteFile(chromePath, []byte("#!/usr/bin/env sh\ntouch \"$CDP_SYNTHETIC_MARKER\"\n"), 0o755); err != nil {
		t.Fatalf("write fake Chrome: %v", err)
	}
	profilePath := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(profilePath, []byte(`{"userAgent":"only-one-field"}`), 0o600); err != nil {
		t.Fatalf("write invalid profile: %v", err)
	}
	_, err := StartManagedChrome(context.Background(), ManagedOptions{StateDir: t.TempDir(), Chrome: chromePath, FingerprintProfile: profilePath})
	if err == nil || !strings.Contains(err.Error(), "fingerprint profile") {
		t.Fatalf("StartManagedChrome error = %v, want profile validation failure", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("invalid profile started Chrome; marker stat error = %v", statErr)
	}
}

func writeManagedFingerprintProfile(t *testing.T, profile map[string]any) string {
	t.Helper()
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	path := filepath.Join(t.TempDir(), "fingerprint.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return path
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
