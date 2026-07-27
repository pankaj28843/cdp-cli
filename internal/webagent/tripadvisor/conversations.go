package tripadvisor

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	ConversationListSchemaVersion   = "tripadvisor-conversation-list/v1"
	ConversationDetailSchemaVersion = "tripadvisor-conversation-detail/v1"
)

var conversationIDPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

type ReadConfig struct {
	BrowserConfig
	Store         *Store
	Timeout       time.Duration
	PollInterval  time.Duration
	QuietInterval time.Duration
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
	SessionMode   string                `json:"session_mode"`
	Metadata      map[string]any        `json:"metadata"`
}

type ConversationDetailData struct {
	SchemaVersion   string         `json:"schema_version"`
	ConversationID  string         `json:"conversation_id"`
	Text            string         `json:"text"`
	CompletionState string         `json:"completion_state"`
	ReadMode        string         `json:"read_mode"`
	SessionMode     string         `json:"session_mode"`
	Metadata        map[string]any `json:"metadata"`
}

type renderedConversation struct {
	ID    string `json:"conversation_id"`
	Title string `json:"title"`
}

type listObservation struct {
	DrawerReady    bool                   `json:"drawer_ready"`
	RenderedTitles int                    `json:"rendered_title_count"`
	OmittedNoID    int                    `json:"omitted_without_id"`
	Conversations  []renderedConversation `json:"conversations"`
}

func ListConversations(
	ctx context.Context,
	config ReadConfig,
	limit int,
) webagent.Result {
	runID := webagent.NewRunID()
	data := ConversationListData{
		SchemaVersion: ConversationListSchemaVersion,
		Conversations: []ConversationSummary{},
		ReadMode:      "headed_browser",
		Metadata:      map[string]any{},
	}
	if limit < 0 || limit > 100 {
		return operationFailure(
			runID, config.BuildCommit,
			webagent.OperationConversationsList,
			webagent.StagePlanned, "not_started",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"tripadvisor_invalid_list_limit", "usage",
			"Tripadvisor conversation limit must be between 0 and 100",
			"", data, nil,
		)
	}
	if limit == 0 {
		data.ReadMode = "local_empty_limit"
		result := operationSuccess(
			runID, config.BuildCommit,
			webagent.OperationConversationsList,
			webagent.StateReady, webagent.StageMetadata,
			"local_empty_limit",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil, data, []string{},
		)
		result.Evidence.BrowserMode = "none"
		return result
	}
	status, failure := readPreflight(
		ctx,
		config,
		runID,
		webagent.OperationConversationsList,
		data,
		nil,
	)
	if failure != nil {
		return *failure
	}
	data.SessionMode = status.SessionMode
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationConversationsList,
		"",
		"about:blank",
		"headed_browser",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			if err := preparePage(
				ctx,
				config.Client,
				session,
				HomeURL,
			); err != nil {
				return readFailure(
					runID, config,
					webagent.OperationConversationsList,
					webagent.StageAttached,
					target, pending, nil,
					"tripadvisor_list_page_unavailable", "connection",
					"Tripadvisor conversation history page could not be prepared",
					data,
				)
			}
			_, attempts, _, panelOpened, _, err := ensureSession(
				ctx,
				session,
				config.Timeout,
				config.PollInterval,
				false,
			)
			data.Metadata["session_attempts"] = attempts
			data.Metadata["panel_opened"] = panelOpened
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return readFailure(
					runID, config,
					webagent.OperationConversationsList,
					webagent.StageAttached,
					target, pending, nil,
					"tripadvisor_list_controls_unavailable", "provider",
					"Tripadvisor rendered history controls did not become ready",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return readFailure(
					runID, config,
					webagent.OperationConversationsList,
					webagent.StageAttached,
					target, pending, nil,
					"tripadvisor_list_prepare_state_failed", "internal",
					"Tripadvisor conversation-list preparation could not be persisted",
					data,
				)
			}
			if err := clickHistoryDrawer(ctx, session); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return readFailure(
					runID, config,
					webagent.OperationConversationsList,
					webagent.StagePrepared,
					target, pending, nil,
					"tripadvisor_history_open_failed", "provider",
					"Tripadvisor All chats control could not be opened exactly once",
					data,
				)
			}
			var observation listObservation
			listAttempts, err := pollUntil(
				ctx,
				config.Timeout,
				config.PollInterval,
				func() (bool, error) {
					if err := observeConversationList(
						ctx,
						session,
						&observation,
					); err != nil {
						return false, err
					}
					return conversationListReady(observation), nil
				},
			)
			data.Metadata["list_attempts"] = listAttempts
			data.Metadata["rendered_title_count"] =
				observation.RenderedTitles
			data.Metadata["omitted_without_id"] =
				observation.OmittedNoID
			if observation.OmittedNoID > 0 {
				data.Metadata["identity_policy"] =
					"title_only_entries_omitted_without_guessing"
			}
			if !observation.DrawerReady &&
				observation.RenderedTitles > 0 {
				data.Metadata["history_surface"] =
					"rendered_rows_without_legacy_markers"
			}
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return readFailure(
					runID, config,
					webagent.OperationConversationsList,
					webagent.StagePrepared,
					target, pending, nil,
					"tripadvisor_history_not_rendered", "provider",
					"Tripadvisor history drawer did not become rendered",
					data,
				)
			}
			for _, item := range observation.Conversations {
				if len(data.Conversations) >= limit {
					break
				}
				if !validConversationID(item.ID) {
					continue
				}
				data.Conversations = append(
					data.Conversations,
					ConversationSummary{
						ID:    item.ID,
						Title: strings.TrimSpace(item.Title),
						URL:   conversationURL(item.ID),
						Metadata: map[string]any{
							"identity_source": "rendered_attribute_or_href",
						},
					},
				)
			}
			data.Metadata["returned_count"] = len(data.Conversations)
			if err := lease.MarkTerminal(ctx); err != nil {
				return readFailure(
					runID, config,
					webagent.OperationConversationsList,
					webagent.StageObserveTerminal,
					target, pending, nil,
					"tripadvisor_list_terminal_state_failed", "internal",
					"Tripadvisor conversation-list terminal state could not be persisted",
					data,
				)
			}
			return operationSuccess(
				runID, config.BuildCommit,
				webagent.OperationConversationsList,
				webagent.StateReady,
				webagent.StageObserveTerminal,
				"headed_browser",
				target, pending, nil, nil, data,
				[]string{},
			)
		},
	)
}

