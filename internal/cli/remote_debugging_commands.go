package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/spf13/cobra"
)

const (
	headedRemoteDebuggingRepairLease     = 20 * time.Second
	headedRemoteDebuggingRepairReconnect = time.Second
)

// runHeadedRemoteDebuggingRepair starts or reuses the real daemon transport
// while the native approval helper authorizes only the exact Chrome prompt.
// StartKeepAlive returns only after the daemon has connected and opened RPC.
func (a *app) runHeadedRemoteDebuggingRepair(ctx context.Context) (browser.ProbeResult, browser.RemoteDebuggingApprovalResult, error) {
	if a.browserModeName() != string(config.BrowserModeHeaded) || !a.opts.autoConnect {
		return browser.ProbeResult{}, browser.RemoteDebuggingApprovalResult{}, commandError(
			"invalid_connection",
			"usage",
			"headed remote-debugging approval repair requires --browser-mode headed --auto-connect",
			ExitUsage,
			[]string{"cdp --browser-mode headed --auto-connect daemon approve --json"},
		)
	}
	repairCtx, cancel := context.WithTimeout(ctx, headedRemoteDebuggingRepairLease)
	defer cancel()
	store, err := a.stateStore()
	if err != nil {
		return browser.ProbeResult{}, browser.RemoteDebuggingApprovalResult{}, err
	}
	lock, acquired, existing, err := daemon.AcquireLockWithLease(repairCtx, store.Dir, "headed-remote-debugging-repair", 0, 10*time.Minute, headedRemoteDebuggingRepairLease, daemon.LockMetadata{
		Name:  "headed-remote-debugging-repair",
		Phase: "starting",
	})
	if err != nil {
		return browser.ProbeResult{}, browser.RemoteDebuggingApprovalResult{}, fmt.Errorf("acquire headed remote-debugging repair lock: %w", err)
	}
	if !acquired {
		return browser.ProbeResult{}, browser.RemoteDebuggingApprovalResult{}, fmt.Errorf("headed remote-debugging repair already running (pid %d)", existing.PID)
	}
	defer lock.Release()

	a.opts.activeProbe = true
	if _, err := browser.PrepareRemoteDebuggingApproval(repairCtx, a.opts.channel); err != nil {
		return browser.ProbeResult{
			State:               "permission_pending",
			Message:             "headed Chrome remote-debugging permission could not be prepared",
			ConnectionMode:      "auto_connect",
			Channel:             a.opts.channel,
			RemediationCommands: permissionRemediationCommands(),
		}, browser.RemoteDebuggingApprovalResult{
			Supported:          true,
			Platform:           "darwin",
			Adapter:            "macos-accessibility",
			BrowserApplication: "Google Chrome",
			ApprovalURL:        browser.RemoteDebuggingApprovalURL,
			QueueDrained:       false,
			Action:             "failed",
			Message:            "could not re-enable Chrome remote-debugging permission",
			Detail:             err.Error(),
		}, err
	}
	endpoint, err := a.browserEndpoint(repairCtx)
	if err != nil {
		return browser.ProbeResult{}, browser.RemoteDebuggingApprovalResult{}, err
	}
	type keepAliveOutcome struct {
		runtime daemon.Runtime
		err     error
	}
	keepAliveCh := make(chan keepAliveOutcome, 1)
	go func() {
		runtime, _, err := a.startKeepAlive(repairCtx, endpoint, nil, headedRemoteDebuggingRepairReconnect)
		keepAliveCh <- keepAliveOutcome{runtime: runtime, err: err}
	}()

	approval, approvalErr := browser.DrainRemoteDebuggingApprovalQueue(repairCtx, a.opts.channel)
	outcome := <-keepAliveCh
	if approvalErr != nil {
		return browser.ProbeResult{}, approval, approvalErr
	}
	if outcome.err != nil {
		return browser.ProbeResult{
			State:               "permission_pending",
			Message:             "headed daemon did not establish a CDP transport",
			ConnectionMode:      "auto_connect",
			Channel:             a.opts.channel,
			RemediationCommands: permissionRemediationCommands(),
		}, approval, outcome.err
	}
	return browser.ProbeResult{
		State:                "cdp_available",
		Message:              "headed daemon established a usable Chrome DevTools WebSocket",
		ConnectionMode:       "auto_connect",
		Channel:              a.opts.channel,
		WebSocketDebuggerURL: true,
	}, approval, nil
}

func (a *app) newDaemonApproveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve",
		Short: "Drain Chrome remote-debugging approval sheets and verify CDP",
		Long:  "Drain only Chrome's exact ‘Allow remote debugging?’ sheets across all Chrome windows while an active headed probe is running, then report whether the real CDP transport became usable.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()

			if err := a.applySelectedConnection(ctx); err != nil {
				return err
			}
			if _, err := ensureChromeForKeepalive(ctx, "", defaultChromeCommand(), nil); err != nil {
				return commandError(
					"chrome_start_failed",
					"connection",
					fmt.Sprintf("ensure headed Chrome window: %v", err),
					ExitConnection,
					[]string{"open chrome://inspect/#remote-debugging", "cdp daemon approve --json"},
				)
			}
			probe, approval, err := a.runHeadedRemoteDebuggingRepair(ctx)
			if err != nil {
				return commandErrorWithData(
					"permission_pending",
					"permission",
					fmt.Sprintf("remote-debugging approval repair failed: %v", err),
					ExitPermission,
					permissionRemediationCommands(),
					permissionPendingData(map[string]any{"approval": approval, "probe": probe}),
				)
			}

			data := map[string]any{
				"ok":       probe.State == "cdp_available" && approval.QueueDrained,
				"approval": approval,
				"probe":    probe,
				"state":    probe.State,
			}
			if probe.State != "cdp_available" || !approval.QueueDrained {
				return commandErrorWithData(
					"permission_pending",
					"permission",
					"Chrome remote-debugging approval was not verified by a usable CDP transport",
					ExitPermission,
					permissionRemediationCommands(),
					permissionPendingData(data),
				)
			}
			return a.render(ctx, "remote debugging approved; headed CDP is usable", data)
		},
	}
	return cmd
}

func remoteDebuggingApprovalCommand() string {
	return "cdp --browser-mode headed --auto-connect daemon approve --json"
}

func remoteDebuggingApprovalEnabled(cmd *cobra.Command, requested bool) bool {
	if cmd.Flags().Changed("macos-self-heal-approval") {
		return requested
	}
	return requested || envBool("CDP_MACOS_SELF_HEAL_APPROVAL")
}

func remoteDebuggingApprovalMessage(approval browser.RemoteDebuggingApprovalResult) string {
	if strings.TrimSpace(approval.Detail) != "" {
		return approval.Message + ": " + approval.Detail
	}
	return approval.Message
}
