package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
	"github.com/spf13/cobra"
)

type chatgptCapabilitiesContract struct {
	webagent.Capabilities
	Runtime chatgpt.RuntimeStatus `json:"runtime"`
}

func (a *app) chatgptCapabilitiesData(
	ctx context.Context,
	capabilities webagent.Capabilities,
) any {
	stateStore, err := a.stateStore()
	if err != nil {
		return chatgptCapabilitiesContract{
			Capabilities: capabilities,
			Runtime: chatgpt.RuntimeStatus{
				SchemaVersion:       chatgpt.RuntimeCapabilitiesSchemaVersion,
				State:               "unavailable",
				StatePath:           chatgpt.RelativeCapabilitiesPath,
				ProductModes:        []string{},
				IntelligenceOptions: []string{},
				Tools:               []string{},
				Reason:              "owner-only state directory is unavailable",
			},
		}
	}
	store, err := chatgpt.NewStore(stateStore.Dir)
	if err != nil {
		return chatgptCapabilitiesContract{
			Capabilities: capabilities,
			Runtime: chatgpt.RuntimeStatus{
				SchemaVersion:       chatgpt.RuntimeCapabilitiesSchemaVersion,
				State:               "unavailable",
				StatePath:           chatgpt.RelativeCapabilitiesPath,
				ProductModes:        []string{},
				IntelligenceOptions: []string{},
				Tools:               []string{},
				Reason:              "owner-only runtime capability state is unavailable",
			},
		}
	}
	return chatgptCapabilitiesContract{
		Capabilities: capabilities,
		Runtime: store.RuntimeStatus(
			ctx,
			time.Now(),
			chatgpt.DefaultCapabilitiesTTL,
		),
	}
}

func (a *app) newWorkflowAgentChatGPTDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report ChatGPT readiness from owner-only local evidence",
		Long: "Read owner-only ChatGPT auth and paid-composer capability evidence without opening or probing Chrome. " +
			"Browser submission remains an explicit headed-runtime operation.",
		Example: "  cdp workflow agent chatgpt doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt doctor: unavailable",
					chatgpt.UnavailableDoctor(a.build.Commit),
				)
			}
			store, err := chatgpt.NewStore(stateStore.Dir)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt doctor: unavailable",
					chatgpt.UnavailableDoctor(a.build.Commit),
				)
			}
			result := chatgpt.Doctor(ctx, store, time.Now(), a.build.Commit)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("chatgpt doctor: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentChatGPTAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect or refresh ChatGPT auth evidence",
	}
	cmd.AddCommand(a.newWorkflowAgentChatGPTAuthRefreshCommand())
	return cmd
}

func (a *app) newWorkflowAgentChatGPTConversationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversations",
		Short: "Read exact stored ChatGPT conversations",
		Long: "Use browser-observed auth state for bounded stable HTTP reads. " +
			"These commands never submit, continue, or delete a conversation.",
	}
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsListCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsContinueCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsDetailCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsAwaitCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsDeleteCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsDownloadArtifactCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsExportResearchCommand())
	return cmd
}

func (a *app) newWorkflowAgentChatGPTResearchCommand() *cobra.Command {
	var stdin bool
	var browserExport bool
	cmd := &cobra.Command{
		Use:   "research [PROMPT]",
		Short: "Report the live ChatGPT Deep Research boundary",
		Long: "Deep Research submission is intentionally unavailable until the headed paid UI exposes one exact runtime product control. " +
			"The current browser-observed surface proves ordinary Chat/Medium and file upload, but not Deep Research.",
		Example: "  printf '%s' 'Research this topic.' | cdp workflow agent chatgpt research --stdin --browser-export --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			if stdin && len(args) == 1 {
				return commandError(
					"chatgpt_prompt_source_conflict",
					"usage",
					"ChatGPT research accepts either PROMPT or --stdin, not both",
					ExitUsage,
					nil,
				)
			}
			_ = browserExport
			result := chatgpt.UnsupportedOperation(
				a.build.Commit,
				webagent.OperationResearch,
				"chatgpt_deep_research_control_unproven",
				"ChatGPT Deep Research is unavailable because the exact headed runtime control is not currently proven",
			)
			return a.renderWebAgentResult(
				ctx,
				"chatgpt research: unsupported",
				result,
			)
		},
	}
	cmd.Flags().BoolVar(
		&stdin,
		"stdin",
		false,
		"read the research prompt from stdin",
	)
	cmd.Flags().BoolVar(
		&browserExport,
		"browser-export",
		false,
		"request headed report export when the capability becomes proven",
	)
	return cmd
}

