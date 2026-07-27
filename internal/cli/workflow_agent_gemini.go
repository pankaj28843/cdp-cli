package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/gemini"
	"github.com/spf13/cobra"
)

type geminiCapabilitiesContract struct {
	webagent.Capabilities
	Runtime gemini.RuntimeStatus `json:"runtime"`
}

func (a *app) geminiCapabilitiesData(
	ctx context.Context,
	capabilities webagent.Capabilities,
) any {
	store, err := a.stateStore()
	if err != nil {
		return geminiCapabilitiesContract{
			Capabilities: capabilities,
			Runtime: gemini.RuntimeStatus{
				SchemaVersion: gemini.RuntimeCapabilitiesSchemaVersion,
				State:         "unavailable",
				StatePath:     gemini.RelativeCapabilitiesPath,
				ModeOptions:   []string{},
				Reason:        "owner-only state directory is unavailable",
			},
		}
	}
	providerStore, err := gemini.NewStore(store.Dir)
	if err != nil {
		return geminiCapabilitiesContract{
			Capabilities: capabilities,
			Runtime: gemini.RuntimeStatus{
				SchemaVersion: gemini.RuntimeCapabilitiesSchemaVersion,
				State:         "unavailable",
				StatePath:     gemini.RelativeCapabilitiesPath,
				ModeOptions:   []string{},
				Reason:        "owner-only runtime capability state is unavailable",
			},
		}
	}
	return geminiCapabilitiesContract{
		Capabilities: capabilities,
		Runtime: providerStore.RuntimeStatus(
			ctx,
			time.Now(),
			gemini.DefaultCapabilitiesTTL,
		),
	}
}

func (a *app) newWorkflowAgentGeminiDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report Gemini readiness from owner-only local evidence",
		Long: "Read owner-only Gemini auth and runtime capability evidence without opening or probing Chrome. " +
			"Browser readiness remains an explicit headed-runtime requirement.",
		Example: "  cdp workflow agent gemini doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			store, err := a.stateStore()
			if err != nil {
				result := gemini.UnavailableDoctor(a.build.Commit)
				return a.renderWebAgentResult(ctx, "gemini doctor: unavailable", result)
			}
			providerStore, err := gemini.NewStore(store.Dir)
			if err != nil {
				result := gemini.UnavailableDoctor(a.build.Commit)
				return a.renderWebAgentResult(ctx, "gemini doctor: unavailable", result)
			}
			result := gemini.Doctor(ctx, providerStore, time.Now(), a.build.Commit)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("gemini doctor: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentGeminiAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect or refresh Gemini auth evidence",
	}
	cmd.AddCommand(a.newWorkflowAgentGeminiAuthRefreshCommand())
	return cmd
}

func (a *app) newWorkflowAgentGeminiAuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh safe Gemini auth evidence from headed Chrome",
		Long: "Open one fresh owned Gemini target, observe only signed-in UI and cookie-name evidence, " +
			"persist safe booleans in owner-only state, and exact-close the target. This never submits a prompt.",
		Example: "  cdp workflow agent gemini auth refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 45*time.Second)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := geminiUnavailableOperation(
					a.build.Commit,
					webagent.OperationAuthRefresh,
					"gemini_headed_browser_required",
					"usage",
					"Gemini auth refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(ctx, "gemini auth: headed browser required", result)
			}
			config, providerStore, unavailable := a.geminiBrowserOperationConfig(
				ctx,
				webagent.OperationAuthRefresh,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "gemini auth: unavailable", *unavailable)
			}
			result := gemini.RefreshAuth(ctx, gemini.AuthRefreshConfig{
				BrowserConfig: config,
				Store:         providerStore,
				Timeout:       30 * time.Second,
			})
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("gemini auth: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentGeminiCapabilitiesRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Observe Gemini runtime modes and tools in headed Chrome",
		Long: "Open one fresh owned Gemini target, observe the unique rendered mode picker and its options, " +
			"persist safe runtime metadata in owner-only state, and exact-close the target. This never submits a prompt.",
		Example: "  cdp workflow agent gemini capabilities refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 45*time.Second)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := geminiUnavailableOperation(
					a.build.Commit,
					webagent.OperationCapabilities,
					"gemini_headed_browser_required",
					"usage",
					"Gemini runtime capability refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"gemini capabilities: headed browser required",
					result,
				)
			}
			config, providerStore, unavailable := a.geminiBrowserOperationConfig(
				ctx,
				webagent.OperationCapabilities,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"gemini capabilities: unavailable",
					*unavailable,
				)
			}
			result := gemini.RefreshCapabilities(ctx, gemini.CapabilityRefreshConfig{
				BrowserConfig: config,
				Store:         providerStore,
				Timeout:       30 * time.Second,
			})
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("gemini capabilities: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentGeminiAskCommand() *cobra.Command {
	var stdin bool
	cmd := &cobra.Command{
		Use:   "ask [PROMPT]",
		Short: "Submit one exact visible Gemini request",
		Long: "Open one fresh headed tab, verify the visible mode, submit the exact prompt with one Send, read the rendered answer, " +
			"preserve the observed conversation ID, and close only that tab.",
		Example: "  cdp workflow agent gemini ask 'Review this design.' --json\n" +
			"  printf '%s' 'Review this diff.' | cdp workflow agent gemini ask --stdin --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 4*time.Minute)
			defer cancel()
			if stdin && len(args) > 0 {
				return commandError(
					"gemini_prompt_source_conflict",
					"usage",
					"Gemini ask accepts either PROMPT or --stdin, not both",
					ExitUsage,
					[]string{"cdp workflow agent gemini ask --stdin --json"},
				)
			}
			prompt := ""
			if stdin {
				data, err := io.ReadAll(
					io.LimitReader(
						cmd.InOrStdin(),
						int64(gemini.MaxPromptCharacters*4+2),
					),
				)
				if err != nil {
					return commandError(
						"gemini_prompt_read_failed",
						"usage",
						"Gemini prompt could not be read from stdin",
						ExitUsage,
						nil,
					)
				}
				prompt = string(data)
			} else if len(args) == 1 {
				prompt = args[0]
			}
			if !a.selectHeadedProviderRuntime() {
				result := geminiUnavailableOperation(
					a.build.Commit,
					webagent.OperationAsk,
					"gemini_headed_browser_required",
					"usage",
					"Gemini ask requires the headed browser runtime",
				)
				return a.renderWebAgentResult(ctx, "gemini ask: headed browser required", result)
			}
			config, providerStore, unavailable := a.geminiBrowserOperationConfig(
				ctx,
				webagent.OperationAsk,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "gemini ask: unavailable", *unavailable)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			result := gemini.Ask(ctx, gemini.AskConfig{
				BrowserConfig: config,
				Store:         providerStore,
				Timeout:       timeout,
			}, prompt)
			human := fmt.Sprintf("gemini ask: %v", result.State)
			if data, ok := result.Data.(gemini.AskData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read the exact prompt from stdin")
	return cmd
}

func (a *app) newWorkflowAgentGeminiConversationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversations",
		Short: "List, read, or await rendered Gemini conversations",
		Long: "Use a fresh exact owned headed target for each rendered read. " +
			"Await observes only the exact conversation and never resubmits a prompt.",
	}
	cmd.AddCommand(a.newWorkflowAgentGeminiConversationsListCommand())
	cmd.AddCommand(a.newWorkflowAgentGeminiConversationsDetailCommand())
	cmd.AddCommand(a.newWorkflowAgentGeminiConversationsAwaitCommand())
	cmd.AddCommand(a.newWorkflowAgentGeminiConversationsDeleteCommand())
	return cmd
}

func (a *app) newWorkflowAgentGeminiConversationsListCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List rendered Gemini Recents",
		Example: "  cdp workflow agent gemini conversations list --limit 30 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 45*time.Second)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := geminiUnavailableOperation(
					a.build.Commit,
					webagent.OperationConversationsList,
					"gemini_headed_browser_required",
					"usage",
					"Gemini conversation list requires the headed browser runtime",
				)
				return a.renderWebAgentResult(ctx, "gemini conversations: headed browser required", result)
			}
			config, providerStore, unavailable := a.geminiBrowserOperationConfig(
				ctx,
				webagent.OperationConversationsList,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "gemini conversations: unavailable", *unavailable)
			}
			result := gemini.ListConversations(ctx, gemini.ReadConfig{
				BrowserConfig: config,
				Store:         providerStore,
				Timeout:       30 * time.Second,
			}, limit)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("gemini conversations: %v", result.State),
				result,
			)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 30, "maximum rendered conversations to return (0-100)")
	return cmd
}

func (a *app) newWorkflowAgentGeminiConversationsDetailCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "detail CONVERSATION_ID",
		Short:   "Read one exact rendered Gemini conversation",
		Example: "  cdp workflow agent gemini conversations detail <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 45*time.Second)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := geminiUnavailableOperation(
					a.build.Commit,
					webagent.OperationConversationsDetail,
					"gemini_headed_browser_required",
					"usage",
					"Gemini conversation detail requires the headed browser runtime",
				)
				return a.renderWebAgentResult(ctx, "gemini detail: headed browser required", result)
			}
			config, providerStore, unavailable := a.geminiBrowserOperationConfig(
				ctx,
				webagent.OperationConversationsDetail,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "gemini detail: unavailable", *unavailable)
			}
			result := gemini.DetailConversation(ctx, gemini.ReadConfig{
				BrowserConfig: config,
				Store:         providerStore,
				Timeout:       30 * time.Second,
			}, args[0])
			human := fmt.Sprintf("gemini detail: %v", result.State)
			if data, ok := result.Data.(gemini.ConversationDetailData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
}

