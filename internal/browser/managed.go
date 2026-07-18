package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	ManagedProfileDirName = "headless-profile"
	ManagedMetadataName   = "managed-browser.json"
	ManagedRegistryName   = "managed-processes.json"

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
	Checked         bool                     `json:"checked"`
	Stopped         bool                     `json:"stopped"`
	Skipped         bool                     `json:"skipped"`
	Force           bool                     `json:"force,omitempty"`
	Reason          string                   `json:"reason,omitempty"`
	PIDs            []int                    `json:"pids,omitempty"`
	SafetyChecks    []string                 `json:"safety_checks,omitempty"`
	ProcessEvidence []ManagedProcessEvidence `json:"process_evidence,omitempty"`
	Browser         ManagedStatus            `json:"browser,omitempty"`
}

type ManagedStopOptions struct {
	Force  bool
	Signal func(int) error
}

type ManagedProcessRegistry struct {
	Version     int                    `json:"version"`
	BrowserMode string                 `json:"browser_mode"`
	Records     []ManagedProcessRecord `json:"records"`
}

type ManagedProcessEvidence struct {
	PID                int    `json:"pid"`
	ParentPID          int    `json:"parent_pid,omitempty"`
	RootPID            int    `json:"root_pid"`
	Role               string `json:"role"`
	ProfileMatched     bool   `json:"profile_matched"`
	DebuggingPort      string `json:"debugging_port,omitempty"`
	DebuggingPortMatch bool   `json:"debugging_port_match"`
}

type ManagedOwnershipEvidence struct {
	Checked       bool     `json:"checked"`
	Owned         bool     `json:"owned"`
	PID           int      `json:"pid,omitempty"`
	DebuggingPort string   `json:"debugging_port,omitempty"`
	UserDataDir   string   `json:"user_data_dir,omitempty"`
	SafetyChecks  []string `json:"safety_checks,omitempty"`
	Reasons       []string `json:"reasons,omitempty"`
}

type ManagedProcessRecord struct {
	PID                 int    `json:"pid"`
	BrowserMode         string `json:"browser_mode"`
	UserDataDir         string `json:"user_data_dir"`
	DebuggingPort       string `json:"debugging_port,omitempty"`
	StartedAt           string `json:"started_at,omitempty"`
	LastSeenAt          string `json:"last_seen_at,omitempty"`
	ExitedAt            string `json:"exited_at,omitempty"`
	State               string `json:"state,omitempty"`
	CleanupAt           string `json:"cleanup_at,omitempty"`
	CleanupReason       string `json:"cleanup_reason,omitempty"`
	OwnershipMarker     string `json:"ownership_token,omitempty"`
	ProcessStartTime    string `json:"process_start_time,omitempty"`
	ProfileSeedStrategy string `json:"profile_seed_strategy,omitempty"`
}

type ManagedProcessRecordStatus struct {
	PID                 int    `json:"pid"`
	BrowserMode         string `json:"browser_mode"`
	UserDataDir         string `json:"user_data_dir"`
	DebuggingPort       string `json:"debugging_port,omitempty"`
	StartedAt           string `json:"started_at,omitempty"`
	LastSeenAt          string `json:"last_seen_at,omitempty"`
	ExitedAt            string `json:"exited_at,omitempty"`
	State               string `json:"state,omitempty"`
	CleanupAt           string `json:"cleanup_at,omitempty"`
	CleanupReason       string `json:"cleanup_reason,omitempty"`
	ProfileSeedStrategy string `json:"profile_seed_strategy,omitempty"`
}

type ManagedProcessReconcileOptions struct {
	ActivePID     int
	ReapExtras    bool
	ReadOnly      bool
	Now           func() time.Time
	Signal        func(int) error
	ProcessLister func(context.Context, string) ([]int, error)
}

type ManagedProcessSignalFailure struct {
	PID   int    `json:"pid"`
	Error string `json:"error"`
}

