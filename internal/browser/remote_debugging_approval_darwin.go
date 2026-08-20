//go:build darwin

package browser

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type nativeRemoteDebuggingApprovalResult = NativeRemoteDebuggingApprovalResult

const remoteDebuggingApprovalScript = `on run argv
  set browserProcessName to item 1 of argv
  tell application "System Events"
    -- Chrome may have both a cdp-owned headless process and one or more
    -- headed processes. Select only processes that actually own windows.
    set windowsScanned to 0
    set frontmostSet to false
    repeat with candidate in (every process whose name is browserProcessName)
      try
        set candidateWindowCount to count of windows of candidate
        if candidateWindowCount is greater than 0 then
          set windowsScanned to windowsScanned + candidateWindowCount
          if frontmostSet is false then
            tell candidate
              set frontmost to true
            end tell
            set frontmostSet to true
          end if
        end if
      end try
    end repeat

    set promptCountBefore to 0
    set waitDeadline to (current date) + 12
    repeat while (current date) is less than waitDeadline
      set promptCountBefore to 0
      repeat with candidate in (every process whose name is browserProcessName)
        try
          if (count of windows of candidate) is greater than 0 then
            tell candidate
              repeat with windowRef in windows
                try
                  if exists sheet 1 of windowRef then
                    if (name of sheet 1 of windowRef as text) is "Allow remote debugging?" then set promptCountBefore to promptCountBefore + 1
                  end if
                end try
              end repeat
            end tell
          end if
        end try
      end repeat
      if promptCountBefore is greater than 0 then exit repeat
      delay 0.2
    end repeat
    set approvedCount to 0

    -- A connection can enqueue another native sheet immediately after an
    -- approval. Drain the bounded queue across every Chrome window instead
    -- of reporting success after the first accessibility action.
    repeat with passNumber from 1 to 20
      set approvedThisPass to 0
      repeat with candidate in (every process whose name is browserProcessName)
        try
          if (count of windows of candidate) is greater than 0 then
            tell candidate
              repeat with windowRef in windows
                try
                  if exists sheet 1 of windowRef then
                    if (name of sheet 1 of windowRef as text) is "Allow remote debugging?" then
                      -- Query the nested native button by its exact
                      -- accessibility description. System Events cannot
                      -- reliably coerce Chrome's entire-contents references
                      -- to role/description values on recent macOS releases.
                      click first button of group 1 of group 2 of group 1 of group 1 of group 1 of sheet 1 of windowRef whose description is "Allow"
                      set approvedThisPass to approvedThisPass + 1
                      set approvedCount to approvedCount + 1
                    end if
                  end if
                end try
              end repeat
            end tell
          end if
        end try
      end repeat
      if approvedThisPass is 0 then exit repeat
      delay 0.2
    end repeat

    set promptCountAfter to 0
    repeat with candidate in (every process whose name is browserProcessName)
      try
        if (count of windows of candidate) is greater than 0 then
          tell candidate
            repeat with windowRef in windows
              try
                if exists sheet 1 of windowRef then
                  if (name of sheet 1 of windowRef as text) is "Allow remote debugging?" then set promptCountAfter to promptCountAfter + 1
                end if
              end try
            end repeat
          end tell
        end if
      end try
    end repeat
    return "windows=" & windowsScanned & tab & "before=" & promptCountBefore & tab & "approved=" & approvedCount & tab & "after=" & promptCountAfter
  end tell
end run
`