func (a *app) newWorkflowAgentChatGPTConversationsExportResearchCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "export-research CONVERSATION_ID",
		Short: "Report the live ChatGPT research-export boundary",
		Long: "Rendered Deep Research export remains unavailable until one exact completed research surface and export control are live-proven. " +
			"No guessed DOM action or replay is attempted.",
		Example: "  cdp workflow agent chatgpt conversations export-research CONVERSATION_ID --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			_ = args
			result := chatgpt.UnsupportedOperation(
				a.build.Commit,
				webagent.OperationResearchExport,
				"chatgpt_research_export_unproven",
				"ChatGPT research export is unavailable because no exact completed headed research surface is currently proven",
			)
			return a.renderWebAgentResult(
				ctx,
				"chatgpt research export: unsupported",
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentChatGPTCalibrationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "calibration",
		Short: "Inspect or safely reconcile the last ChatGPT calibration",
		Long: "Read owner-only ChatGPT calibration state without probing Chrome, or explicitly reconcile only the exact recorded target " +
			"and acknowledged disposable conversation without repeating an ambiguous action.",
	}
	cmd.AddCommand(
		a.newWorkflowAgentChatGPTCalibrationStatusCommand(),
	)
	cmd.AddCommand(
		a.newWorkflowAgentChatGPTCalibrationCleanupCommand(),
	)
	return cmd
}

