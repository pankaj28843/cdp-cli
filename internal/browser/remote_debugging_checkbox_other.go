//go:build !darwin

package browser

import (
	"context"
	"fmt"
)

func PrepareRemoteDebuggingApproval(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func EnableRemoteDebuggingPreference(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func EnableNativeRemoteDebuggingCheckbox(_ string) (bool, error) {
	return false, fmt.Errorf("native remote-debugging checkbox self-heal is unsupported on this platform")
}

func ScanNativeRemoteDebuggingApproval(_ string, _ bool) (NativeRemoteDebuggingApprovalResult, error) {
	return NativeRemoteDebuggingApprovalResult{}, fmt.Errorf("native remote-debugging approval self-heal is unsupported on this platform")
}
