package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	ManagedProfileDirName = "headless-profile"
	ManagedMetadataName   = "managed-browser.json"

	ProfileSeedStrategyManaged     = "managed"
	ProfileSeedStrategyCopyDefault = "copy-default"
)

type ManagedOptions struct {
	StateDir            string
	Chrome              string
	ProfileSeedStrategy string
	ProfileRefreshAfter time.Duration
	Now                 func() time.Time
}

type ManagedMetadata struct {
	BrowserMode          string `json:"browser_mode"`
	ChromePID            int    `json:"chrome_pid,omitempty"`
	StartedAt            string `json:"started_at,omitempty"`
	UserDataDir          string `json:"user_data_dir"`
	DebuggingPort        string `json:"debugging_port,omitempty"`
	ProfileSeedStrategy  string `json:"profile_seed_strategy"`
	LastSeededAt         string `json:"last_seeded_at,omitempty"`
	DefaultProfileCopied bool   `json:"default_profile_copied,omitempty"`
	CopiedFileCount      int    `json:"copied_file_count,omitempty"`
	OwnedMarker          string `json:"ownership_token,omitempty"`
	ProcessStartTime     string `json:"process_start_time,omitempty"`
}

type ManagedStatus struct {
	BrowserMode          string `json:"browser_mode"`
	ChromePID            int    `json:"chrome_pid,omitempty"`
	StartedAt            string `json:"started_at,omitempty"`
	UserDataDir          string `json:"user_data_dir"`
	DebuggingPort        string `json:"debugging_port,omitempty"`
	ProfileSeedStrategy  string `json:"profile_seed_strategy"`
	LastSeededAt         string `json:"last_seeded_at,omitempty"`
	DefaultProfileCopied bool   `json:"default_profile_copied,omitempty"`
	CopiedFileCount      int    `json:"copied_file_count,omitempty"`
}

type ManagedLaunch struct {
	Endpoint string
	Command  *exec.Cmd
	Metadata ManagedMetadata
}

type ManagedStopResult struct {
	Checked      bool          `json:"checked"`
	Stopped      bool          `json:"stopped"`
	Skipped      bool          `json:"skipped"`
	Force        bool          `json:"force,omitempty"`
	Reason       string        `json:"reason,omitempty"`
	PIDs         []int         `json:"pids,omitempty"`
	SafetyChecks []string      `json:"safety_checks,omitempty"`
	Browser      ManagedStatus `json:"browser,omitempty"`
}

type ManagedStopOptions struct {
	Force  bool
	Signal func(int) error
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
	return PrepareManagedProfileWithStrategy(stateDir, ProfileSeedStrategyManaged, now)
}

func PrepareManagedProfileWithStrategy(stateDir, strategy string, now time.Time) (ManagedMetadata, error) {
	strategy = NormalizeProfileSeedStrategy(strategy)
	if !SupportedProfileSeedStrategy(strategy) {
		return ManagedMetadata{}, fmt.Errorf("unsupported managed profile seed strategy %q", strategy)
	}
	profileDir := ManagedProfileDir(stateDir)
	metadata := ManagedMetadata{
		BrowserMode:         "headless",
		UserDataDir:         profileDir,
		ProfileSeedStrategy: strategy,
		LastSeededAt:        now.UTC().Format(time.RFC3339),
	}
	if strategy == ProfileSeedStrategyCopyDefault {
		copied, err := ReplaceManagedProfileFromDefault(profileDir, DefaultChromeUserDataDir())
		if err != nil {
			return ManagedMetadata{}, err
		}
		metadata.DefaultProfileCopied = true
		metadata.CopiedFileCount = copied
	} else if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return ManagedMetadata{}, fmt.Errorf("create managed profile directory: %w", err)
	}
	if err := os.Chmod(profileDir, 0o700); err != nil {
		return ManagedMetadata{}, fmt.Errorf("secure managed profile directory: %w", err)
	}
	if err := SaveManagedMetadata(stateDir, metadata); err != nil {
		return ManagedMetadata{}, err
	}
	return metadata, nil
}

func NormalizeProfileSeedStrategy(strategy string) string {
	strategy = strings.TrimSpace(strings.ToLower(strategy))
	if strategy == "" {
		return ProfileSeedStrategyManaged
	}
	return strategy
}

