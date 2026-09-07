//go:build darwin

package browser

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// macOS's System Events Apple Events permission is not a reliable dependency
// for a background repair task. Keep the window boundary intentionally small:
// discover the headed Chrome process, launch only when it is absent,
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
	before, err := headedChromeProcessCount(ctx, processName, channel)
	if err != nil {
		result.Action = "failed"
		result.Message = "could not inspect headed Chrome process table"
		result.Detail = err.Error()
		return result, nil
	}
	result.WindowsBefore = before
	if before > 0 {
		// Resolving Chrome by application name can select the headless process
		// sharing its bundle, promote it into the Dock, and steal URL delivery.
		result.WindowsAfter = before
		result.WindowReady = true
		result.Action = "already_ready"
		result.Message = "headed Chrome is running; activation was not needed"
		return result, nil
	}
	if _, err := runOwnedBrowserCommand(ctx, "open", "-n", "-a", processName, "--args", "--profile-directory=Default", "--new-window", RemoteDebuggingApprovalURL); err != nil {
		result.Action = "failed"
		result.Message = "could not launch a headed Chrome window"
		result.Detail = err.Error()
		return result, nil
	}
	result.Action = "created"

	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}
	after, err := headedChromeProcessCount(ctx, processName, channel)
	if err != nil {
		result.Action = "failed"
		result.Message = "could not inspect headed Chrome process table"
		result.Detail = err.Error()
		return result, nil
	}
	result.WindowsAfter = after
	result.WindowReady = after > 0
	if result.WindowReady {
		result.Message = "launched a headed Chrome window"
	} else {
		result.Action = "missing"
		result.Message = "Chrome has no headed process after the bounded ensure attempt"
	}
	return result, nil
}

func headedChromeProcessCount(ctx context.Context, processName, channel string) (int, error) {
	pids, err := headedChromeProcessIDs(ctx, processName, channel)
	return len(pids), err
}

func headedChromeProcessIDs(ctx context.Context, processName, channel string) ([]int, error) {
	userDataDir, err := defaultUserDataDir(channel)
	if err != nil {
		return nil, err
	}
	output, err := runManagedProcessTable(ctx, "-axo", "pid=,command=")
	if err != nil {
		return nil, fmt.Errorf("inspect headed Chrome process table: %w", err)
	}
	var pids []int
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		command := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
		if err != nil || pid <= 0 || strings.Contains(command, "--headless") || strings.Contains(command, "--type=") {
			continue
		}
		if strings.Contains(command, "--user-data-dir") && !commandLineContainsFlagValue(command, "--user-data-dir", userDataDir) {
			continue
		}
		// Match the browser executable, not helper names or wrapper arguments.
		executable := processName
		if i := strings.Index(command, ".app/Contents/MacOS/"+processName); i >= 0 {
			if strings.Contains(command[:i], " -") {
				continue
			}
			executable = command[:i] + ".app/Contents/MacOS/" + processName
		}
		if command == executable || strings.HasPrefix(command, executable+" ") {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
