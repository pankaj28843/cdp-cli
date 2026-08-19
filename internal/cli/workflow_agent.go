package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/claude"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowAgentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run authenticated web-agent provider workflows",
		Long: "Expose capability-backed authenticated provider workflows behind one stable operation envelope. " +
			"Provider browser mechanics live below the CLI boundary; capability metadata never probes Chrome.\n\n" +
			"Google rendered search AI mode is configured as a machine policy and used by " +
			"`workflow web-research serp`: leave `agents.google.exclusive_ai_mode` unset or false " +
			"for inline AI Overview expansion on corporate/Zscaler browsers, or set it true " +
			"for exclusive Google AI Mode navigation on a non-isolated machine. An explicit " +
			"`--google-ai auto|mode|off` overrides the config for one run.",
		Example: "  cdp workflow agent providers --json\n" +
			"  cdp workflow agent claude capabilities --json\n" +
			"  cdp workflow agent gemini capabilities --json\n" +
			"  cdp --config cdp.json workflow web-research serp --query-file tmp/queries.txt --serp google --json\n" +
			"  cdp workflow web-research serp --query-file tmp/queries.txt --serp google --google-ai auto --json\n" +
			"  cdp workflow web-research serp --query-file tmp/queries.txt --serp google --google-ai mode --json\n" +
			"  cdp schema webagent-operation --json",
	}
	cmd.AddCommand(a.newWorkflowAgentProvidersCommand())
	cmd.AddCommand(a.newWorkflowAgentAggregateAuthCommand())
	cmd.AddCommand(a.newWorkflowAgentAggregateCapabilitiesCommand())
	for _, provider := range webagent.Providers() {
		cmd.AddCommand(a.newWorkflowAgentProviderCommand(provider))
	}
	return cmd
}

func (a *app) newWorkflowAgentProvidersCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List provider workflow capabilities without probing Chrome",
		Long: "List every provider and operation in the installed contract. " +
			"Planned and unsupported operations remain explicit and are never treated as callable.",
		Example: "  cdp workflow agent providers --json\n" +
			"  cdp workflow agent providers --jq '.data.providers[] | {provider, implementation_status}'",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			catalog := webagent.Catalog()
			result := webagent.NewMetadataResult(
				webagent.ProviderCatalog,
				webagent.OperationProviders,
				catalog,
				a.build.Commit,
				[]string{
					"cdp schema webagent-operation --json",
					"cdp schema webagent-capabilities --json",
					"cdp describe --command 'workflow agent' --json",
				},
			)
			lines := make([]string, 0, len(catalog.Providers))
			for _, provider := range catalog.Providers {
				lines = append(lines, fmt.Sprintf("%s\t%s", provider.Provider, provider.ImplementationStatus))
			}
			return a.renderWebAgentResult(ctx, strings.Join(lines, "\n"), result)
		},
	}
}

