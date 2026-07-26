package gemini

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	ConversationListSchemaVersion   = "gemini-conversation-list/v1"
	ConversationDetailSchemaVersion = "gemini-conversation-detail/v1"
)

var conversationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`)

type ReadConfig struct {
	BrowserConfig
	Store        *Store
	Timeout      time.Duration
	PollInterval time.Duration
}

type ConversationSummary struct {
	ID       string         `json:"conversation_id"`
	Title    string         `json:"title"`
	URL      string         `json:"url"`
	Metadata map[string]any `json:"metadata"`
}

type ConversationListData struct {
	SchemaVersion string                `json:"schema_version"`
	Conversations []ConversationSummary `json:"conversations"`
	ReadMode      string                `json:"read_mode"`
	Metadata      map[string]any        `json:"metadata"`
}

type ConversationDetailData struct {
	SchemaVersion   string         `json:"schema_version"`
	ConversationID  string         `json:"conversation_id"`
	Text            string         `json:"text"`
	CompletionState string         `json:"completion_state"`
	ReadMode        string         `json:"read_mode"`
	Metadata        map[string]any `json:"metadata"`
}

type renderedConversation struct {
	ID    string `json:"conversation_id"`
	Title string `json:"title"`
}

type listObservation struct {
	RouteReady      bool                    `json:"route_ready"`
	ComposerReady   bool                    `json:"composer_ready"`
	SidebarExpanded bool                    `json:"sidebar_expanded"`
	RecentsExpanded bool                    `json:"recents_expanded"`
	Loading         bool                    `json:"loading"`
	Sidebar         actionablePoint         `json:"sidebar"`
	Recents         actionablePoint         `json:"recents"`
	Scroller        listScrollerObservation `json:"scroller"`
	Conversations   []renderedConversation  `json:"conversations"`
}

type listScrollerObservation struct {
	Ready        bool    `json:"ready"`
	Count        int     `json:"count"`
	ScrollTop    float64 `json:"scroll_top"`
	ScrollHeight float64 `json:"scroll_height"`
	ClientHeight float64 `json:"client_height"`
}

type listHydrationStats struct {
	Attempts         int
	ScrollDispatches int
	ScrollAdvances   int
}

type listScrollResult struct {
	Ready      bool `json:"ready"`
	Dispatched bool `json:"dispatched"`
	Advanced   bool `json:"advanced"`
}

type actionablePoint struct {
	Ready bool    `json:"ready"`
	Count int     `json:"count"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type detailObservation struct {
	RouteMatches   bool   `json:"route_matches"`
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
	Streaming      bool   `json:"is_streaming"`
	AnswerCount    int    `json:"answer_count"`
}

type promptCaptureObservation struct {
	Prompt               string `json:"prompt"`
	QueryCount           int    `json:"query_count"`
	CopyButtonCount      int    `json:"copy_button_count"`
	ClipboardIntercepted bool   `json:"clipboard_intercepted"`
	Captured             bool   `json:"captured"`
}