func (a *app) newWorkflowAgentChatGPTCalibrationStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Read the last ChatGPT calibration state without probing Chrome",
		Example: "  cdp workflow agent chatgpt calibration status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContext(cmd)
			defer cancel()
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration: unavailable",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_calibration_state_unavailable",
						"internal",
						"ChatGPT owner-only calibration state is unavailable",
					),
				)
			}
			store, err := chatgpt.NewCalibrationStore(
				stateStore.Dir,
			)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration: unavailable",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_calibration_state_unavailable",
						"internal",
						"ChatGPT owner-only calibration state is unavailable",
					),
				)
			}
			journal, err := browserflow.NewFileJournal(
				stateStore.Dir,
			)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration: unavailable",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_calibration_recovery_unavailable",
						"internal",
						"ChatGPT exact-target recovery state is unavailable",
					),
				)
			}
			result := chatgpt.CalibrationStatus(
				ctx,
				store,
				journal,
				a.build.Commit,
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf(
					"chatgpt calibration: %v",
					result.State,
				),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentChatGPTCalibrationCleanupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup",
		Short: "Reconcile only the exact resources from the last ChatGPT calibration",
		Long: "Close only the exact persisted owned target, then delete only a persisted acknowledged disposable conversation. " +
			"Never repeat an ambiguous Send or delete action.",
		Example: "  cdp --timeout 1m workflow agent chatgpt calibration cleanup --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(
				cmd,
				time.Minute,
			)
			defer cancel()
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration cleanup: unavailable",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_calibration_state_unavailable",
						"internal",
						"ChatGPT owner-only calibration state is unavailable",
					),
				)
			}
			calibrationStore, err := chatgpt.NewCalibrationStore(
				stateStore.Dir,
			)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration cleanup: unavailable",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_calibration_state_unavailable",
						"internal",
						"ChatGPT owner-only calibration state is unavailable",
					),
				)
			}
			journal, err := browserflow.NewFileJournal(
				stateStore.Dir,
			)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration cleanup: unavailable",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_calibration_recovery_unavailable",
						"internal",
						"ChatGPT exact-target recovery state is unavailable",
					),
				)
			}
			status := chatgpt.CalibrationStatus(
				ctx,
				calibrationStore,
				journal,
				a.build.Commit,
			)
			if !status.OK {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration cleanup: unavailable",
					status,
				)
			}
			if data, ok := status.Data.(chatgpt.CalibrationStatusData); ok &&
				!data.RecoveryRequired {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration cleanup: not required",
					status,
				)
			}
			if !a.selectHeadedProviderRuntime() {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration cleanup: headed browser required",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_headed_browser_required",
						"usage",
						"ChatGPT calibration cleanup requires the headed browser runtime",
					),
				)
			}
			browserConfig, providerStore, unavailable :=
				a.chatgptBrowserOperationConfig(
					ctx,
					webagent.OperationCalibrate,
				)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration cleanup: unavailable",
					*unavailable,
				)
			}
			result := chatgpt.CleanupCalibration(
				ctx,
				chatgpt.CalibrationCleanupConfig{
					Store:       calibrationStore,
					Journal:     browserConfig.Journal,
					Engine:      browserConfig.Engine,
					BuildCommit: a.build.Commit,
					Delete: chatgpt.DeleteConfig{
						BrowserConfig: browserConfig,
						Store:         providerStore,
						Timeout:       45 * time.Second,
					},
				},
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf(
					"chatgpt calibration cleanup: %v",
					result.State,
				),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentChatGPTCalibrateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "calibrate",
		Short: "Run one disposable ChatGPT create/capture/delete transaction",
		Long: "Use one fresh owned headed target for one memory-only calibration prompt, one visible Send, " +
			"same-target rendered answer capture, exact same-target deletion, and exact close.",
		Example: "  cdp --timeout 4m workflow agent chatgpt calibrate --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(
				cmd,
				4*time.Minute,
			)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration: headed browser required",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_headed_browser_required",
						"usage",
						"ChatGPT calibration requires the headed browser runtime",
					),
				)
			}
			stateStore, err := a.stateStore()
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration: unavailable",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_calibration_state_unavailable",
						"internal",
						"ChatGPT owner-only calibration state is unavailable",
					),
				)
			}
			calibrationStore, err := chatgpt.NewCalibrationStore(
				stateStore.Dir,
			)
			if err != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration: unavailable",
					chatgpt.UnavailableOperation(
						a.build.Commit,
						webagent.OperationCalibrate,
						"chatgpt_calibration_state_unavailable",
						"internal",
						"ChatGPT owner-only calibration state is unavailable",
					),
				)
			}
			browserConfig, providerStore, unavailable :=
				a.chatgptBrowserOperationConfig(
					ctx,
					webagent.OperationCalibrate,
				)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt calibration: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 4 * time.Minute
			}
			result := chatgpt.Calibrate(
				ctx,
				chatgpt.CalibrationConfig{
					BrowserConfig: browserConfig,
					AuthStore:     providerStore,
					Store:         calibrationStore,
					Timeout:       timeout,
				},
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf(
					"chatgpt calibration: %v",
					result.State,
				),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentChatGPTConversationsDownloadArtifactCommand() *cobra.Command {
	var fileName string
	var outputPath string
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "download-artifact CONVERSATION_ID",
		Short: "Download one exact finished ChatGPT artifact",
		Long: "Read one exact hydrated conversation in a fresh owned headed target, resolve one unambiguous sandbox artifact from a finished assistant turn, " +
			"validate same-origin metadata and bounded content, then atomically write the explicit local destination.",
		Example: "  cdp workflow agent chatgpt conversations download-artifact CONVERSATION_ID --filename report.csv --output ./report.csv --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(
				cmd,
				2*time.Minute,
			)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := chatgpt.UnavailableOperation(
					a.build.Commit,
					webagent.OperationArtifactDownload,
					"chatgpt_headed_browser_required",
					"usage",
					"ChatGPT artifact download requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"chatgpt artifact download: headed browser required",
					result,
				)
			}
			browserConfig, store, unavailable :=
				a.chatgptBrowserOperationConfig(
					ctx,
					webagent.OperationArtifactDownload,
				)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt artifact download: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 2 * time.Minute
			}
			result := chatgpt.DownloadArtifact(
				ctx,
				chatgpt.ArtifactDownloadConfig{
					BrowserConfig: browserConfig,
					Store:         store,
					OutputPath:    outputPath,
					Overwrite:     overwrite,
					Timeout:       timeout,
				},
				args[0],
				fileName,
			)
			human := fmt.Sprintf(
				"chatgpt artifact download: %v",
				result.State,
			)
			if data, ok := result.Data.(chatgpt.ArtifactDownloadData); ok &&
				data.OutputPath != "" && result.OK {
				human = data.OutputPath
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().StringVar(
		&fileName,
		"filename",
		"",
		"exact generated artifact filename shown by ChatGPT",
	)
	cmd.Flags().StringVar(
		&outputPath,
		"output",
		"",
		"explicit local destination path",
	)
	cmd.Flags().BoolVar(
		&overwrite,
		"overwrite",
		false,
		"atomically replace an existing regular destination",
	)
	_ = cmd.MarkFlagRequired("filename")
	_ = cmd.MarkFlagRequired("output")
	return cmd
}

