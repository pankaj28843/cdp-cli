package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/alex"
	"github.com/spf13/cobra"
)

func (a *app) newWorkflowAgentAlexDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report Ask Alex readiness from owner-only local evidence",
		Long: "Read owner-only ByteByteGo request-template and dynamic catalog freshness without probing Chrome. " +
			"Authenticated discovery remains an explicit headed-runtime operation.",
		Example: "  cdp workflow agent alex doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			store, err := a.alexStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"alex doctor: unavailable",
					alex.UnavailableDoctor(a.build.Commit),
				)
			}
			result := alex.Doctor(
				ctx,
				store,
				time.Now(),
				a.build.Commit,
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("alex doctor: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentAlexAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Refresh Ask Alex request-template evidence",
	}
	cmd.AddCommand(a.newWorkflowAgentAlexAuthRefreshCommand())
	return cmd
}

func (a *app) newWorkflowAgentAlexAuthRefreshCommand() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Observe ByteByteGo auth through one exact headed target",
		Long: "Open one fresh owned ByteByteGo chapter target, make at most three bounded cookie observations with reload between attempts, " +
			"derive the stable POST request template, and exact-close the target. No Ask Alex request is submitted.",
		Example: "  cdp workflow agent alex auth refresh --json\n" +
			"  cdp workflow agent alex auth refresh --dry-run --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := alexUnavailableOperation(
					a.build.Commit,
					webagent.OperationAuthRefresh,
					"alex_headed_browser_required",
					"usage",
					"Ask Alex auth refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"alex auth: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.alexBrowserOperationConfig(
				ctx,
				webagent.OperationAuthRefresh,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"alex auth: unavailable",
					*unavailable,
				)
			}
			result := alex.RefreshAuth(ctx, alex.AuthRefreshConfig{
				BrowserConfig: config,
				Store:         store,
				Timeout:       45 * time.Second,
				DryRun:        dryRun,
			})
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("alex auth: %v", result.State),
				result,
			)
		},
	}
	cmd.Flags().BoolVar(
		&dryRun,
		"dry-run",
		false,
		"prove browser auth is readable without updating owner-only state",
	)
	return cmd
}

func (a *app) newWorkflowAgentAlexCatalogCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Inspect or refresh the dynamic ByteByteGo catalog",
	}
	cmd.AddCommand(a.newWorkflowAgentAlexCatalogStatusCommand())
	cmd.AddCommand(a.newWorkflowAgentAlexCatalogRefreshCommand())
	return cmd
}

func (a *app) newWorkflowAgentAlexCatalogStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Read dynamic catalog freshness without probing Chrome",
		Example: "  cdp workflow agent alex catalog status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			store, err := a.alexStore()
			if err != nil {
				result := alexUnavailableOperation(
					a.build.Commit,
					webagent.OperationCatalogStatus,
					"alex_state_unavailable",
					"internal",
					"Ask Alex owner-only catalog state is unavailable",
				)
				return a.renderWebAgentResult(
					ctx,
					"alex catalog: unavailable",
					result,
				)
			}
			result := alex.CatalogState(
				ctx,
				store,
				time.Now(),
				a.build.Commit,
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("alex catalog: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentAlexCatalogRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Discover current ByteByteGo courses and chapter TOCs",
		Long: "Use one fresh exact owned headed target for my-courses and every default chapter route, " +
			"derive courses from current same-origin application chunks, prefer __NEXT_DATA__ TOCs, persist only bounded owner-only catalog state, and exact-close.",
		Example: "  cdp --timeout 4m workflow agent alex catalog refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 4*time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := alexUnavailableOperation(
					a.build.Commit,
					webagent.OperationCatalogRefresh,
					"alex_headed_browser_required",
					"usage",
					"Ask Alex catalog refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"alex catalog: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.alexBrowserOperationConfig(
				ctx,
				webagent.OperationCatalogRefresh,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"alex catalog: unavailable",
					*unavailable,
				)
			}
			result := alex.RefreshCatalog(
				ctx,
				alex.CatalogRefreshConfig{
					BrowserConfig: config,
					Store:         store,
					Timeout:       3 * time.Minute,
				},
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("alex catalog: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentAlexCoursesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "courses",
		Short: "Read dynamically discovered ByteByteGo courses",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Short:   "List courses from owner-only dynamic catalog state",
		Example: "  cdp workflow agent alex courses list --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			store, err := a.alexStore()
			if err != nil {
				result := alexUnavailableOperation(
					a.build.Commit,
					webagent.OperationCoursesList,
					"alex_state_unavailable",
					"internal",
					"Ask Alex owner-only catalog state is unavailable",
				)
				return a.renderWebAgentResult(
					ctx,
					"alex courses: unavailable",
					result,
				)
			}
			result := alex.ListCourses(ctx, store, a.build.Commit)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("alex courses: %v", result.State),
				result,
			)
		},
	})
	return cmd
}

