package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/perplexity"
	"github.com/spf13/cobra"
)

type perplexityCapabilitiesContract struct {
	webagent.Capabilities
	Runtime perplexity.RuntimeStatus `json:"runtime"`
}

func (a *app) perplexityCapabilitiesData(
	ctx context.Context,
	capabilities webagent.Capabilities,
) any {
	stateStore, err := a.stateStore()
	if err != nil {
		return perplexityCapabilitiesContract{
			Capabilities: capabilities,
			Runtime: perplexity.RuntimeStatus{
				SchemaVersion: perplexity.RuntimeCapabilitiesSchemaVersion,
				State:         "unavailable",
				StatePath:     perplexity.RelativeCapabilitiesPath,
				Capabilities:  []perplexity.ComposerCapability{},
				Reason:        "owner-only state directory is unavailable",
			},
		}
	}
	store, err := perplexity.NewStore(stateStore.Dir)
	if err != nil {
		return perplexityCapabilitiesContract{
			Capabilities: capabilities,
			Runtime: perplexity.RuntimeStatus{
				SchemaVersion: perplexity.RuntimeCapabilitiesSchemaVersion,
				State:         "unavailable",
				StatePath:     perplexity.RelativeCapabilitiesPath,
				Capabilities:  []perplexity.ComposerCapability{},
				Reason:        "owner-only runtime capability state is unavailable",
			},
		}
	}
	return perplexityCapabilitiesContract{
		Capabilities: capabilities,
		Runtime: store.RuntimeStatus(
			ctx,
			time.Now(),
			perplexity.DefaultCapabilitiesTTL,
		),
	}
}

func (a *app) newWorkflowAgentPerplexityDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report Perplexity readiness from owner-only local evidence",
		Long: "Read owner-only Perplexity auth and runtime capability evidence without opening or probing Chrome. " +
			"Browser readiness remains an explicit headed-runtime requirement.",
		Example: "  cdp workflow agent perplexity doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"perplexity doctor: unavailable",
					perplexity.UnavailableDoctor(a.build.Commit),
				)
			}
			store, err := perplexity.NewStore(stateStore.Dir)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"perplexity doctor: unavailable",
					perplexity.UnavailableDoctor(a.build.Commit),
				)
			}
			result := perplexity.Doctor(
				ctx,
				store,
				time.Now(),
				a.build.Commit,
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("perplexity doctor: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentPerplexityAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect or refresh Perplexity auth evidence",
	}
	cmd.AddCommand(a.newWorkflowAgentPerplexityAuthRefreshCommand())
	return cmd
}

func (a *app) newWorkflowAgentPerplexityAuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh Perplexity browser-observed request evidence",
		Long: "Open one fresh owned Perplexity target, observe the signed-in conversation-list request, " +
			"refresh owner-only replay state, and exact-close the target without creating a conversation.",
		Example: "  cdp workflow agent perplexity auth refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := perplexityUnavailableOperation(
					a.build.Commit,
					webagent.OperationAuthRefresh,
					"perplexity_headed_browser_required",
					"usage",
					"Perplexity auth refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"perplexity auth: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.perplexityBrowserOperationConfig(
				ctx,
				webagent.OperationAuthRefresh,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"perplexity auth: unavailable",
					*unavailable,
				)
			}
			result := perplexity.RefreshAuth(ctx, perplexity.AuthRefreshConfig{
				BrowserConfig: config,
				Store:         store,
			})
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("perplexity auth: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentPerplexityCapabilitiesRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Observe Perplexity composer capabilities in headed Chrome",
		Long: "Open one fresh owned Perplexity target, observe the provider-owned v2 model configuration when present, " +
			"persist only sanitized capability labels, and exact-close the target.",
		Example: "  cdp workflow agent perplexity capabilities refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := perplexityUnavailableOperation(
					a.build.Commit,
					webagent.OperationCapabilities,
					"perplexity_headed_browser_required",
					"usage",
					"Perplexity capability refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"perplexity capabilities: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.perplexityBrowserOperationConfig(
				ctx,
				webagent.OperationCapabilities,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"perplexity capabilities: unavailable",
					*unavailable,
				)
			}
			result := perplexity.RefreshCapabilities(
				ctx,
				perplexity.CapabilityRefreshConfig{
					BrowserConfig: config,
					Store:         store,
				},
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("perplexity capabilities: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentPerplexityAskCommand() *cobra.Command {
	var stdin bool
	cmd := &cobra.Command{
		Use:   "ask [PROMPT]",
		Short: "Submit one exact visible Perplexity request",
		Long: "Open one fresh headed tab, verify Search mode, submit the exact prompt with one Send, read the answer, " +
			"preserve the observed conversation ID, and close only that tab.",
		Example: "  printf '%s' 'Review this implementation.' | cdp workflow agent perplexity ask --stdin --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 4*time.Minute)
			defer cancel()
			if stdin && len(args) > 0 {
				return commandError(
					"perplexity_prompt_source_conflict",
					"usage",
					"Perplexity ask accepts either PROMPT or --stdin, not both",
					ExitUsage,
					[]string{"cdp workflow agent perplexity ask --stdin --json"},
				)
			}
			prompt := ""
			if stdin {
				data, err := io.ReadAll(
					io.LimitReader(
						cmd.InOrStdin(),
						int64(perplexity.MaxPromptCharacters*4+2),
					),
				)
				if err != nil {
					return commandError(
						"perplexity_prompt_read_failed",
						"usage",
						"Perplexity prompt could not be read from stdin",
						ExitUsage,
						nil,
					)
				}
				prompt = string(data)
			} else if len(args) == 1 {
				prompt = args[0]
			}
			if !a.selectHeadedProviderRuntime() {
				result := perplexityUnavailableOperation(
					a.build.Commit,
					webagent.OperationAsk,
					"perplexity_headed_browser_required",
					"usage",
					"Perplexity ask requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"perplexity ask: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.perplexityBrowserOperationConfig(
				ctx,
				webagent.OperationAsk,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"perplexity ask: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			result := perplexity.Ask(ctx, perplexity.AskConfig{
				BrowserConfig: config,
				Store:         store,
				Timeout:       timeout,
			}, prompt)
			human := fmt.Sprintf("perplexity ask: %v", result.State)
			if data, ok := result.Data.(perplexity.AskData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read the exact prompt from stdin")
	return cmd
}

func (a *app) newWorkflowAgentPerplexityConversationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversations",
		Short: "List, read, await, or delete Perplexity conversations",
		Long: "Attempt browser-observed candidate HTTP for stored reads, with one fresh exact headed fallback when browser context is required. " +
			"Await never resubmits a prompt.",
	}
	cmd.AddCommand(a.newWorkflowAgentPerplexityConversationsListCommand())
	cmd.AddCommand(a.newWorkflowAgentPerplexityConversationsDetailCommand())
	cmd.AddCommand(a.newWorkflowAgentPerplexityConversationsAwaitCommand())
	cmd.AddCommand(a.newWorkflowAgentPerplexityConversationsDeleteCommand())
	return cmd
}

func (a *app) newWorkflowAgentPerplexityConversationsListCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List stored Perplexity conversations",
		Example: "  cdp workflow agent perplexity conversations list --limit 20 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			config, unavailable := a.perplexityReadConfig(
				webagent.OperationConversationsList,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"perplexity conversations: unavailable",
					*unavailable,
				)
			}
			result := perplexity.ListConversations(ctx, config, limit)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("perplexity conversations: %v", result.State),
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

func (a *app) newWorkflowAgentPerplexityConversationsDetailCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "detail CONVERSATION_ID",
		Short:   "Read one exact stored Perplexity conversation",
		Example: "  cdp workflow agent perplexity conversations detail <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			config, unavailable := a.perplexityReadConfig(
				webagent.OperationConversationsDetail,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"perplexity detail: unavailable",
					*unavailable,
				)
			}
			result := perplexity.DetailConversation(ctx, config, args[0])
			human := fmt.Sprintf("perplexity detail: %v", result.State)
			if data, ok := result.Data.(perplexity.ConversationDetailData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
}

func (a *app) newWorkflowAgentPerplexityConversationsAwaitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "await CONVERSATION_ID",
		Short: "Await one exact stored Perplexity conversation without resubmitting",
		Long: "Read only the exact identified conversation until its stored assistant response is terminal or the bounded deadline expires. " +
			"This command never submits a prompt.",
		Example: "  cdp --timeout 3m workflow agent perplexity conversations await <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 3*time.Minute)
			defer cancel()
			config, unavailable := a.perplexityReadConfig(
				webagent.OperationConversationsAwait,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"perplexity await: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			result := perplexity.AwaitConversation(
				ctx,
				config,
				args[0],
				timeout,
			)
			human := fmt.Sprintf("perplexity await: %v", result.State)
			if data, ok := result.Data.(perplexity.ConversationDetailData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
}

