package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/config"
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
				ModelOptions:        []string{},
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
				ModelOptions:        []string{},
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
		Long: "Read owner-only ChatGPT auth and composer capability evidence without opening or probing Chrome. " +
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

func (a *app) newWorkflowAgentChatGPTTranscribeCommand() *cobra.Command {
	var filePath string
	var durationMilliseconds int64
	cmd := &cobra.Command{
		Use:   "transcribe",
		Short: "Transcribe one local WebM file through ChatGPT",
		Long: "Use the exact headed ChatGPT browser network boundary for one persisted WebM audio file " +
			"when the headed runtime is available; direct HTTP remains a bounded fallback when it is not. " +
			"A headed auth refresh is lazy and bounded to repair stale evidence.",
		Example: "  cdp workflow agent chatgpt transcribe --file ~/.cache/whisper.webm --duration-ms 4200 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 2*time.Minute)
			defer cancel()
			if filePath == "" {
				return commandError(
					"chatgpt_transcription_file_required",
					"usage",
					"ChatGPT transcription requires --file",
					ExitUsage,
					[]string{"cdp workflow agent chatgpt transcribe --file /path/to/whisper.webm --duration-ms 1000 --json"},
				)
			}
			stateStore, err := a.stateStore()
			if err != nil {
				result := chatgpt.UnavailableOperation(
					a.build.Commit,
					webagent.OperationTranscribe,
					"chatgpt_state_unavailable",
					"internal",
					"ChatGPT owner-only state is unavailable",
				)
				return a.renderWebAgentResult(ctx, "chatgpt transcribe: unavailable", result)
			}
			store, err := chatgpt.NewStore(stateStore.Dir)
			if err != nil {
				result := chatgpt.UnavailableOperation(
					a.build.Commit,
					webagent.OperationTranscribe,
					"chatgpt_state_unavailable",
					"internal",
					"ChatGPT owner-only state is unavailable",
				)
				return a.renderWebAgentResult(ctx, "chatgpt transcribe: unavailable", result)
			}
			var browserConfig *chatgpt.BrowserConfig
			if a.selectHeadedProviderRuntime() {
				candidate, _, unavailable := a.chatgptBrowserOperationConfig(
					ctx,
					webagent.OperationTranscribe,
				)
				if unavailable == nil {
					browserConfig = &candidate
				}
			}

			refreshAuth := func(refreshCtx context.Context) error {
				if !a.selectHeadedProviderRuntime() {
					return fmt.Errorf("ChatGPT headed browser runtime is unavailable for auth repair")
				}
				browserConfig, refreshedStore, unavailable := a.chatgptBrowserOperationConfig(
					refreshCtx,
					webagent.OperationTranscribe,
				)
				if unavailable != nil {
					if unavailable.Error != nil {
						return fmt.Errorf("%s", unavailable.Error.Message)
					}
					return fmt.Errorf("ChatGPT headed browser auth repair is unavailable")
				}
				result := chatgpt.RefreshAuth(refreshCtx, chatgpt.AuthRefreshConfig{
					BrowserConfig: browserConfig,
					Store:         refreshedStore,
				})
				if !result.OK {
					if result.Error != nil {
						return fmt.Errorf("%s", result.Error.Message)
					}
					return fmt.Errorf("ChatGPT auth repair failed")
				}
				return nil
			}

			result := chatgpt.Transcribe(
				ctx,
				chatgpt.TranscribeConfig{
					Store:       store,
					Browser:     browserConfig,
					BuildCommit: a.build.Commit,
					RefreshAuth: refreshAuth,
				},
				filePath,
				durationMilliseconds,
			)
			human := fmt.Sprintf("chatgpt transcribe: %v", result.State)
			if data, ok := result.Data.(chatgpt.TranscriptionData); ok && result.OK {
				human = data.Transcript
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().StringVar(&filePath, "file", "", "local WebM/Opus file to transcribe")
	cmd.Flags().Int64Var(&durationMilliseconds, "duration-ms", 0, "recorded audio duration in milliseconds")
	return cmd
}

func (a *app) newWorkflowAgentChatGPTConversationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversations",
		Short: "Read or continue exact stored ChatGPT conversations",
		Long: "Use browser-observed auth state for bounded stable reads and " +
			"explicit headed operations on one exact conversation. Read commands " +
			"do not mutate provider state; continue and delete remain explicit.",
	}
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsListCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsContinueCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsDetailCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsAwaitCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsDeleteCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsDownloadArtifactCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsDownloadAttachmentsCommand())
	cmd.AddCommand(a.newWorkflowAgentChatGPTConversationsExportResearchCommand())
	return cmd
}

