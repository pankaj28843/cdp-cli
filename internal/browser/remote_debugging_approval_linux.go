//go:build linux

package browser

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// linuxRemoteDebuggingApprovalScript is embedded so an installed cdp binary
// does not depend on a companion file whose path can drift between desktops.
// The helper is deliberately narrow: it can inspect only the whitelisted
// Chrome application and can activate only an exact native "Allow" action.
//
//go:embed linux_remote_debugging_approval.py
var linuxRemoteDebuggingApprovalScript []byte

type linuxRemoteDebuggingApprovalReport struct {
	WindowsScanned    int `json:"windows_scanned"`
	PromptCountBefore int `json:"prompt_count_before"`
	ApprovedCount     int `json:"approved_count"`
	PromptCountAfter  int `json:"prompt_count_after"`
}

const linuxRemoteDebuggingApprovalLease = 15 * time.Second

// drainRemoteDebuggingApprovalQueue uses a bounded system Python AT-SPI
// helper. Chrome's Linux approval sheet is a native accessibility surface,
// not a DOM element, so a browser-page selector cannot safely approve it.
func drainRemoteDebuggingApprovalQueue(ctx context.Context, channel string) (RemoteDebuggingApprovalResult, error) {
	processNames, ok := chromeApplicationNames(channel)
	if !ok {
		return unsupportedRemoteDebuggingApproval(channel), nil
	}

	result := RemoteDebuggingApprovalResult{
		Supported:          true,
		Platform:           "linux",
		Adapter:            "linux-atspi",
		BrowserApplication: strings.Join(processNames, ", "),
		ApprovalURL:        RemoteDebuggingApprovalURL,
		Action:             "scan",
		Message:            "scanning all Chrome windows for queued remote-debugging approvals",
	}
	report, err := runLinuxRemoteDebuggingApprovalHelper(ctx, processNames)
	if err != nil {
		result.Action = "failed"
		result.Message = "could not inspect Chrome's remote-debugging approval queue"
		result.Detail = err.Error()
		return result, nil
	}
	result.WindowsScanned = report.WindowsScanned
	result.PromptCountBefore = report.PromptCountBefore
	result.ApprovedCount = report.ApprovedCount
	result.PromptCountAfter = report.PromptCountAfter
	if report.PromptCountAfter == 0 {
		result.QueueDrained = true
		if report.ApprovedCount > 0 {
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

func runLinuxRemoteDebuggingApprovalHelper(ctx context.Context, processNames []string) (linuxRemoteDebuggingApprovalReport, error) {
	python, err := linuxSystemPython()
	if err != nil {
		return linuxRemoteDebuggingApprovalReport{}, err
	}

	helperCtx, cancel := context.WithTimeout(ctx, linuxRemoteDebuggingApprovalLease)
	defer cancel()
	args := []string{"-c", string(linuxRemoteDebuggingApprovalScript)}
	for _, processName := range processNames {
		args = append(args, "--process-name", processName)
	}
	args = append(args, "--press")
	cmd := exec.CommandContext(helperCtx, python, args...)
	output, err := cmd.Output()
	if err != nil {
		if helperCtx.Err() != nil {
			return linuxRemoteDebuggingApprovalReport{}, fmt.Errorf("Linux AT-SPI helper timed out")
		}
		return linuxRemoteDebuggingApprovalReport{}, fmt.Errorf("Linux AT-SPI helper failed: %w", err)
	}

	var report linuxRemoteDebuggingApprovalReport
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(&report); err != nil {
		return linuxRemoteDebuggingApprovalReport{}, fmt.Errorf("parse Linux AT-SPI helper report: %w", err)
	}
	if report.WindowsScanned < 0 || report.PromptCountBefore < 0 || report.ApprovedCount < 0 || report.PromptCountAfter < 0 {
		return linuxRemoteDebuggingApprovalReport{}, fmt.Errorf("Linux AT-SPI helper returned negative counters")
	}
	return report, nil
}

func linuxSystemPython() (string, error) {
	const systemPython = "/usr/bin/python3"
	if info, err := os.Stat(systemPython); err == nil && !info.IsDir() {
		return systemPython, nil
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return "", fmt.Errorf("find system Python 3 for Linux AT-SPI: %w", err)
	}
	return python, nil
}

func chromeApplicationNames(channel string) ([]string, bool) {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "", "stable":
		return []string{"Google Chrome", "Chromium"}, true
	case "beta":
		return []string{"Google Chrome Beta"}, true
	case "canary":
		return []string{"Google Chrome Canary"}, true
	case "dev":
		return []string{"Google Chrome Dev"}, true
	default:
		return nil, false
	}
}