func (a *app) newWorkflowAgentAlexChaptersCommand() *cobra.Command {
	var courseID string
	list := &cobra.Command{
		Use:     "list",
		Short:   "List chapters for one exact dynamic course",
		Example: "  cdp workflow agent alex chapters list --course system-design-interview --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			store, err := a.alexStore()
			if err != nil {
				result := alexUnavailableOperation(
					a.build.Commit,
					webagent.OperationChaptersList,
					"alex_state_unavailable",
					"internal",
					"Ask Alex owner-only catalog state is unavailable",
				)
				return a.renderWebAgentResult(
					ctx,
					"alex chapters: unavailable",
					result,
				)
			}
			result := alex.ListChapters(
				ctx,
				store,
				courseID,
				a.build.Commit,
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("alex chapters: %v", result.State),
				result,
			)
		},
	}
	list.Flags().StringVar(
		&courseID,
		"course",
		alex.DefaultCourseID,
		"exact course key from the dynamic catalog",
	)
	cmd := &cobra.Command{
		Use:   "chapters",
		Short: "Read dynamically discovered ByteByteGo chapters",
	}
	cmd.AddCommand(list)
	return cmd
}

func (a *app) newWorkflowAgentAlexContentCommand() *cobra.Command {
	var courseID string
	var chapterID string
	var allChapters bool
	var allCourses bool
	var limit int
	fetch := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch rendered exact-chapter content through headed CDP",
		Long: "Resolve only current dynamic catalog identities and reuse one fresh exact owned headed target across the selected routes. " +
			"Rendered content is cached in owner-only state and the target is exact-closed.",
		Example: "  cdp workflow agent alex content fetch --course system-design-interview --chapter-id design-a-rate-limiter --json\n" +
			"  cdp --timeout 10m workflow agent alex content fetch --all-courses --limit 2 --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 10*time.Minute)
			defer cancel()
			stateStore, err := a.alexStore()
			if err != nil {
				result := alexUnavailableOperation(
					a.build.Commit,
					webagent.OperationContentFetch,
					"alex_state_unavailable",
					"internal",
					"Ask Alex owner-only content state is unavailable",
				)
				return a.renderWebAgentResult(
					ctx,
					"alex content: unavailable",
					result,
				)
			}
			catalog, err := stateStore.LoadCatalog(ctx)
			if err != nil {
				result := alexUnavailableOperation(
					a.build.Commit,
					webagent.OperationContentFetch,
					"alex_catalog_unavailable",
					"capability",
					"Ask Alex dynamic catalog is unavailable",
				)
				return a.renderWebAgentResult(
					ctx,
					"alex content: catalog unavailable",
					result,
				)
			}
			selectedCourse := courseID
			selectedChapter := chapterID
			if allCourses && !cmd.Flags().Changed("course") {
				selectedCourse = ""
			}
			if !allCourses &&
				!allChapters &&
				!cmd.Flags().Changed("chapter-id") {
				selectedChapter = alex.DefaultChapterID
			}
			targets, selectErr := alex.SelectContentTargets(
				catalog,
				selectedCourse,
				selectedChapter,
				allChapters,
				allCourses,
				limit,
			)
			if selectErr != nil {
				result := alexUnavailableOperation(
					a.build.Commit,
					webagent.OperationContentFetch,
					"alex_content_selection_invalid",
					"usage",
					selectErr.Error(),
				)
				return a.renderWebAgentResult(
					ctx,
					"alex content: invalid selection",
					result,
				)
			}
			if !a.selectHeadedProviderRuntime() {
				result := alexUnavailableOperation(
					a.build.Commit,
					webagent.OperationContentFetch,
					"alex_headed_browser_required",
					"usage",
					"Ask Alex content fetch requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"alex content: headed browser required",
					result,
				)
			}
			config, _, unavailable := a.alexBrowserOperationConfig(
				ctx,
				webagent.OperationContentFetch,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"alex content: unavailable",
					*unavailable,
				)
			}
			result := alex.FetchContent(
				ctx,
				alex.ContentFetchConfig{
					BrowserConfig: config,
					Store:         stateStore,
					Timeout:       45 * time.Second,
				},
				targets,
				limit > 0,
			)
			human := fmt.Sprintf("alex content: %v", result.State)
			if data, ok := result.Data.(alex.ContentFetchData); ok &&
				data.Content != nil {
				human = data.Content.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	fetch.Flags().StringVar(
		&courseID,
		"course",
		alex.DefaultCourseID,
		"exact course key from the dynamic catalog",
	)
	fetch.Flags().StringVar(
		&chapterID,
		"chapter-id",
		"",
		"exact chapter id from the dynamic catalog",
	)
	fetch.Flags().BoolVar(
		&allChapters,
		"all-chapters",
		false,
		"fetch every discovered chapter in the selected course",
	)
	fetch.Flags().BoolVar(
		&allCourses,
		"all-courses",
		false,
		"fetch every discovered chapter in every discovered course",
	)
	fetch.Flags().IntVar(
		&limit,
		"limit",
		0,
		"limit a bulk fetch to this many exact chapters",
	)
	cmd := &cobra.Command{
		Use:   "content",
		Short: "Fetch rendered ByteByteGo chapter content",
	}
	cmd.AddCommand(fetch)
	return cmd
}