func ListConversations(ctx context.Context, config ReadConfig, limit int) webagent.Result {
	runID := webagent.NewRunID()
	empty := ConversationListData{
		SchemaVersion: ConversationListSchemaVersion,
		Conversations: []ConversationSummary{},
		ReadMode:      "headed_browser",
		Metadata:      map[string]any{},
	}
	if limit < 0 || limit > 100 {
		return operationFailure(
			runID, config.BuildCommit, webagent.OperationConversationsList,
			webagent.StagePlanned, "not_started",
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"gemini_invalid_list_limit", "usage",
			"Gemini conversation limit must be between 0 and 100", "",
			empty, nil,
		)
	}
	if limit == 0 {
		empty.ReadMode = "local_empty_limit"
		result := operationSuccess(
			runID, config.BuildCommit, webagent.OperationConversationsList,
			webagent.StateReady, webagent.StageMetadata, "local_empty_limit",
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil, empty, []string{},
		)
		result.Evidence.BrowserMode = "none"
		return result
	}
	if failure := readPreflight(ctx, config, runID, webagent.OperationConversationsList, empty, nil); failure != nil {
		return *failure
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationConversationsList,
		"about:blank",
		"headed_browser",
		empty,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			if err := preparePage(ctx, config.Client, session, HomeURL); err != nil {
				return readFailure(
					runID, config, webagent.OperationConversationsList,
					webagent.StageAttached, target, pending, nil,
					"gemini_list_page_unavailable", "connection",
					"Gemini conversation history page could not be prepared",
					empty,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return readFailure(
					runID, config, webagent.OperationConversationsList,
					webagent.StageAttached, target, pending, nil,
					"gemini_list_prepare_state_failed", "internal",
					"Gemini conversation-list preparation could not be persisted",
					empty,
				)
			}
			var observation listObservation
			initialAttempts, err := pollUntil(
				ctx,
				config.Timeout,
				config.PollInterval,
				func() (bool, error) {
					if err := observeConversationList(ctx, session, &observation); err != nil {
						return false, err
					}
					return observation.RouteReady &&
						observation.ComposerReady &&
						(observation.SidebarExpanded ||
							(observation.Sidebar.Count == 1 && observation.Sidebar.Ready)), nil
				},
			)
			empty.Metadata["initial_attempts"] = initialAttempts
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return readFailure(
					runID, config, webagent.OperationConversationsList,
					webagent.StagePrepared, target, pending, nil,
					"gemini_list_controls_not_ready", "provider",
					"Gemini conversation history controls did not become uniquely ready",
					empty,
				)
			}
			if !observation.SidebarExpanded {
				if err := clickReversibleControl(ctx, session, observation.Sidebar); err != nil {
					_ = lease.MarkIncomplete(context.Background())
					return readFailure(
						runID, config, webagent.OperationConversationsList,
						webagent.StagePrepared, target, pending, nil,
						"gemini_sidebar_open_failed", "provider",
						"Gemini sidebar could not be opened once on the exact target",
						empty,
					)
				}
				attempts, waitErr := waitForListState(
					ctx, config, session, &observation,
					func(value listObservation) bool { return value.SidebarExpanded },
				)
				empty.Metadata["sidebar_attempts"] = attempts
				if waitErr != nil {
					_ = lease.MarkIncomplete(context.Background())
					return readFailure(
						runID, config, webagent.OperationConversationsList,
						webagent.StagePrepared, target, pending, nil,
						"gemini_sidebar_not_expanded", "provider",
						"Gemini sidebar did not become expanded after the one control action",
						empty,
					)
				}
			}
			if !observation.RecentsExpanded {
				if observation.Recents.Count != 1 || !observation.Recents.Ready {
					attempts, waitErr := waitForListState(
						ctx, config, session, &observation,
						func(value listObservation) bool {
							return value.RecentsExpanded ||
								(value.Recents.Count == 1 && value.Recents.Ready)
						},
					)
					empty.Metadata["recents_control_attempts"] = attempts
					if waitErr != nil {
						_ = lease.MarkIncomplete(context.Background())
						return readFailure(
							runID, config, webagent.OperationConversationsList,
							webagent.StagePrepared, target, pending, nil,
							"gemini_recents_control_not_ready", "provider",
							"Gemini Recents control did not become uniquely ready",
							empty,
						)
					}
				}
				if !observation.RecentsExpanded {
					if err := clickReversibleControl(ctx, session, observation.Recents); err != nil {
						_ = lease.MarkIncomplete(context.Background())
						return readFailure(
							runID, config, webagent.OperationConversationsList,
							webagent.StagePrepared, target, pending, nil,
							"gemini_recents_open_failed", "provider",
							"Gemini Recents could not be opened once on the exact target",
							empty,
						)
					}
					attempts, waitErr := waitForListState(
						ctx, config, session, &observation,
						func(value listObservation) bool { return value.RecentsExpanded },
					)
					empty.Metadata["recents_attempts"] = attempts
					if waitErr != nil {
						_ = lease.MarkIncomplete(context.Background())
						return readFailure(
							runID, config, webagent.OperationConversationsList,
							webagent.StagePrepared, target, pending, nil,
							"gemini_recents_not_expanded", "provider",
							"Gemini Recents did not become expanded after the one control action",
							empty,
						)
					}
				}
			}
			hydration, hydrationErr := stabilizeConversationList(
				ctx,
				config,
				session,
				&observation,
				limit,
			)
			empty.Metadata["hydration_attempts"] = hydration.Attempts
			empty.Metadata["scroll_dispatches"] = hydration.ScrollDispatches
			empty.Metadata["scroll_advances"] = hydration.ScrollAdvances
			empty.Metadata["last_rendered_count"] = len(observation.Conversations)
			empty.Metadata["last_loading"] = observation.Loading
			empty.Metadata["last_scroller_count"] = observation.Scroller.Count
			empty.Metadata["last_scroller_ready"] = observation.Scroller.Ready
			empty.Metadata["last_scroll_top"] = observation.Scroller.ScrollTop
			empty.Metadata["last_scroll_height"] = observation.Scroller.ScrollHeight
			empty.Metadata["last_client_height"] = observation.Scroller.ClientHeight
			if hydrationErr != nil {
				_ = lease.MarkIncomplete(context.Background())
				return readFailure(
					runID, config, webagent.OperationConversationsList,
					webagent.StagePrepared, target, pending, nil,
					"gemini_recents_not_stable", "provider",
					"Gemini rendered Recents did not reach a stable bounded snapshot",
					empty,
				)
			}
			conversations := make([]ConversationSummary, 0, min(limit, len(observation.Conversations)))
			seen := make(map[string]struct{}, len(observation.Conversations))
			for _, item := range observation.Conversations {
				if !conversationIDPattern.MatchString(item.ID) {
					continue
				}
				if _, exists := seen[item.ID]; exists {
					continue
				}
				seen[item.ID] = struct{}{}
				conversations = append(conversations, ConversationSummary{
					ID:       item.ID,
					Title:    strings.TrimSpace(item.Title),
					URL:      HomeURL + "/" + item.ID,
					Metadata: map[string]any{"source": "headed-cdp-rendered-recents"},
				})
				if len(conversations) == limit {
					break
				}
			}
			empty.Conversations = conversations
			empty.Metadata["rendered_count"] = len(observation.Conversations)
			empty.Metadata["returned_count"] = len(conversations)
			empty.Metadata["loading"] = observation.Loading
			empty.Metadata["scroller_ready"] = observation.Scroller.Ready
			empty.Metadata["scroller_at_end"] = conversationListAtEnd(
				observation.Scroller,
			)
			if err := lease.MarkTerminal(ctx); err != nil {
				return readFailure(
					runID, config, webagent.OperationConversationsList,
					webagent.StageObserveTerminal, target, pending, nil,
					"gemini_list_terminal_state_failed", "internal",
					"Gemini conversation-list terminal state could not be persisted",
					empty,
				)
			}
			return operationSuccess(
				runID, config.BuildCommit, webagent.OperationConversationsList,
				webagent.StateReady, webagent.StageObserveTerminal,
				"headed_browser", target, pending, nil, nil, empty,
				[]string{"cdp workflow agent gemini conversations detail <conversation-id> --json"},
			)
		},
	)
}