type ManagedProcessReconcileResult struct {
	Checked         bool                          `json:"checked"`
	State           string                        `json:"state"`
	BrowserMode     string                        `json:"browser_mode"`
	ActivePID       int                           `json:"active_pid,omitempty"`
	RegisteredCount int                           `json:"registered_count"`
	LiveCount       int                           `json:"live_count"`
	StaleCount      int                           `json:"stale_count"`
	ReapedCount     int                           `json:"reaped_count"`
	ReapedPIDs      []int                         `json:"reaped_pids,omitempty"`
	SkippedPIDs     []int                         `json:"skipped_pids,omitempty"`
	SignalFailures  []ManagedProcessSignalFailure `json:"signal_failures,omitempty"`
	SafetyChecks    []string                      `json:"safety_checks,omitempty"`
	Records         []ManagedProcessRecordStatus  `json:"records,omitempty"`
	Reason          string                        `json:"reason,omitempty"`
	NextCommands    []string                      `json:"next_commands,omitempty"`
}

func ManagedProfileDir(stateDir string) string {
	return filepath.Join(stateDir, "browser", ManagedProfileDirName)
}

func ManagedMetadataPath(stateDir string) string {
	return filepath.Join(stateDir, "browser", ManagedMetadataName)
}

func ManagedProcessRegistryPath(stateDir string) string {
	return filepath.Join(stateDir, "browser", ManagedRegistryName)
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
	return replaceManagedProfileFromDefault(dstRoot, srcRoot, profileReplaceOps{})
}

type profileReplaceOps struct {
	copyProfileTree              func(string, string) (int, error)
	removeChromeRuntimeArtifacts func(string) error
	chmod                        func(string, os.FileMode) error
	rename                       func(string, string) error
	removeAll                    func(string) error
}

func (ops profileReplaceOps) withDefaults() profileReplaceOps {
	if ops.copyProfileTree == nil {
		ops.copyProfileTree = copyProfileTree
	}
	if ops.removeChromeRuntimeArtifacts == nil {
		ops.removeChromeRuntimeArtifacts = removeChromeRuntimeArtifacts
	}
	if ops.chmod == nil {
		ops.chmod = os.Chmod
	}
	if ops.rename == nil {
		ops.rename = os.Rename
	}
	if ops.removeAll == nil {
		ops.removeAll = removeAllWithRetry
	}
	return ops
}

func replaceManagedProfileFromDefault(dstRoot, srcRoot string, ops profileReplaceOps) (int, error) {
	ops = ops.withDefaults()
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
			_ = ops.removeAll(tmpRoot)
		}
	}()
	copied, err := ops.copyProfileTree(srcRoot, tmpRoot)
	if err != nil {
		return 0, err
	}
	if err := ops.removeChromeRuntimeArtifacts(tmpRoot); err != nil {
		return 0, err
	}
	if err := ops.chmod(tmpRoot, 0o700); err != nil {
		return 0, fmt.Errorf("secure managed profile copy: %w", err)
	}
	backupRoot := ""
	if _, err := os.Stat(dstRoot); err == nil {
		backupRoot, err = managedProfileBackupPath(filepath.Dir(dstRoot), filepath.Base(dstRoot))
		if err != nil {
			return 0, err
		}
		if err := ops.rename(dstRoot, backupRoot); err != nil {
			return 0, fmt.Errorf("stage old managed profile backup: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("stat old managed profile: %w", err)
	}
	if err := ops.rename(tmpRoot, dstRoot); err != nil {
		if rollbackErr := rollbackManagedProfileBackup(dstRoot, backupRoot, ops); rollbackErr != nil {
			return 0, fmt.Errorf("install managed profile copy: %w; rollback old managed profile: %v", err, rollbackErr)
		}
		return 0, fmt.Errorf("install managed profile copy: %w", err)
	}
	complete = true
	if backupRoot != "" {
		_ = ops.removeAll(backupRoot)
	}
	return copied, nil
}

func managedProfileBackupPath(parent, base string) (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		path := filepath.Join(parent, fmt.Sprintf(".%s-backup-%d-%d-%d", base, os.Getpid(), time.Now().UnixNano(), attempt))
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return path, nil
		} else if err != nil {
			return "", fmt.Errorf("stat managed profile backup path: %w", err)
		}
	}
	return "", fmt.Errorf("allocate managed profile backup path")
}

