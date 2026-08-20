//go:build darwin

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// EnableRemoteDebuggingPreference updates Chrome's persisted opt-in before a
// headed launch when the default profile is not in use. This is the cheap,
// deterministic path; the native checkbox remains a fallback for an already
// running headed browser.
func EnableRemoteDebuggingPreference(ctx context.Context, channel string) (bool, error) {
	processName, ok := chromeApplicationName(channel)
	if !ok {
		return false, nil
	}
	userDataDir, err := defaultUserDataDir(channel)
	if err != nil {
		return false, err
	}
	if chromeDefaultProfileInUse(ctx, processName, userDataDir) {
		return false, nil
	}
	path := filepath.Join(userDataDir, "Local State")
	original, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read Chrome Local State: %w", err)
	}
	updated, changed, err := enableRemoteDebuggingInLocalState(original)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	// Do not replace a profile file if Chrome started while it was being
	// prepared. A later bounded repair can use the native checkbox fallback.
	if chromeDefaultProfileInUse(ctx, processName, userDataDir) {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat Chrome Local State: %w", err)
	}
	temporary, err := os.CreateTemp(userDataDir, ".Local State.cdp-*")
	if err != nil {
		return false, fmt.Errorf("create Chrome Local State temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("preserve Chrome Local State permissions: %w", err)
	}
	if _, err := temporary.Write(updated); err != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("write Chrome Local State: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("sync Chrome Local State: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close Chrome Local State: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, fmt.Errorf("replace Chrome Local State: %w", err)
	}
	return true, nil
}

func enableRemoteDebuggingInLocalState(data []byte) ([]byte, bool, error) {
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false, fmt.Errorf("parse Chrome Local State: %w", err)
	}
	devtools, ok := state["devtools"].(map[string]any)
	if !ok {
		devtools = map[string]any{}
		state["devtools"] = devtools
	}
	remoteDebugging, ok := devtools["remote_debugging"].(map[string]any)
	if !ok {
		remoteDebugging = map[string]any{}
		devtools["remote_debugging"] = remoteDebugging
	}
	if enabled, ok := remoteDebugging["user-enabled"].(bool); ok && enabled {
		return data, false, nil
	}
	remoteDebugging["user-enabled"] = true
	updated, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("marshal Chrome Local State: %w", err)
	}
	return append(updated, '\n'), true, nil
}

func chromeDefaultProfileInUse(ctx context.Context, processName, userDataDir string) bool {
	output, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=").Output()
	if err != nil {
		// Refuse a profile rewrite when process ownership cannot be checked.
		return true
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, processName) {
			continue
		}
		if chromeCommandUsesDefaultProfile(line, userDataDir) {
			return true
		}
	}
	return false
}

func chromeCommandUsesDefaultProfile(command, userDataDir string) bool {
	if strings.Contains(command, "chrome_crashpad_handler") {
		return false
	}
	if strings.Contains(command, "--user-data-dir="+userDataDir) {
		return true
	}
	for _, field := range strings.Fields(command) {
		if strings.HasPrefix(field, "--user-data-dir=") {
			return filepath.Clean(strings.TrimPrefix(field, "--user-data-dir=")) == filepath.Clean(userDataDir)
		}
	}
	// A Chrome process without an explicit profile uses the default profile.
	return true
}

// PrepareRemoteDebuggingApproval re-enables Chrome's explicit remote-
// debugging checkbox when the default profile has been revoked. The native
// approval sheet is still drained separately and is never treated as
// approved without a verified CDP probe.
func PrepareRemoteDebuggingApproval(ctx context.Context, channel string) (bool, error) {
	processName, ok := chromeApplicationName(channel)
	if !ok {
		return false, nil
	}

	if changed, err := EnableRemoteDebuggingPreference(ctx, channel); err != nil {
		return false, err
	} else if changed {
		return true, nil
	}

	enabled, known, err := chromeRemoteDebuggingEnabled(channel)
	if err != nil || !known || enabled {
		return false, err
	}

	clicked, err := enableRemoteDebuggingCheckbox(ctx, processName)
	if err != nil {
		return false, err
	}
	if clicked {
		if err := waitForRemoteDebuggingEnabled(ctx, channel); err == nil {
			return true, nil
		}
	}

	// Finding the control is not proof that the input event changed Chrome's
	// profile. If the first attempt was ignored by macOS input policy, bring
	// the exact inspect page forward and retry once before reporting failure.
	if err := openRemoteDebuggingApprovalPage(ctx, processName); err != nil {
		return false, fmt.Errorf("open Chrome remote-debugging approval page: %w", err)
	}
	if err := waitForRemoteDebuggingPage(ctx); err != nil {
		return false, err
	}
	clicked, err = enableRemoteDebuggingCheckbox(ctx, processName)
	if err != nil {
		return false, err
	}
	if !clicked {
		return false, fmt.Errorf("Chrome remote-debugging checkbox was not found in a headed window")
	}
	if err := waitForRemoteDebuggingEnabled(ctx, channel); err != nil {
		return false, err
	}
	return true, nil
}

