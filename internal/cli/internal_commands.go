package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/browser"
)

const (
	internalRemoteDebuggingApprovalCommand = "--internal-macos-remote-debugging-approval"
	internalRemoteDebuggingCheckboxCommand = "--internal-macos-remote-debugging-checkbox"
)

// ExecuteInternal handles narrowly scoped helper invocations that must run in
// a separate process. macOS accessibility calls can outlive a Go context when
// Chrome's AX server is wedged; the parent process therefore launches this
// helper and kills it at the process boundary if it exceeds its budget.
func ExecuteInternal(ctx context.Context, args []string, stdout, stderr io.Writer) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	if args[0] != internalRemoteDebuggingApprovalCommand && args[0] != internalRemoteDebuggingCheckboxCommand {
		return 0, false
	}

	processName := ""
	press := false
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--process-name":
			if index+1 >= len(args) {
				fmt.Fprintln(stderr, "missing value for --process-name")
				return 2, true
			}
			processName = strings.TrimSpace(args[index+1])
			index++
		case "--press":
			press = true
		default:
			fmt.Fprintf(stderr, "unknown internal macOS accessibility argument %q\n", args[index])
			return 2, true
		}
	}
	if processName == "" {
		fmt.Fprintln(stderr, "missing --process-name")
		return 2, true
	}

	encoder := json.NewEncoder(stdout)
	if args[0] == internalRemoteDebuggingCheckboxCommand {
		enabled, err := browser.EnableNativeRemoteDebuggingCheckbox(processName)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1, true
		}
		if err := encoder.Encode(struct {
			Enabled bool `json:"enabled"`
		}{Enabled: enabled}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1, true
		}
		return 0, true
	}

	result, err := browser.ScanNativeRemoteDebuggingApproval(processName, press)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1, true
	}
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1, true
	}
	return 0, true
}