func (a *app) newWorkflowAgentProviderCommand(provider webagent.Provider) *cobra.Command {
	capabilities, _ := webagent.CapabilitiesFor(provider)
	cmd := &cobra.Command{
		Use:   string(provider),
		Short: fmt.Sprintf("Run %s workflows", capabilities.DisplayName),
	}
	cmd.AddCommand(a.newWorkflowAgentCapabilitiesCommand(provider))
	if provider == webagent.ProviderAlex {
		cmd.AddCommand(a.newWorkflowAgentAlexDoctorCommand())
		cmd.AddCommand(a.newWorkflowAgentAlexAuthCommand())
		cmd.AddCommand(a.newWorkflowAgentAlexCatalogCommand())
		cmd.AddCommand(a.newWorkflowAgentAlexCoursesCommand())
		cmd.AddCommand(a.newWorkflowAgentAlexChaptersCommand())
		cmd.AddCommand(a.newWorkflowAgentAlexContentCommand())
		cmd.AddCommand(a.newWorkflowAgentAlexAskCommand())
	}
	if provider == webagent.ProviderChatGPT {
		cmd.AddCommand(a.newWorkflowAgentChatGPTDoctorCommand())
		cmd.AddCommand(a.newWorkflowAgentChatGPTAuthCommand())
		cmd.AddCommand(a.newWorkflowAgentChatGPTTranscribeCommand())
		cmd.AddCommand(a.newWorkflowAgentChatGPTAskCommand())
		cmd.AddCommand(a.newWorkflowAgentChatGPTResearchCommand())
		cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsCommand())
	}
	if provider == webagent.ProviderM365 {
		cmd.AddCommand(a.newWorkflowAgentM365DoctorCommand())
		cmd.AddCommand(a.newWorkflowAgentM365AuthCommand())
		cmd.AddCommand(a.newWorkflowAgentM365TranscribeCommand())
	}
	if provider == webagent.ProviderClaude {
		cmd.AddCommand(a.newWorkflowAgentClaudeDoctorCommand())
		cmd.AddCommand(a.newWorkflowAgentClaudeAuthCommand())
		cmd.AddCommand(a.newWorkflowAgentClaudeAskCommand())
		cmd.AddCommand(a.newWorkflowAgentClaudeConversationsCommand())
	}
	if provider == webagent.ProviderGemini {
		cmd.AddCommand(a.newWorkflowAgentGeminiDoctorCommand())
		cmd.AddCommand(a.newWorkflowAgentGeminiAuthCommand())
		cmd.AddCommand(a.newWorkflowAgentGeminiAskCommand())
		cmd.AddCommand(a.newWorkflowAgentGeminiConversationsCommand())
	}
	if provider == webagent.ProviderGrok {
		cmd.AddCommand(a.newWorkflowAgentGrokDoctorCommand())
		cmd.AddCommand(a.newWorkflowAgentGrokAuthCommand())
		cmd.AddCommand(a.newWorkflowAgentGrokAskCommand())
		cmd.AddCommand(a.newWorkflowAgentGrokConversationsCommand())
	}
	if provider == webagent.ProviderPerplexity {
		cmd.AddCommand(a.newWorkflowAgentPerplexityDoctorCommand())
		cmd.AddCommand(a.newWorkflowAgentPerplexityAuthCommand())
		cmd.AddCommand(a.newWorkflowAgentPerplexityAskCommand())
		cmd.AddCommand(a.newWorkflowAgentPerplexityConversationsCommand())
	}
	if provider == webagent.ProviderTripadvisor {
		cmd.AddCommand(a.newWorkflowAgentTripadvisorDoctorCommand())
		cmd.AddCommand(a.newWorkflowAgentTripadvisorAuthCommand())
		cmd.AddCommand(a.newWorkflowAgentTripadvisorAskCommand())
		cmd.AddCommand(a.newWorkflowAgentTripadvisorConversationsCommand())
	}
	return cmd
}

func (a *app) newWorkflowAgentClaudeAskCommand() *cobra.Command {
	var stdin bool
	cmd := &cobra.Command{
		Use:   "ask [PROMPT]",
		Short: "Submit one visible Claude request",
		Long: "Open one fresh headed tab, submit the exact prompt with one Send, read the assistant response, " +
			"preserve the observed conversation ID, and close only that tab.",
		Example: "  cdp workflow agent claude ask 'Review this design.' --json\n" +
			"  printf '%s' 'Review this diff.' | cdp workflow agent claude ask --stdin --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 4*time.Minute)
			defer cancel()
			if stdin && len(args) > 0 {
				return commandError(
					"claude_prompt_source_conflict",
					"usage",
					"Claude ask accepts either PROMPT or --stdin, not both",
					ExitUsage,
					[]string{"cdp workflow agent claude ask --stdin --json"},
				)
			}
			prompt := ""
			if stdin {
				data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), int64(claude.MaxPromptCharacters*4+2)))
				if err != nil {
					return commandError(
						"claude_prompt_read_failed",
						"usage",
						"Claude prompt could not be read from stdin",
						ExitUsage,
						nil,
					)
				}
				prompt = string(data)
			} else if len(args) == 1 {
				prompt = args[0]
			}
			if !a.selectHeadedProviderRuntime() {
				result := claude.UnavailableAsk(
					a.build.Commit,
					"claude_headed_browser_required",
					"usage",
					"Claude ask requires the headed browser runtime",
					[]string{"cdp workflow agent claude doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude ask: headed browser required", result)
			}
			store, err := a.stateStore()
			if err != nil {
				result := claude.UnavailableAsk(
					a.build.Commit,
					"claude_state_unavailable",
					"internal",
					"Claude owner-only state is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude ask: state unavailable", result)
			}
			authStore, err := claude.NewStore(store.Dir)
			if err != nil {
				result := claude.UnavailableAsk(
					a.build.Commit,
					"claude_state_unavailable",
					"internal",
					"Claude owner-only state is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude ask: state unavailable", result)
			}
			journal, err := browserflow.NewFileJournal(store.Dir)
			if err != nil {
				result := claude.UnavailableAsk(
					a.build.Commit,
					"claude_lifecycle_state_unavailable",
					"internal",
					"Claude exact-target lifecycle state is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude ask: lifecycle state unavailable", result)
			}
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				result := claude.UnavailableAsk(
					a.build.Commit,
					"claude_browser_unavailable",
					"connection",
					"Claude headed browser runtime is unavailable",
					[]string{
						"cdp --browser-mode headed daemon status --json",
						"cdp workflow agent claude doctor --json",
					},
				)
				return a.renderWebAgentResult(ctx, "claude ask: browser unavailable", result)
			}
			defer closeClient(context.Background())
			engine, err := browserflow.New(browserflow.Config{
				Client:          client,
				Journal:         journal,
				Budget:          a.browserResourceBudgetOptions(),
				AllowOverBudget: a.opts.allowOverBudget,
				InputLockPath:   browserflow.HeadedInputLockPath(store.Dir),
			})
			if err != nil {
				result := claude.UnavailableAsk(
					a.build.Commit,
					"claude_browserflow_unavailable",
					"internal",
					"Claude exact-target browser transaction is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude ask: transaction unavailable", result)
			}
			askTimeout := a.opts.timeout
			if askTimeout <= 0 {
				askTimeout = 3 * time.Minute
			}
			result := claude.Ask(ctx, claude.AskConfig{
				Client:      client,
				Engine:      engine,
				Journal:     journal,
				Store:       authStore,
				BuildCommit: a.build.Commit,
				Timeout:     askTimeout,
			}, prompt)
			human := fmt.Sprintf("claude ask: %v", result.State)
			if data, ok := result.Data.(claude.AskData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read the exact prompt from stdin")
	return cmd
}

func (a *app) newWorkflowAgentClaudeConversationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversations",
		Short: "Read, await, or delete stored Claude conversations",
		Long: "Use the browser-observed owner-only request template for bounded stable HTTP reads. " +
			"Await retries only typed incomplete detail and never resubmits.",
	}
	cmd.AddCommand(a.newWorkflowAgentClaudeConversationsListCommand())
	cmd.AddCommand(a.newWorkflowAgentClaudeConversationsDetailCommand())
	cmd.AddCommand(a.newWorkflowAgentClaudeConversationsAwaitCommand())
	cmd.AddCommand(a.newWorkflowAgentClaudeConversationsDeleteCommand())
	return cmd
}

func (a *app) newWorkflowAgentClaudeConversationsListCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List stored Claude conversations",
		Example: "  cdp workflow agent claude conversations list --limit 30 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()
			config, unavailable := a.claudeReadConfig(webagent.OperationConversationsList)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "claude conversations: unavailable", *unavailable)
			}
			result := claude.ListConversations(ctx, config, limit)
			return a.renderWebAgentResult(ctx, fmt.Sprintf("claude conversations: %v", result.State), result)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 30, "maximum stored conversations to return (0-100)")
	return cmd
}

func (a *app) newWorkflowAgentClaudeConversationsDetailCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "detail CONVERSATION_ID",
		Short:   "Read one exact stored Claude conversation",
		Example: "  cdp workflow agent claude conversations detail <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()
			config, unavailable := a.claudeReadConfig(webagent.OperationConversationsDetail)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "claude detail: unavailable", *unavailable)
			}
			result := claude.DetailConversation(ctx, config, args[0])
			human := fmt.Sprintf("claude detail: %v", result.State)
			if data, ok := result.Data.(claude.ConversationDetailData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
}

func (a *app) newWorkflowAgentClaudeConversationsAwaitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "await CONVERSATION_ID",
		Short: "Await one exact Claude conversation without resubmitting",
		Long: "Retry only provider-typed incomplete stable detail until terminal, timeout, or a permanent failure. " +
			"This command never performs browser input.",
		Example: "  cdp --timeout 3m workflow agent claude conversations await <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 3*time.Minute)
			defer cancel()
			config, unavailable := a.claudeReadConfig(webagent.OperationConversationsAwait)
			if unavailable != nil {
				return a.renderWebAgentResult(ctx, "claude await: unavailable", *unavailable)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 3 * time.Minute
			}
			result := claude.AwaitConversation(ctx, config, args[0], timeout)
			human := fmt.Sprintf("claude await: %v", result.State)
			if data, ok := result.Data.(claude.ConversationDetailData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
}

