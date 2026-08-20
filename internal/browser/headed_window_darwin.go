//go:build darwin

package browser

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// macOS's System Events Apple Events permission is not a reliable dependency
// for a background repair task. Keep the window boundary intentionally small:
// discover the headed Chrome process, activate it through Launch Services,
// and let the real CDP probe decide whether the browser is usable.
func ensureHeadedChromeWindow(ctx context.Context, channel string) (HeadedWindowResult, error) {
	processName, ok := chromeApplicationName(channel)
	if !ok {
		return unsupportedHeadedWindow(channel), nil
	}

	result := HeadedWindowResult{
		Supported:          true,
		Platform:           "darwin",
		Adapter:            "macos-launch-services",
		BrowserApplication: processName,
		Action:             "scan",
		Message:            "checking for a headed Chrome process",
	}
	before := headedChromeProcessCount(ctx, processName, channel)
	result.WindowsBefore = before
	if before == 0 {
		if err := exec.CommandContext(ctx, "open", "-n", "-a", processName, "--args", "--profile-directory=Default", "--new-window", RemoteDebuggingApprovalURL).Run(); err != nil {
			result.Action = "failed"
			result.Message = "could not launch a headed Chrome window"
			result.Detail = err.Error()
			return result, nil
		}
		result.Action = "created"
	} else {
		if err := exec.CommandContext(ctx, "open", "-a", processName).Run(); err != nil {
			result.Action = "failed"
			result.Message = "could not activate headed Chrome"
			result.Detail = err.Error()
			return result, nil
		}
		result.Action = "already_ready"
	}

	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}
	after := headedChromeProcessCount(ctx, processName, channel)
	result.WindowsAfter = after
	result.WindowReady = after > 0
	if result.WindowReady {
		if result.Action == "created" {
			result.Message = "launched a headed Chrome window"
		} else {
			result.Message = "headed Chrome is running and activated"
		}
	} else {
		result.Action = "missing"
		result.Message = "Chrome has no headed process after the bounded ensure attempt"
	}
	return result, nil
}

func headedChromeProcessCount(ctx context.Context, processName, channel string) int {
	userDataDir, err := defaultUserDataDir(channel)
	if err != nil {
		return 0
	}
	output, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, processName) && chromeCommandUsesDefaultProfile(line, userDataDir) {
			count++
		}
	}
	return count
}