func stabilizeConversationList(
	ctx context.Context,
	config ReadConfig,
	session *cdp.PageSession,
	observation *listObservation,
	limit int,
) (listHydrationStats, error) {
	const endQuietInterval = 5 * time.Second
	stats := listHydrationStats{}
	deadline := time.Now().Add(config.Timeout)
	lastEndSignature := ""
	lastAdvanceSignature := ""
	stableSince := time.Time{}
	for {
		stats.Attempts++
		if err := observeConversationList(ctx, session, observation); err == nil &&
			observation.RecentsExpanded {
			signature, count := conversationListSignature(
				observation.Conversations,
			)
			if count >= limit {
				return stats, nil
			}
			now := time.Now()
			dispatched := false
			if observation.Loading {
				lastEndSignature = ""
				stableSince = time.Time{}
			} else if observation.Scroller.Ready {
				advanceSignature := fmt.Sprintf(
					"%s\n%.0f\n%.0f\n%.0f",
					signature,
					observation.Scroller.ScrollTop,
					observation.Scroller.ScrollHeight,
					observation.Scroller.ClientHeight,
				)
				if advanceSignature != lastAdvanceSignature {
					scroll, err := advanceConversationList(
						ctx,
						session,
					)
					if err != nil {
						return stats, err
					}
					if scroll.Ready {
						lastAdvanceSignature = advanceSignature
					}
					if scroll.Dispatched {
						stats.ScrollDispatches++
						dispatched = true
					}
					if scroll.Advanced {
						stats.ScrollAdvances++
					}
					lastEndSignature = ""
					stableSince = time.Time{}
				}
			}
			if !observation.Loading &&
				!dispatched &&
				observation.Scroller.Ready &&
				conversationListAtEnd(observation.Scroller) {
				endSignature := fmt.Sprintf(
					"%s\n%.0f",
					signature,
					observation.Scroller.ScrollHeight,
				)
				if endSignature != lastEndSignature {
					lastEndSignature = endSignature
					stableSince = now
				} else if !stableSince.IsZero() &&
					now.Sub(stableSince) >= endQuietInterval {
					return stats, nil
				}
			} else {
				lastEndSignature = ""
				stableSince = time.Time{}
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return stats, fmt.Errorf(
				"Gemini rendered conversation list did not stabilize",
			)
		}
		if !waitRendered(ctx, config.PollInterval, remaining) {
			return stats, ctx.Err()
		}
	}
}

func conversationListAtEnd(scroller listScrollerObservation) bool {
	if !scroller.Ready ||
		scroller.ClientHeight <= 0 ||
		scroller.ScrollHeight <= 0 {
		return false
	}
	return scroller.ScrollTop+scroller.ClientHeight >=
		scroller.ScrollHeight-2
}

func advanceConversationList(
	ctx context.Context,
	session *cdp.PageSession,
) (listScrollResult, error) {
	var result listScrollResult
	err := evaluateInto(ctx, session, `(() => {
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const matches = Array.from(
	    document.querySelectorAll(
	      '.sidenav-with-history-container infinite-scroller'
	    )
	  ).filter(visible);
	  if (matches.length !== 1) {
	    return {ready: false, dispatched: false, advanced: false};
	  }
	  const scroller = matches[0];
	  const maximum = Math.max(0, scroller.scrollHeight - scroller.clientHeight);
	  const before = scroller.scrollTop;
	  scroller.scrollTop = maximum;
	  scroller.dispatchEvent(new Event('scroll', {bubbles: true}));
	  return {
	    ready: true,
	    dispatched: true,
	    advanced: scroller.scrollTop > before + 2
	  };
	})()`, &result)
	if err != nil {
		return listScrollResult{}, fmt.Errorf(
			"advance Gemini rendered conversation list: %w",
			err,
		)
	}
	return result, nil
}

func conversationListSignature(
	conversations []renderedConversation,
) (string, int) {
	seen := make(map[string]struct{}, len(conversations))
	ids := make([]string, 0, len(conversations))
	for _, conversation := range conversations {
		if !conversationIDPattern.MatchString(conversation.ID) {
			continue
		}
		if _, exists := seen[conversation.ID]; exists {
			continue
		}
		seen[conversation.ID] = struct{}{}
		ids = append(ids, conversation.ID)
	}
	return strings.Join(ids, "\n"), len(ids)
}

func DetailConversation(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
) webagent.Result {
	return readConversation(ctx, config, conversationID, false)
}

func AwaitConversation(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
) webagent.Result {
	return readConversation(ctx, config, conversationID, true)
}

func readConversation(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
	await bool,
) webagent.Result {
	conversationID = strings.TrimSpace(conversationID)
	operation := webagent.OperationConversationsDetail
	if await {
		operation = webagent.OperationConversationsAwait
	}
	runID := webagent.NewRunID()
	data := ConversationDetailData{
		SchemaVersion:   ConversationDetailSchemaVersion,
		ConversationID:  conversationID,
		CompletionState: "incomplete",
		ReadMode:        "headed_browser",
		Metadata:        map[string]any{},
	}
	if !conversationIDPattern.MatchString(conversationID) {
		return operationFailure(
			runID, config.BuildCommit, operation,
			webagent.StagePlanned, "not_started",
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"gemini_invalid_conversation_id", "usage",
			"Gemini conversation id must contain exactly 16 safe characters", "",
			data, nil,
		)
	}
	conversation := conversationRef(conversationID)
	if failure := readPreflight(ctx, config, runID, operation, data, conversation); failure != nil {
		return *failure
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		operation,
		"about:blank",
		"headed_browser",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			if err := preparePage(ctx, config.Client, session, HomeURL+"/"+conversationID); err != nil {
				return readFailure(
					runID, config, operation, webagent.StageAttached,
					target, pending, conversation,
					"gemini_detail_page_unavailable", "connection",
					"Gemini exact conversation page could not be prepared",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return readFailure(
					runID, config, operation, webagent.StageAttached,
					target, pending, conversation,
					"gemini_detail_prepare_state_failed", "internal",
					"Gemini exact conversation preparation could not be persisted",
					data,
				)
			}
			var observation detailObservation
			attempts, pollErr := pollUntil(
				ctx,
				config.Timeout,
				config.PollInterval,
				func() (bool, error) {
					if err := observeConversationDetail(
						ctx,
						session,
						conversationID,
						&observation,
					); err != nil {
						return false, err
					}
					return observation.RouteMatches &&
						observation.ConversationID == conversationID &&
						observation.AnswerCount > 0 &&
						strings.TrimSpace(observation.Text) != "" &&
						!observation.Streaming, nil
				},
			)
			data.Metadata["detail_read_attempts"] = attempts
			data.Metadata["source"] = "headed-cdp-rendered-detail"
			data.Metadata["exact_route_ready"] = observation.RouteMatches
			data.Metadata["answer_count"] = observation.AnswerCount
			if !observation.RouteMatches || observation.ConversationID != conversationID {
				_ = lease.MarkIncomplete(context.Background())
				return readFailure(
					runID, config, operation, webagent.StagePrepared,
					target, pending, conversation,
					"gemini_exact_route_not_ready", "provider",
					"Gemini exact conversation route did not become ready",
					data,
				)
			}
			var promptCapture promptCaptureObservation
			captureErr := captureExactRenderedPrompt(ctx, session, &promptCapture)
			promptFingerprint := exactCapturedPromptFingerprint(&promptCapture)
			if captureErr == nil && promptFingerprint != "" {
				data.Metadata["prompt_fingerprint"] = promptFingerprint
				data.Metadata["prompt_capture_source"] = "intercepted_copy_prompt"
			}
			data.Metadata["prompt_query_count"] = promptCapture.QueryCount
			data.Metadata["prompt_copy_button_count"] = promptCapture.CopyButtonCount
			data.Metadata["prompt_clipboard_intercepted"] = promptCapture.ClipboardIntercepted
			terminal := pollErr == nil &&
				observation.AnswerCount > 0 &&
				strings.TrimSpace(observation.Text) != "" &&
				!observation.Streaming
			if terminal {
				data.Text = strings.TrimSpace(observation.Text)
				data.CompletionState = "terminal"
				if err := lease.MarkTerminal(ctx); err != nil {
					return readFailure(
						runID, config, operation, webagent.StageObserveTerminal,
						target, pending, conversation,
						"gemini_detail_terminal_state_failed", "internal",
						"Gemini exact conversation terminal state could not be persisted",
						data,
					)
				}
				return operationSuccess(
					runID, config.BuildCommit, operation,
					webagent.StateTerminal, webagent.StageObserveTerminal,
					"headed_browser", target, pending, nil, conversation, data,
					[]string{
						fmt.Sprintf(
							"cdp workflow agent gemini conversations delete %s --json",
							conversationID,
						),
					},
				)
			}
			_ = lease.MarkIncomplete(context.Background())
			data.Metadata["is_streaming"] = observation.Streaming
			return operationSuccess(
				runID, config.BuildCommit, operation,
				webagent.StateIncomplete, webagent.StageObserveTerminal,
				"headed_browser", target, pending, nil, conversation, data,
				[]string{
					fmt.Sprintf(
						"cdp workflow agent gemini conversations await %s --json",
						conversationID,
					),
				},
			)
		},
	)
}

func readPreflight(
	ctx context.Context,
	config ReadConfig,
	runID string,
	operation webagent.Operation,
	data any,
	conversation *webagent.ConversationRef,
) *webagent.Result {
	if config.Store == nil {
		result := operationFailure(
			runID, config.BuildCommit, operation,
			webagent.StagePlanned, "owner_only_local_state",
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, conversation,
			"gemini_state_unavailable", "internal",
			"Gemini owner-only state is unavailable", "",
			data, []string{"cdp workflow agent gemini doctor --json"},
		)
		result.Evidence.BrowserMode = "none"
		return &result
	}
	status := config.Store.AuthStatus(ctx, time.Now(), DefaultAuthTTL)
	if status.Ready {
		return nil
	}
	result := operationFailure(
		runID, config.BuildCommit, operation,
		webagent.StagePlanned, "owner_only_local_state",
		nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		nil, conversation,
		"gemini_auth_"+status.State, "auth",
		"Gemini auth evidence is not ready for headed conversation reads", "",
		data, []string{"cdp workflow agent gemini auth refresh --json"},
	)
	result.Evidence.BrowserMode = "none"
	return &result
}

func readFailure(
	runID string,
	config ReadConfig,
	operation webagent.Operation,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	conversation *webagent.ConversationRef,
	code string,
	errClass string,
	message string,
	data any,
) webagent.Result {
	return operationFailure(
		runID, config.BuildCommit, operation, stage, "headed_browser",
		target, cleanup, nil, conversation,
		code, errClass, message, "", data,
		cleanupCommands(runID, cleanup),
	)
}

func waitForListState(
	ctx context.Context,
	config ReadConfig,
	session *cdp.PageSession,
	observation *listObservation,
	ready func(listObservation) bool,
) (int, error) {
	return pollUntil(
		ctx,
		config.Timeout,
		config.PollInterval,
		func() (bool, error) {
			if err := observeConversationList(ctx, session, observation); err != nil {
				return false, err
			}
			return ready(*observation), nil
		},
	)
}

func clickReversibleControl(
	ctx context.Context,
	session *cdp.PageSession,
	point actionablePoint,
) error {
	if point.Count != 1 || !point.Ready {
		return fmt.Errorf("control is not uniquely actionable")
	}
	outcome, err := browserflow.ClickPoint(ctx, session, point.X, point.Y)
	if outcome.Dispatch == browserflow.DispatchNotPerformed {
		return fmt.Errorf("control action was not performed")
	}
	if err != nil && outcome.Dispatch != browserflow.DispatchUnknown {
		return err
	}
	return nil
}

func observeConversationList(
	ctx context.Context,
	session *cdp.PageSession,
	observation *listObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const inspect = elements => {
	    const matches = Array.from(elements).filter(visible);
	    if (matches.length !== 1) return {ready: false, count: matches.length, x: 0, y: 0};
	    const element = matches[0];
	    element.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
	    const rect = element.getBoundingClientRect();
	    const x = rect.left + rect.width / 2;
	    const y = rect.top + rect.height / 2;
	    const top = document.elementFromPoint(x, y);
	    const enabled = !element.hasAttribute('disabled') &&
	      element.getAttribute('aria-disabled') !== 'true';
	    return {
	      ready: enabled && Boolean(top && (top === element || element.contains(top))),
	      count: 1,
	      x,
	      y
	    };
	  };
	  const sidebarElements = document.querySelectorAll("button[aria-label='Open sidebar']");
	  const recentsElements = document.querySelectorAll("button[aria-label='Toggle Recents']");
	  const loading = Array.from(
	    document.querySelectorAll('[role=progressbar], mat-progress-spinner')
	  ).some(element =>
	    visible(element) &&
	    /loading (conversation history|gems and recent conversations)/i.test(
	      element.getAttribute('aria-label') || ''
	    )
	  );
	  const scrollers = Array.from(
	    document.querySelectorAll(
	      '.sidenav-with-history-container infinite-scroller'
	    )
	  ).filter(visible);
	  const scroller = scrollers.length === 1 ? scrollers[0] : null;
	  const conversations = Array.from(document.querySelectorAll('a[href^="/app/"]')).map(anchor => {
	    const match = (anchor.getAttribute('href') || '').match(
	      /^\/app\/([A-Za-z0-9_-]{16})(?:[/?#]|$)/
	    );
	    return match ? {
	      conversation_id: match[1],
	      title: (anchor.innerText || anchor.textContent || '').trim()
	    } : null;
	  }).filter(Boolean);
	  return {
	    route_ready: location.origin === 'https://gemini.google.com' &&
	      location.pathname === '/app',
	    composer_ready: Boolean(
	      document.querySelector('[role=textbox][contenteditable=true]')
	    ),
	    sidebar_expanded: Boolean(
	      document.querySelector('.sidenav-with-history-container:not(.collapsed)')
	    ),
	    recents_expanded: Array.from(recentsElements).some(button =>
	      button.getAttribute('aria-expanded') === 'true'
	    ),
	    loading,
	    sidebar: inspect(sidebarElements),
	    recents: inspect(recentsElements),
	    scroller: {
	      ready: Boolean(scroller),
	      count: scrollers.length,
	      scroll_top: scroller ? scroller.scrollTop : 0,
	      scroll_height: scroller ? scroller.scrollHeight : 0,
	      client_height: scroller ? scroller.clientHeight : 0
	    },
	    conversations
	  };
	})()`, observation)
}

func observeConversationDetail(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	observation *detailObservation,
) error {
	idJSON, err := json.Marshal(conversationID)
	if err != nil {
		return fmt.Errorf("encode Gemini conversation id")
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const match = location.pathname.match(/^\/app\/([A-Za-z0-9_-]{16})$/);
	  const answers = Array.from(
	    document.querySelectorAll('model-response message-content')
	  ).map(element =>
	    (element.innerText || element.textContent || '').trim()
	  ).filter(Boolean);
	  const streaming = Array.from(document.querySelectorAll('button')).some(button =>
	    /stop/i.test(button.getAttribute('aria-label') || '')
	  );
	  return {
	    route_matches: location.origin === 'https://gemini.google.com' &&
	      Boolean(match) && match[1] === expected,
	    conversation_id: match ? match[1] : '',
	    text: answers.at(-1) || '',
	    is_streaming: streaming,
	    answer_count: answers.length
	  };
	})()`, idJSON)
	return evaluateInto(ctx, session, expression, observation)
}

