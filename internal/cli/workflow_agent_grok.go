package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/grok"
	"github.com/spf13/cobra"
)

type grokCapabilitiesContract struct {
	webagent.Capabilities
	Runtime grok.RuntimeStatus `json:"runtime"`
}

func (a *app) grokCapabilitiesData(
	ctx context.Context,
	capabilities webagent.Capabilities,
) any {
	stateStore, err := a.stateStore()
	if err != nil {
		return grokCapabilitiesContract{
			Capabilities: capabilities,
			Runtime: grok.RuntimeStatus{
				SchemaVersion: grok.RuntimeCapabilitiesSchemaVersion,
				State:         "unavailable",
				StatePath:     grok.RelativeCapabilitiesPath,
				Modes:         []grok.Mode{},
				Reason:        "owner-only state directory is unavailable",
			},
		}
	}
	store, err := grok.NewStore(stateStore.Dir)
	if err != nil {
		return grokCapabilitiesContract{
			Capabilities: capabilities,
			Runtime: grok.RuntimeStatus{
				SchemaVersion: grok.RuntimeCapabilitiesSchemaVersion,
				State:         "unavailable",
				StatePath:     grok.RelativeCapabilitiesPath,
				Modes:         []grok.Mode{},
				Reason:        "owner-only runtime capability state is unavailable",
			},
		}
	}
	return grokCapabilitiesContract{
		Capabilities: capabilities,
		Runtime: store.RuntimeStatus(
			ctx,
			time.Now(),
			grok.DefaultCapabilitiesTTL,
		),
	}
}

func (a *app) newWorkflowAgentGrokDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report Grok readiness from owner-only local evidence",
		Long: "Read owner-only Grok auth and runtime capability evidence without opening or probing Chrome. " +
			"Browser readiness remains an explicit headed-runtime requirement.",
		Example: "  cdp workflow agent grok doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok doctor: unavailable",
					grok.UnavailableDoctor(a.build.Commit),
				)
			}
			store, err := grok.NewStore(stateStore.Dir)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok doctor: unavailable",
					grok.UnavailableDoctor(a.build.Commit),
				)
			}
			result := grok.Doctor(
				ctx,
				store,
				time.Now(),
				a.build.Commit,
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("grok doctor: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentGrokAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect or refresh Grok auth evidence",
	}
	cmd.AddCommand(a.newWorkflowAgentGrokAuthRefreshCommand())
	return cmd
}

func (a *app) newWorkflowAgentGrokAuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh Grok browser-observed request evidence",
		Long: "Open one fresh owned Grok target, observe the signed-in conversation-list request, " +
			"refresh owner-only replay state, and exact-close the target without creating a conversation.",
		Example: "  cdp workflow agent grok auth refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := grokUnavailableOperation(
					a.build.Commit,
					webagent.OperationAuthRefresh,
					"grok_headed_browser_required",
					"usage",
					"Grok auth refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"grok auth: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.grokBrowserOperationConfig(
				ctx,
				webagent.OperationAuthRefresh,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok auth: unavailable",
					*unavailable,
				)
			}
			result := grok.RefreshAuth(ctx, grok.AuthRefreshConfig{
				BrowserConfig: config,
				Store:         store,
			})
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("grok auth: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentGrokCapabilitiesRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Observe Grok runtime modes in headed Chrome",
		Long: "Open one fresh owned Grok target, observe the stable /rest/modes response, " +
			"persist only the safe mode catalog, and exact-close the target.",
		Example: "  cdp workflow agent grok capabilities refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := grokUnavailableOperation(
					a.build.Commit,
					webagent.OperationCapabilities,
					"grok_headed_browser_required",
					"usage",
					"Grok capability refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"grok capabilities: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.grokBrowserOperationConfig(
				ctx,
				webagent.OperationCapabilities,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok capabilities: unavailable",
					*unavailable,
				)
			}
			result := grok.RefreshCapabilities(
				ctx,
				grok.CapabilityRefreshConfig{
					BrowserConfig: config,
					Store:         store,
				},
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("grok capabilities: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentGrokAskCommand() *cobra.Command {
	var stdin bool
	cmd := &cobra.Command{
		Use:   "ask [PROMPT]",
		Short: "Submit one exact visible Grok request",
		Long: "Start one fresh Grok conversation in one fresh exact owned headed target, verify the cached default mode and exact prompt, " +
			"persist action_pending, click Send once, acknowledge the same-target route, and return canonical stored detail without resubmitting.",
		Example: "  printf '%s' 'Review this implementation.' | cdp workflow agent grok ask --stdin --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 4*time.Minute)
			defer cancel()
			if stdin && len(args) > 0 {
				return commandError(
					"grok_prompt_source_conflict",
					"usage",
					"Grok ask accepts either PROMPT or --stdin, not both",
					ExitUsage,
					[]string{"cdp workflow agent grok ask --stdin --json"},
				)
			}
			prompt := ""
			if stdin {
				data, err := io.ReadAll(
					io.LimitReader(
						cmd.InOrStdin(),
						int64(grok.MaxPromptCharacters*4+2),
					),
				)
				if err != nil {
					return commandError(
						"grok_prompt_read_failed",
						"usage",
						"Grok prompt could not be read from stdin",
						ExitUsage,
						nil,
					)
				}
				prompt = string(data)
			} else if len(args) == 1 {
				prompt = args[0]
			}
			if !a.selectHeadedProviderRuntime() {
				result := grokUnavailableOperation(
					a.build.Commit,
					webagent.OperationAsk,
					"grok_headed_browser_required",
					"usage",
					"Grok ask requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"grok ask: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.grokBrowserOperationConfig(
				ctx,
				webagent.OperationAsk,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok ask: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			result := grok.Ask(ctx, grok.AskConfig{
				BrowserConfig: config,
				Store:         store,
				Timeout:       timeout,
			}, prompt)
			human := fmt.Sprintf("grok ask: %v", result.State)
			if data, ok := result.Data.(grok.AskData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read the exact prompt from stdin")
	return cmd
}

func (a *app) newWorkflowAgentGrokConversationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversations",
		Short: "List, read, await, or delete Grok conversations",
		Long: "Use browser-observed stable HTTP for stored reads, with one fresh exact headed fallback only when browser context is required. " +
			"Await never resubmits a prompt.",
	}
	cmd.AddCommand(a.newWorkflowAgentGrokConversationsListCommand())
	cmd.AddCommand(a.newWorkflowAgentGrokConversationsDetailCommand())
	cmd.AddCommand(a.newWorkflowAgentGrokConversationsAwaitCommand())
	cmd.AddCommand(a.newWorkflowAgentGrokConversationsDeleteCommand())
	return cmd
}

func (a *app) newWorkflowAgentGrokConversationsListCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List stored Grok conversations",
		Example: "  cdp workflow agent grok conversations list --limit 20 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			config, unavailable := a.grokReadConfig(
				webagent.OperationConversationsList,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok conversations: unavailable",
					*unavailable,
				)
			}
			result := grok.ListConversations(ctx, config, limit)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("grok conversations: %v", result.State),
				result,
			)
		},
	}
	cmd.Flags().IntVar(
		&limit,
		"limit",
		20,
		"maximum stored conversations to return (0-100)",
	)
	return cmd
}

func (a *app) newWorkflowAgentGrokConversationsDetailCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "detail CONVERSATION_ID",
		Short:   "Read one exact stored Grok conversation",
		Example: "  cdp workflow agent grok conversations detail <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			config, unavailable := a.grokReadConfig(
				webagent.OperationConversationsDetail,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok detail: unavailable",
					*unavailable,
				)
			}
			result := grok.DetailConversation(ctx, config, args[0])
			human := fmt.Sprintf("grok detail: %v", result.State)
			if data, ok := result.Data.(grok.ConversationDetailData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
}