func (a *app) newWorkflowAgentPerplexityConversationsDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete CONVERSATION_ID",
		Short: "Visibly delete one exact Perplexity conversation",
		Long: "Own one fresh headed target, open the exact conversation menu, dispatch one raw-input Delete confirmation, " +
			"prove the same-target home redirect, and exact-close the target.",
		Example: "  cdp workflow agent perplexity conversations delete <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := perplexityUnavailableOperation(
					a.build.Commit,
					webagent.OperationConversationsDelete,
					"perplexity_headed_browser_required",
					"usage",
					"Perplexity conversation delete requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"perplexity delete: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.perplexityBrowserOperationConfig(
				ctx,
				webagent.OperationConversationsDelete,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"perplexity delete: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 45 * time.Second
			}
			result := perplexity.DeleteConversation(ctx, perplexity.DeleteConfig{
				BrowserConfig: config,
				Store:         store,
				Timeout:       timeout,
			}, args[0])
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("perplexity delete: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) perplexityBrowserOperationConfig(
	ctx context.Context,
	operation webagent.Operation,
) (perplexity.BrowserConfig, *perplexity.Store, *webagent.Result) {
	stateStore, err := a.stateStore()
	if err != nil {
		result := perplexityUnavailableOperation(
			a.build.Commit, operation,
			"perplexity_state_unavailable", "internal",
			"Perplexity owner-only state is unavailable",
		)
		return perplexity.BrowserConfig{}, nil, &result
	}
	store, err := perplexity.NewStore(stateStore.Dir)
	if err != nil {
		result := perplexityUnavailableOperation(
			a.build.Commit, operation,
			"perplexity_state_unavailable", "internal",
			"Perplexity owner-only state is unavailable",
		)
		return perplexity.BrowserConfig{}, nil, &result
	}
	journal, err := browserflow.NewFileJournal(stateStore.Dir)
	if err != nil {
		result := perplexityUnavailableOperation(
			a.build.Commit, operation,
			"perplexity_lifecycle_state_unavailable", "internal",
			"Perplexity exact-target lifecycle state is unavailable",
		)
		return perplexity.BrowserConfig{}, nil, &result
	}
	client, _, err := a.browserEventCDPClient(ctx)
	if err != nil {
		result := perplexityUnavailableOperation(
			a.build.Commit, operation,
			"perplexity_browser_unavailable", "connection",
			"Perplexity headed browser runtime is unavailable",
		)
		return perplexity.BrowserConfig{}, nil, &result
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:          client,
		Journal:         journal,
		Budget:          a.browserResourceBudgetOptions(),
		AllowOverBudget: a.opts.allowOverBudget,
		InputLockPath:   browserflow.HeadedInputLockPath(stateStore.Dir),
	})
	if err != nil {
		result := perplexityUnavailableOperation(
			a.build.Commit, operation,
			"perplexity_browserflow_unavailable", "internal",
			"Perplexity exact-target browser transaction is unavailable",
		)
		return perplexity.BrowserConfig{}, nil, &result
	}
	return perplexity.BrowserConfig{
		Client:      client,
		Engine:      engine,
		Journal:     journal,
		BuildCommit: a.build.Commit,
	}, store, nil
}

func (a *app) perplexityReadConfig(
	operation webagent.Operation,
) (perplexity.ReadConfig, *webagent.Result) {
	stateStore, err := a.stateStore()
	if err != nil {
		result := perplexity.UnavailableRead(
			a.build.Commit, operation,
			"perplexity_state_unavailable", "internal",
			"Perplexity owner-only state is unavailable",
		)
		return perplexity.ReadConfig{}, &result
	}
	store, err := perplexity.NewStore(stateStore.Dir)
	if err != nil {
		result := perplexity.UnavailableRead(
			a.build.Commit, operation,
			"perplexity_state_unavailable", "internal",
			"Perplexity owner-only state is unavailable",
		)
		return perplexity.ReadConfig{}, &result
	}
	return perplexity.ReadConfig{
		Store:       store,
		BuildCommit: a.build.Commit,
		NewRenderedFallback: func(
			ctx context.Context,
		) (perplexity.RenderedReadConfig, func(context.Context) error, error) {
			if !a.selectHeadedProviderRuntime() {
				return perplexity.RenderedReadConfig{}, nil, fmt.Errorf(
					"headed browser runtime is required",
				)
			}
			journal, err := browserflow.NewFileJournal(stateStore.Dir)
			if err != nil {
				return perplexity.RenderedReadConfig{}, nil, err
			}
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return perplexity.RenderedReadConfig{}, nil, err
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
				return perplexity.RenderedReadConfig{}, nil, err
			}
			return perplexity.RenderedReadConfig{
				Client:      client,
				Engine:      engine,
				Journal:     journal,
				BuildCommit: a.build.Commit,
				Timeout:     a.opts.timeout,
			}, closeClient, nil
		},
	}, nil
}

func perplexityUnavailableOperation(
	buildCommit string,
	operation webagent.Operation,
	code string,
	errClass string,
	message string,
) webagent.Result {
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderPerplexity,
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
			"schema_version": "perplexity-unavailable/v1",
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
			"cdp workflow agent perplexity doctor --json",
		},
	}
}