func rollbackManagedProfileBackup(dstRoot, backupRoot string, ops profileReplaceOps) error {
	if backupRoot == "" {
		return nil
	}
	if _, err := os.Stat(dstRoot); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return ops.rename(backupRoot, dstRoot)
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

func ClearManagedRuntimeArtifacts(stateDir string) error {
	profileDir := filepath.Clean(ManagedProfileDir(stateDir))
	if profileDir == "." || strings.TrimSpace(stateDir) == "" {
		return fmt.Errorf("managed state directory is required")
	}
	if err := removeChromeRuntimeArtifacts(profileDir); err != nil {
		return fmt.Errorf("clear managed Chrome runtime artifacts: %w", err)
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
	if err := atomicWriteManagedFile(ManagedMetadataPath(stateDir), data, 0o600); err != nil {
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

func SaveManagedProcessRegistry(stateDir string, registry ManagedProcessRegistry) error {
	if registry.Version <= 0 {
		registry.Version = 1
	}
	if strings.TrimSpace(registry.BrowserMode) == "" {
		registry.BrowserMode = "headless"
	}
	sortManagedProcessRecords(registry.Records)
	if err := os.MkdirAll(filepath.Dir(ManagedProcessRegistryPath(stateDir)), 0o700); err != nil {
		return fmt.Errorf("create managed process registry directory: %w", err)
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal managed process registry: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWriteManagedFile(ManagedProcessRegistryPath(stateDir), data, 0o600); err != nil {
		return fmt.Errorf("write managed process registry: %w", err)
	}
	return nil
}

func atomicWriteManagedFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".managed-state-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	removeTemp = false
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func LoadManagedProcessRegistry(stateDir string) (ManagedProcessRegistry, bool, error) {
	data, err := os.ReadFile(ManagedProcessRegistryPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return ManagedProcessRegistry{}, false, nil
		}
		return ManagedProcessRegistry{}, false, fmt.Errorf("read managed process registry: %w", err)
	}
	var registry ManagedProcessRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return ManagedProcessRegistry{}, false, fmt.Errorf("parse managed process registry: %w", err)
	}
	if registry.Version <= 0 {
		registry.Version = 1
	}
	if strings.TrimSpace(registry.BrowserMode) == "" {
		registry.BrowserMode = "headless"
	}
	return registry, true, nil
}

func VerifyManagedOwnership(stateDir string, expected ManagedStatus) ManagedOwnershipEvidence {
	evidence := ManagedOwnershipEvidence{
		Checked:       true,
		PID:           expected.ChromePID,
		DebuggingPort: strings.TrimSpace(expected.DebuggingPort),
		UserDataDir:   strings.TrimSpace(expected.UserDataDir),
	}
	metadata, ok, err := LoadManagedMetadata(stateDir)
	if err != nil {
		evidence.Reasons = append(evidence.Reasons, "managed_metadata_unreadable")
		return evidence
	}
	if !ok {
		evidence.Reasons = append(evidence.Reasons, "managed_metadata_missing")
		return evidence
	}
	managedProfile := cleanPath(ManagedProfileDir(stateDir))
	if metadata.BrowserMode != "headless" {
		evidence.Reasons = append(evidence.Reasons, "browser_mode_not_headless")
	}
	if cleanPath(metadata.UserDataDir) != managedProfile {
		evidence.Reasons = append(evidence.Reasons, "managed_profile_path_mismatch")
	} else {
		evidence.SafetyChecks = append(evidence.SafetyChecks, "managed_profile_path_matches_state_dir")
	}
	if strings.TrimSpace(metadata.OwnedMarker) == "" {
		evidence.Reasons = append(evidence.Reasons, "ownership_token_missing")
	} else {
		evidence.SafetyChecks = append(evidence.SafetyChecks, "ownership_token_present")
	}
	if strings.TrimSpace(metadata.ProcessStartTime) == "" {
		evidence.Reasons = append(evidence.Reasons, "process_start_time_missing")
	} else {
		evidence.SafetyChecks = append(evidence.SafetyChecks, "process_start_time_present")
	}
	if metadata.ChromePID <= 0 {
		evidence.Reasons = append(evidence.Reasons, "chrome_pid_missing")
	} else {
		evidence.PID = metadata.ChromePID
	}
	if strings.TrimSpace(metadata.DebuggingPort) == "" {
		evidence.Reasons = append(evidence.Reasons, "debugging_port_missing")
	} else {
		evidence.DebuggingPort = metadata.DebuggingPort
	}
	if expected.ChromePID > 0 && metadata.ChromePID != expected.ChromePID {
		evidence.Reasons = append(evidence.Reasons, "runtime_pid_mismatch")
	}
	if expected.UserDataDir != "" && cleanPath(expected.UserDataDir) != cleanPath(metadata.UserDataDir) {
		evidence.Reasons = append(evidence.Reasons, "runtime_profile_mismatch")
	}
	if expected.DebuggingPort != "" && expected.DebuggingPort != metadata.DebuggingPort {
		evidence.Reasons = append(evidence.Reasons, "runtime_debugging_port_mismatch")
	}

	registry, registryOK, err := LoadManagedProcessRegistry(stateDir)
	if err != nil {
		evidence.Reasons = append(evidence.Reasons, "managed_registry_unreadable")
		return evidence
	}
	if !registryOK {
		evidence.Reasons = append(evidence.Reasons, "managed_registry_missing")
		return evidence
	}
	recordMatched := false
	for _, record := range registry.Records {
		if record.PID != metadata.ChromePID || record.BrowserMode != "headless" || cleanPath(record.UserDataDir) != managedProfile {
			continue
		}
		if record.OwnershipMarker != metadata.OwnedMarker || record.ProcessStartTime != metadata.ProcessStartTime {
			continue
		}
		if record.DebuggingPort != "" && metadata.DebuggingPort != "" && record.DebuggingPort != metadata.DebuggingPort {
			continue
		}
		recordMatched = true
		break
	}
	if !recordMatched {
		evidence.Reasons = append(evidence.Reasons, "managed_registry_record_mismatch")
	} else {
		evidence.SafetyChecks = append(evidence.SafetyChecks, "managed_registry_record_matches")
	}
	evidence.Owned = len(evidence.Reasons) == 0
	return evidence
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

func ManagedProcessStatus(record ManagedProcessRecord) ManagedProcessRecordStatus {
	return ManagedProcessRecordStatus{
		PID:                 record.PID,
		BrowserMode:         record.BrowserMode,
		UserDataDir:         record.UserDataDir,
		DebuggingPort:       record.DebuggingPort,
		StartedAt:           record.StartedAt,
		LastSeenAt:          record.LastSeenAt,
		ExitedAt:            record.ExitedAt,
		State:               record.State,
		CleanupAt:           record.CleanupAt,
		CleanupReason:       record.CleanupReason,
		ProfileSeedStrategy: record.ProfileSeedStrategy,
	}
}

func ManagedProcessStatuses(records []ManagedProcessRecord) []ManagedProcessRecordStatus {
	statuses := make([]ManagedProcessRecordStatus, 0, len(records))
	for _, record := range records {
		statuses = append(statuses, ManagedProcessStatus(record))
	}
	return statuses
}

func RegisterManagedProcessLaunch(stateDir string, metadata ManagedMetadata) error {
	if metadata.ChromePID <= 0 {
		return fmt.Errorf("register managed process launch: missing Chrome PID")
	}
	registry, ok, err := LoadManagedProcessRegistry(stateDir)
	if err != nil {
		return err
	}
	if !ok {
		registry = ManagedProcessRegistry{Version: 1, BrowserMode: "headless"}
	}
	registry.Records = upsertManagedProcessRecord(registry.Records, managedProcessRecordFromMetadata(metadata, "live"))
	return SaveManagedProcessRegistry(stateDir, registry)
}

func ReconcileManagedProcesses(ctx context.Context, stateDir string, opts ManagedProcessReconcileOptions) (ManagedProcessReconcileResult, error) {
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	nowText := now.Format(time.RFC3339)
	managedProfile := filepath.Clean(ManagedProfileDir(stateDir))
	result := ManagedProcessReconcileResult{
		Checked:      true,
		State:        "healthy",
		BrowserMode:  "headless",
		SafetyChecks: []string{"browser_mode=headless", "managed_profile_path_matches_state_dir"},
	}

	registry, ok, err := LoadManagedProcessRegistry(stateDir)
	if err != nil {
		result.State = "error"
		result.Reason = err.Error()
		return result, err
	}
	if !ok {
		registry = ManagedProcessRegistry{Version: 1, BrowserMode: "headless"}
	}
	metadata, metadataOK, err := LoadManagedMetadata(stateDir)
	if err != nil {
		result.State = "error"
		result.Reason = err.Error()
		return result, err
	}
	if metadataOK && metadata.ChromePID > 0 && metadata.BrowserMode == "headless" && cleanPath(metadata.UserDataDir) == managedProfile {
		registry.Records = upsertManagedProcessRecord(registry.Records, managedProcessRecordFromMetadata(metadata, "metadata"))
		if opts.ActivePID <= 0 {
			opts.ActivePID = metadata.ChromePID
		}
	}
	result.ActivePID = opts.ActivePID

	lister := opts.ProcessLister
	if lister == nil {
		lister = managedChromeProcesses
	}
	livePIDs, err := lister(ctx, managedProfile)
	if err != nil {
		result.State = "error"
		result.Reason = err.Error()
		return result, err
	}
	livePIDs = uniqueSortedPIDs(livePIDs)
	liveSet := map[int]bool{}
	for _, pid := range livePIDs {
		liveSet[pid] = true
	}
	if len(livePIDs) > 0 {
		result.SafetyChecks = append(result.SafetyChecks, "process_command_line_matches_managed_profile")
	}

	for _, pid := range livePIDs {
		if findManagedProcessRecord(registry.Records, pid) >= 0 {
			continue
		}
		registry.Records = upsertManagedProcessRecord(registry.Records, ManagedProcessRecord{
			PID:         pid,
			BrowserMode: "headless",
			UserDataDir: managedProfile,
			LastSeenAt:  nowText,
			State:       "live_unregistered",
		})
	}

	for i := range registry.Records {
		record := &registry.Records[i]
		if record.PID <= 0 || record.BrowserMode != "headless" || cleanPath(record.UserDataDir) != managedProfile {
			continue
		}
		if liveSet[record.PID] {
			record.State = "live"
			record.LastSeenAt = nowText
			record.ExitedAt = ""
			continue
		}
		if record.State != "reaped" && record.State != "signal_failed" {
			record.State = "exited"
			if record.ExitedAt == "" {
				record.ExitedAt = nowText
			}
		}
	}

	if opts.ReapExtras && len(livePIDs) > 1 {
		retainPID := opts.ActivePID
		if retainPID <= 0 || !liveSet[retainPID] {
			retainPID = managedProcessPIDToRetain(registry.Records, liveSet)
			if retainPID > 0 {
				result.ActivePID = retainPID
				result.Reason = "retained one live managed Chrome because no active runtime PID was supplied"
			}
		}
		if retainPID > 0 {
			if opts.Signal == nil {
				opts.Signal = signalProcess
			}
			for _, pid := range livePIDs {
				if pid == retainPID {
					continue
				}
				if err := opts.Signal(pid); err != nil {
					result.SignalFailures = append(result.SignalFailures, ManagedProcessSignalFailure{PID: pid, Error: err.Error()})
					result.SkippedPIDs = append(result.SkippedPIDs, pid)
					markManagedProcessRecordCleanup(registry.Records, pid, "signal_failed", nowText, err.Error())
					continue
				}
				delete(liveSet, pid)
				result.ReapedPIDs = append(result.ReapedPIDs, pid)
				markManagedProcessRecordCleanup(registry.Records, pid, "reaped", nowText, "duplicate cdp-owned managed headless Chrome process")
			}
		}
	}

	result.LiveCount = 0
	result.StaleCount = 0
	for _, record := range registry.Records {
		if record.PID <= 0 || record.BrowserMode != "headless" || cleanPath(record.UserDataDir) != managedProfile {
			continue
		}
		if liveSet[record.PID] {
			result.LiveCount++
			continue
		}
		result.StaleCount++
	}
	result.RegisteredCount = len(registry.Records)
	result.ReapedCount = len(result.ReapedPIDs)
	sort.Ints(result.ReapedPIDs)
	sort.Ints(result.SkippedPIDs)
	sortManagedProcessRecords(registry.Records)
	result.Records = ManagedProcessStatuses(registry.Records)

	switch {
	case len(result.SignalFailures) > 0:
		result.State = "degraded"
		result.NextCommands = []string{"cdp --browser-mode headless daemon stop --force-managed --json", "cdp --browser-mode headless daemon keepalive --repair --force --json"}
	case result.LiveCount > 1:
		result.State = "over_budget"
		result.Reason = "more than one cdp-owned managed headless Chrome process is live for this state directory"
		result.NextCommands = []string{"cdp --browser-mode headless daemon keepalive --managed-process-sweep --repair --force --json"}
	case result.ReapedCount > 0:
		result.State = "reaped"
	default:
		result.State = "healthy"
	}
	if !opts.ReadOnly {
		if err := SaveManagedProcessRegistry(stateDir, registry); err != nil {
			result.State = "error"
			result.Reason = err.Error()
			return result, err
		}
	}
	return result, nil
}

func managedProcessRecordFromMetadata(metadata ManagedMetadata, state string) ManagedProcessRecord {
	return ManagedProcessRecord{
		PID:                 metadata.ChromePID,
		BrowserMode:         metadata.BrowserMode,
		UserDataDir:         metadata.UserDataDir,
		DebuggingPort:       metadata.DebuggingPort,
		StartedAt:           metadata.StartedAt,
		LastSeenAt:          metadata.ProcessStartTime,
		State:               state,
		OwnershipMarker:     metadata.OwnedMarker,
		ProcessStartTime:    metadata.ProcessStartTime,
		ProfileSeedStrategy: metadata.ProfileSeedStrategy,
	}
}

func upsertManagedProcessRecord(records []ManagedProcessRecord, record ManagedProcessRecord) []ManagedProcessRecord {
	if record.PID <= 0 {
		return records
	}
	if strings.TrimSpace(record.BrowserMode) == "" {
		record.BrowserMode = "headless"
	}
	idx := findManagedProcessRecord(records, record.PID)
	if idx < 0 {
		return append(records, record)
	}
	existing := records[idx]
	if strings.TrimSpace(record.BrowserMode) == "" {
		record.BrowserMode = existing.BrowserMode
	}
	if strings.TrimSpace(record.UserDataDir) == "" {
		record.UserDataDir = existing.UserDataDir
	}
	if strings.TrimSpace(record.DebuggingPort) == "" {
		record.DebuggingPort = existing.DebuggingPort
	}
	if strings.TrimSpace(record.StartedAt) == "" {
		record.StartedAt = existing.StartedAt
	}
	if strings.TrimSpace(record.LastSeenAt) == "" {
		record.LastSeenAt = existing.LastSeenAt
	}
	if strings.TrimSpace(record.ExitedAt) == "" {
		record.ExitedAt = existing.ExitedAt
	}
	if strings.TrimSpace(record.State) == "" {
		record.State = existing.State
	}
	if strings.TrimSpace(record.CleanupAt) == "" {
		record.CleanupAt = existing.CleanupAt
	}
	if strings.TrimSpace(record.CleanupReason) == "" {
		record.CleanupReason = existing.CleanupReason
	}
	if strings.TrimSpace(record.OwnershipMarker) == "" {
		record.OwnershipMarker = existing.OwnershipMarker
	}
	if strings.TrimSpace(record.ProcessStartTime) == "" {
		record.ProcessStartTime = existing.ProcessStartTime
	}
	if strings.TrimSpace(record.ProfileSeedStrategy) == "" {
		record.ProfileSeedStrategy = existing.ProfileSeedStrategy
	}
	records[idx] = record
	return records
}

func findManagedProcessRecord(records []ManagedProcessRecord, pid int) int {
	for i, record := range records {
		if record.PID == pid {
			return i
		}
	}
	return -1
}

func markManagedProcessRecordCleanup(records []ManagedProcessRecord, pid int, state, at, reason string) {
	idx := findManagedProcessRecord(records, pid)
	if idx < 0 {
		return
	}
	records[idx].State = state
	records[idx].CleanupAt = at
	records[idx].CleanupReason = reason
	if state == "reaped" {
		records[idx].ExitedAt = at
	}
}

func markManagedProcessesStopped(stateDir string, pids []int, state, reason string) {
	if len(pids) == 0 {
		return
	}
	registry, ok, err := LoadManagedProcessRegistry(stateDir)
	if err != nil || !ok {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, pid := range pids {
		markManagedProcessRecordCleanup(registry.Records, pid, state, now, reason)
	}
	_ = SaveManagedProcessRegistry(stateDir, registry)
}

func managedProcessPIDToRetain(records []ManagedProcessRecord, liveSet map[int]bool) int {
	var best ManagedProcessRecord
	for _, record := range records {
		if !liveSet[record.PID] {
			continue
		}
		if best.PID == 0 {
			best = record
			continue
		}
		if managedProcessRecordNewer(record, best) {
			best = record
		}
	}
	if best.PID > 0 {
		return best.PID
	}
	for pid := range liveSet {
		if best.PID == 0 || pid > best.PID {
			best.PID = pid
		}
	}
	return best.PID
}

func managedProcessRecordNewer(a, b ManagedProcessRecord) bool {
	aTime := managedProcessRecordTime(a)
	bTime := managedProcessRecordTime(b)
	if !aTime.IsZero() || !bTime.IsZero() {
		return aTime.After(bTime)
	}
	return a.PID > b.PID
}

func managedProcessRecordTime(record ManagedProcessRecord) time.Time {
	for _, value := range []string{record.ProcessStartTime, record.StartedAt, record.LastSeenAt} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func sortManagedProcessRecords(records []ManagedProcessRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].PID < records[j].PID
	})
}

func uniqueSortedPIDs(pids []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, pid := range pids {
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
	}
	sort.Ints(out)
	return out
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func StopOwnedManagedChrome(ctx context.Context, stateDir string, signal func(int) error) (ManagedStopResult, error) {
	return StopManagedChrome(ctx, stateDir, ManagedStopOptions{Signal: signal})
}

func StopManagedChrome(ctx context.Context, stateDir string, opts ManagedStopOptions) (ManagedStopResult, error) {
	metadata, ok, err := LoadManagedMetadata(stateDir)
	if err != nil {
		return ManagedStopResult{Checked: true, Force: opts.Force, Skipped: true, Reason: "managed metadata unavailable"}, err
	}
	if !ok {
		result := ManagedStopResult{Checked: true, Force: opts.Force}
		if !opts.Force {
			result.Skipped = true
			result.Reason = "managed metadata missing"
			return result, nil
		}
		metadata = ManagedMetadata{
			BrowserMode:         "headless",
			UserDataDir:         ManagedProfileDir(stateDir),
			ProfileSeedStrategy: ProfileSeedStrategyManaged,
		}
		pids, checks, processEvidence, err := forceManagedStopCandidates(ctx, stateDir, metadata)
		result.PIDs = pids
		result.SafetyChecks = checks
		result.ProcessEvidence = processEvidence
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
	result := ManagedStopResult{Checked: true, Force: opts.Force, Browser: ManagedMetadataStatus(metadata)}
	incompleteOwnership := metadata.BrowserMode != "headless" || metadata.ChromePID <= 0 || strings.TrimSpace(metadata.OwnedMarker) == "" || strings.TrimSpace(metadata.ProcessStartTime) == ""
	if incompleteOwnership || opts.Force {
		if !opts.Force {
			result.Skipped = true
			result.Reason = "managed ownership metadata incomplete"
			return result, nil
		}
		pids, checks, processEvidence, err := forceManagedStopCandidates(ctx, stateDir, metadata)
		result.PIDs = pids
		result.SafetyChecks = checks
		result.ProcessEvidence = processEvidence
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
		markManagedProcessesStopped(stateDir, pids, "stopped", result.Reason)
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
	markManagedProcessesStopped(stateDir, result.PIDs, "stopped", "owned managed headless cleanup")
	return result, nil
}

func forceManagedStopCandidates(ctx context.Context, stateDir string, metadata ManagedMetadata) ([]int, []string, []ManagedProcessEvidence, error) {
	managedProfile := filepath.Clean(ManagedProfileDir(stateDir))
	userDataDir := filepath.Clean(strings.TrimSpace(metadata.UserDataDir))
	if userDataDir == "." || userDataDir == "" {
		userDataDir = managedProfile
	}
	checks := []string{"force_requested", "browser_mode=headless"}
	if metadata.BrowserMode != "headless" {
		return nil, checks, nil, nil
	}
	if userDataDir != managedProfile {
		checks = append(checks, "managed_profile_path_mismatch")
		return nil, checks, nil, nil
	}
	checks = append(checks, "managed_profile_path_matches_state_dir")
	knownPort := strings.TrimSpace(metadata.DebuggingPort)
	if port, _, err := ReadActivePortFile(managedProfile); err == nil {
		if knownPort == "" || knownPort == port {
			knownPort = port
			checks = append(checks, "devtools_active_port_read")
		}
	}
	pids, evidence, err := managedChromeProcessTreeEvidence(ctx, managedProfile, knownPort)
	if err != nil {
		return pids, checks, evidence, err
	}
	if len(evidence) > 0 {
		checks = append(checks, "process_command_line_matches_managed_profile")
	}
	if knownPort != "" {
		checks = append(checks, "debugging_port_evidence_checked")
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		checks = append(checks, "process_tree_inspected")
	} else {
		checks = append(checks, "process_tree_evidence_unavailable")
	}
	return pids, checks, evidence, nil
}

type managedProcessSnapshot struct {
	PID         int
	ParentPID   int
	CommandLine string
}

func managedChromeProcessTreeEvidence(ctx context.Context, managedProfile, knownPort string) ([]int, []ManagedProcessEvidence, error) {
	snapshots, err := managedProcessSnapshots(ctx)
	if err != nil {
		return nil, nil, err
	}
	children := map[int][]managedProcessSnapshot{}
	var roots []managedProcessSnapshot
	for _, snapshot := range snapshots {
		children[snapshot.ParentPID] = append(children[snapshot.ParentPID], snapshot)
		matched, portMatch, _ := managedChromeCommandLineEvidence(snapshot.CommandLine, managedProfile, knownPort)
		if matched && portMatch {
			roots = append(roots, snapshot)
		}
	}
	seen := map[int]bool{}
	var evidence []ManagedProcessEvidence
	var visit func(managedProcessSnapshot, int, string)
	visit = func(snapshot managedProcessSnapshot, rootPID int, role string) {
		if snapshot.PID <= 0 || seen[snapshot.PID] {
			return
		}
		seen[snapshot.PID] = true
		matched, portMatch, port := managedChromeCommandLineEvidence(snapshot.CommandLine, managedProfile, knownPort)
		if role == "descendant" {
			matched = true
			portMatch = knownPort != ""
			port = knownPort
		}
		evidence = append(evidence, ManagedProcessEvidence{
			PID:                snapshot.PID,
			ParentPID:          snapshot.ParentPID,
			RootPID:            rootPID,
			Role:               role,
			ProfileMatched:     matched,
			DebuggingPort:      port,
			DebuggingPortMatch: portMatch,
		})
		for _, child := range children[snapshot.PID] {
			visit(child, rootPID, "descendant")
		}
	}
	for _, root := range roots {
		visit(root, root.PID, "root")
	}
	pids := make([]int, 0, len(evidence))
	for _, item := range evidence {
		pids = append(pids, item.PID)
	}
	return uniquePIDsPreserveOrder(pids), evidence, nil
}

func managedProcessSnapshots(ctx context.Context) ([]managedProcessSnapshot, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,command=")
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	var snapshots []managedProcessSnapshot
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parentPID, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil || pid == os.Getpid() {
			continue
		}
		commandLine := strings.Join(fields[2:], " ")
		snapshots = append(snapshots, managedProcessSnapshot{PID: pid, ParentPID: parentPID, CommandLine: commandLine})
	}
	return snapshots, nil
}

func managedChromeCommandLineEvidence(cmdline, managedProfile, knownPort string) (bool, bool, string) {
	if !managedChromeCommandLine(cmdline, managedProfile) {
		return false, false, ""
	}
	port := commandLineFlagValue(cmdline, "--remote-debugging-port")
	if knownPort == "" {
		return true, port != "", port
	}
	return true, port == knownPort || port == "0", knownPort
}

func commandLineFlagValue(cmdline, name string) string {
	fields := strings.Fields(cmdline)
	for i, field := range fields {
		if strings.HasPrefix(field, name+"=") {
			return strings.TrimPrefix(field, name+"=")
		}
		if field == name && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

func uniquePIDsPreserveOrder(pids []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(pids))
	for _, pid := range pids {
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
	}
	return out
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
	if errors.Is(interruptErr, os.ErrProcessDone) || errors.Is(interruptErr, syscall.ESRCH) {
		return nil
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if killErr := process.Kill(); killErr != nil {
		if errors.Is(killErr, os.ErrProcessDone) || errors.Is(killErr, syscall.ESRCH) {
			return nil
		}
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
	reconcile, err := ReconcileManagedProcesses(ctx, opts.StateDir, ManagedProcessReconcileOptions{ReapExtras: true})
	if err != nil {
		return ManagedLaunch{}, err
	}
	if reconcile.LiveCount > 0 {
		return ManagedLaunch{}, fmt.Errorf("managed headless Chrome already running for this state directory; reconcile state %s has %d live process(es)", reconcile.State, reconcile.LiveCount)
	}
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
	if err := SaveManagedMetadata(opts.StateDir, metadata); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ManagedLaunch{}, err
	}
	if err := RegisterManagedProcessLaunch(opts.StateDir, metadata); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ManagedLaunch{}, err
	}

	port, path, err := WaitManagedActivePort(ctx, metadata.UserDataDir)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		markManagedProcessesStopped(opts.StateDir, []int{metadata.ChromePID}, "launch_failed", err.Error())
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
	if err := RegisterManagedProcessLaunch(opts.StateDir, metadata); err != nil {
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