func (a *app) newWorkflowAgentAlexAskCommand() *cobra.Command {
	var stdin bool
	var courseID string
	var chapterID string
	var includeRaw bool
	cmd := &cobra.Command{
		Use:   "ask [PROMPT]",
		Short: "Ask ByteByteGo through one direct request",
		Long: "Resolve one exact dynamic catalog context from the signed-in session, perform one POST to the Ask Alex endpoint, " +
			"and return the response.",
		Example: "  cdp workflow agent alex ask 'Explain this pattern.' --json\n" +
			"  printf '%s' 'Review this design.' | cdp workflow agent alex ask --stdin --course system-design-interview --chapter-id design-a-rate-limiter --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if stdin && len(args) > 0 {
				return commandError(
					"alex_prompt_source_conflict",
					"usage",
					"Ask Alex accepts either PROMPT or --stdin, not both",
					ExitUsage,
					[]string{"cdp workflow agent alex ask --stdin --json"},
				)
			}
			prompt := ""
			if stdin {
				data, err := io.ReadAll(io.LimitReader(
					cmd.InOrStdin(),
					int64(alex.MaxPromptCharacters*4+2),
				))
				if err != nil {
					return commandError(
						"alex_prompt_read_failed",
						"usage",
						"Ask Alex prompt could not be read from stdin",
						ExitUsage,
						nil,
					)
				}
				prompt = string(data)
			} else if len(args) == 1 {
				prompt = args[0]
			}
			store, unavailable := a.alexReplayConfig(
				webagent.OperationAsk,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"alex ask: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 45 * time.Second
			}
			result := alex.Ask(
				ctx,
				alex.AskConfig{
					Store:       store,
					Timeout:     timeout,
					IncludeRaw:  includeRaw,
					BuildCommit: a.build.Commit,
				},
				prompt,
				courseID,
				chapterID,
			)
			human := fmt.Sprintf("alex ask: %v", result.State)
			if data, ok := result.Data.(alex.AskData); ok &&
				strings.TrimSpace(data.Text) != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read the exact prompt from stdin")
	cmd.Flags().StringVar(
		&courseID,
		"course",
		alex.DefaultCourseID,
		"exact course key from the dynamic catalog",
	)
	cmd.Flags().StringVar(
		&chapterID,
		"chapter-id",
		alex.DefaultChapterID,
		"exact chapter id from the dynamic catalog",
	)
	cmd.Flags().BoolVar(
		&includeRaw,
		"raw",
		false,
		"include the bounded raw provider response in JSON data",
	)
	return cmd
}

