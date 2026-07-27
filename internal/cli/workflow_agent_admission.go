package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/alex"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowAgentAdmissionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admission",
		Short: "Inspect or explicitly resolve provider admission quarantine",
		Long: "Inspect privacy-safe provider admission state without probing Chrome. " +
			"Resolution is local-only, exact-run scoped, and allowed only after exact recovery evidence proves browser cleanup settled or that a direct replay created no target.",
	}
	cmd.AddCommand(a.newWorkflowAgentAdmissionStatusCommand())
	cmd.AddCommand(a.newWorkflowAgentAdmissionResolveCommand())
	return cmd
}

func (a *app) newWorkflowAgentAdmissionStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "status PROVIDER",
		Short:   "Inspect one provider's owner-only admission record",
		Example: "  cdp workflow agent admission status chatgpt --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			providerName := strings.TrimSpace(args[0])
			provider, realProvider := webagent.ParseProvider(providerName)
			virtualReadLane := providerName == "chatgpt-read" ||
				providerName == "chatgpt-rate"
			if !realProvider && !virtualReadLane {
				return commandError("invalid_provider", "usage", fmt.Sprintf("unknown provider %q", args[0]), ExitUsage, []string{"cdp workflow agent providers --json"})
			}
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			gate, err := admission.New(admission.Config{StateDir: store.Dir})
			if err != nil {
				return commandError("admission_unavailable", "internal", err.Error(), ExitInternal, nil)
			}
			record, found, err := gate.Status(ctx, providerName)
			if err != nil {
				return commandError("admission_status_failed", "internal", err.Error(), ExitInternal, nil)
			}
			state := "not_found"
			resolutionRequired := false
			doctorProvider := providerName
			if virtualReadLane {
				doctorProvider = string(webagent.ProviderChatGPT)
			}
			nextCommands := []string{
				fmt.Sprintf(
					"cdp workflow agent %s doctor --json",
					doctorProvider,
				),
			}
			if found {
				state = string(record.Phase)
				resolutionRequired = record.RequiresResolution()
				if resolutionRequired {
					nextCommands = []string{
						fmt.Sprintf("cdp workflow agent admission resolve %s %s --acknowledge-unknown --json", providerName, record.RunID),
					}
					if !(realProvider &&
						provider == webagent.ProviderAlex &&
						record.Operation == string(webagent.OperationAsk)) {
						nextCommands = append([]string{
							fmt.Sprintf("cdp workflow agent recovery inspect %s --json", record.RunID),
							fmt.Sprintf("cdp workflow agent recovery close %s --json", record.RunID),
						}, nextCommands...)
					}
				}
			}
			return a.render(ctx, fmt.Sprintf("%s\t%s", providerName, state), map[string]any{
				"ok":                  true,
				"provider":            providerName,
				"state":               state,
				"found":               found,
				"resolution_required": resolutionRequired,
				"record":              record,
				"next_commands":       nextCommands,
			})
		},
	}
}