func conversationListReady(observation listObservation) bool {
	return observation.DrawerReady || observation.RenderedTitles > 0
}

func DetailConversation(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
) webagent.Result {
	return readConversation(
		ctx,
		config,
		webagent.OperationConversationsDetail,
		conversationID,
	)
}

func AwaitConversation(
	ctx context.Context,
	config ReadConfig,
	conversationID string,
) webagent.Result {
	return readConversation(
		ctx,
		config,
		webagent.OperationConversationsAwait,
		conversationID,
	)
}

func readConversation(
	ctx context.Context,
	config ReadConfig,
	operation webagent.Operation,
	conversationID string,
) webagent.Result {
	conversationID = strings.TrimSpace(conversationID)
	runID := webagent.NewRunID()
	data := ConversationDetailData{
		SchemaVersion:   ConversationDetailSchemaVersion,
		ConversationID:  conversationID,
		CompletionState: "not_observed",
		ReadMode:        "headed_browser",
		Metadata:        map[string]any{},
	}
	var conversation *webagent.ConversationRef
	if validConversationID(conversationID) {
		conversation = conversationRef(conversationID)
	}
	if !validConversationID(conversationID) {
		return operationFailure(
			runID, config.BuildCommit, operation,
			webagent.StagePlanned, "not_started",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"tripadvisor_invalid_conversation_id", "usage",
			"Tripadvisor conversation id must be an exact lowercase UUID",
			"", data, nil,
		)
	}
	status, failure := readPreflight(
		ctx,
		config,
		runID,
		operation,
		data,
		conversation,
	)
	if failure != nil {
		return *failure
	}
	data.SessionMode = status.SessionMode
	if config.Timeout <= 0 {
		if operation == webagent.OperationConversationsAwait {
			config.Timeout = 3 * time.Minute
		} else {
			config.Timeout = 45 * time.Second
		}
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	if config.QuietInterval <= 0 {
		config.QuietInterval = 2 * time.Second
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		operation,
		"",
		"about:blank",
		"headed_browser",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			if err := preparePage(
				ctx,
				config.Client,
				session,
				conversationURL(conversationID),
			); err != nil {
				return readFailure(
					runID, config, operation,
					webagent.StageAttached,
					target, pending, conversation,
					"tripadvisor_detail_page_unavailable", "connection",
					"Tripadvisor exact conversation page could not be prepared",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return readFailure(
					runID, config, operation,
					webagent.StageAttached,
					target, pending, conversation,
					"tripadvisor_detail_prepare_state_failed", "internal",
					"Tripadvisor exact conversation preparation could not be persisted",
					data,
				)
			}
			deadline := time.Now().Add(config.Timeout)
			var observation routeObservation
			attempts, terminal := awaitStableConversation(
				ctx,
				session,
				conversationID,
				deadline,
				config.PollInterval,
				config.QuietInterval,
				&observation,
			)
			data.Metadata["read_attempts"] = attempts
			data.Metadata["answer_count"] = observation.AnswerCount
			data.Metadata["prompt_count"] = observation.PromptCount
			data.Metadata["provider_error_visible"] =
				observation.ProviderError
			data.Text = strings.TrimSpace(observation.Text)
			if terminal {
				data.CompletionState = "terminal"
				if err := lease.MarkTerminal(ctx); err != nil {
					return readFailure(
						runID, config, operation,
						webagent.StageObserveTerminal,
						target, pending, conversation,
						"tripadvisor_detail_terminal_state_failed", "internal",
						"Tripadvisor exact conversation terminal state could not be persisted",
						data,
					)
				}
				return operationSuccess(
					runID, config.BuildCommit,
					operation,
					webagent.StateTerminal,
					webagent.StageObserveTerminal,
					"headed_browser",
					target, pending, nil, conversation, data,
					[]string{},
				)
			}
			_ = lease.MarkIncomplete(context.Background())
			if observation.ProviderError {
				return readFailure(
					runID, config, operation,
					webagent.StageObserveTerminal,
					target, pending, conversation,
					"tripadvisor_conversation_unavailable", "provider",
					"Tripadvisor could not render the exact conversation",
					data,
				)
			}
			data.CompletionState = "incomplete"
			return operationSuccess(
				runID, config.BuildCommit,
				operation,
				webagent.StateIncomplete,
				webagent.StageObserveTerminal,
				"headed_browser",
				target, pending, nil, conversation, data,
				[]string{
					fmt.Sprintf(
						"cdp workflow agent tripadvisor conversations await %s --json",
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
) (SessionStatus, *webagent.Result) {
	if config.Store == nil {
		result := operationFailure(
			runID, config.BuildCommit, operation,
			webagent.StagePlanned, "not_started",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, conversation,
			"tripadvisor_state_unavailable", "internal",
			"Tripadvisor owner-only session state is unavailable",
			"", data,
			[]string{"cdp workflow agent tripadvisor doctor --json"},
		)
		return SessionStatus{}, &result
	}
	status := config.Store.Status(
		ctx,
		time.Now(),
		DefaultAuthTTL,
	)
	if !status.Ready {
		result := operationFailure(
			runID, config.BuildCommit, operation,
			webagent.StagePlanned, "not_started",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, conversation,
			"tripadvisor_session_"+status.State, "auth",
			"Tripadvisor rendered session evidence is not ready",
			"", data,
			[]string{
				"cdp workflow agent tripadvisor auth refresh --json",
			},
		)
		return status, &result
	}
	return status, nil
}

func clickHistoryDrawer(
	ctx context.Context,
	session *cdp.PageSession,
) error {
	var result struct {
		Clicked bool `json:"clicked"`
		Count   int  `json:"count"`
	}
	if err := evaluateInto(ctx, session, `(() => {
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const panel = Array.from(document.querySelectorAll(
	    'aside[aria-label="AI Chat Assistant"]'
	  )).filter(visible);
	  const controls = panel.length === 1
	    ? Array.from(panel[0].querySelectorAll(
	      'button[aria-label="All chats"]'
	    )).filter(visible)
	    : [];
	  if (controls.length !== 1 ||
	      controls[0].hasAttribute('disabled') ||
	      controls[0].getAttribute('aria-disabled') === 'true') {
	    return {clicked: false, count: controls.length};
	  }
	  controls[0].click();
	  return {clicked: true, count: 1};
	})()`, &result); err != nil {
		return err
	}
	if !result.Clicked || result.Count != 1 {
		return fmt.Errorf("unique Tripadvisor All chats control was not clicked")
	}
	return nil
}

func observeConversationList(
	ctx context.Context,
	session *cdp.PageSession,
	observation *listObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
	  const uuidInRoute = /#\/(?:active-)?chat\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:\?|$)/;
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const panels = Array.from(document.querySelectorAll(
	    'aside[aria-label="AI Chat Assistant"]'
	  )).filter(visible);
	  const panel = panels.length === 1 ? panels[0] : null;
	  const close = panel ? Array.from(panel.querySelectorAll(
	    '[data-automation="closeChatsPanel"]'
	  )).filter(visible) : [];
	  const recents = panel ? Array.from(panel.querySelectorAll('*'))
	    .filter(element => visible(element) && element.children.length === 0 &&
	      (element.innerText || element.textContent || '').trim() === 'Recents') : [];
	  const deletes = panel ? Array.from(panel.querySelectorAll(
	    'button[aria-label="delete-button"]'
	  )).filter(visible) : [];
	  const rows = [];
	  let omitted = 0;
	  for (const remove of deletes) {
	    const row = remove.parentElement?.parentElement;
	    const buttons = row ? Array.from(row.querySelectorAll('button'))
	      .filter(button => button !== remove && visible(button)) : [];
	    if (buttons.length !== 1) continue;
	    const entry = buttons[0];
	    const title = (entry.innerText || entry.textContent || '').trim();
	    if (!title) continue;
	    const values = [];
	    for (const name of [
	      'data-conversation-id', 'data-chat-id', 'data-id', 'id', 'value', 'href'
	    ]) {
	      const value = entry.getAttribute(name);
	      if (value) values.push(value.trim());
	    }
	    for (let node = entry; node && node !== panel; node = node.parentElement) {
	      if (node instanceof HTMLAnchorElement && node.getAttribute('href')) {
	        values.push(node.getAttribute('href').trim());
	      }
	    }
	    const ids = new Set();
	    for (const value of values) {
	      if (uuid.test(value)) ids.add(value);
	      const route = value.match(uuidInRoute);
	      if (route) ids.add(route[1]);
	    }
	    if (ids.size !== 1) {
	      omitted++;
	      continue;
	    }
	    rows.push({
	      conversation_id: Array.from(ids)[0],
	      title
	    });
	  }
	  return {
	    drawer_ready: Boolean(panel) && close.length === 1 && recents.length === 1,
	    rendered_title_count: deletes.length,
	    omitted_without_id: omitted,
	    conversations: rows
	  };
	})()`, observation)
}

func awaitStableConversation(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	deadline time.Time,
	poll time.Duration,
	quiet time.Duration,
	observation *routeObservation,
) (int, bool) {
	if quiet <= 0 {
		quiet = 2 * time.Second
	}
	attempts := 0
	lastText := ""
	stableSince := time.Time{}
	for {
		attempts++
		_ = observeRoute(ctx, session, observation)
		exact := observation.RouteMatches &&
			observation.ConversationID == conversationID &&
			observation.AnswerCount > 0 &&
			strings.TrimSpace(observation.Text) != "" &&
			!observation.Streaming &&
			!observation.ProviderError
		if exact {
			text := strings.TrimSpace(observation.Text)
			if text != lastText {
				lastText = text
				stableSince = time.Now()
			} else if !stableSince.IsZero() &&
				time.Since(stableSince) >= quiet {
				return attempts, true
			}
		} else {
			lastText = ""
			stableSince = time.Time{}
		}
		if !waitUntilNextPoll(ctx, poll, deadline) {
			return attempts, false
		}
	}
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
		runID, config.BuildCommit, operation,
		stage, "headed_browser",
		target, cleanup, nil, conversation,
		code, errClass, message, "", data,
		cleanupCommands(runID, cleanup),
	)
}

func validConversationID(value string) bool {
	return conversationIDPattern.MatchString(strings.TrimSpace(value))
}

func conversationURL(id string) string {
	return fmt.Sprintf("%s/#/chat/%s?canvas=1", Origin, id)
}