func (a *app) newWorkflowAgentChatGPTConversationsDownloadAttachmentsCommand() *cobra.Command {
	var outputDir string
	cmd := &cobra.Command{
		Use:   "download-attachments CONVERSATION_ID",
		Short: "Export every attachment from one terminal ChatGPT answer",
		Long: "Read one exact hydrated terminal answer, resolve only admitted ChatGPT attachment sources, and export bounded original provider bytes plus a deterministic owner-only manifest. " +
			"Existing paths are never overwritten; independent item failures produce an explicit partial batch.",
		Example: "  cdp workflow agent chatgpt conversations download-attachments CONVERSATION_ID --output-dir ./designs --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 5*time.Minute)
			defer cancel()
			readConfig, unavailable := a.chatgptReadConfig(
				ctx,
				webagent.OperationAttachmentsDownload,
			)
			if unavailable != nil {
				return a.renderWebAgentResult(
					ctx,
					"chatgpt attachment export: unavailable",
					*unavailable,
				)
			}
			result := chatgpt.DownloadAttachments(
				ctx,
				chatgpt.AttachmentBatchConfig{
					ReadConfig: readConfig,
					OutputDir:  outputDir,
				},
				args[0],
			)
			human := fmt.Sprintf(
				"chatgpt attachment export: %v",
				result.State,
			)
			if data, ok := result.Data.(chatgpt.AttachmentBatchData); ok &&
				data.ManifestPath != "" && result.OK {
				human = data.ManifestPath
			}
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().StringVar(
		&outputDir,
		"output-dir",
		"",
		"explicit local directory for attachments and the deterministic manifest",
	)
	_ = cmd.MarkFlagRequired("output-dir")
	return cmd
}

func (a *app) newWorkflowAgentChatGPTResearchCommand() *cobra.Command {
	var stdin bool
	var browserExport bool
	cmd := &cobra.Command{
		Use:   "research [PROMPT]",
		Short: "Report the live ChatGPT Deep Research boundary",
		Long: "Deep Research remains capability-only at this boundary. The headed UI accepts the mode and embeds its report in a sandboxed app, but the current cdp page/frame reader cannot prove a readable terminal report or export lifecycle. " +
			"No guessed DOM action, iframe replay, or second Send is attempted.",
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
				"ChatGPT Deep Research is visible and selectable, but its embedded sandbox report is not readable through the current cdp page/frame boundary",
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
		Long: "Rendered Deep Research export remains unavailable because the report is hosted in an embedded sandbox whose completed readable surface and export control are not exposed through the current cdp page/frame boundary. " +
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
				"ChatGPT research export is unavailable because the embedded Deep Research report and export control are not readable through the current cdp page/frame boundary",
			)
			return a.renderWebAgentResult(
				ctx,
				"chatgpt research export: unsupported",
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
	var thinking string
	var reasoning string
	var intelligence string
	var minimumThinking string
	var model string
	cmd := &cobra.Command{
		Use:   "continue CONVERSATION_ID [PROMPT]",
		Short: "Visibly continue one exact stored ChatGPT conversation",
		Long: "Open one fresh exact headed target on the requested conversation, prove its terminal rendered baseline, " +
			"apply the configured or explicit thinking/model policy, verify its minimum, the exact continuation prompt, and stable route, then click Send once and read the new rendered assistant turn.",
		Example: "  cdp workflow agent chatgpt conversations continue CONVERSATION_ID 'Add missing risks.' --json --timeout 40m\n" +
			"  printf '%s' 'Add validation cases.' | cdp workflow agent chatgpt conversations continue CONVERSATION_ID --stdin --json --timeout 40m",
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
			selection, selectionErr := a.resolveChatGPTSelection(
				cmd,
				thinking,
				reasoning,
				intelligence,
				minimumThinking,
				model,
			)
			if selectionErr != nil {
				return selectionErr
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
					Selection:     selection,
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
	addChatGPTSelectionFlags(
		cmd,
		&thinking,
		&reasoning,
		&intelligence,
		&minimumThinking,
		&model,
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
		Long: "Poll the exact hydrated conversation detail through direct stable HTTP first. " +
			"An eligible auth/transport failure may lazily self-heal one headed read target; this never resubmits the conversation.",
		Example: "  cdp workflow agent chatgpt conversations await CONVERSATION_ID --wait 40m --timeout 40m30s --json",
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
		Long: "Own one fresh headed target, resolve one exact history row and confirmation dialog, dispatch one raw-input Delete action, " +
			"prove the same-target home redirect, and exact-close the target.",
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
		Short: "Observe ChatGPT composer capabilities in headed Chrome",
		Long: "Open one fresh owned ChatGPT target, sanitize the visible Chat product, logically ascending thinking options, model options, upload, and tool labels, " +
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
	var thinking string
	var reasoning string
	var intelligence string
	var minimumThinking string
	var model string
	var tool string
	cmd := &cobra.Command{
		Use:   "ask [PROMPT]",
		Short: "Submit one exact visible ChatGPT request",
		Long: "Open one fresh headed tab, apply the configured or explicit thinking/model policy, optionally select one verified tool, submit the exact prompt with one Send, " +
			"advance one provider Answer-now gate when it appears, read the assistant response or generated image, preserve the observed conversation ID, and close only that tab.",
		Example: "  printf '%s' 'I keep waking with the taste of salt in my mouth, and every morning there is one more wet footprint on the attic stairs. Write the opening scene of an original gothic story.' | cdp workflow agent chatgpt ask --stdin --thinking Medium --model 'GPT-5.6 Sol' --json\n" +
			"  printf '%s' 'A paper boat has washed up at the lighthouse during a silver storm. Paint the moment the keeper opens it.' | cdp workflow agent chatgpt ask --stdin --tool create-image --thinking Pro --model 'GPT-5.6 Sol' --json --timeout 40m",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			commandTimeout := 4 * time.Minute
			if normalizedTool, err := chatgpt.NormalizeTool(tool); err == nil {
				switch normalizedTool {
				case chatgpt.ToolWebSearch, chatgpt.ToolGitHub, chatgpt.ToolOpenAIPlatform:
					commandTimeout = 8 * time.Minute
				case chatgpt.ToolCreateImage, chatgpt.ToolVisualize:
					commandTimeout = 40 * time.Minute
				}
			}
			ctx, cancel := a.commandContextWithDefault(cmd, commandTimeout)
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
			selection, selectionErr := a.resolveChatGPTSelection(
				cmd,
				thinking,
				reasoning,
				intelligence,
				minimumThinking,
				model,
			)
			if selectionErr != nil {
				return selectionErr
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
				if normalizedTool, err := chatgpt.NormalizeTool(tool); err == nil {
					switch normalizedTool {
					case chatgpt.ToolWebSearch, chatgpt.ToolGitHub, chatgpt.ToolOpenAIPlatform:
						timeout = 8 * time.Minute
					case chatgpt.ToolCreateImage, chatgpt.ToolVisualize:
						timeout = 40 * time.Minute
					}
				}
			}
			result := chatgpt.Ask(ctx, chatgpt.AskConfig{
				BrowserConfig: browserConfig,
				Store:         store,
				FilePath:      filePath,
				Tool:          tool,
				Timeout:       timeout,
				Selection:     selection,
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
	cmd.Flags().StringVar(
		&tool,
		"tool",
		"",
		"verified ChatGPT tools: create-image, visualize, web-search, github, openai-platform; deep research and Gmail remain capability-only",
	)
	addChatGPTSelectionFlags(
		cmd,
		&thinking,
		&reasoning,
		&intelligence,
		&minimumThinking,
		&model,
	)
	return cmd
}

func addChatGPTSelectionFlags(
	cmd *cobra.Command,
	thinking *string,
	reasoning *string,
	intelligence *string,
	minimumThinking *string,
	model *string,
) {
	cmd.Flags().StringVar(
		thinking,
		"thinking",
		"",
		"thinking selection: current; Instant 5.5, Medium, High, Extra High, Pro; or the highest policy",
	)
	cmd.Flags().StringVar(
		reasoning,
		"reasoning",
		"",
		"alias for --thinking",
	)
	cmd.Flags().StringVar(
		intelligence,
		"intelligence",
		"",
		"compatibility alias for --thinking",
	)
	cmd.Flags().StringVar(
		minimumThinking,
		"minimum-thinking",
		"",
		"fail before Send below this minimum: Instant 5.5, Medium, High, Extra High, or Pro",
	)
	cmd.Flags().StringVar(
		model,
		"model",
		"",
		"model selection: current, an exact visible model label, or the highest policy",
	)
}

func (a *app) resolveChatGPTSelection(
	cmd *cobra.Command,
	thinking string,
	reasoning string,
	intelligence string,
	minimumThinking string,
	model string,
) (chatgpt.SelectionPolicy, error) {
	cfg, err := config.Load(a.opts.config)
	if err != nil {
		return chatgpt.SelectionPolicy{}, commandError(
			"invalid_config",
			"usage",
			err.Error(),
			ExitUsage,
			nil,
		)
	}
	policy := chatgpt.SelectionPolicy{
		Thinking:        cfg.Agents.ChatGPT.Thinking,
		MinimumThinking: cfg.Agents.ChatGPT.MinimumThinking,
		Model:           cfg.Agents.ChatGPT.Model,
	}
	thinkingFlags := []struct {
		name  string
		value string
	}{
		{"thinking", thinking},
		{"reasoning", reasoning},
		{"intelligence", intelligence},
	}
	selectedThinkingFlags := 0
	for _, candidate := range thinkingFlags {
		if cmd.Flags().Changed(candidate.name) {
			selectedThinkingFlags++
			policy.Thinking = candidate.value
		}
	}
	if selectedThinkingFlags > 1 {
		return chatgpt.SelectionPolicy{}, commandError(
			"chatgpt_thinking_flag_conflict",
			"usage",
			"Use only one of --thinking, --reasoning, or --intelligence",
			ExitUsage,
			[]string{"cdp workflow agent chatgpt ask --thinking highest --json"},
		)
	}
	if cmd.Flags().Changed("minimum-thinking") {
		policy.MinimumThinking = minimumThinking
	}
	if cmd.Flags().Changed("model") {
		policy.Model = model
	}
	normalized, err := chatgpt.NormalizeSelectionPolicy(policy)
	if err != nil {
		return chatgpt.SelectionPolicy{}, commandError(
			"chatgpt_selection_invalid",
			"usage",
			err.Error(),
			ExitUsage,
			[]string{"cdp workflow agent chatgpt ask --help"},
		)
	}
	return normalized, nil
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
			"chatgpt_lifecycle_state_unavailable", "internal",
			"ChatGPT exact-target lifecycle state is unavailable",
		)
		return chatgpt.BrowserConfig{}, nil, &result
	}
	client, _, err := a.browserEventCDPClient(ctx)
	if err != nil {
		if a.opts.debug {
			_, _ = fmt.Fprintf(a.err, "chatgpt headed browser runtime unavailable: %v\n", err)
		}
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
		BuildCommit: a.build.Commit,
	}, store, nil
}

func (a *app) chatgptReadConfig(
	ctx context.Context,
	operation webagent.Operation,
) (chatgpt.ReadConfig, *webagent.Result) {
	stateStore, err := a.stateStore()
	if err != nil {
		result := chatgpt.UnavailableRead(
			a.build.Commit, operation,
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only read state is unavailable",
		)
		return chatgpt.ReadConfig{}, &result
	}
	store, err := chatgpt.NewStore(stateStore.Dir)
	if err != nil {
		result := chatgpt.UnavailableRead(
			a.build.Commit, operation,
			"chatgpt_state_unavailable", "internal",
			"ChatGPT owner-only read state is unavailable",
		)
		return chatgpt.ReadConfig{}, &result
	}
	return chatgpt.ReadConfig{
		Store:       store,
		BuildCommit: a.build.Commit,
		BrowserFallback: func(
			fallbackCtx context.Context,
		) (*chatgpt.BrowserConfig, error) {
			if !a.selectHeadedProviderRuntime() {
				return nil, fmt.Errorf(
					"ChatGPT headed browser runtime is unavailable",
				)
			}
			browserConfig, _, unavailable :=
				a.chatgptBrowserOperationConfig(
					fallbackCtx,
					operation,
				)
			if unavailable != nil {
				if unavailable.Error != nil {
					return nil, fmt.Errorf(
						"%s",
						unavailable.Error.Message,
					)
				}
				return nil, fmt.Errorf(
					"ChatGPT headed browser fallback is unavailable",
				)
			}
			return &browserConfig, nil
		},
	}, nil
}
