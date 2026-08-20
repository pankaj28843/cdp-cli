//go:build linux

package browser

import "context"

// Linux/Ubuntu deliberately reports a capability placeholder until a desktop
// accessibility adapter is selected and implemented. The shared CLI contract
// is already usable: an adapter only needs to drain the exact Chrome approval
// action and return a verified active probe.
func drainRemoteDebuggingApprovalQueue(_ context.Context, channel string) (RemoteDebuggingApprovalResult, error) {
	result := unsupportedRemoteDebuggingApproval(channel)
	result.Platform = "linux"
	result.Adapter = "linux-desktop-accessibility-placeholder"
	result.NextImplementation = "choose AT-SPI or an equivalent desktop accessibility adapter, then implement all-window exact-title approval draining"
	result.Message = "automatic Chrome remote-debugging approval is not implemented for Linux/Ubuntu yet"
	return result, nil
}
