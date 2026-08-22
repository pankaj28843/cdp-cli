package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/m365"
	"github.com/spf13/cobra"
)

type m365CapabilitiesContract struct {
	webagent.Capabilities
	Runtime m365.RuntimeStatus `json:"runtime"`
}

func (a *app) m365CapabilitiesData(
	ctx context.Context,
	capabilities webagent.Capabilities,
) any {
	stateStore, err := a.stateStore()
	if err != nil {
		return m365CapabilitiesContract{
			Capabilities: capabilities,
			Runtime: m365.RuntimeStatus{
				SchemaVersion: m365.RuntimeCapabilitiesSchemaVersion,
				State:         "unavailable",
				StatePath:     m365.RelativeCapabilitiesPath,
				AudioProtocol: "AugLoop_Voice_VoiceTile/v2",
				Reason:        "owner-only state directory is unavailable",
			},
		}
	}
	store, err := m365.NewStore(stateStore.Dir)
	if err != nil {
		return m365CapabilitiesContract{
			Capabilities: capabilities,
			Runtime: m365.RuntimeStatus{
				SchemaVersion: m365.RuntimeCapabilitiesSchemaVersion,
				State:         "unavailable",
				StatePath:     m365.RelativeCapabilitiesPath,
				AudioProtocol: "AugLoop_Voice_VoiceTile/v2",
				Reason:        "owner-only runtime capability state is unavailable",
			},
		}
	}
	return m365CapabilitiesContract{
		Capabilities: capabilities,
		Runtime: store.RuntimeStatus(
			ctx,
			time.Now(),
			m365.DefaultCapabilitiesTTL,
		),
	}
}

func (a *app) newWorkflowAgentM365DoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report Microsoft 365 dictation readiness from owner-only local evidence",
		Long: "Read owner-only Microsoft 365 auth and AugLoop dictation evidence without opening or probing Chrome. " +
			"Browser submission remains an explicit headed-runtime operation.",
		Example: "  cdp workflow agent m365 doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"m365 doctor: unavailable",
					m365.UnavailableDoctor(a.build.Commit),
				)
			}
			store, err := m365.NewStore(stateStore.Dir)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"m365 doctor: unavailable",
					m365.UnavailableDoctor(a.build.Commit),
				)
			}
			result := m365.Doctor(ctx, store, time.Now(), a.build.Commit)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("m365 doctor: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentM365AuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect or refresh Microsoft 365 dictation auth evidence",
	}
	cmd.AddCommand(a.newWorkflowAgentM365AuthRefreshCommand())
	return cmd
}

func (a *app) newWorkflowAgentM365AuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh Microsoft 365 AugLoop auth evidence with a bounded dictation probe",
		Long: "Open one owned Microsoft 365 target, trigger the exact dictation control, observe the AugLoop " +
			"session metadata and token provision, persist owner-only evidence, and exact-close the target.",
		Example: "  cdp workflow agent m365 auth refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := m365.UnavailableOperation(
					a.build.Commit,
					webagent.OperationAuthRefresh,
					"m365_headed_browser_required",
					"usage",
					"Microsoft 365 auth refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(ctx, "m365 auth: headed browser required", result)
			}
			browserConfig, store, unavailable := a.m365BrowserOperationConfig(
				ctx,
				webagent.OperationAuthRefresh,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "m365 auth: unavailable", *unavailable)
			}
			result := m365.RefreshAuth(ctx, m365.AuthRefreshConfig{
				BrowserConfig: browserConfig,
				Store:         store,
			})
			return a.renderWebAgentResult(ctx, fmt.Sprintf("m365 auth: %v", result.State), result)
		},
	}
}

func (a *app) newWorkflowAgentM365CapabilitiesRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh Microsoft 365 AugLoop dictation capability evidence",
		Long: "Run the bounded Microsoft 365 dictation probe and persist the observed AugLoop VoiceTile " +
			"protocol and result subscriptions without submitting a user prompt.",
		Example: "  cdp workflow agent m365 capabilities refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := m365.UnavailableOperation(
					a.build.Commit,
					webagent.OperationCapabilities,
					"m365_headed_browser_required",
					"usage",
					"Microsoft 365 capability refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(ctx, "m365 capabilities: headed browser required", result)
			}
			browserConfig, store, unavailable := a.m365BrowserOperationConfig(
				ctx,
				webagent.OperationCapabilities,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "m365 capabilities: unavailable", *unavailable)
			}
			result := m365.RefreshCapabilities(ctx, m365.AuthRefreshConfig{
				BrowserConfig: browserConfig,
				Store:         store,
			})
			return a.renderWebAgentResult(ctx, fmt.Sprintf("m365 capabilities: %v", result.State), result)
		},
	}
}

