package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/tripadvisor"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowAgentTripadvisorDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report Tripadvisor readiness from safe local evidence",
		Long: "Read owner-only rendered Tripadvisor session evidence without opening or probing Chrome. " +
			"Anonymous and signed-in operational sessions are reported honestly.",
		Example: "  cdp workflow agent tripadvisor doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			store, err := a.stateStore()
			if err != nil {
				result := tripadvisor.UnavailableDoctor(a.build.Commit)
				return a.renderWebAgentResult(
					ctx,
					"tripadvisor doctor: unavailable",
					result,
				)
			}
			providerStore, err := tripadvisor.NewStore(store.Dir)
			if err != nil {
				result := tripadvisor.UnavailableDoctor(a.build.Commit)
				return a.renderWebAgentResult(
					ctx,
					"tripadvisor doctor: unavailable",
					result,
				)
			}
			result := tripadvisor.Doctor(
				ctx,
				providerStore,
				time.Now(),
				a.build.Commit,
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("tripadvisor doctor: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentTripadvisorAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Refresh safe Tripadvisor rendered-session evidence",
	}
	cmd.AddCommand(a.newWorkflowAgentTripadvisorAuthRefreshCommand())
	return cmd
}

func (a *app) newWorkflowAgentTripadvisorAuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Observe Tripadvisor AI readiness in headed Chrome",
		Long: "Open one fresh owned Tripadvisor target, observe the rendered AI panel, composer, history control, " +
			"and honest anonymous-or-signed-in mode, persist only safe booleans, and exact-close the target. This never submits a prompt.",
		Example: "  cdp workflow agent tripadvisor auth refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(
				cmd,
				45*time.Second,
			)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := tripadvisorUnavailableOperation(
					a.build.Commit,
					webagent.OperationAuthRefresh,
					"tripadvisor_headed_browser_required",
					"usage",
					"Tripadvisor auth refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"tripadvisor auth: headed browser required",
					result,
				)
			}
			config, providerStore, unavailable :=
				a.tripadvisorBrowserOperationConfig(
					ctx,
					webagent.OperationAuthRefresh,
				)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"tripadvisor auth: unavailable",
					*unavailable,
				)
			}
			result := tripadvisor.RefreshAuth(
				ctx,
				tripadvisor.AuthRefreshConfig{
					BrowserConfig: config,
					Store:         providerStore,
					Timeout:       30 * time.Second,
				},
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("tripadvisor auth: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentTripadvisorAskCommand() *cobra.Command {
	var stdin bool
	cmd := &cobra.Command{
		Use:   "ask [PROMPT]",
		Short: "Submit one exact visible Tripadvisor request",
		Long: "Start one fresh Tripadvisor conversation in one fresh exact owned headed target, verify the exact prompt and unique Send control, " +
			"persist action_pending, click Send once, acknowledge the same-target UUID route, and read a stable rendered answer without resubmitting.",
		Example: "  cdp workflow agent tripadvisor ask 'Compare two rainy-day options.' --json\n" +
			"  printf '%s' 'Critique this itinerary.' | cdp workflow agent tripadvisor ask --stdin --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(
				cmd,
				4*time.Minute,
			)
			defer cancel()
			if stdin && len(args) > 0 {
				return commandError(
					"tripadvisor_prompt_source_conflict",
					"usage",
					"Tripadvisor ask accepts either PROMPT or --stdin, not both",
					ExitUsage,
					[]string{
						"cdp workflow agent tripadvisor ask --stdin --json",
					},
				)
			}
			prompt := ""
			if stdin {
				encoded, err := io.ReadAll(
					io.LimitReader(
						cmd.InOrStdin(),
						int64(tripadvisor.MaxPromptCharacters*4+2),
					),
				)
				if err != nil {
					return commandError(
						"tripadvisor_prompt_read_failed",
						"usage",
						"Tripadvisor prompt could not be read from stdin",
						ExitUsage,
						nil,
					)
				}
				prompt = string(encoded)
			} else if len(args) == 1 {
				prompt = args[0]
			}
			if !a.selectHeadedProviderRuntime() {
				result := tripadvisorUnavailableOperation(
					a.build.Commit,
					webagent.OperationAsk,
					"tripadvisor_headed_browser_required",
					"usage",
					"Tripadvisor ask requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"tripadvisor ask: headed browser required",
					result,
				)
			}
			config, providerStore, unavailable :=
				a.tripadvisorBrowserOperationConfig(
					ctx,
					webagent.OperationAsk,
				)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"tripadvisor ask: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			result := tripadvisor.Ask(
				ctx,
				tripadvisor.AskConfig{
					BrowserConfig: config,
					Store:         providerStore,
					Timeout:       timeout,
				},
				prompt,
			)
			human := fmt.Sprintf(
				"tripadvisor ask: %v",
				result.State,
			)
			if askData, ok := result.Data.(tripadvisor.AskData); ok &&
				askData.Text != "" {
				human = askData.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().BoolVar(
		&stdin,
		"stdin",
		false,
		"read the exact prompt from stdin",
	)
	return cmd
}

func (a *app) newWorkflowAgentTripadvisorConversationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversations",
		Short: "List, read, or await rendered Tripadvisor conversations",
		Long: "Use a fresh exact owned headed target for each rendered read. " +
			"History entries without a rendered UUID identity are omitted instead of guessed.",
	}
	cmd.AddCommand(a.newWorkflowAgentTripadvisorConversationsListCommand())
	cmd.AddCommand(a.newWorkflowAgentTripadvisorConversationsDetailCommand())
	cmd.AddCommand(a.newWorkflowAgentTripadvisorConversationsAwaitCommand())
	return cmd
}