// The script is kept as a literal and passed on stdin. There is no shell
// interpolation and the browser process name is selected from a whitelist.
func drainRemoteDebuggingApprovalQueue(ctx context.Context, channel string) (RemoteDebuggingApprovalResult, error) {
	processName, ok := chromeApplicationName(channel)
	if !ok {
		return unsupportedRemoteDebuggingApproval(channel), nil
	}

	result := RemoteDebuggingApprovalResult{
		Supported:          true,
		BrowserApplication: processName,
		ApprovalURL:        RemoteDebuggingApprovalURL,
		Action:             "scan",
		Message:            "scanning all Chrome windows for queued remote-debugging approvals",
	}
	if native, available, nativeErr := drainRemoteDebuggingApprovalQueueNative(ctx, processName); available {
		result.Platform = "darwin"
		result.Adapter = "macos-accessibility"
		if nativeErr != nil {
			result.Action = "failed"
			result.Message = "could not inspect Chrome's remote-debugging approval queue"
			result.Detail = nativeErr.Error()
			return result, nil
		}
		result.WindowsScanned = native.WindowsScanned
		result.PromptCountBefore = native.PromptCountBefore
		result.ApprovedCount = native.ApprovedCount
		result.PromptCountAfter = native.PromptCountAfter
		if result.PromptCountAfter == 0 {
			result.QueueDrained = true
			if result.ApprovedCount > 0 {
				result.Action = "approved"
				result.Message = "drained Chrome remote-debugging approvals across all scanned windows"
			} else {
				result.Action = "already_clear"
				result.Message = "no Chrome remote-debugging approval sheets were queued"
			}
		} else {
			result.Action = "queue_remaining"
			result.Message = "Chrome still has remote-debugging approval sheets after the bounded drain"
		}
		return result, nil
	}

	// The script needs to observe a prompt created by a concurrent active CDP
	// probe. Keep the wait bounded so a broken browser cannot stall keepalive.
	output, err := runSystemEventsScript(ctx, remoteDebuggingApprovalScript, processName, 15*time.Second)
	if err != nil {
		result.Action = "failed"
		result.Message = "could not inspect Chrome's remote-debugging approval queue"
		result.Detail = strings.TrimSpace(string(output))
		if result.Detail == "" {
			result.Detail = err.Error()
		}
		return result, nil
	}

	parsed, err := parseRemoteDebuggingApprovalReport(string(output))
	if err != nil {
		result.Action = "failed"
		result.Message = "Chrome approval queue inspection returned an invalid report"
		result.Detail = err.Error()
		return result, nil
	}
	parsed.Supported = true
	parsed.Platform = "darwin"
	parsed.Adapter = "macos-system-events"
	parsed.BrowserApplication = processName
	parsed.ApprovalURL = RemoteDebuggingApprovalURL
	if parsed.PromptCountAfter == 0 {
		parsed.QueueDrained = true
		if parsed.ApprovedCount > 0 {
			parsed.Action = "approved"
			parsed.Message = "drained Chrome remote-debugging approvals across all scanned windows"
		} else {
			parsed.Action = "already_clear"
			parsed.Message = "no Chrome remote-debugging approval sheets were queued"
		}
	} else {
		parsed.QueueDrained = false
		parsed.Action = "queue_remaining"
		parsed.Message = "Chrome still has remote-debugging approval sheets after the bounded drain"
	}
	return parsed, nil
}

func chromeApplicationName(channel string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "", "stable":
		return "Google Chrome", true
	case "beta":
		return "Google Chrome Beta", true
	case "canary":
		return "Google Chrome Canary", true
	case "dev":
		return "Google Chrome Dev", true
	default:
		return "", false
	}
}

func parseRemoteDebuggingApprovalReport(report string) (RemoteDebuggingApprovalResult, error) {
	result := RemoteDebuggingApprovalResult{}
	seenCounter := false
	for _, field := range strings.Split(strings.TrimSpace(report), "\t") {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value, err := strconv.Atoi(parts[1])
		if err != nil {
			return RemoteDebuggingApprovalResult{}, fmt.Errorf("parse %s: %w", parts[0], err)
		}
		switch parts[0] {
		case "windows":
			result.WindowsScanned = value
			seenCounter = true
		case "before":
			result.PromptCountBefore = value
			seenCounter = true
		case "approved":
			result.ApprovedCount = value
			seenCounter = true
		case "after":
			result.PromptCountAfter = value
			seenCounter = true
		}
	}
	if !seenCounter {
		return RemoteDebuggingApprovalResult{}, fmt.Errorf("missing approval queue counters")
	}
	return result, nil
}