func (a *app) newWorkflowAgentAdmissionResolveCommand() *cobra.Command {
	var acknowledgeUnknown bool
	cmd := &cobra.Command{
		Use:   "resolve PROVIDER RUN_ID",
		Short: "Acknowledge one exact quarantined run after cleanup reconciliation",
		Long: "Release only the exact persisted admission run after its recovery evidence proves no owned target remains. " +
			"This does not claim the provider mutation did or did not occur; --acknowledge-unknown explicitly accepts that uncertainty before future new work.",
		Example: "  cdp workflow agent recovery inspect <run-id> --json\n" +
			"  cdp workflow agent recovery close <run-id> --json\n" +
			"  cdp workflow agent admission resolve chatgpt <run-id> --acknowledge-unknown --json",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			if !acknowledgeUnknown {
				return commandError(
					"admission_acknowledgement_required",
					"usage",
					"--acknowledge-unknown is required because the prior mutation outcome may be unknown",
					ExitUsage,
					[]string{fmt.Sprintf("cdp workflow agent admission status %s --json", args[0])},
				)
			}
			provider, ok := webagent.ParseProvider(args[0])
			if !ok {
				return commandError("invalid_provider", "usage", fmt.Sprintf("unknown provider %q", args[0]), ExitUsage, []string{"cdp workflow agent providers --json"})
			}
			store, err := a.stateStore()
			if err != nil {
				return err
			}
			gate, err := admission.New(admission.Config{StateDir: store.Dir})
			if err != nil {
				return commandError("admission_unavailable", "internal", err.Error(), ExitInternal, nil)
			}
			record, found, err := gate.Status(ctx, string(provider))
			if err != nil {
				return commandError("admission_status_failed", "internal", err.Error(), ExitInternal, nil)
			}
			if !found || record.RunID != args[1] || record.Provider != string(provider) {
				return commandError(
					"admission_run_mismatch",
					"usage",
					"provider admission state does not match the exact requested run",
					ExitUsage,
					[]string{fmt.Sprintf("cdp workflow agent admission status %s --json", provider)},
				)
			}
			journal, err := browserflow.NewFileJournal(store.Dir)
			if err != nil {
				return commandError("recovery_unavailable", "internal", err.Error(), ExitInternal, nil)
			}
			recovery, recoveryErr := journal.Load(ctx, record.RunID)
			evidenceKind := "browserflow"
			evidencePhase := string(recovery.Phase)
			cleanup := recovery.Cleanup
			if errors.Is(recoveryErr, browserflow.ErrRunNotFound) &&
				provider == webagent.ProviderAlex &&
				record.Operation == string(webagent.OperationAsk) {
				alexStore, storeErr := alex.NewStore(store.Dir)
				if storeErr != nil {
					return commandError("admission_recovery_unavailable", "internal", storeErr.Error(), ExitInternal, nil)
				}
				actionRecord, loadErr := alexStore.LoadAskRecord(ctx, record.RunID)
				if loadErr != nil {
					return commandError(
						"admission_recovery_evidence_missing",
						"cleanup",
						"exact Ask Alex action evidence is required before admission resolution",
						ExitCheckFailed,
						[]string{fmt.Sprintf("cdp workflow agent admission status %s --json", provider)},
					)
				}
				evidenceKind = "direct_http_action_record"
				evidencePhase = actionRecord.State
				cleanup = browserflow.CleanupNotRequired
				recoveryErr = nil
			}
			if recoveryErr != nil {
				return commandError(
					"admission_recovery_evidence_missing",
					"cleanup",
					"exact browserflow recovery evidence is required before admission resolution",
					ExitCheckFailed,
					[]string{fmt.Sprintf("cdp workflow agent recovery inspect %s --json", record.RunID)},
				)
			}
			if evidenceKind == "browserflow" {
				if recovery.Provider != record.Provider || recovery.Operation != record.Operation {
					return commandError("admission_recovery_identity_mismatch", "cleanup", "browserflow recovery identity does not match admission state", ExitCheckFailed, nil)
				}
				cleanupSettled := (recovery.TargetID == "" && recovery.Cleanup == browserflow.CleanupNotRequired) ||
					(recovery.Phase == browserflow.PhaseClosed && recovery.Cleanup == browserflow.CleanupClosed)
				if !cleanupSettled {
					return commandError(
						"admission_recovery_incomplete",
						"cleanup",
						"exact browserflow target cleanup must settle before admission resolution",
						ExitCheckFailed,
						[]string{fmt.Sprintf("cdp workflow agent recovery close %s --json", record.RunID)},
					)
				}
			}
			resolved, err := gate.Resolve(ctx, admission.Request{
				Provider:  record.Provider,
				Operation: record.Operation,
				RunID:     record.RunID,
			})
			if err != nil {
				return commandError("admission_resolution_failed", "internal", err.Error(), ExitInternal, nil)
			}
			return a.render(ctx, fmt.Sprintf("%s\tacknowledged", provider), map[string]any{
				"ok":                  true,
				"provider":            provider,
				"run_id":              record.RunID,
				"state":               resolved.Phase,
				"outcome":             resolved.Outcome,
				"resolution_required": resolved.RequiresResolution(),
				"evidence_kind":       evidenceKind,
				"evidence_phase":      evidencePhase,
				"cleanup":             cleanup,
				"next_commands":       []string{fmt.Sprintf("cdp workflow agent %s doctor --json", provider)},
			})
		},
	}
	cmd.Flags().BoolVar(&acknowledgeUnknown, "acknowledge-unknown", false, "explicitly accept that the prior provider mutation may already have occurred")
	return cmd
}