func (a *app) newWorkflowAgentTripadvisorConversationsListCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List only identity-proven Tripadvisor chat entries",
		Example: "  cdp workflow agent tripadvisor conversations list --limit 30 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(
				cmd,
				45*time.Second,
			)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := tripadvisorUnavailableOperation(
					a.build.Commit,
					webagent.OperationConversationsList,
					"tripadvisor_headed_browser_required",
					"usage",
					"Tripadvisor conversation list requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"tripadvisor conversations: headed browser required",
					result,
				)
			}
			config, providerStore, unavailable :=
				a.tripadvisorBrowserOperationConfig(
					ctx,
					webagent.OperationConversationsList,
				)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"tripadvisor conversations: unavailable",
					*unavailable,
				)
			}
			result := tripadvisor.ListConversations(
				ctx,
				tripadvisor.ReadConfig{
					BrowserConfig: config,
					Store:         providerStore,
					Timeout:       30 * time.Second,
				},
				limit,
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf(
					"tripadvisor conversations: %v",
					result.State,
				),
				result,
			)
		},
	}
	cmd.Flags().IntVar(
		&limit,
		"limit",
		30,
		"maximum identity-proven conversations to return (0-100)",
	)
	return cmd
}

func (a *app) newWorkflowAgentTripadvisorConversationsDetailCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "detail CONVERSATION_ID",
		Short:   "Read one exact rendered Tripadvisor conversation",
		Example: "  cdp workflow agent tripadvisor conversations detail <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runTripadvisorConversationRead(
				cmd,
				args[0],
				webagent.OperationConversationsDetail,
			)
		},
	}
}

func (a *app) newWorkflowAgentTripadvisorConversationsAwaitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "await CONVERSATION_ID",
		Short: "Await one exact Tripadvisor conversation without resubmitting",
		Long: "Observe only the rendered exact UUID route until a stable answer appears or the deadline expires. " +
			"This command never submits a prompt.",
		Example: "  cdp --timeout 3m workflow agent tripadvisor conversations await <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runTripadvisorConversationRead(
				cmd,
				args[0],
				webagent.OperationConversationsAwait,
			)
		},
	}
}

