package tripadvisor

import (
	"context"
	"fmt"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	AuthRefreshSchemaVersion  = "tripadvisor-auth-refresh/v1"
	DoctorSchemaVersion       = "tripadvisor-doctor/v1"
	defaultObservationTimeout = 30 * time.Second
)

type AuthRefreshConfig struct {
	BrowserConfig
	Store        *Store
	Timeout      time.Duration
	PollInterval time.Duration
	Now          func() time.Time
}

type AuthRefreshData struct {
	SchemaVersion string `json:"schema_version"`
	AuthState     string `json:"auth_state"`
	StatePath     string `json:"state_path"`
	PanelReady    bool   `json:"panel_ready"`
	ComposerReady bool   `json:"composer_ready"`
	HistoryReady  bool   `json:"history_ready"`
	SessionMode   string `json:"session_mode"`
	CapturedAt    string `json:"captured_at,omitempty"`
	Attempts      int    `json:"attempts"`
	PanelOpened   bool   `json:"panel_opened"`
	NewChatOpened bool   `json:"new_chat_opened"`
}

type DoctorData struct {
	SchemaVersion string        `json:"schema_version"`
	Session       SessionStatus `json:"session"`
	BrowserSubmit string        `json:"browser_submit"`
	RenderedReads string        `json:"rendered_reads"`
	BrowserMode   string        `json:"browser_mode"`
	BrowserProbed bool          `json:"browser_probed"`
}

type sessionObservation struct {
	OriginReady      bool `json:"origin_ready"`
	PanelCount       int  `json:"panel_count"`
	PanelReady       bool `json:"panel_ready"`
	ComposerCount    int  `json:"composer_count"`
	ComposerReady    bool `json:"composer_ready"`
	HistoryCount     int  `json:"history_count"`
	HistoryReady     bool `json:"history_ready"`
	SignInVisible    bool `json:"sign_in_visible"`
	PanelOpenerCount int  `json:"panel_opener_count"`
	PanelOpenerReady bool `json:"panel_opener_ready"`
	NewChatCount     int  `json:"new_chat_count"`
	NewChatReady     bool `json:"new_chat_ready"`
}

func RefreshAuth(
	ctx context.Context,
	config AuthRefreshConfig,
) webagent.Result {
	runID := webagent.NewRunID()
	data := AuthRefreshData{
		SchemaVersion: AuthRefreshSchemaVersion,
		AuthState:     "blocked",
		StatePath:     RelativeSessionPath,
	}
	if config.Store == nil {
		return operationFailure(
			runID, config.BuildCommit, webagent.OperationAuthRefresh,
			webagent.StagePlanned, "headed_browser",
			nil, webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			nil, nil,
			"tripadvisor_state_unavailable", "internal",
			"Tripadvisor owner-only session state is unavailable",
			"", data, []string{"cdp doctor --json"},
		)
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultObservationTimeout
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationAuthRefresh,
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
				return authFailure(
					runID, config, webagent.StageAttached,
					target, pending,
					"tripadvisor_auth_page_unavailable", "connection",
					"Tripadvisor rendered session could not be prepared",
					data,
				)
			}
			observation, attempts, panelOpened, newChatOpened, err :=
				ensureSession(
					ctx,
					session,
					config.Timeout,
					config.PollInterval,
					false,
				)
			data.Attempts = attempts
			data.PanelOpened = panelOpened
			data.NewChatOpened = newChatOpened
			data.PanelReady = observation.PanelReady
			data.ComposerReady = observation.ComposerReady
			data.HistoryReady = observation.HistoryReady
			if observation.SignInVisible {
				data.SessionMode = "anonymous"
			} else {
				data.SessionMode = "signed_in"
			}
			if err != nil {
				_ = lease.MarkIncomplete(context.Background())
				data.AuthState = "rendered_session_unavailable"
				return authFailure(
					runID, config, webagent.StageAttached,
					target, pending,
					"tripadvisor_rendered_session_unavailable", "auth",
					"Tripadvisor AI panel, composer, and history controls did not become ready",
					data,
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return authFailure(
					runID, config, webagent.StageAttached,
					target, pending,
					"tripadvisor_auth_prepare_state_failed", "internal",
					"Tripadvisor session preparation could not be persisted",
					data,
				)
			}
			now := time.Now
			if config.Now != nil {
				now = config.Now
			}
			capturedAt := now().UTC().Format(time.RFC3339Nano)
			state := SessionState{
				SchemaVersion: SessionStateSchemaVersion,
				CapturedAt:    capturedAt,
				PanelReady:    observation.PanelReady,
				ComposerReady: observation.ComposerReady,
				HistoryReady:  observation.HistoryReady,
				SessionMode:   data.SessionMode,
				Source:        "headed-cdp-rendered-session",
			}
			if err := config.Store.SaveSession(ctx, state); err != nil {
				_ = lease.MarkIncomplete(context.Background())
				return authFailure(
					runID, config, webagent.StagePrepared,
					target, pending,
					"tripadvisor_auth_state_write_failed", "internal",
					"Tripadvisor safe rendered-session evidence could not be persisted",
					data,
				)
			}
			if err := lease.MarkTerminal(ctx); err != nil {
				return authFailure(
					runID, config, webagent.StageObserveTerminal,
					target, pending,
					"tripadvisor_auth_terminal_state_failed", "internal",
					"Tripadvisor session terminal state could not be persisted",
					data,
				)
			}
			data.AuthState = "ready"
			data.CapturedAt = capturedAt
			return operationSuccess(
				runID, config.BuildCommit,
				webagent.OperationAuthRefresh,
				webagent.StateReady,
				webagent.StageObserveTerminal,
				"headed_browser",
				target, pending, nil, nil, data,
				[]string{
					"cdp workflow agent tripadvisor doctor --json",
					"cdp workflow agent tripadvisor conversations list --json",
				},
			)
		},
	)
}