func (a *app) newWorkflowAgentGrokConversationsAwaitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "await CONVERSATION_ID",
		Short: "Await one exact stored Grok conversation without resubmitting",
		Long: "Read only the exact acknowledged conversation until its stored assistant response is terminal or the bounded deadline expires. " +
			"This command never submits a prompt.",
		Example: "  cdp --timeout 3m workflow agent grok conversations await <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 3*time.Minute)
			defer cancel()
			config, unavailable := a.grokReadConfig(
				webagent.OperationConversationsAwait,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok await: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			result := grok.AwaitConversation(
				ctx,
				config,
				args[0],
				timeout,
			)
			human := fmt.Sprintf("grok await: %v", result.State)
			if data, ok := result.Data.(grok.ConversationDetailData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
}

func (a *app) newWorkflowAgentGrokConversationsDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete CONVERSATION_ID",
		Short: "Visibly delete one exact Grok conversation",
		Long: "Own one fresh headed target, open the exact conversation menu, persist action_pending, " +
			"dispatch one raw-input Delete Chat action, prove the same-target home redirect, and exact-close the target.",
		Example: "  cdp workflow agent grok conversations delete <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := grokUnavailableOperation(
					a.build.Commit,
					webagent.OperationConversationsDelete,
					"grok_headed_browser_required",
					"usage",
					"Grok conversation delete requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"grok delete: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.grokBrowserOperationConfig(
				ctx,
				webagent.OperationConversationsDelete,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok delete: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 45 * time.Second
			}
			result := grok.DeleteConversation(ctx, grok.DeleteConfig{
				BrowserConfig: config,
				Store:         store,
				Timeout:       timeout,
			}, args[0])
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("grok delete: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentGrokCalibrationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "calibration",
		Short: "Inspect or safely reconcile the last Grok calibration",
		Long: "Read owner-only Grok calibration state without probing Chrome, or explicitly reconcile the exact recorded target " +
			"and acknowledged disposable conversation without repeating an ambiguous action.",
	}
	cmd.AddCommand(a.newWorkflowAgentGrokCalibrationStatusCommand())
	cmd.AddCommand(a.newWorkflowAgentGrokCalibrationCleanupCommand())
	return cmd
}

