package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	ManagedProfileDirName = "headless-profile"
	ManagedMetadataName   = "managed-browser.json"
)

type ManagedOptions struct {
	StateDir string
	Chrome   string
	Now      func() time.Time
}

type ManagedMetadata struct {
	BrowserMode         string `json:"browser_mode"`
	ChromePID           int    `json:"chrome_pid,omitempty"`
	StartedAt           string `json:"started_at,omitempty"`
	UserDataDir         string `json:"user_data_dir"`
	DebuggingPort       string `json:"debugging_port,omitempty"`
	ProfileSeedStrategy string `json:"profile_seed_strategy"`
	LastSeededAt        string `json:"last_seeded_at,omitempty"`
	OwnedMarker         string `json:"ownership_token,omitempty"`
	ProcessStartTime    string `json:"process_start_time,omitempty"`
}

type ManagedStatus struct {
	BrowserMode         string `json:"browser_mode"`
	ChromePID           int    `json:"chrome_pid,omitempty"`
	StartedAt           string `json:"started_at,omitempty"`
	UserDataDir         string `json:"user_data_dir"`
	DebuggingPort       string `json:"debugging_port,omitempty"`
	ProfileSeedStrategy string `json:"profile_seed_strategy"`
	LastSeededAt        string `json:"last_seeded_at,omitempty"`
}

type ManagedLaunch struct {
	Endpoint string
	Command  *exec.Cmd
	Metadata ManagedMetadata
}

type ManagedStopResult struct {
	Checked bool          `json:"checked"`
	Stopped bool          `json:"stopped"`
	Skipped bool          `json:"skipped"`
	Reason  string        `json:"reason,omitempty"`
	Browser ManagedStatus `json:"browser,omitempty"`
}

func ManagedProfileDir(stateDir string) string {
	return filepath.Join(stateDir, "browser", ManagedProfileDirName)
}

func ManagedMetadataPath(stateDir string) string {
	return filepath.Join(stateDir, "browser", ManagedMetadataName)
}

func DiscoverChrome(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	candidates := chromeCandidates(runtime.GOOS)
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("chrome executable not found")
}

func ManagedLaunchArgs(chromePath, userDataDir string) []string {
	return []string{
		chromePath,
		"--headless",
		"--remote-debugging-port=0",
		"--user-data-dir=" + userDataDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
}

func PrepareManagedProfile(stateDir string, now time.Time) (ManagedMetadata, error) {
	profileDir := ManagedProfileDir(stateDir)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return ManagedMetadata{}, fmt.Errorf("create managed profile directory: %w", err)
	}
	metadata := ManagedMetadata{
		BrowserMode:         "headless",
		UserDataDir:         profileDir,
		ProfileSeedStrategy: "managed",
		LastSeededAt:        now.UTC().Format(time.RFC3339),
	}
	if err := SaveManagedMetadata(stateDir, metadata); err != nil {
		return ManagedMetadata{}, err
	}
	return metadata, nil
}

func SaveManagedMetadata(stateDir string, metadata ManagedMetadata) error {
	if err := os.MkdirAll(filepath.Dir(ManagedMetadataPath(stateDir)), 0o700); err != nil {
		return fmt.Errorf("create managed metadata directory: %w", err)
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal managed metadata: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(ManagedMetadataPath(stateDir), data, 0o600); err != nil {
		return fmt.Errorf("write managed metadata: %w", err)
	}
	return nil
}

func LoadManagedMetadata(stateDir string) (ManagedMetadata, bool, error) {
	data, err := os.ReadFile(ManagedMetadataPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return ManagedMetadata{}, false, nil
		}
		return ManagedMetadata{}, false, fmt.Errorf("read managed metadata: %w", err)
	}
	var metadata ManagedMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return ManagedMetadata{}, false, fmt.Errorf("parse managed metadata: %w", err)
	}
	return metadata, true, nil
}