func Doctor(
	ctx context.Context,
	store *Store,
	now time.Time,
	buildCommit string,
) webagent.Result {
	if store == nil {
		return UnavailableDoctor(buildCommit)
	}
	status := store.Status(ctx, now, DefaultAuthTTL)
	data := DoctorData{
		SchemaVersion: DoctorSchemaVersion,
		Session:       status,
		BrowserSubmit: "unavailable",
		RenderedReads: "unavailable",
		BrowserMode:   "headed",
		BrowserProbed: false,
	}
	if status.Ready {
		data.BrowserSubmit = "ready"
		data.RenderedReads = "ready"
		result := webagent.NewMetadataResult(
			webagent.ProviderTripadvisor,
			webagent.OperationDoctor,
			data,
			buildCommit,
			[]string{
				"cdp workflow agent tripadvisor capabilities --json",
				"cdp workflow agent tripadvisor conversations list --json",
			},
		)
		result.Evidence.ReadMode = "owner_only_local_state"
		return result
	}
	result := operationFailure(
		webagent.NewRunID(), buildCommit,
		webagent.OperationDoctor,
		webagent.StageMetadata,
		"owner_only_local_state",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		nil, nil,
		"tripadvisor_session_"+status.State, "auth",
		"Tripadvisor rendered session evidence is not ready",
		"", data,
		[]string{
			"cdp workflow agent tripadvisor auth refresh --json",
		},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func UnavailableDoctor(buildCommit string) webagent.Result {
	result := operationFailure(
		webagent.NewRunID(), buildCommit,
		webagent.OperationDoctor,
		webagent.StageMetadata,
		"owner_only_local_state",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		nil, nil,
		"tripadvisor_state_unavailable", "internal",
		"Tripadvisor owner-only state is unavailable",
		"",
		map[string]any{
			"schema_version": DoctorSchemaVersion,
		},
		[]string{"cdp doctor --json"},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func ensureSession(
	ctx context.Context,
	session *cdp.PageSession,
	timeout time.Duration,
	poll time.Duration,
	requireBlank bool,
) (
	sessionObservation,
	int,
	bool,
	bool,
	error,
) {
	if timeout <= 0 {
		timeout = defaultObservationTimeout
	}
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	var observation sessionObservation
	panelOpened := false
	newChatOpened := false
	attempts := 0
	for {
		attempts++
		err := observeSession(ctx, session, &observation)
		if err == nil &&
			observation.OriginReady &&
			observation.PanelReady &&
			observation.ComposerReady &&
			observation.HistoryReady {
			if !requireBlank {
				return observation, attempts, panelOpened, newChatOpened, nil
			}
			var route routeObservation
			routeErr := observeRoute(ctx, session, &route)
			if routeErr == nil && route.Blank && route.AnswerCount == 0 &&
				route.PromptCount == 0 {
				return observation, attempts, panelOpened, newChatOpened, nil
			}
			if !newChatOpened &&
				observation.NewChatCount == 1 &&
				observation.NewChatReady {
				if clickErr := clickUniqueSessionControl(
					ctx,
					session,
					"new_chat",
				); clickErr == nil {
					newChatOpened = true
				}
			}
		} else if err == nil &&
			!panelOpened &&
			observation.PanelOpenerCount == 1 &&
			observation.PanelOpenerReady {
			if clickErr := clickUniqueSessionControl(
				ctx,
				session,
				"panel",
			); clickErr == nil {
				panelOpened = true
			}
		} else if err == nil &&
			observation.PanelReady &&
			!observation.ComposerReady &&
			!newChatOpened &&
			observation.NewChatCount == 1 &&
			observation.NewChatReady {
			if clickErr := clickUniqueSessionControl(
				ctx,
				session,
				"new_chat",
			); clickErr == nil {
				newChatOpened = true
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return observation, attempts, panelOpened, newChatOpened,
				fmt.Errorf("rendered session deadline exhausted")
		}
		delay := poll
		if delay > remaining {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return observation, attempts, panelOpened, newChatOpened, ctx.Err()
		case <-timer.C:
		}
	}
}

func observeSession(
	ctx context.Context,
	session *cdp.PageSession,
	observation *sessionObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 && rect.width > 0 && rect.height > 0;
	  };
	  const actionable = element => {
	    if (!element || !visible(element)) return false;
	    const rect = element.getBoundingClientRect();
	    const x = rect.left + rect.width / 2;
	    const y = rect.top + rect.height / 2;
	    const top = document.elementFromPoint(x, y);
	    return Boolean(
	      top && (top === element || element.contains(top)) &&
	      !element.hasAttribute('disabled') &&
	      element.getAttribute('aria-disabled') !== 'true'
	    );
	  };
	  const panels = Array.from(document.querySelectorAll(
	    'aside[aria-label="AI Chat Assistant"]'
	  )).filter(visible);
	  const panel = panels.length === 1 ? panels[0] : null;
	  const composers = panel ? Array.from(panel.querySelectorAll(
	    'textarea[aria-label="Ask anything"]'
	  )).filter(visible) : [];
	  const histories = panel ? Array.from(panel.querySelectorAll(
	    'button[aria-label="All chats"]'
	  )).filter(visible) : [];
	  const newChats = panel ? Array.from(panel.querySelectorAll('button'))
	    .filter(button => visible(button) &&
	      (button.innerText || button.textContent || '').trim() === 'New chat') : [];
	  const openers = Array.from(document.querySelectorAll('button,a'))
	    .filter(element => {
	      const text = (element.innerText || element.textContent || '').trim();
	      return visible(element) && (text === 'Plan with AI' || text === 'Ask AI');
	    });
	  const signIns = Array.from(document.querySelectorAll('button,a'))
	    .filter(element => visible(element) &&
	      (element.innerText || element.textContent || '').trim() === 'Sign in');
	  return {
	    origin_ready: location.origin === 'https://www.tripadvisor.com',
	    panel_count: panels.length,
	    panel_ready: Boolean(panel),
	    composer_count: composers.length,
	    composer_ready: composers.length === 1 && actionable(composers[0]),
	    history_count: histories.length,
	    history_ready: histories.length === 1 && actionable(histories[0]),
	    sign_in_visible: signIns.length > 0,
	    panel_opener_count: openers.length,
	    panel_opener_ready: openers.length === 1 && actionable(openers[0]),
	    new_chat_count: newChats.length,
	    new_chat_ready: newChats.length === 1 && actionable(newChats[0])
	  };
	})()`, observation)
}

func clickUniqueSessionControl(
	ctx context.Context,
	session *cdp.PageSession,
	kind string,
) error {
	expression := `(() => {
	  const kind = ` + fmt.Sprintf("%q", kind) + `;
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
	  let candidates = [];
	  if (kind === 'new_chat' && panel.length === 1) {
	    candidates = Array.from(panel[0].querySelectorAll('button'))
	      .filter(button => visible(button) &&
	        (button.innerText || button.textContent || '').trim() === 'New chat');
	  }
	  if (kind === 'panel') {
	    candidates = Array.from(document.querySelectorAll('button,a'))
	      .filter(element => {
	        const text = (element.innerText || element.textContent || '').trim();
	        return visible(element) && (text === 'Plan with AI' || text === 'Ask AI');
	      });
	  }
	  if (candidates.length !== 1 ||
	      candidates[0].hasAttribute('disabled') ||
	      candidates[0].getAttribute('aria-disabled') === 'true') {
	    return {clicked: false, count: candidates.length};
	  }
	  candidates[0].click();
	  return {clicked: true, count: 1};
	})()`
	var result struct {
		Clicked bool `json:"clicked"`
		Count   int  `json:"count"`
	}
	if err := evaluateInto(ctx, session, expression, &result); err != nil {
		return err
	}
	if !result.Clicked || result.Count != 1 {
		return fmt.Errorf("unique Tripadvisor %s control was not clicked", kind)
	}
	return nil
}

func authFailure(
	runID string,
	config AuthRefreshConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	data AuthRefreshData,
) webagent.Result {
	return operationFailure(
		runID, config.BuildCommit,
		webagent.OperationAuthRefresh,
		stage, "headed_browser",
		target, cleanup, nil, nil,
		code, errClass, message, "", data,
		cleanupCommands(runID, cleanup),
	)
}