func (a *app) runTripadvisorConversationRead(
	cmd *cobra.Command,
	conversationID string,
	operation webagent.Operation,
) error {
	defaultTimeout := 45 * time.Second
	if operation == webagent.OperationConversationsAwait {
		defaultTimeout = 3 * time.Minute
	}
	ctx, cancel := a.commandContextWithDefault(cmd, defaultTimeout)
	defer cancel()
	if !a.selectHeadedProviderRuntime() {
		result := tripadvisorUnavailableOperation(
			a.build.Commit,
			operation,
			"tripadvisor_headed_browser_required",
			"usage",
			"Tripadvisor conversation read requires the headed browser runtime",
		)
		return a.renderWebAgentResult(
			ctx,
			"tripadvisor conversation: headed browser required",
			result,
		)
	}
	config, providerStore, unavailable :=
		a.tripadvisorBrowserOperationConfig(ctx, operation)
	if unavailable != nil {
		return a.renderWebAgentResult(
			ctx,
			"tripadvisor conversation: unavailable",
			*unavailable,
		)
	}
	timeout := a.opts.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	readConfig := tripadvisor.ReadConfig{
		BrowserConfig: config,
		Store:         providerStore,
		Timeout:       timeout,
	}
	var result webagent.Result
	if operation == webagent.OperationConversationsAwait {
		result = tripadvisor.AwaitConversation(
			ctx,
			readConfig,
			conversationID,
		)
	} else {
		result = tripadvisor.DetailConversation(
			ctx,
			readConfig,
			conversationID,
		)
	}
	human := fmt.Sprintf("tripadvisor conversation: %v", result.State)
	if detail, ok := result.Data.(tripadvisor.ConversationDetailData); ok &&
		detail.Text != "" {
		human = detail.Text
	}
	return a.renderWebAgentResult(ctx, human, result)
}

func (a *app) tripadvisorBrowserOperationConfig(
	ctx context.Context,
	operation webagent.Operation,
) (
	tripadvisor.BrowserConfig,
	*tripadvisor.Store,
	*webagent.Result,
) {
	store, err := a.stateStore()
	if err != nil {
		result := tripadvisorUnavailableOperation(
			a.build.Commit,
			operation,
			"tripadvisor_state_unavailable",
			"internal",
			"Tripadvisor owner-only state is unavailable",
		)
		return tripadvisor.BrowserConfig{}, nil, &result
	}
	providerStore, err := tripadvisor.NewStore(store.Dir)
	if err != nil {
		result := tripadvisorUnavailableOperation(
			a.build.Commit,
			operation,
			"tripadvisor_state_unavailable",
			"internal",
			"Tripadvisor owner-only state is unavailable",
		)
		return tripadvisor.BrowserConfig{}, nil, &result
	}
	journal, err := browserflow.NewFileJournal(store.Dir)
	if err != nil {
		result := tripadvisorUnavailableOperation(
			a.build.Commit,
			operation,
			"tripadvisor_recovery_unavailable",
			"internal",
			"Tripadvisor exact-target recovery state is unavailable",
		)
		return tripadvisor.BrowserConfig{}, nil, &result
	}
	gate, err := admission.New(admission.Config{
		StateDir:       store.Dir,
		MinimumSpacing: tripadvisor.DefaultAdmissionSpacing,
	})
	if err != nil {
		result := tripadvisorUnavailableOperation(
			a.build.Commit,
			operation,
			"tripadvisor_admission_unavailable",
			"internal",
			"Tripadvisor provider admission state is unavailable",
		)
		return tripadvisor.BrowserConfig{}, nil, &result
	}
	client, _, err := a.browserCDPClient(ctx)
	if err != nil {
		result := tripadvisorUnavailableOperation(
			a.build.Commit,
			operation,
			"tripadvisor_browser_unavailable",
			"connection",
			"Tripadvisor headed browser runtime is unavailable",
		)
		return tripadvisor.BrowserConfig{}, nil, &result
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:          client,
		Journal:         journal,
		Budget:          a.browserResourceBudgetOptions(),
		AllowOverBudget: a.opts.allowOverBudget,
		InputLockPath:   browserflow.HeadedInputLockPath(store.Dir),
	})
	if err != nil {
		result := tripadvisorUnavailableOperation(
			a.build.Commit,
			operation,
			"tripadvisor_browserflow_unavailable",
			"internal",
			"Tripadvisor exact-target browser transaction is unavailable",
		)
		return tripadvisor.BrowserConfig{}, nil, &result
	}
	return tripadvisor.BrowserConfig{
		Client:      client,
		Engine:      engine,
		Journal:     journal,
		Admission:   gate,
		BuildCommit: a.build.Commit,
	}, providerStore, nil
}

func tripadvisorUnavailableOperation(
	buildCommit string,
	operation webagent.Operation,
	code string,
	errClass string,
	message string,
) webagent.Result {
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderTripadvisor,
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
			"schema_version": "tripadvisor-unavailable/v1",
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
			"cdp workflow agent tripadvisor doctor --json",
		},
	}
}