func (a *app) newWorkflowAgentGrokCalibrationStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Read the last Grok calibration state without probing Chrome",
		Example: "  cdp workflow agent grok calibration status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration: unavailable",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_calibration_state_unavailable",
						"internal",
						"Grok owner-only calibration state is unavailable",
					),
				)
			}
			store, err := grok.NewCalibrationStore(stateStore.Dir)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration: unavailable",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_calibration_state_unavailable",
						"internal",
						"Grok owner-only calibration state is unavailable",
					),
				)
			}
			journal, err := browserflow.NewFileJournal(stateStore.Dir)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration: unavailable",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_calibration_recovery_unavailable",
						"internal",
						"Grok exact-target recovery state is unavailable",
					),
				)
			}
			result := grok.CalibrationStatus(
				ctx,
				store,
				journal,
				a.build.Commit,
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("grok calibration: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentGrokCalibrationCleanupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup",
		Short: "Reconcile only the exact resources from the last Grok calibration",
		Long: "Close only the exact persisted owned target, then delete only a persisted acknowledged disposable conversation. " +
			"Never repeat an ambiguous Send or delete action.",
		Example: "  cdp --timeout 1m workflow agent grok calibration cleanup --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration cleanup: unavailable",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_calibration_state_unavailable",
						"internal",
						"Grok owner-only calibration state is unavailable",
					),
				)
			}
			calibrationStore, err := grok.NewCalibrationStore(
				stateStore.Dir,
			)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration cleanup: unavailable",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_calibration_state_unavailable",
						"internal",
						"Grok owner-only calibration state is unavailable",
					),
				)
			}
			journal, err := browserflow.NewFileJournal(stateStore.Dir)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration cleanup: unavailable",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_calibration_recovery_unavailable",
						"internal",
						"Grok exact-target recovery state is unavailable",
					),
				)
			}
			status := grok.CalibrationStatus(
				ctx,
				calibrationStore,
				journal,
				a.build.Commit,
			)
			if !status.OK {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration cleanup: unavailable",
					status,
				)
			}
			if data, ok := status.Data.(grok.CalibrationStatusData); ok && !data.RecoveryRequired {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration cleanup: not required",
					status,
				)
			}
			if !a.selectHeadedProviderRuntime() {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration cleanup: headed browser required",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_headed_browser_required",
						"usage",
						"Grok calibration cleanup requires the headed browser runtime",
					),
				)
			}
			browserConfig, providerStore, unavailable :=
				a.grokBrowserOperationConfig(
					ctx,
					webagent.OperationCalibrate,
				)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration cleanup: unavailable",
					*unavailable,
				)
			}
			result := grok.CleanupCalibration(
				ctx,
				grok.CalibrationCleanupConfig{
					Store:       calibrationStore,
					Journal:     browserConfig.Journal,
					Engine:      browserConfig.Engine,
					BuildCommit: a.build.Commit,
					Delete: grok.DeleteConfig{
						BrowserConfig: browserConfig,
						Store:         providerStore,
						Timeout:       45 * time.Second,
					},
				},
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf(
					"grok calibration cleanup: %v",
					result.State,
				),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentGrokCalibrateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "calibrate",
		Short: "Run one disposable Grok create/capture/delete transaction",
		Long: "Use one fresh owned headed target for one memory-only calibration prompt, one Send, " +
			"canonical stored answer capture, exact same-target deletion, and exact close.",
		Example: "  cdp --timeout 3m workflow agent grok calibrate --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 4*time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration: headed browser required",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_headed_browser_required",
						"usage",
						"Grok calibration requires the headed browser runtime",
					),
				)
			}
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration: unavailable",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_calibration_state_unavailable",
						"internal",
						"Grok owner-only calibration state is unavailable",
					),
				)
			}
			calibrationStore, err := grok.NewCalibrationStore(
				stateStore.Dir,
			)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration: unavailable",
					grokCalibrationUnavailable(
						a.build.Commit,
						"grok_calibration_state_unavailable",
						"internal",
						"Grok owner-only calibration state is unavailable",
					),
				)
			}
			browserConfig, providerStore, unavailable :=
				a.grokBrowserOperationConfig(
					ctx,
					webagent.OperationCalibrate,
				)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"grok calibration: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			result := grok.Calibrate(
				ctx,
				grok.CalibrationConfig{
					BrowserConfig: browserConfig,
					AuthStore:     providerStore,
					Store:         calibrationStore,
					Timeout:       timeout,
				},
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("grok calibration: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) grokBrowserOperationConfig(
	ctx context.Context,
	operation webagent.Operation,
) (grok.BrowserConfig, *grok.Store, *webagent.Result) {
	stateStore, err := a.stateStore()
	if err != nil {
		result := grokUnavailableOperation(
			a.build.Commit, operation,
			"grok_state_unavailable", "internal",
			"Grok owner-only state is unavailable",
		)
		return grok.BrowserConfig{}, nil, &result
	}
	store, err := grok.NewStore(stateStore.Dir)
	if err != nil {
		result := grokUnavailableOperation(
			a.build.Commit, operation,
			"grok_state_unavailable", "internal",
			"Grok owner-only state is unavailable",
		)
		return grok.BrowserConfig{}, nil, &result
	}
	journal, err := browserflow.NewFileJournal(stateStore.Dir)
	if err != nil {
		result := grokUnavailableOperation(
			a.build.Commit, operation,
			"grok_recovery_unavailable", "internal",
			"Grok exact-target recovery state is unavailable",
		)
		return grok.BrowserConfig{}, nil, &result
	}
	gate, err := admission.New(admission.Config{
		StateDir:       stateStore.Dir,
		MinimumSpacing: grok.DefaultAdmissionSpacing,
	})
	if err != nil {
		result := grokUnavailableOperation(
			a.build.Commit, operation,
			"grok_admission_unavailable", "internal",
			"Grok provider admission state is unavailable",
		)
		return grok.BrowserConfig{}, nil, &result
	}
	client, _, err := a.browserEventCDPClient(ctx)
	if err != nil {
		result := grokUnavailableOperation(
			a.build.Commit, operation,
			"grok_browser_unavailable", "connection",
			"Grok headed browser runtime is unavailable",
		)
		return grok.BrowserConfig{}, nil, &result
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:          client,
		Journal:         journal,
		Budget:          a.browserResourceBudgetOptions(),
		AllowOverBudget: a.opts.allowOverBudget,
		InputLockPath:   browserflow.HeadedInputLockPath(stateStore.Dir),
	})
	if err != nil {
		result := grokUnavailableOperation(
			a.build.Commit, operation,
			"grok_browserflow_unavailable", "internal",
			"Grok exact-target browser transaction is unavailable",
		)
		return grok.BrowserConfig{}, nil, &result
	}
	return grok.BrowserConfig{
		Client:      client,
		Engine:      engine,
		Journal:     journal,
		Admission:   gate,
		BuildCommit: a.build.Commit,
	}, store, nil
}