func ManagedMetadataStatus(metadata ManagedMetadata) ManagedStatus {
	return ManagedStatus{
		BrowserMode:         metadata.BrowserMode,
		ChromePID:           metadata.ChromePID,
		StartedAt:           metadata.StartedAt,
		UserDataDir:         metadata.UserDataDir,
		DebuggingPort:       metadata.DebuggingPort,
		ProfileSeedStrategy: metadata.ProfileSeedStrategy,
		LastSeededAt:        metadata.LastSeededAt,
	}
}

func StopOwnedManagedChrome(ctx context.Context, stateDir string, signal func(int) error) (ManagedStopResult, error) {
	metadata, ok, err := LoadManagedMetadata(stateDir)
	if err != nil || !ok {
		return ManagedStopResult{Checked: true, Skipped: true, Reason: "managed metadata missing"}, err
	}
	result := ManagedStopResult{Checked: true, Browser: ManagedMetadataStatus(metadata)}
	if metadata.BrowserMode != "headless" || metadata.ChromePID <= 0 || strings.TrimSpace(metadata.OwnedMarker) == "" || strings.TrimSpace(metadata.ProcessStartTime) == "" {
		result.Skipped = true
		result.Reason = "managed ownership metadata incomplete"
		return result, nil
	}
	if signal == nil {
		signal = signalProcess
	}
	if err := signal(metadata.ChromePID); err != nil {
		return result, err
	}
	result.Stopped = true
	return result, nil
}

func signalProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find managed chrome process: %w", err)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		if killErr := process.Kill(); killErr != nil {
			return fmt.Errorf("stop managed chrome: interrupt: %v; kill: %w", err, killErr)
		}
	}
	return nil
}

func StartManagedChrome(ctx context.Context, opts ManagedOptions) (ManagedLaunch, error) {
	chromePath, err := DiscoverChrome(opts.Chrome)
	if err != nil {
		return ManagedLaunch{}, err
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	metadata, err := PrepareManagedProfile(opts.StateDir, now)
	if err != nil {
		return ManagedLaunch{}, err
	}
	metadata.StartedAt = now.Format(time.RFC3339)
	metadata.OwnedMarker, err = randomToken()
	if err != nil {
		return ManagedLaunch{}, err
	}

	cmd := exec.CommandContext(ctx, chromePath, ManagedLaunchArgs(chromePath, metadata.UserDataDir)[1:]...)
	if err := cmd.Start(); err != nil {
		return ManagedLaunch{}, fmt.Errorf("start managed chrome: %w", err)
	}
	metadata.ChromePID = cmd.Process.Pid
	metadata.ProcessStartTime = now.Format(time.RFC3339)

	port, path, err := WaitManagedActivePort(ctx, metadata.UserDataDir)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ManagedLaunch{}, err
	}
	metadata.DebuggingPort = port
	endpoint := fmt.Sprintf("ws://%s%s", net.JoinHostPort("127.0.0.1", port), path)
	if err := ValidateLoopbackEndpoint(endpoint); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ManagedLaunch{}, err
	}
	if err := SaveManagedMetadata(opts.StateDir, metadata); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ManagedLaunch{}, err
	}
	return ManagedLaunch{Endpoint: endpoint, Command: cmd, Metadata: metadata}, nil
}

func WaitManagedActivePort(ctx context.Context, userDataDir string) (string, string, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		port, path, err := ReadActivePortFile(userDataDir)
		if err == nil {
			return port, path, nil
		}
		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("wait for DevToolsActivePort: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func ValidateLoopbackEndpoint(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse managed endpoint: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("managed endpoint scheme must be ws or wss")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("managed endpoint missing host")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("managed endpoint host must be loopback")
	}
	return nil
}

func chromeCandidates(goos string) []string {
	switch goos {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"google-chrome",
			"chrome",
			"chromium",
		}
	case "windows":
		return []string{"chrome.exe", "google-chrome.exe", "chromium.exe"}
	default:
		return []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"}
	}
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate managed ownership token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