func (a *app) alexStore() (*alex.Store, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, err
	}
	return alex.NewStore(store.Dir)
}

func (a *app) alexReplayConfig(
	operation webagent.Operation,
) (*alex.Store, *webagent.Result) {
	store, err := a.stateStore()
	if err != nil {
		result := alexUnavailableOperation(
			a.build.Commit,
			operation,
			"alex_state_unavailable",
			"internal",
			"Ask Alex owner-only state is unavailable",
		)
		return nil, &result
	}
	providerStore, err := alex.NewStore(store.Dir)
	if err != nil {
		result := alexUnavailableOperation(
			a.build.Commit,
			operation,
			"alex_state_unavailable",
			"internal",
			"Ask Alex owner-only state is unavailable",
		)
		return nil, &result
	}
	return providerStore, nil
}

func (a *app) alexBrowserOperationConfig(
	ctx context.Context,
	operation webagent.Operation,
) (alex.BrowserConfig, *alex.Store, *webagent.Result) {
	store, err := a.stateStore()
	if err != nil {
		result := alexUnavailableOperation(
			a.build.Commit,
			operation,
			"alex_state_unavailable",
			"internal",
			"Ask Alex owner-only state is unavailable",
		)
		return alex.BrowserConfig{}, nil, &result
	}
	providerStore, err := alex.NewStore(store.Dir)
	if err != nil {
		result := alexUnavailableOperation(
			a.build.Commit,
			operation,
			"alex_state_unavailable",
			"internal",
			"Ask Alex owner-only state is unavailable",
		)
		return alex.BrowserConfig{}, nil, &result
	}
	journal, err := browserflow.NewFileJournal(store.Dir)
	if err != nil {
		result := alexUnavailableOperation(
			a.build.Commit,
			operation,
			"alex_lifecycle_state_unavailable",
			"internal",
			"Ask Alex exact-target lifecycle state is unavailable",
		)
		return alex.BrowserConfig{}, nil, &result
	}
	client, _, err := a.browserCDPClient(ctx)
	if err != nil {
		result := alexUnavailableOperation(
			a.build.Commit,
			operation,
			"alex_browser_unavailable",
			"connection",
			"Ask Alex headed browser runtime is unavailable",
		)
		return alex.BrowserConfig{}, nil, &result
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:          client,
		Journal:         journal,
		Budget:          a.browserResourceBudgetOptions(),
		AllowOverBudget: a.opts.allowOverBudget,
		InputLockPath:   browserflow.HeadedInputLockPath(store.Dir),
	})
	if err != nil {
		result := alexUnavailableOperation(
			a.build.Commit,
			operation,
			"alex_browserflow_unavailable",
			"internal",
			"Ask Alex exact-target browser transaction is unavailable",
		)
		return alex.BrowserConfig{}, nil, &result
	}
	return alex.BrowserConfig{
		Client:      client,
		Engine:      engine,
		Journal:     journal,
		BuildCommit: a.build.Commit,
	}, providerStore, nil
}

func alexUnavailableOperation(
	buildCommit string,
	operation webagent.Operation,
	code string,
	errClass string,
	message string,
) webagent.Result {
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderAlex,
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
			"schema_version": "alex-unavailable/v1",
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
		NextCommands: []string{"cdp workflow agent alex doctor --json"},
	}
}