func chromeRemoteDebuggingEnabled(channel string) (enabled, known bool, err error) {
	userDataDir, err := defaultUserDataDir(channel)
	if err != nil {
		return false, false, err
	}
	b, err := os.ReadFile(filepath.Join(userDataDir, "Local State"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	var state struct {
		Devtools struct {
			RemoteDebugging struct {
				UserEnabled *bool `json:"user-enabled"`
			} `json:"remote_debugging"`
		} `json:"devtools"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return false, false, fmt.Errorf("parse Chrome Local State: %w", err)
	}
	if state.Devtools.RemoteDebugging.UserEnabled == nil {
		return false, false, nil
	}
	return *state.Devtools.RemoteDebugging.UserEnabled, true, nil
}

func openRemoteDebuggingApprovalPage(ctx context.Context, processName string) error {
	return exec.CommandContext(ctx, "open", "-a", processName, RemoteDebuggingApprovalURL).Run()
}

func waitForRemoteDebuggingPage(ctx context.Context) error {
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitForRemoteDebuggingEnabled(ctx context.Context, channel string) error {
	// Chrome may persist the setting asynchronously while it tears down the
	// just-authorized DevTools connection. Keep this bounded below the repair
	// lease instead of treating a slow write as a failed click.
	for attempt := 0; attempt < 20; attempt++ {
		enabled, known, err := chromeRemoteDebuggingEnabled(channel)
		if err != nil {
			return err
		}
		if known && enabled {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("Chrome remote-debugging checkbox click did not enable the profile")
}

func parseChromeProcessIDs(output string) []int {
	var pids []int
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

func enableRemoteDebuggingCheckbox(ctx context.Context, processName string) (bool, error) {
	// AX can inspect Chrome while it is backgrounded, but Quartz input is
	// delivered to the active application. Bring the headed browser forward
	// before asking the native helper to click the exact checkbox.
	openErr := exec.CommandContext(ctx, "open", "-a", processName).Run()
	activateCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	activateScript := fmt.Sprintf("tell application %q to activate", processName)
	activateErr := exec.CommandContext(activateCtx, "osascript", "-e", activateScript).Run()
	cancel()
	if openErr != nil && activateErr != nil {
		return false, fmt.Errorf("activate headed Chrome: open: %v; AppleScript: %w", openErr, activateErr)
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(300 * time.Millisecond):
	}

	output, err := runRemoteDebuggingNativeHelper(ctx, "--internal-macos-remote-debugging-checkbox", "--process-name", processName)
	if err != nil {
		return false, err
	}
	var result struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return false, fmt.Errorf("parse macOS remote-debugging checkbox helper: %w", err)
	}
	return result.Enabled, nil
}

func drainRemoteDebuggingApprovalQueueNative(ctx context.Context, processName string) (nativeRemoteDebuggingApprovalResult, bool, error) {
	result := nativeRemoteDebuggingApprovalResult{}
	deadline := time.Now().Add(12 * time.Second)
	for {
		current, err := runRemoteDebuggingNativeApprovalHelper(ctx, processName, false)
		if err != nil {
			return nativeRemoteDebuggingApprovalResult{}, true, err
		}
		result.WindowsScanned = current.WindowsScanned
		result.PromptCountBefore = current.PromptCountBefore
		if result.PromptCountBefore > 0 || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nativeRemoteDebuggingApprovalResult{}, true, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}

	for pass := 0; pass < 40; pass++ {
		current, err := runRemoteDebuggingNativeApprovalHelper(ctx, processName, true)
		if err != nil {
			return nativeRemoteDebuggingApprovalResult{}, true, err
		}
		result.WindowsScanned = current.WindowsScanned
		result.ApprovedCount += current.ApprovedCount
		if current.ApprovedCount == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return nativeRemoteDebuggingApprovalResult{}, true, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}

	final, err := runRemoteDebuggingNativeApprovalHelper(ctx, processName, false)
	if err != nil {
		return nativeRemoteDebuggingApprovalResult{}, true, err
	}
	result.WindowsScanned = final.WindowsScanned
	result.PromptCountAfter = final.PromptCountBefore
	return result, true, nil
}

func runRemoteDebuggingNativeApprovalHelper(ctx context.Context, processName string, press bool) (NativeRemoteDebuggingApprovalResult, error) {
	args := []string{"--internal-macos-remote-debugging-approval", "--process-name", processName}
	if press {
		args = append(args, "--press")
	}
	output, err := runRemoteDebuggingNativeHelper(ctx, args...)
	if err != nil {
		return NativeRemoteDebuggingApprovalResult{}, err
	}
	var result NativeRemoteDebuggingApprovalResult
	if err := json.Unmarshal(output, &result); err != nil {
		return NativeRemoteDebuggingApprovalResult{}, fmt.Errorf("parse macOS remote-debugging approval helper: %w", err)
	}
	return result, nil
}

func runRemoteDebuggingNativeHelper(ctx context.Context, helperArgs ...string) ([]byte, error) {
	helperCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	helperPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve cdp executable for macOS accessibility helper: %w", err)
	}
	output, err := exec.CommandContext(helperCtx, helperPath, helperArgs...).CombinedOutput()
	if err != nil {
		if helperCtx.Err() != nil {
			return nil, fmt.Errorf("macOS accessibility helper timed out: %w", helperCtx.Err())
		}
		if detail := strings.TrimSpace(string(output)); detail != "" {
			return nil, fmt.Errorf("macOS accessibility helper failed: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("macOS accessibility helper failed: %w", err)
	}
	return output, nil
}
