//go:build !darwin && !linux

package browser

import "context"

func drainRemoteDebuggingApprovalQueue(_ context.Context, channel string) (RemoteDebuggingApprovalResult, error) {
	return unsupportedRemoteDebuggingApproval(channel), nil
}
