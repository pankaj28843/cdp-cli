//go:build darwin && !cgo

package browser

import "fmt"

func EnableNativeRemoteDebuggingCheckbox(_ string) (bool, error) {
	return false, fmt.Errorf("macOS remote-debugging checkbox self-heal requires a cgo build")
}

func ScanNativeRemoteDebuggingApproval(_ string, _ bool) (NativeRemoteDebuggingApprovalResult, error) {
	return NativeRemoteDebuggingApprovalResult{}, fmt.Errorf("macOS remote-debugging approval self-heal requires a cgo build")
}