func (a *app) newWorkflowAgentClaudeConversationsDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete CONVERSATION_ID",
		Short: "Visibly delete one exact Claude conversation",
		Long: "Own one fresh headed target, prepare the exact Claude confirmation dialog, dispatch one raw-input confirmation, " +
			"prove the same-target /new postcondition, and exact-close the target.",
		Example: "  cdp workflow agent claude conversations delete <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := claude.UnavailableDelete(
					a.build.Commit,
					"claude_headed_browser_required",
					"usage",
					"Claude delete requires the headed browser runtime",
					[]string{"cdp workflow agent claude doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude delete: headed browser required", result)
			}
			store, err := a.stateStore()
			if err != nil {
				result := claude.UnavailableDelete(
					a.build.Commit,
					"claude_state_unavailable",
					"internal",
					"Claude owner-only state is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude delete: state unavailable", result)
			}
			journal, err := browserflow.NewFileJournal(store.Dir)
			if err != nil {
				result := claude.UnavailableDelete(
					a.build.Commit,
					"claude_lifecycle_state_unavailable",
					"internal",
					"Claude exact-target lifecycle state is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude delete: lifecycle state unavailable", result)
			}
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				result := claude.UnavailableDelete(
					a.build.Commit,
					"claude_browser_unavailable",
					"connection",
					"Claude headed browser runtime is unavailable",
					[]string{
						"cdp --browser-mode headed daemon status --json",
						"cdp workflow agent claude doctor --json",
					},
				)
				return a.renderWebAgentResult(ctx, "claude delete: browser unavailable", result)
			}
			defer closeClient(context.Background())
			engine, err := browserflow.New(browserflow.Config{
				Client:          client,
				Journal:         journal,
				Budget:          a.browserResourceBudgetOptions(),
				AllowOverBudget: a.opts.allowOverBudget,
				InputLockPath:   browserflow.HeadedInputLockPath(store.Dir),
			})
			if err != nil {
				result := claude.UnavailableDelete(
					a.build.Commit,
					"claude_browserflow_unavailable",
					"internal",
					"Claude exact-target browser transaction is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude delete: transaction unavailable", result)
			}
			deleteTimeout := a.opts.timeout
			if deleteTimeout <= 0 {
				deleteTimeout = 45 * time.Second
			}
			result := claude.DeleteConversation(ctx, claude.DeleteConfig{
				Client:      client,
				Engine:      engine,
				Journal:     journal,
				BuildCommit: a.build.Commit,
				Timeout:     deleteTimeout,
			}, args[0])
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("claude delete: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) claudeReadConfig(operation webagent.Operation) (claude.ReadConfig, *webagent.Result) {
	store, err := a.stateStore()
	if err != nil {
		result := claude.UnavailableRead(
			a.build.Commit,
			operation,
			"claude_state_unavailable",
			"internal",
			"Claude owner-only state is unavailable",
		)
		return claude.ReadConfig{}, &result
	}
	authStore, err := claude.NewStore(store.Dir)
	if err != nil {
		result := claude.UnavailableRead(
			a.build.Commit,
			operation,
			"claude_state_unavailable",
			"internal",
			"Claude owner-only state is unavailable",
		)
		return claude.ReadConfig{}, &result
	}
	return claude.ReadConfig{
		Store:       authStore,
		BuildCommit: a.build.Commit,
		NewRenderedFallback: func(
			ctx context.Context,
		) (claude.RenderedReadConfig, func(context.Context) error, error) {
			if !a.selectHeadedProviderRuntime() {
				return claude.RenderedReadConfig{}, nil, fmt.Errorf(
					"headed browser runtime is required",
				)
			}
			journal, err := browserflow.NewFileJournal(store.Dir)
			if err != nil {
				return claude.RenderedReadConfig{}, nil, err
			}
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return claude.RenderedReadConfig{}, nil, err
			}
			engine, err := browserflow.New(browserflow.Config{
				Client:          client,
				Journal:         journal,
				Budget:          a.browserResourceBudgetOptions(),
				AllowOverBudget: a.opts.allowOverBudget,
				InputLockPath:   browserflow.HeadedInputLockPath(store.Dir),
			})
			if err != nil {
				_ = closeClient(context.Background())
				return claude.RenderedReadConfig{}, nil, err
			}
			return claude.RenderedReadConfig{
				Client:      client,
				Engine:      engine,
				Journal:     journal,
				BuildCommit: a.build.Commit,
				Timeout:     a.opts.timeout,
			}, closeClient, nil
		},
	}, nil
}

func (a *app) newWorkflowAgentClaudeDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report Claude readiness from owner-only local state",
		Long: "Validate Claude's browser-observed request template without probing Chrome. " +
			"Browser runtime health remains an explicit separate diagnostic.",
		Example: "  cdp workflow agent claude doctor --json\n" +
			"  cdp workflow agent claude auth refresh --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			store, err := a.stateStore()
			if err != nil {
				result := claude.UnavailableDoctor(a.build.Commit)
				return a.renderWebAgentResult(ctx, "claude: state unavailable", result)
			}
			authStore, err := claude.NewStore(store.Dir)
			if err != nil {
				result := claude.UnavailableDoctor(a.build.Commit)
				return a.renderWebAgentResult(ctx, "claude: state unavailable", result)
			}
			result := claude.Doctor(ctx, authStore, time.Now(), claude.DefaultAuthTTL, a.build.Commit)
			return a.renderWebAgentResult(ctx, fmt.Sprintf("claude: %v", result.State), result)
		},
	}
}

func (a *app) newWorkflowAgentClaudeAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage Claude browser-observed authentication",
	}
	cmd.AddCommand(a.newWorkflowAgentClaudeAuthRefreshCommand())
	return cmd
}

func (a *app) newWorkflowAgentClaudeAuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh Claude auth without creating a conversation",
		Long: "Own one fresh headed target, derive Claude organization and request shape from exact-session network evidence, " +
			"persist credentials in owner-only state, and exact-close that target without submitting a prompt.",
		Example: "  cdp workflow agent claude auth refresh --json\n" +
			"  cdp workflow agent claude doctor --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 60*time.Second)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := claude.UnavailableAuthRefresh(
					a.build.Commit,
					"claude_headed_browser_required",
					"usage",
					"Claude auth refresh requires the headed browser runtime",
					[]string{
						"cdp --browser-mode headed daemon status --json",
						"cdp workflow agent claude doctor --json",
					},
				)
				return a.renderWebAgentResult(ctx, "claude auth: headed browser required", result)
			}
			store, err := a.stateStore()
			if err != nil {
				result := claude.UnavailableAuthRefresh(
					a.build.Commit,
					"claude_state_unavailable",
					"internal",
					"Claude owner-only state is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude auth: state unavailable", result)
			}
			authStore, err := claude.NewStore(store.Dir)
			if err != nil {
				result := claude.UnavailableAuthRefresh(
					a.build.Commit,
					"claude_state_unavailable",
					"internal",
					"Claude owner-only state is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude auth: state unavailable", result)
			}
			journal, err := browserflow.NewFileJournal(store.Dir)
			if err != nil {
				result := claude.UnavailableAuthRefresh(
					a.build.Commit,
					"claude_lifecycle_state_unavailable",
					"internal",
					"Claude exact-target lifecycle state is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude auth: lifecycle state unavailable", result)
			}
			client, closeClient, err := a.browserEventCDPClient(ctx)
			if err != nil {
				result := claude.UnavailableAuthRefresh(
					a.build.Commit,
					"claude_browser_unavailable",
					"connection",
					"Claude headed browser runtime is unavailable",
					[]string{
						"cdp --browser-mode headed daemon status --json",
						"cdp workflow agent claude doctor --json",
					},
				)
				return a.renderWebAgentResult(ctx, "claude auth: browser unavailable", result)
			}
			defer closeClient(context.Background())
			engine, err := browserflow.New(browserflow.Config{
				Client:          client,
				Journal:         journal,
				Budget:          a.browserResourceBudgetOptions(),
				AllowOverBudget: a.opts.allowOverBudget,
				InputLockPath:   browserflow.HeadedInputLockPath(store.Dir),
			})
			if err != nil {
				result := claude.UnavailableAuthRefresh(
					a.build.Commit,
					"claude_browserflow_unavailable",
					"internal",
					"Claude exact-target browser transaction is unavailable",
					[]string{"cdp doctor --json"},
				)
				return a.renderWebAgentResult(ctx, "claude auth: transaction unavailable", result)
			}
			result := claude.RefreshAuth(ctx, claude.AuthRefreshConfig{
				Client:      client,
				Engine:      engine,
				Journal:     journal,
				Store:       authStore,
				BuildCommit: a.build.Commit,
			})
			return a.renderWebAgentResult(ctx, fmt.Sprintf("claude auth: %v", result.State), result)
		},
	}
}