func (a *app) newWorkflowAgentChatGPTConversationsContinueCommand() *cobra.Command {
	var stdin bool
	cmd := &cobra.Command{
		Use:   "continue CONVERSATION_ID [PROMPT]",
		Short: "Visibly continue one exact stored ChatGPT conversation",
		Long: "Open one fresh exact headed target on the requested conversation, prove its terminal rendered baseline, " +
			"verify Chat product, Medium intelligence, the exact continuation prompt, and stable route, then click Send once and read the new rendered assistant turn.",
		Example: "  cdp workflow agent chatgpt conversations continue CONVERSATION_ID 'Add missing risks.' --json\n" +
			"  printf '%s' 'Add validation cases.' | cdp workflow agent chatgpt conversations continue CONVERSATION_ID --stdin --json",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 4*time.Minute)
			defer cancel()
			if stdin && len(args) == 2 {
				return commandError(
					"chatgpt_prompt_source_conflict",
					"usage",
					"ChatGPT continuation accepts either PROMPT or --stdin, not both",
					ExitUsage,
					nil,
				)
			}
			prompt := ""
			if stdin {
				data, err := io.ReadAll(
					io.LimitReader(
						cmd.InOrStdin(),
						int64(chatgpt.MaxPromptCharacters*4+2),
					),
				)
				if err != nil {
					return commandError(
						"chatgpt_prompt_read_failed",
						"usage",
						"ChatGPT continuation prompt could not be read from stdin",
						ExitUsage,
						nil,
					)
				}
				prompt = string(data)
			} else if len(args) == 2 {
				prompt = args[1]
			}
			if !a.selectHeadedProviderRuntime() {
				result := chatgpt.UnavailableOperation(
					a.build.Commit,
					webagent.OperationConversationsContinue,
					"chatgpt_headed_browser_required",
					"usage",
					"ChatGPT continuation requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"chatgpt continuation: headed browser required",
					result,
				)
			}
			browserConfig, store, unavailable :=
				a.chatgptBrowserOperationConfig(
					ctx,
					webagent.OperationConversationsContinue,
				)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt continuation: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 4 * time.Minute
			}
			result := chatgpt.ContinueConversation(
				ctx,
				chatgpt.ContinueConfig{
					BrowserConfig: browserConfig,
					Store:         store,
					Timeout:       timeout,
				},
				args[0],
				prompt,
			)
			human := fmt.Sprintf(
				"chatgpt continuation: %v",
				result.State,
			)
			if data, ok := result.Data.(chatgpt.ContinueData); ok &&
				data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().BoolVar(
		&stdin,
		"stdin",
		false,
		"read the exact continuation prompt from stdin",
	)
	return cmd
}