func exactCapturedPromptFingerprint(observation *promptCaptureObservation) string {
	if observation == nil ||
		!observation.ClipboardIntercepted ||
		!observation.Captured ||
		observation.QueryCount < 1 ||
		observation.CopyButtonCount != 1 ||
		strings.TrimSpace(observation.Prompt) == "" {
		return ""
	}
	return fingerprintPrompt(observation.Prompt)
}

func captureExactRenderedPrompt(
	ctx context.Context,
	session *cdp.PageSession,
	observation *promptCaptureObservation,
) error {
	return evaluateInto(ctx, session, `(async () => {
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const queries = Array.from(document.querySelectorAll('user-query')).filter(query => {
	    const visibleCopy = Array.from(query.querySelectorAll('button')).some(button =>
	      visible(button) &&
	      !button.disabled &&
	      (button.getAttribute('aria-label') || '').trim() === 'Copy prompt'
	    );
	    const visibleContent = Array.from(
	      query.querySelectorAll('.query-content, .query-text, [data-test-id="luminous-collapsed-bubble"]')
	    ).some(visible);
	    return visibleCopy || visibleContent;
	  });
	  const query = queries.at(-1);
	  const buttons = query
	    ? Array.from(query.querySelectorAll('button')).filter(button =>
	        visible(button) &&
	        !button.disabled &&
	        (button.getAttribute('aria-label') || '').trim() === 'Copy prompt'
	      )
	    : [];
	  const clipboard = navigator.clipboard;
	  const prior = clipboard
	    ? Object.getOwnPropertyDescriptor(clipboard, 'writeText')
	    : null;
	  let prompt = '';
	  let clipboardIntercepted = false;
	  if (clipboard && buttons.length === 1) {
	    const intercept = async value => {
	      prompt = String(value);
	    };
	    try {
	      Object.defineProperty(clipboard, 'writeText', {
	        configurable: true,
	        value: intercept
	      });
	      clipboardIntercepted = clipboard.writeText === intercept;
	      if (clipboardIntercepted) {
	        buttons[0].click();
	        await new Promise(resolve => setTimeout(resolve, 100));
	      }
	    } finally {
	      if (prior) {
	        Object.defineProperty(clipboard, 'writeText', prior);
	      } else {
	        delete clipboard.writeText;
	      }
	    }
	  }
	  return {
	    prompt,
	    query_count: queries.length,
	    copy_button_count: buttons.length,
	    clipboard_intercepted: clipboardIntercepted,
	    captured: clipboardIntercepted && prompt.length > 0
	  };
	})()`, observation)
}

func fingerprintPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(prompt)))
	return hex.EncodeToString(sum[:])
}

func conversationRef(id string) *webagent.ConversationRef {
	return &webagent.ConversationRef{
		ID:  id,
		URL: HomeURL + "/" + id,
	}
}