func SupportedProfileSeedStrategy(strategy string) bool {
	switch NormalizeProfileSeedStrategy(strategy) {
	case ProfileSeedStrategyManaged, ProfileSeedStrategyCopyDefault:
		return true
	default:
		return false
	}
}

func DefaultChromeUserDataDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "User Data")
	default:
		if xdgConfig := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdgConfig != "" {
			return filepath.Join(xdgConfig, "google-chrome")
		}
		return filepath.Join(home, ".config", "google-chrome")
	}
}

func ReplaceManagedProfileFromDefault(dstRoot, srcRoot string) (int, error) {
	if strings.TrimSpace(srcRoot) == "" {
		return 0, fmt.Errorf("default Chrome profile directory is unavailable")
	}
	srcRoot = filepath.Clean(srcRoot)
	dstRoot = filepath.Clean(dstRoot)
	if srcRoot == dstRoot || strings.HasPrefix(srcRoot, dstRoot+string(os.PathSeparator)) || strings.HasPrefix(dstRoot, srcRoot+string(os.PathSeparator)) {
		return 0, fmt.Errorf("managed profile copy source and destination must be separate")
	}
	if info, err := os.Stat(filepath.Join(srcRoot, "Default")); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return 0, fmt.Errorf("default Chrome profile not found: %w", err)
	}
	parent := filepath.Dir(dstRoot)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return 0, fmt.Errorf("create managed profile parent: %w", err)
	}
	tmpRoot, err := os.MkdirTemp(parent, ".headless-profile-copy-*")
	if err != nil {
		return 0, fmt.Errorf("create managed profile copy temp dir: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(tmpRoot)
		}
	}()
	copied, err := copyProfileTree(srcRoot, tmpRoot)
	if err != nil {
		return 0, err
	}
	if err := removeChromeRuntimeArtifacts(tmpRoot); err != nil {
		return 0, err
	}
	if err := os.Chmod(tmpRoot, 0o700); err != nil {
		return 0, fmt.Errorf("secure managed profile copy: %w", err)
	}
	if err := removeAllWithRetry(dstRoot); err != nil {
		return 0, fmt.Errorf("replace old managed profile: %w", err)
	}
	if err := os.Rename(tmpRoot, dstRoot); err != nil {
		return 0, fmt.Errorf("install managed profile copy: %w", err)
	}
	complete = true
	return copied, nil
}

func copyProfileTree(srcRoot, dstRoot string) (int, error) {
	copied := 0
	err := filepath.WalkDir(srcRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if chromeRuntimeArtifact(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dstPath := filepath.Join(dstRoot, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
				return err
			}
			_ = os.Remove(dstPath)
			if err := os.Symlink(target, dstPath); err != nil {
				return err
			}
			copied++
			return nil
		}
		if info.IsDir() {
			if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(dstPath, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := copyProfileFile(path, dstPath, info.Mode().Perm()); err != nil {
			return err
		}
		copied++
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("copy default Chrome profile: %w", err)
	}
	return copied, nil
}

func copyProfileFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func removeAllWithRetry(path string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		err = os.RemoveAll(path)
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

func removeChromeRuntimeArtifacts(root string) error {
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket", "DevToolsActivePort"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove Chrome runtime artifact %s: %w", name, err)
		}
	}
	return nil
}

func chromeRuntimeArtifact(rel string) bool {
	switch filepath.Clean(rel) {
	case "SingletonLock", "SingletonCookie", "SingletonSocket", "DevToolsActivePort":
		return true
	default:
		return false
	}
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
		BrowserMode:          metadata.BrowserMode,
		ChromePID:            metadata.ChromePID,
		StartedAt:            metadata.StartedAt,
		UserDataDir:          metadata.UserDataDir,
		DebuggingPort:        metadata.DebuggingPort,
		ProfileSeedStrategy:  metadata.ProfileSeedStrategy,
		LastSeededAt:         metadata.LastSeededAt,
		DefaultProfileCopied: metadata.DefaultProfileCopied,
		CopiedFileCount:      metadata.CopiedFileCount,
	}
}