func (a *app) newWorkflowAgentGeminiConversationsAwaitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "await CONVERSATION_ID",
		Short: "Await one exact Gemini conversation without resubmitting",
		Long: "Observe only the rendered exact conversation route until a non-streaming answer appears or the deadline expires. " +
			"This command never submits a prompt.",
		Example: "  cdp --timeout 3m workflow agent gemini conversations await <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 3*time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := geminiUnavailableOperation(
					a.build.Commit,
					webagent.OperationConversationsAwait,
					"gemini_headed_browser_required",
					"usage",
					"Gemini conversation await requires the headed browser runtime",
				)
				return a.renderWebAgentResult(ctx, "gemini await: headed browser required", result)
			}
			config, providerStore, unavailable := a.geminiBrowserOperationConfig(
				ctx,
				webagent.OperationConversationsAwait,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "gemini await: unavailable", *unavailable)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			result := gemini.AwaitConversation(ctx, gemini.ReadConfig{
				BrowserConfig: config,
				Store:         providerStore,
				Timeout:       timeout,
			}, args[0])
			human := fmt.Sprintf("gemini await: %v", result.State)
			if data, ok := result.Data.(gemini.ConversationDetailData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
}

func (a *app) newWorkflowAgentGeminiConversationsDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete CONVERSATION_ID",
		Short: "Visibly delete one exact Gemini conversation",
		Long: "Own one fresh headed target, prepare the exact Gemini confirmation dialog, dispatch one raw-input confirmation, " +
			"prove the same-target /app postcondition, and exact-close the target.",
		Example: "  cdp workflow agent gemini conversations delete <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := geminiUnavailableOperation(
					a.build.Commit,
					webagent.OperationConversationsDelete,
					"gemini_headed_browser_required",
					"usage",
					"Gemini conversation delete requires the headed browser runtime",
				)
				return a.renderWebAgentResult(ctx, "gemini delete: headed browser required", result)
			}
			config, providerStore, unavailable := a.geminiBrowserOperationConfig(
				ctx,
				webagent.OperationConversationsDelete,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "gemini delete: unavailable", *unavailable)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 45 * time.Second
			}
			result := gemini.DeleteConversation(ctx, gemini.DeleteConfig{
				BrowserConfig: config,
				Store:         providerStore,
				Timeout:       timeout,
			}, args[0])
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("gemini delete: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) geminiBrowserOperationConfig(
	ctx context.Context,
	operation webagent.Operation,
) (gemini.BrowserConfig, *gemini.Store, *webagent.Result) {
	store, err := a.stateStore()
	if err != nil {
		result := geminiUnavailableOperation(
			a.build.Commit,
			operation,
			"gemini_state_unavailable",
			"internal",
			"Gemini owner-only state is unavailable",
		)
		return gemini.BrowserConfig{}, nil, &result
	}
	providerStore, err := gemini.NewStore(store.Dir)
	if err != nil {
		result := geminiUnavailableOperation(
			a.build.Commit,
			operation,
			"gemini_state_unavailable",
			"internal",
			"Gemini owner-only state is unavailable",
		)
		return gemini.BrowserConfig{}, nil, &result
	}
	journal, err := browserflow.NewFileJournal(store.Dir)
	if err != nil {
		result := geminiUnavailableOperation(
			a.build.Commit,
			operation,
			"gemini_lifecycle_state_unavailable",
			"internal",
			"Gemini exact-target lifecycle state is unavailable",
		)
		return gemini.BrowserConfig{}, nil, &result
	}
	client, _, err := a.browserCDPClient(ctx)
	if err != nil {
		result := geminiUnavailableOperation(
			a.build.Commit,
			operation,
			"gemini_browser_unavailable",
			"connection",
			"Gemini headed browser runtime is unavailable",
		)
		return gemini.BrowserConfig{}, nil, &result
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:          client,
		Journal:         journal,
		Budget:          a.browserResourceBudgetOptions(),
		AllowOverBudget: a.opts.allowOverBudget,
		InputLockPath:   browserflow.HeadedInputLockPath(store.Dir),
	})
	if err != nil {
		result := geminiUnavailableOperation(
			a.build.Commit,
			operation,
			"gemini_browserflow_unavailable",
			"internal",
			"Gemini exact-target browser transaction is unavailable",
		)
		return gemini.BrowserConfig{}, nil, &result
	}
	return gemini.BrowserConfig{
		Client:      client,
		Engine:      engine,
		Journal:     journal,
		BuildCommit: a.build.Commit,
	}, providerStore, nil
}

func geminiUnavailableOperation(
	buildCommit string,
	operation webagent.Operation,
	code string,
	errClass string,
	message string,
) webagent.Result {
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderGemini,
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
			"schema_version": "gemini-unavailable/v1",
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
		NextCommands: []string{"cdp workflow agent gemini doctor --json"},
	}
}