func (a *app) newWorkflowAgentChatGPTConversationsListCommand() *cobra.Command {
	var limit int
	var offset int
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List recent ChatGPT conversations through stable HTTP",
		Example: "  cdp workflow agent chatgpt conversations list --limit 20 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 45*time.Second)
			defer cancel()
			config, unavailable := a.chatgptReadConfig(
				ctx,
				webagent.OperationConversationsList,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt conversations list: unavailable",
					*unavailable,
				)
			}
			result := chatgpt.ListConversations(ctx, config, limit, offset)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("chatgpt conversations list: %v", result.State),
				result,
			)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "number of conversations to return (1-100)")
	cmd.Flags().IntVar(&offset, "offset", 0, "non-negative conversation list offset")
	return cmd
}

func (a *app) newWorkflowAgentChatGPTConversationsDetailCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "detail CONVERSATION_ID",
		Short:   "Read one exact hydrated ChatGPT conversation",
		Example: "  cdp workflow agent chatgpt conversations detail CONVERSATION_ID --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 45*time.Second)
			defer cancel()
			config, unavailable := a.chatgptReadConfig(
				ctx,
				webagent.OperationConversationsDetail,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt conversation detail: unavailable",
					*unavailable,
				)
			}
			result := chatgpt.DetailConversation(ctx, config, args[0])
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("chatgpt conversation detail: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentChatGPTConversationsAwaitCommand() *cobra.Command {
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "await CONVERSATION_ID",
		Short: "Wait for one exact ChatGPT conversation to become terminal",
		Long: "Poll only the exact hydrated conversation detail through stable HTTP. " +
			"This never resubmits or reloads a browser target.",
		Example: "  cdp workflow agent chatgpt conversations await CONVERSATION_ID --wait 3m --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, wait+30*time.Second)
			defer cancel()
			config, unavailable := a.chatgptReadConfig(
				ctx,
				webagent.OperationConversationsAwait,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt conversation await: unavailable",
					*unavailable,
				)
			}
			result := chatgpt.AwaitConversation(ctx, config, args[0], wait)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("chatgpt conversation await: %v", result.State),
				result,
			)
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 3*time.Minute, "maximum exact-detail wait")
	return cmd
}

func (a *app) newWorkflowAgentChatGPTConversationsDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete CONVERSATION_ID",
		Short: "Visibly delete one exact ChatGPT conversation",
		Long: "Own one fresh headed target, resolve one exact history row and confirmation dialog, persist action_pending, " +
			"dispatch one raw-input Delete action, prove the same-target home redirect, and exact-close the target.",
		Example: "  cdp workflow agent chatgpt conversations delete <conversation-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := chatgpt.UnavailableOperation(
					a.build.Commit,
					webagent.OperationConversationsDelete,
					"chatgpt_headed_browser_required",
					"usage",
					"ChatGPT conversation delete requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"chatgpt delete: headed browser required",
					result,
				)
			}
			browserConfig, store, unavailable := a.chatgptBrowserOperationConfig(
				ctx,
				webagent.OperationConversationsDelete,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt delete: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 45 * time.Second
			}
			result := chatgpt.DeleteConversation(
				ctx,
				chatgpt.DeleteConfig{
					BrowserConfig: browserConfig,
					Store:         store,
					Timeout:       timeout,
				},
				args[0],
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("chatgpt delete: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentChatGPTAuthRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh ChatGPT browser-observed read evidence",
		Long: "Open one fresh owned ChatGPT target, observe signed-in UI, cookies, and a conversation-read request, " +
			"persist owner-only replay state, and exact-close the target without creating a conversation.",
		Example: "  cdp workflow agent chatgpt auth refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := chatgpt.UnavailableOperation(
					a.build.Commit,
					webagent.OperationAuthRefresh,
					"chatgpt_headed_browser_required",
					"usage",
					"ChatGPT auth refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"chatgpt auth: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.chatgptBrowserOperationConfig(
				ctx,
				webagent.OperationAuthRefresh,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt auth: unavailable",
					*unavailable,
				)
			}
			result := chatgpt.RefreshAuth(ctx, chatgpt.AuthRefreshConfig{
				BrowserConfig: config,
				Store:         store,
			})
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("chatgpt auth: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentChatGPTCapabilitiesRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Observe paid ChatGPT composer capabilities in headed Chrome",
		Long: "Open one fresh owned ChatGPT target, sanitize the visible Chat product, Medium intelligence, upload, and tool labels, " +
			"persist only those labels, and exact-close the target without submitting a prompt.",
		Example: "  cdp workflow agent chatgpt capabilities refresh --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, time.Minute)
			defer cancel()
			if !a.selectHeadedProviderRuntime() {
				result := chatgpt.UnavailableOperation(
					a.build.Commit,
					webagent.OperationCapabilities,
					"chatgpt_headed_browser_required",
					"usage",
					"ChatGPT capability refresh requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"chatgpt capabilities: headed browser required",
					result,
				)
			}
			config, store, unavailable := a.chatgptBrowserOperationConfig(
				ctx,
				webagent.OperationCapabilities,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt capabilities: unavailable",
					*unavailable,
				)
			}
			result := chatgpt.RefreshCapabilities(
				ctx,
				chatgpt.CapabilityRefreshConfig{
					BrowserConfig: config,
					Store:         store,
				},
			)
			return a.renderWebAgentResult(
				ctx,
				fmt.Sprintf("chatgpt capabilities: %v", result.State),
				result,
			)
		},
	}
}