func (a *app) newWorkflowAgentM365TranscribeCommand() *cobra.Command {
	var filePath string
	var durationMilliseconds int64
	var stream bool
	cmd := &cobra.Command{
		Use:   "transcribe",
		Short: "Transcribe one local WebM file through Microsoft 365 AugLoop",
		Long: "Read the owner-only Microsoft 365 AugLoop auth template and send one persisted WebM audio file " +
			"over the observed direct WebSocket transport. The normal path does not open Chrome; a headed auth " +
			"refresh is lazy and bounded when the saved evidence is stale or rejected. With --stream, use the " +
			"JSON-lines PCM boundary for live partial results while recording.",
		Example: "  cdp workflow agent m365 transcribe --file ~/.cache/whisper.webm --duration-ms 4200 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			defaultTimeout := 2 * time.Minute
			if !stream && durationMilliseconds > 0 {
				defaultTimeout = maxDuration(
					defaultTimeout,
					time.Duration(durationMilliseconds)*time.Millisecond+30*time.Second,
				)
			}
			ctx, cancel := a.commandContextWithDefault(cmd, defaultTimeout)
			defer cancel()
			if !stream && filePath == "" {
				return commandError(
					"m365_transcription_file_required",
					"usage",
					"Microsoft 365 transcription requires --file",
					ExitUsage,
					[]string{"cdp workflow agent m365 transcribe --file /path/to/whisper.webm --duration-ms 1000 --json"},
				)
			}
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(ctx, "m365 transcribe: unavailable", m365.UnavailableOperation(
					a.build.Commit,
					webagent.OperationTranscribe,
					"m365_state_unavailable",
					"internal",
					"Microsoft 365 owner-only state is unavailable",
				))
			}
			store, err := m365.NewStore(stateStore.Dir)
			if err != nil {
				return a.renderWebAgentResult(ctx, "m365 transcribe: unavailable", m365.UnavailableOperation(
					a.build.Commit,
					webagent.OperationTranscribe,
					"m365_state_unavailable",
					"internal",
					"Microsoft 365 owner-only state is unavailable",
				))
			}

			refreshAuth := func(refreshCtx context.Context) error {
				if !a.selectHeadedProviderRuntime() {
					return fmt.Errorf("Microsoft 365 headed browser runtime is unavailable for auth repair")
				}
				browserConfig, refreshedStore, unavailable := a.m365BrowserOperationConfig(
					refreshCtx,
					webagent.OperationTranscribe,
				)
				if unavailable != nil {
					if unavailable.Error != nil {
						return fmt.Errorf("%s", unavailable.Error.Message)
					}
					return fmt.Errorf("Microsoft 365 headed browser auth repair is unavailable")
				}
				result := m365.RefreshAuth(refreshCtx, m365.AuthRefreshConfig{
					BrowserConfig: browserConfig,
					Store:         refreshedStore,
				})
				if !result.OK {
					if result.Error != nil {
						return fmt.Errorf("%s", result.Error.Message)
					}
					return fmt.Errorf("Microsoft 365 auth repair failed")
				}
				return nil
			}

			if stream {
				return m365.StreamTranscribe(ctx, m365.TranscribeConfig{
					Store:       store,
					BuildCommit: a.build.Commit,
					RefreshAuth: refreshAuth,
					Dial:        augloop.Dial,
				}, cmd.InOrStdin(), a.out)
			}

			result := m365.Transcribe(ctx, m365.TranscribeConfig{
				Store:       store,
				BuildCommit: a.build.Commit,
				RefreshAuth: refreshAuth,
				Dial:        augloop.Dial,
			}, filePath, durationMilliseconds)
			human := fmt.Sprintf("m365 transcribe: %v", result.State)
			if data, ok := result.Data.(m365.TranscriptionData); ok && result.OK {
				human = data.Transcript
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().StringVar(&filePath, "file", "", "local WebM/Opus file to transcribe")
	cmd.Flags().Int64Var(&durationMilliseconds, "duration-ms", 0, "recorded audio duration in milliseconds")
	cmd.Flags().BoolVar(&stream, "stream", false, "read JSON-lines 16 kHz PCM from stdin and emit live partial results")
	return cmd
}

func maxDuration(left, right time.Duration) time.Duration {
	if right > left {
		return right
	}
	return left
}

func (a *app) m365BrowserOperationConfig(
	ctx context.Context,
	operation webagent.Operation,
) (m365.BrowserConfig, *m365.Store, *webagent.Result) {
	stateStore, err := a.stateStore()
	if err != nil {
		result := m365.UnavailableOperation(
			a.build.Commit,
			operation,
			"m365_state_unavailable",
			"internal",
			"Microsoft 365 owner-only state is unavailable",
		)
		return m365.BrowserConfig{}, nil, &result
	}
	store, err := m365.NewStore(stateStore.Dir)
	if err != nil {
		result := m365.UnavailableOperation(
			a.build.Commit,
			operation,
			"m365_state_unavailable",
			"internal",
			"Microsoft 365 owner-only state is unavailable",
		)
		return m365.BrowserConfig{}, nil, &result
	}
	journal, err := browserflow.NewFileJournal(stateStore.Dir)
	if err != nil {
		result := m365.UnavailableOperation(
			a.build.Commit,
			operation,
			"m365_lifecycle_state_unavailable",
			"internal",
			"Microsoft 365 exact-target lifecycle state is unavailable",
		)
		return m365.BrowserConfig{}, nil, &result
	}
	client, _, err := a.browserEventCDPClient(ctx)
	if err != nil {
		if a.opts.debug {
			_, _ = fmt.Fprintf(a.err, "m365 headed browser runtime unavailable: %v\n", err)
		}
		result := m365.UnavailableOperation(
			a.build.Commit,
			operation,
			"m365_browser_unavailable",
			"connection",
			"Microsoft 365 headed browser runtime is unavailable",
		)
		return m365.BrowserConfig{}, nil, &result
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:          client,
		Journal:         journal,
		Budget:          a.browserResourceBudgetOptions(),
		AllowOverBudget: a.headedProviderRepairMayUseOwnedTarget(operation),
		InputLockPath:   browserflow.HeadedInputLockPath(stateStore.Dir),
	})
	if err != nil {
		result := m365.UnavailableOperation(
			a.build.Commit,
			operation,
			"m365_browserflow_unavailable",
			"internal",
			"Microsoft 365 exact-target browser transaction is unavailable",
		)
		return m365.BrowserConfig{}, nil, &result
	}
	return m365.BrowserConfig{
		Client:      client,
		Engine:      engine,
		Journal:     journal,
		BuildCommit: a.build.Commit,
	}, store, nil
}