func (a *app) selectHeadedProviderRuntime() bool {
	if a.root == nil {
		return true
	}
	flags := a.root.PersistentFlags()
	explicit := flags.Changed("browser-mode") || flags.Changed("browserMode")
	if explicit {
		return a.browserModeName() == "headed"
	}
	a.opts.browserMode = "headed"
	if err := flags.Set("browser-mode", "headed"); err != nil {
		return false
	}
	return true
}

func (a *app) newWorkflowAgentCapabilitiesCommand(provider webagent.Provider) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Report the installed provider operation contract",
		Long: "Report implemented, planned, and unsupported provider operations without probing Chrome. " +
			"Only operations with supported=true are callable.",
		Example: fmt.Sprintf(
			"  cdp workflow agent %s capabilities --json\n  cdp workflow agent %s capabilities --jq '.data.operations[] | select(.supported)'",
			provider,
			provider,
		),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()

			capabilities, ok := webagent.CapabilitiesFor(provider)
			if !ok {
				return commandError(
					"unknown_provider",
					"usage",
					fmt.Sprintf("unknown web-agent provider %q", provider),
					ExitUsage,
					[]string{"cdp workflow agent providers --json"},
				)
			}
			result := webagent.NewMetadataResult(
				provider,
				webagent.OperationCapabilities,
				a.workflowAgentCapabilitiesData(ctx, provider, capabilities),
				a.build.Commit,
				[]string{
					"cdp workflow agent providers --json",
					"cdp schema webagent-operation --json",
					"cdp schema webagent-capabilities --json",
				},
			)
			return a.renderWebAgentResult(ctx, fmt.Sprintf("%s: %s", capabilities.DisplayName, capabilities.ImplementationStatus), result)
		},
	}
	if provider == webagent.ProviderGemini {
		cmd.AddCommand(a.newWorkflowAgentGeminiCapabilitiesRefreshCommand())
	}
	if provider == webagent.ProviderChatGPT {
		cmd.AddCommand(a.newWorkflowAgentChatGPTCapabilitiesRefreshCommand())
	}
	if provider == webagent.ProviderM365 {
		cmd.AddCommand(a.newWorkflowAgentM365CapabilitiesRefreshCommand())
	}
	if provider == webagent.ProviderGrok {
		cmd.AddCommand(a.newWorkflowAgentGrokCapabilitiesRefreshCommand())
	}
	if provider == webagent.ProviderPerplexity {
		cmd.AddCommand(a.newWorkflowAgentPerplexityCapabilitiesRefreshCommand())
	}
	return cmd
}

func (a *app) workflowAgentCapabilitiesData(
	ctx context.Context,
	provider webagent.Provider,
	capabilities webagent.Capabilities,
) any {
	if provider == webagent.ProviderGemini {
		return a.geminiCapabilitiesData(ctx, capabilities)
	}
	if provider == webagent.ProviderChatGPT {
		return a.chatgptCapabilitiesData(ctx, capabilities)
	}
	if provider == webagent.ProviderM365 {
		return a.m365CapabilitiesData(ctx, capabilities)
	}
	if provider == webagent.ProviderGrok {
		return a.grokCapabilitiesData(ctx, capabilities)
	}
	if provider == webagent.ProviderPerplexity {
		return a.perplexityCapabilitiesData(ctx, capabilities)
	}
	return capabilities
}

func (a *app) renderWebAgentResult(ctx context.Context, human string, result webagent.Result) error {
	if err := result.Validate(); err != nil {
		return commandError(
			"invalid_webagent_result",
			"internal",
			fmt.Sprintf("authenticated provider result violated %s: %v", webagent.OperationSchemaVersion, err),
			ExitInternal,
			[]string{"cdp schema webagent-operation --json"},
		)
	}
	if err := a.render(ctx, human, result); err != nil {
		return err
	}
	if result.OK {
		return nil
	}
	return &renderedResultExit{ExitCode: webAgentResultExitCode(result)}
}

func webAgentResultExitCode(result webagent.Result) int {
	if result.Error == nil {
		return ExitInternal
	}
	switch result.Error.ErrClass {
	case "usage":
		return ExitUsage
	case "connection":
		return ExitConnection
	case "auth", "permission":
		return ExitPermission
	case "timeout", "completion":
		return ExitTimeout
	case "unsupported":
		return ExitNotImplemented
	case "internal":
		return ExitInternal
	default:
		return ExitCheckFailed
	}
}
