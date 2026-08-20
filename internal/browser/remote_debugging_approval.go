package browser

import (
	"context"
	"fmt"
	"runtime"
	"strings"
)

const RemoteDebuggingApprovalURL = "chrome://inspect/#remote-debugging"

// RemoteDebuggingApprovalResult describes an attempted cleanup of Chrome's
// native remote-debugging approval queue. The queue is deliberately narrow:
// only Chrome's exact "Allow remote debugging?" sheet is actionable here.
type RemoteDebuggingApprovalResult struct {
	Supported          bool   `json:"supported"`
	Platform           string `json:"platform,omitempty"`
	Adapter            string `json:"adapter,omitempty"`
	NextImplementation string `json:"next_implementation,omitempty"`
	BrowserApplication string `json:"browser_application,omitempty"`
	ApprovalURL        string `json:"approval_url"`
	WindowsScanned     int    `json:"windows_scanned,omitempty"`
	PromptCountBefore  int    `json:"prompt_count_before,omitempty"`
	ApprovedCount      int    `json:"approved_count,omitempty"`
	PromptCountAfter   int    `json:"prompt_count_after,omitempty"`
	QueueDrained       bool   `json:"queue_drained"`
	Action             string `json:"action"`
	Message            string `json:"message"`
	Detail             string `json:"detail,omitempty"`
}

// NativeRemoteDebuggingApprovalResult is the small platform bridge payload
// used by the bounded macOS helper process. It is defined on every platform
// so the hidden helper entry point remains cross-buildable.
type NativeRemoteDebuggingApprovalResult struct {
	WindowsScanned    int `json:"windows_scanned"`
	PromptCountBefore int `json:"prompt_count_before"`
	ApprovedCount     int `json:"approved_count"`
	PromptCountAfter  int `json:"prompt_count_after"`
}

// DrainRemoteDebuggingApprovalQueue approves only the explicit Chrome
// remote-debugging sheets, across every Chrome window, for a bounded period.
// It never treats an attempted UI action as success: callers must also verify
// a usable CDP probe.
func DrainRemoteDebuggingApprovalQueue(ctx context.Context, channel string) (RemoteDebuggingApprovalResult, error) {
	return drainRemoteDebuggingApprovalQueue(ctx, channel)
}

func unsupportedRemoteDebuggingApproval(channel string) RemoteDebuggingApprovalResult {
	return RemoteDebuggingApprovalResult{
		Supported:          false,
		Platform:           runtime.GOOS,
		Adapter:            "unimplemented",
		NextImplementation: "add a platform accessibility adapter that drains only the exact Chrome remote-debugging approval action, then reuse the active-probe verification contract",
		ApprovalURL:        RemoteDebuggingApprovalURL,
		QueueDrained:       false,
		Action:             "unsupported",
		Message:            fmt.Sprintf("automatic Chrome remote-debugging approval is not supported on this platform or channel (%s)", strings.TrimSpace(channel)),
	}
}