func StopOwnedManagedChrome(ctx context.Context, stateDir string, signal func(int) error) (ManagedStopResult, error) {
	return StopManagedChrome(ctx, stateDir, ManagedStopOptions{Signal: signal})
}

func StopManagedChrome(ctx context.Context, stateDir string, opts ManagedStopOptions) (ManagedStopResult, error) {
	metadata, ok, err := LoadManagedMetadata(stateDir)
	if err != nil || !ok {
		return ManagedStopResult{Checked: true, Force: opts.Force, Skipped: true, Reason: "managed metadata missing"}, err
	}
	result := ManagedStopResult{Checked: true, Force: opts.Force, Browser: ManagedMetadataStatus(metadata)}
	if metadata.BrowserMode != "headless" || metadata.ChromePID <= 0 || strings.TrimSpace(metadata.OwnedMarker) == "" || strings.TrimSpace(metadata.ProcessStartTime) == "" {
		if !opts.Force {
			result.Skipped = true
			result.Reason = "managed ownership metadata incomplete"
			return result, nil
		}
		pids, checks, err := forceManagedStopCandidates(ctx, stateDir, metadata)
		result.PIDs = pids
		result.SafetyChecks = checks
		if err != nil {
			return result, err
		}
		if len(pids) == 0 {
			result.Skipped = true
			result.Reason = "no cdp-owned managed headless Chrome process candidates found"
			return result, nil
		}
		if opts.Signal == nil {
			opts.Signal = signalProcess
		}
		for _, pid := range pids {
			if err := opts.Signal(pid); err != nil {
				return result, err
			}
		}
		result.Stopped = true
		result.Reason = "forced managed headless cleanup"
		return result, nil
	}
	if opts.Signal == nil {
		opts.Signal = signalProcess
	}
	if err := opts.Signal(metadata.ChromePID); err != nil {
		return result, err
	}
	result.Stopped = true
	result.PIDs = []int{metadata.ChromePID}
	result.SafetyChecks = []string{"managed_metadata_complete", "browser_mode=headless", "ownership_marker_present", "start_time_present"}
	return result, nil
}

