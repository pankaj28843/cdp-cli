//go:build darwin && !cgo

package browser

import (
	"context"
	"fmt"
)

func EnableNativeRemoteDebuggingCheckbox(_ context.Context, _ string) (bool, error) {
	return false, fmt.Errorf("macOS remote-debugging checkbox self-heal requires a cgo build")
}

func ScanNativeRemoteDebuggingApproval(_ context.Context, _ string, _ bool) (NativeRemoteDebuggingApprovalResult, error) {
	return NativeRemoteDebuggingApprovalResult{}, fmt.Errorf("macOS remote-debugging approval self-heal requires a cgo build")
}