func (a *app) grokReadConfig(
	operation webagent.Operation,
) (grok.ReadConfig, *webagent.Result) {
	stateStore, err := a.stateStore()
	if err != nil {
		result := grok.UnavailableRead(
			a.build.Commit, operation,
			"grok_state_unavailable", "internal",
			"Grok owner-only state is unavailable",
		)
		return grok.ReadConfig{}, &result
	}
	store, err := grok.NewStore(stateStore.Dir)
	if err != nil {
		result := grok.UnavailableRead(
			a.build.Commit, operation,
			"grok_state_unavailable", "internal",
			"Grok owner-only state is unavailable",
		)
		return grok.ReadConfig{}, &result
	}
	gate, err := admission.New(admission.Config{
		StateDir:       stateStore.Dir,
		MinimumSpacing: grok.DefaultAdmissionSpacing,
	})
	if err != nil {
		result := grok.UnavailableRead(
			a.build.Commit, operation,
			"grok_admission_unavailable", "internal",
			"Grok provider admission state is unavailable",
		)
		return grok.ReadConfig{}, &result
	}
	return grok.ReadConfig{
		Store:       store,
		Admission:   gate,
		BuildCommit: a.build.Commit,
		NewRenderedFallback: func(
			ctx context.Context,
		) (grok.RenderedReadConfig, func(context.Context) error, error) {
			if !a.selectHeadedProviderRuntime() {
				return grok.RenderedReadConfig{}, nil, fmt.Errorf(
					"headed browser runtime is required",
				)
			}
			journal, err := browserflow.NewFileJournal(stateStore.Dir)
			if err != nil {
				return grok.RenderedReadConfig{}, nil, err
			}
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return grok.RenderedReadConfig{}, nil, err
			}
			engine, err := browserflow.New(browserflow.Config{
				Client:          client,
				Journal:         journal,
				Budget:          a.browserResourceBudgetOptions(),
				AllowOverBudget: a.opts.allowOverBudget,
				InputLockPath: browserflow.HeadedInputLockPath(
					stateStore.Dir,
				),
			})
			if err != nil {
				_ = closeClient(context.Background())
				return grok.RenderedReadConfig{}, nil, err
			}
			return grok.RenderedReadConfig{
				Client:      client,
				Engine:      engine,
				Journal:     journal,
				BuildCommit: a.build.Commit,
				Timeout:     a.opts.timeout,
			}, closeClient, nil
		},
	}, nil
}

func grokCalibrationUnavailable(
	buildCommit string,
	code string,
	errClass string,
	message string,
) webagent.Result {
	result := grokUnavailableOperation(
		buildCommit,
		webagent.OperationCalibrate,
		code,
		errClass,
		message,
	)
	result.Data = grok.CalibrationStatusData{
		SchemaVersion:    grok.CalibrationStateSchemaVersion,
		State:            "unavailable",
		RecoveryRequired: true,
	}
	return result
}

func grokUnavailableOperation(
	buildCommit string,
	operation webagent.Operation,
	code string,
	errClass string,
	message string,
) webagent.Result {
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderGrok,
		Operation:     operation,
		State:         webagent.StateFailed,
		Stage:         webagent.StagePlanned,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: true,
		},
		Data: map[string]any{
			"schema_version": "grok-unavailable/v1",
		},
		Evidence: webagent.Evidence{
			RunID:       webagent.NewRunID(),
			BuildCommit: buildCommit,
			BrowserMode: "none",
			ReadMode:    "not_started",
		},
		Cleanup: webagent.CleanupEvidence{
			State: webagent.CleanupNotRequired,
		},
		NextCommands: []string{
			"cdp workflow agent grok doctor --json",
		},
	}
}