func forceManagedStopCandidates(ctx context.Context, stateDir string, metadata ManagedMetadata) ([]int, []string, error) {
	managedProfile := filepath.Clean(ManagedProfileDir(stateDir))
	userDataDir := filepath.Clean(strings.TrimSpace(metadata.UserDataDir))
	if userDataDir == "." || userDataDir == "" {
		userDataDir = managedProfile
	}
	checks := []string{"force_requested", "browser_mode=headless"}
	if metadata.BrowserMode != "headless" {
		return nil, checks, nil
	}
	if userDataDir != managedProfile {
		checks = append(checks, "managed_profile_path_mismatch")
		return nil, checks, nil
	}
	checks = append(checks, "managed_profile_path_matches_state_dir")
	seen := map[int]bool{}
	var pids []int
	add := func(pid int) {
		if pid <= 0 || seen[pid] {
			return
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	processes, err := managedChromeProcesses(ctx, managedProfile)
	if err != nil {
		return pids, checks, err
	}
	if len(processes) > 0 {
		checks = append(checks, "process_command_line_matches_managed_profile")
	}
	for _, pid := range processes {
		add(pid)
	}
	return pids, checks, nil
}

func managedChromeProcesses(ctx context.Context, managedProfile string) ([]int, error) {
	switch runtime.GOOS {
	case "linux":
		return managedChromeProcessesLinux(ctx, managedProfile)
	case "darwin":
		return managedChromeProcessesPS(ctx, managedProfile)
	default:
		return nil, nil
	}
}

func managedChromeProcessesLinux(ctx context.Context, managedProfile string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, nil
	}
	var pids []int
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return pids, ctx.Err()
		default:
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		cmdline := strings.ReplaceAll(string(raw), "\x00", " ")
		if managedChromeCommandLine(cmdline, managedProfile) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func managedChromeProcessesPS(ctx context.Context, managedProfile string) ([]int, error) {
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=")
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == os.Getpid() {
			continue
		}
		cmdline := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if managedChromeCommandLine(cmdline, managedProfile) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func managedChromeCommandLine(cmdline, managedProfile string) bool {
	if strings.TrimSpace(cmdline) == "" {
		return false
	}
	return strings.Contains(cmdline, "--headless") &&
		strings.Contains(cmdline, "--remote-debugging-port") &&
		(strings.Contains(cmdline, "--user-data-dir="+managedProfile) || strings.Contains(cmdline, "--user-data-dir "+managedProfile))
}

func signalProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find managed chrome process: %w", err)
	}
	interruptErr := process.Signal(os.Interrupt)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if killErr := process.Kill(); killErr != nil {
		return fmt.Errorf("stop managed chrome: interrupt: %v; kill: %w", interruptErr, killErr)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
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
	metadata, err := prepareManagedProfileForLaunch(opts.StateDir, NormalizeProfileSeedStrategy(opts.ProfileSeedStrategy), now)
	if err != nil {
		return ManagedLaunch{}, err
	}
	metadata.StartedAt = now.Format(time.RFC3339)
	metadata.OwnedMarker, err = randomToken()
	if err != nil {
		return ManagedLaunch{}, err
	}
	if err := os.Remove(filepath.Join(metadata.UserDataDir, "DevToolsActivePort")); err != nil && !os.IsNotExist(err) {
		return ManagedLaunch{}, fmt.Errorf("remove stale managed active port file: %w", err)
	}

	cmd := exec.Command(chromePath, ManagedLaunchArgs(chromePath, metadata.UserDataDir)[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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
	if err := cmd.Process.Release(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ManagedLaunch{}, fmt.Errorf("release managed chrome process: %w", err)
	}
	return ManagedLaunch{Endpoint: endpoint, Command: cmd, Metadata: metadata}, nil
}

func prepareManagedProfileForLaunch(stateDir, strategy string, now time.Time) (ManagedMetadata, error) {
	if !SupportedProfileSeedStrategy(strategy) {
		return ManagedMetadata{}, fmt.Errorf("unsupported managed profile seed strategy %q", strategy)
	}
	existing, ok, err := LoadManagedMetadata(stateDir)
	if err != nil {
		return ManagedMetadata{}, err
	}
	if ok && strings.TrimSpace(existing.UserDataDir) == ManagedProfileDir(stateDir) {
		if info, statErr := os.Stat(existing.UserDataDir); statErr == nil && info.IsDir() {
			existing.ProfileSeedStrategy = NormalizeProfileSeedStrategy(existing.ProfileSeedStrategy)
			if strategy == ProfileSeedStrategyManaged || existing.ProfileSeedStrategy == strategy {
				return existing, nil
			}
		}
	}
	return PrepareManagedProfileWithStrategy(stateDir, strategy, now)
}

func ReuseManagedChrome(ctx context.Context, stateDir string) (ManagedLaunch, bool, error) {
	metadata, ok, err := LoadManagedMetadata(stateDir)
	if err != nil || !ok {
		return ManagedLaunch{}, false, err
	}
	if metadata.BrowserMode != "headless" || metadata.ChromePID <= 0 || strings.TrimSpace(metadata.UserDataDir) == "" {
		return ManagedLaunch{}, false, nil
	}
	select {
	case <-ctx.Done():
		return ManagedLaunch{}, false, ctx.Err()
	default:
	}
	port, path, err := ReadActivePortFile(metadata.UserDataDir)
	if err != nil {
		return ManagedLaunch{}, false, nil
	}
	metadata.DebuggingPort = port
	endpoint := fmt.Sprintf("ws://%s%s", net.JoinHostPort("127.0.0.1", port), path)
	if err := ValidateLoopbackEndpoint(endpoint); err != nil {
		return ManagedLaunch{}, false, err
	}
	if !managedEndpointReachable(ctx, endpoint) {
		return ManagedLaunch{}, false, nil
	}
	return ManagedLaunch{Endpoint: endpoint, Metadata: metadata}, true, nil
}

func managedEndpointReachable(ctx context.Context, endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	u.Scheme = "http"
	u.Path = "/json/version"
	u.RawQuery = ""
	u.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
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
	if value := strings.TrimSpace(os.Getenv("CDP_CHROME_CANDIDATES")); value != "" {
		return strings.Split(value, string(os.PathListSeparator))
	}
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