func (a *app) newWorkflowAgentChatGPTAskCommand() *cobra.Command {
	var stdin bool
	var filePath string
	cmd := &cobra.Command{
		Use:   "ask [PROMPT]",
		Short: "Submit one exact visible paid ChatGPT request",
		Long: "Start one fresh ChatGPT conversation in one fresh exact owned headed target, verify Chat product, Medium intelligence, and the exact prompt, " +
			"persist action_pending, click Send once, acknowledge the same-target route, and read the terminal assistant message without reloading or resubmitting.",
		Example: "  cdp workflow agent chatgpt ask 'Review this design.' --json\n" +
			"  printf '%s' 'Review this diff.' | cdp workflow agent chatgpt ask --stdin --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 4*time.Minute)
			defer cancel()
			if stdin && len(args) > 0 {
				return commandError(
					"chatgpt_prompt_source_conflict",
					"usage",
					"ChatGPT ask accepts either PROMPT or --stdin, not both",
					ExitUsage,
					[]string{"cdp workflow agent chatgpt ask --stdin --json"},
				)
			}
			prompt := ""
			if stdin {
				data, err := io.ReadAll(
					io.LimitReader(
						cmd.InOrStdin(),
						int64(chatgpt.MaxPromptCharacters*4+2),
					),
				)
				if err != nil {
					return commandError(
						"chatgpt_prompt_read_failed",
						"usage",
						"ChatGPT prompt could not be read from stdin",
						ExitUsage,
						nil,
					)
				}
				prompt = string(data)
			} else if len(args) == 1 {
				prompt = args[0]
			}
			if !a.selectHeadedProviderRuntime() {
				result := chatgpt.UnavailableOperation(
					a.build.Commit,
					webagent.OperationAsk,
					"chatgpt_headed_browser_required",
					"usage",
					"ChatGPT ask requires the headed browser runtime",
				)
				return a.renderWebAgentResult(
					ctx,
					"chatgpt ask: headed browser required",
					result,
				)
			}
			browserConfig, store, unavailable := a.chatgptBrowserOperationConfig(
				ctx,
				webagent.OperationAsk,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt ask: unavailable",
					*unavailable,
				)
			}
			timeout := a.opts.timeout
			if timeout <= 0 {
				timeout = 4 * time.Minute
			}
			result := chatgpt.Ask(ctx, chatgpt.AskConfig{
				BrowserConfig: browserConfig,
				Store:         store,
				FilePath:      filePath,
				Timeout:       timeout,
			}, prompt)
			human := fmt.Sprintf("chatgpt ask: %v", result.State)
			if data, ok := result.Data.(chatgpt.AskData); ok && data.Text != "" {
				human = data.Text
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read the exact prompt from stdin")
	cmd.Flags().StringVar(
		&filePath,
		"file",
		"",
		"attach one readable local file to the visible request",
	)
	return cmd
}

func (a *app) chatgptBrowserOperationConfig(
	ctx context.Context,
	operation webagent.Operation,
) (chatgpt.BrowserConfig, *chatgpt.Store, *webagent.Result) {
	stateStore, err := a.stateStore()
	if err != nil {
		result := chatgpt.UnavailableOperation(
			a.build.Commit, operation,
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only state is unavailable",
		)
		return chatgpt.BrowserConfig{}, nil, &result
	}
	store, err := chatgpt.NewStore(stateStore.Dir)
	if err != nil {
		result := chatgpt.UnavailableOperation(
			a.build.Commit, operation,
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only state is unavailable",
		)
		return chatgpt.BrowserConfig{}, nil, &result
	}
	journal, err := browserflow.NewFileJournal(stateStore.Dir)
	if err != nil {
		result := chatgpt.UnavailableOperation(
			a.build.Commit, operation,
			"chatgpt_recovery_unavailable", "internal",
			"ChatGPT exact-target recovery state is unavailable",
		)
		return chatgpt.BrowserConfig{}, nil, &result
	}
	gate, err := admission.New(admission.Config{
		StateDir:       stateStore.Dir,
		MinimumSpacing: chatgpt.DefaultAdmissionSpacing,
	})
	if err != nil {
		result := chatgpt.UnavailableOperation(
			a.build.Commit, operation,
			"chatgpt_admission_unavailable", "internal",
			"ChatGPT provider admission state is unavailable",
		)
		return chatgpt.BrowserConfig{}, nil, &result
	}
	client, _, err := a.browserEventCDPClient(ctx)
	if err != nil {
		result := chatgpt.UnavailableOperation(
			a.build.Commit, operation,
			"chatgpt_browser_unavailable", "connection",
			"ChatGPT headed browser runtime is unavailable",
		)
		return chatgpt.BrowserConfig{}, nil, &result
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:          client,
		Journal:         journal,
		Budget:          a.browserResourceBudgetOptions(),
		AllowOverBudget: a.opts.allowOverBudget,
		InputLockPath:   browserflow.HeadedInputLockPath(stateStore.Dir),
	})
	if err != nil {
		result := chatgpt.UnavailableOperation(
			a.build.Commit, operation,
			"chatgpt_browserflow_unavailable", "internal",
			"ChatGPT exact-target browser transaction is unavailable",
		)
		return chatgpt.BrowserConfig{}, nil, &result
	}
	return chatgpt.BrowserConfig{
		Client:      client,
		Engine:      engine,
		Journal:     journal,
		Admission:   gate,
		BuildCommit: a.build.Commit,
	}, store, nil
}

func (a *app) chatgptReadConfig(
	ctx context.Context,
	operation webagent.Operation,
) (chatgpt.ReadConfig, *webagent.Result) {
	if !a.selectHeadedProviderRuntime() {
		result := chatgpt.UnavailableRead(
			a.build.Commit, operation,
			"chatgpt_headed_browser_required", "usage",
			"ChatGPT stable reads require the headed browser network context",
		)
		return chatgpt.ReadConfig{}, &result
	}
	browserConfig, store, unavailable := a.chatgptBrowserOperationConfig(
		ctx,
		operation,
	)
	if unavailable != nil {
		return chatgpt.ReadConfig{}, unavailable
	}
	return chatgpt.ReadConfig{
		Store:         store,
		Admission:     browserConfig.Admission,
		BrowserConfig: &browserConfig,
		BuildCommit:   a.build.Commit,
	}, nil
}
