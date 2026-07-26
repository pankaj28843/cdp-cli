package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestRefreshAuthObservesPrivateStateAndClosesOnlyExactTarget(t *testing.T) {
	const privateCanary = "PRIVATE-CLAUDE-SESSION-CANARY"
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	client.cookieValue = privateCanary
	config := newAuthRefreshTestConfig(t, stateDir, client, cdp.BrowserResourceBudgetOptions{
		MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
	})

	result := RefreshAuth(context.Background(), config)
	if !result.OK ||
		result.Operation != webagent.OperationAuthRefresh ||
		result.State != webagent.StateReady ||
		result.Stage != webagent.StageClosed ||
		result.Error != nil ||
		result.Evidence.Target == nil ||
		!result.Evidence.Target.Closed ||
		result.Cleanup.State != webagent.CleanupClosed {
		t.Fatalf("RefreshAuth result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("RefreshAuth validation: %v", err)
	}
	data, ok := result.Data.(AuthRefreshData)
	if !ok ||
		data.AuthState != "ready" ||
		!data.OrganizationDerived ||
		!data.SessionCookieObserved ||
		data.RequestShape != "observed_list" {
		t.Fatalf("RefreshAuth data = %#v", result.Data)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(encoded), privateCanary) ||
		strings.Contains(string(encoded), "org-private") ||
		strings.Contains(string(encoded), "secret-authorization") {
		t.Fatalf("private auth material leaked into result: %s", encoded)
	}

	template, err := config.Store.Load(context.Background())
	if err != nil {
		t.Fatalf("load persisted template: %v", err)
	}
	if template.OrganizationID != "org-private" ||
		template.Cookies["sessionKey"] != privateCanary ||
		template.Headers["accept"] != "application/json" {
		t.Fatalf("persisted template = %+v", template)
	}
	if _, ok := template.Headers["authorization"]; ok {
		t.Fatalf("forbidden authorization header persisted: %+v", template.Headers)
	}
	if client.hasTarget("owned-1") || !client.hasTarget("user-page") {
		t.Fatalf("target ownership after refresh: owned=%v user=%v", client.hasTarget("owned-1"), client.hasTarget("user-page"))
	}
	if client.callCount("Target.createTarget") != 1 ||
		client.callCount("Target.closeTarget") != 1 ||
		client.callCount("Input.insertText") != 0 ||
		client.callCount("Input.dispatchKeyEvent") != 0 {
		t.Fatalf("CDP counts = %+v", client.countSnapshot())
	}

	record, err := config.Journal.Load(context.Background(), result.Evidence.RunID)
	if err != nil {
		t.Fatalf("load recovery record: %v", err)
	}
	if record.Phase != browserflow.PhaseClosed ||
		record.Cleanup != browserflow.CleanupClosed ||
		record.ActionAttemptCount != 0 ||
		record.RawInputCount != 0 ||
		record.TargetID != "owned-1" {
		t.Fatalf("recovery record = %+v", record)
	}
	for _, path := range []string{
		filepath.Join(stateDir, "webagent", "recovery", result.Evidence.RunID+".json"),
		filepath.Join(stateDir, "webagent", "admission", "claude.json"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read safe state %s: %v", path, err)
		}
		if strings.Contains(string(raw), privateCanary) {
			t.Fatalf("private canary leaked into lifecycle state %s", path)
		}
	}
}

func TestRefreshAuthSignedOutAndBudgetFailureRemainTypedAndClean(t *testing.T) {
	t.Run("signed out", func(t *testing.T) {
		stateDir := t.TempDir()
		client := newAuthFakeClient("user-page")
		client.emitList = false
		client.cookieValue = ""
		config := newAuthRefreshTestConfig(t, stateDir, client, cdp.BrowserResourceBudgetOptions{
			MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
		})
		config.ObservationTimeout = time.Millisecond

		result := RefreshAuth(context.Background(), config)
		if result.OK ||
			result.Error == nil ||
			result.Error.Code != "claude_signed_out" ||
			result.Error.ErrClass != "auth" ||
			result.Stage != webagent.StageClosed ||
			result.Cleanup.State != webagent.CleanupClosed ||
			result.Evidence.Target == nil ||
			!result.Evidence.Target.Closed {
			t.Fatalf("signed-out result = %+v", result)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("signed-out result validation: %v", err)
		}
		if client.hasTarget("owned-1") || !client.hasTarget("user-page") {
			t.Fatalf("signed-out targets: owned=%v user=%v", client.hasTarget("owned-1"), client.hasTarget("user-page"))
		}
	})

	t.Run("bounded observation timeout retries reload", func(t *testing.T) {
		stateDir := t.TempDir()
		client := newAuthFakeClient("user-page")
		client.emitList = false
		client.readDeadlineImmediately = true
		config := newAuthRefreshTestConfig(t, stateDir, client, cdp.BrowserResourceBudgetOptions{
			MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
		})
		config.ObservationAttempts = 3

		result := RefreshAuth(context.Background(), config)
		if result.OK ||
			result.Error == nil ||
			result.Error.Code != "claude_list_request_not_observed" ||
			result.Stage != webagent.StageClosed ||
			client.callCount("Page.reload") != 2 {
			t.Fatalf("bounded-timeout result = %+v counts=%+v", result, client.countSnapshot())
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("bounded-timeout validation: %v", err)
		}
	})

	t.Run("budget", func(t *testing.T) {
		stateDir := t.TempDir()
		client := newAuthFakeClient("user-page")
		config := newAuthRefreshTestConfig(t, stateDir, client, cdp.BrowserResourceBudgetOptions{
			MaxTabs: 1, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
		})

		result := RefreshAuth(context.Background(), config)
		if result.OK ||
			result.Error == nil ||
			result.Error.Code != "claude_browser_resource_budget_exceeded" ||
			result.Cleanup.State != webagent.CleanupNotRequired ||
			result.Evidence.Target != nil ||
			client.callCount("Target.createTarget") != 0 {
			t.Fatalf("budget result = %+v counts=%+v", result, client.countSnapshot())
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("budget result validation: %v", err)
		}
	})
}

func TestRefreshAuthCleanupFailureReturnsExactRecoveryCommand(t *testing.T) {
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	client.fail["Target.closeTarget"] = errors.New("PRIVATE-CLOSE-CANARY")
	config := newAuthRefreshTestConfig(t, stateDir, client, cdp.BrowserResourceBudgetOptions{
		MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
	})

	result := RefreshAuth(context.Background(), config)
	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "claude_exact_target_cleanup_failed" ||
		result.Cleanup.State != webagent.CleanupFailed ||
		result.Cleanup.TargetID != "owned-1" ||
		!strings.Contains(result.Cleanup.RecoveryCommand, result.Evidence.RunID) ||
		result.Evidence.Target == nil ||
		result.Evidence.Target.Closed {
		t.Fatalf("cleanup-failure result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("cleanup-failure validation: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal cleanup failure: %v", err)
	}
	if strings.Contains(string(encoded), "PRIVATE-CLOSE-CANARY") {
		t.Fatalf("private cleanup error leaked: %s", encoded)
	}
	if !client.hasTarget("owned-1") || !client.hasTarget("user-page") {
		t.Fatalf("cleanup-failure targets: owned=%v user=%v", client.hasTarget("owned-1"), client.hasTarget("user-page"))
	}
}

func TestNetworkObserverRequiresSuccessfulSameOrganizationEvidence(t *testing.T) {
	observer := networkObserver{records: map[string]*requestRecord{}}
	observer.add(networkResponseEvent("session-owned-1", "request-1", 200))
	observer.add(networkRequestEvent("session-owned-1", "request-1"))
	observation, ok := observer.selectObservation(nil)
	if !ok ||
		observation.OrganizationID != "org-private" ||
		observation.RequestShape != "observed_list" ||
		observation.ListURL == "" {
		t.Fatalf("observed list = %+v ok=%v", observation, ok)
	}

	record := observer.records["request-1"]
	record.ResponseURL = Origin + "/api/organizations/other/chat_conversations_v2?starred=false"
	if _, ok := observer.selectObservation(nil); ok {
		t.Fatal("mismatched request/response organization was accepted")
	}
}

func newAuthRefreshTestConfig(
	t *testing.T,
	stateDir string,
	client *authFakeClient,
	budget cdp.BrowserResourceBudgetOptions,
) AuthRefreshConfig {
	t.Helper()
	journal, err := browserflow.NewFileJournal(stateDir)
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:            client,
		Journal:           journal,
		Budget:            budget,
		CloseTimeout:      20 * time.Millisecond,
		ClosePollInterval: time.Millisecond,
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("browserflow.New: %v", err)
	}
	gate, err := admission.New(admission.Config{
		StateDir:       stateDir,
		MinimumSpacing: 0,
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return AuthRefreshConfig{
		Client:              client,
		Engine:              engine,
		Journal:             journal,
		Admission:           gate,
		Store:               store,
		BuildCommit:         "test-commit",
		ObservationTimeout:  20 * time.Millisecond,
		ObservationAttempts: 1,
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
		},
	}
}

type authFakeClient struct {
	mu                      sync.Mutex
	targets                 map[string]cdp.TargetInfo
	events                  []cdp.Event
	counts                  map[string]int
	fail                    map[string]error
	nextID                  int
	emitList                bool
	cookieValue             string
	readDeadlineImmediately bool
	composerReady           bool
	quotaLimited            bool
	modelLabel              string
	ackConversationID       string
	ackStreaming            bool
	insertedPrompt          string
	deleteRoute             bool
	deleteStage             int
	renderedSidebarExpanded bool
	renderedConversations   []map[string]any
	renderedListSnapshots   [][]map[string]any
	renderedListReads       int
	renderedDetailText      string
	renderedDetailPrompt    string
	renderedDetailStreaming bool
}

func newAuthFakeClient(targetIDs ...string) *authFakeClient {
	client := &authFakeClient{
		targets:       map[string]cdp.TargetInfo{},
		counts:        map[string]int{},
		fail:          map[string]error{},
		emitList:      true,
		cookieValue:   "private-session",
		composerReady: true,
		modelLabel:    "Test Model",
	}
	for _, targetID := range targetIDs {
		client.targets[targetID] = cdp.TargetInfo{
			TargetID: targetID,
			Type:     "page",
			URL:      "https://user.test/",
		}
	}
	return client
}

func (c *authFakeClient) Call(_ context.Context, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[method]++
	if err := c.fail[method]; err != nil {
		return err
	}
	switch method {
	case "Target.getTargets":
		targets := make([]cdp.TargetInfo, 0, len(c.targets))
		for _, target := range c.targets {
			targets = append(targets, target)
		}
		return assignAuthJSON(result, map[string]any{"targetInfos": targets})
	case "Browser.getWindowForTarget":
		return assignAuthJSON(result, map[string]any{"windowId": 1})
	case "Target.createTarget":
		c.nextID++
		targetID := fmt.Sprintf("owned-%d", c.nextID)
		c.targets[targetID] = cdp.TargetInfo{
			TargetID: targetID,
			Type:     "page",
			URL:      authStringParam(params, "url"),
		}
		return assignAuthJSON(result, map[string]any{"targetId": targetID})
	case "Target.attachToTarget":
		targetID := authStringParam(params, "targetId")
		if _, ok := c.targets[targetID]; !ok {
			return fmt.Errorf("target not found")
		}
		return assignAuthJSON(result, map[string]any{"sessionId": "session-" + targetID})
	case "Browser.getVersion":
		return assignAuthJSON(result, map[string]any{"userAgent": "Browser/Test"})
	case "Target.activateTarget":
		return assignAuthJSON(result, map[string]any{})
	case "Target.detachFromTarget":
		return assignAuthJSON(result, map[string]any{})
	case "Target.closeTarget":
		targetID := authStringParam(params, "targetId")
		delete(c.targets, targetID)
		return assignAuthJSON(result, map[string]any{"success": true})
	default:
		return fmt.Errorf("unexpected browser call %s", method)
	}
}

func (c *authFakeClient) CallSession(_ context.Context, sessionID, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[method]++
	if err := c.fail[method]; err != nil {
		return err
	}
	switch method {
	case "Network.enable", "Page.enable", "Runtime.enable":
		return assignAuthJSON(result, map[string]any{})
	case "Page.navigate", "Page.reload":
		if c.emitList {
			c.events = append(c.events,
				networkRequestEvent(sessionID, "request-1"),
				networkResponseEvent(sessionID, "request-1", 200),
			)
		}
		return assignAuthJSON(result, map[string]any{"frameId": "frame-1"})
	case "Network.getCookies":
		cookies := []map[string]string{{"name": "other", "value": "value"}}
		if c.cookieValue != "" {
			cookies = append(cookies, map[string]string{"name": "sessionKey", "value": c.cookieValue})
		}
		return assignAuthJSON(result, map[string]any{"cookies": cookies})
	case "Runtime.evaluate":
		expression := authStringParam(params, "expression")
		value := any(map[string]any{})
		switch {
		case strings.Contains(expression, "menu_delete:"):
			value = map[string]any{
				"route_matches": c.deleteRoute,
				"header": map[string]any{
					"ready": c.deleteRoute && c.deleteStage == 0,
					"count": boolCount(c.deleteRoute && c.deleteStage == 0),
					"x":     10,
					"y":     20,
				},
				"menu_delete": map[string]any{
					"ready": c.deleteRoute && c.deleteStage == 1,
					"count": boolCount(c.deleteRoute && c.deleteStage == 1),
					"x":     30,
					"y":     40,
				},
				"confirm": map[string]any{
					"ready": c.deleteRoute && c.deleteStage == 2,
					"count": boolCount(c.deleteRoute && c.deleteStage == 2),
					"x":     50,
					"y":     60,
				},
			}
		case strings.Contains(expression, "deleted:"):
			value = map[string]any{"deleted": c.deleteRoute && c.deleteStage >= 3}
		case strings.Contains(expression, "expanded:"):
			value = map[string]any{
				"expanded": c.renderedSidebarExpanded,
				"ready":    c.renderedSidebarExpanded,
				"count":    1,
				"x":        10,
				"y":        20,
			}
		case strings.Contains(expression, "const conversations = []"):
			conversations := c.renderedConversations
			if len(c.renderedListSnapshots) > 0 {
				index := c.renderedListReads
				if index >= len(c.renderedListSnapshots) {
					index = len(c.renderedListSnapshots) - 1
				}
				conversations = c.renderedListSnapshots[index]
				c.renderedListReads++
			}
			value = map[string]any{"conversations": conversations}
		case strings.Contains(expression, "answer_count:"):
			value = map[string]any{
				"route_matches":   true,
				"conversation_id": c.ackConversationID,
				"text":            c.renderedDetailText,
				"prompt":          c.renderedDetailPrompt,
				"is_streaming":    c.renderedDetailStreaming,
				"answer_count":    boolCount(c.renderedDetailText != ""),
			}
		case strings.Contains(expression, "composer_ready"):
			value = map[string]any{
				"composer_ready": c.composerReady,
				"quota_limited":  c.quotaLimited,
				"model_label":    c.modelLabel,
			}
		case strings.Contains(expression, "range.selectNodeContents"):
			value = map[string]any{"ok": true}
		case strings.Contains(expression, "matches:"):
			value = map[string]any{"ok": true, "matches": c.insertedPrompt != ""}
		case strings.Contains(expression, "conversation_id"):
			value = map[string]any{
				"conversation_id": c.ackConversationID,
				"is_streaming":    c.ackStreaming,
			}
		}
		return assignAuthJSON(result, map[string]any{
			"result": map[string]any{"type": "object", "value": value},
		})
	case "Input.insertText":
		c.insertedPrompt = authStringParam(params, "text")
		return assignAuthJSON(result, map[string]any{})
	case "Input.dispatchKeyEvent":
		return assignAuthJSON(result, map[string]any{})
	case "Input.dispatchMouseEvent":
		if authStringParam(params, "type") == "mouseReleased" {
			c.deleteStage++
		}
		return assignAuthJSON(result, map[string]any{})
	default:
		return fmt.Errorf("unexpected session call %s", method)
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (c *authFakeClient) ReadEvent(ctx context.Context) (cdp.Event, error) {
	for {
		c.mu.Lock()
		if c.readDeadlineImmediately {
			c.mu.Unlock()
			return cdp.Event{}, context.DeadlineExceeded
		}
		if len(c.events) > 0 {
			event := c.events[0]
			c.events = c.events[1:]
			c.mu.Unlock()
			return event, nil
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return cdp.Event{}, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func (c *authFakeClient) hasTarget(targetID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.targets[targetID]
	return ok
}

func (c *authFakeClient) callCount(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[method]
}

func (c *authFakeClient) countSnapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.counts))
	for name, count := range c.counts {
		out[name] = count
	}
	return out
}

func networkRequestEvent(sessionID, requestID string) cdp.Event {
	return cdp.Event{
		SessionID: sessionID,
		Method:    "Network.requestWillBeSent",
		Params: mustAuthJSON(map[string]any{
			"requestId": requestID,
			"request": map[string]any{
				"url":    Origin + "/api/organizations/org-private/chat_conversations_v2?limit=30&starred=false&consistency=eventual",
				"method": "GET",
				"headers": map[string]any{
					"Accept":        "application/json",
					"Authorization": "secret-authorization",
				},
			},
		}),
	}
}

func networkResponseEvent(sessionID, requestID string, status int) cdp.Event {
	return cdp.Event{
		SessionID: sessionID,
		Method:    "Network.responseReceived",
		Params: mustAuthJSON(map[string]any{
			"requestId": requestID,
			"response": map[string]any{
				"url":    Origin + "/api/organizations/org-private/chat_conversations_v2?limit=30&starred=false&consistency=eventual",
				"status": status,
			},
		}),
	}
}

func authStringParam(params any, key string) string {
	data, _ := json.Marshal(params)
	var values map[string]any
	_ = json.Unmarshal(data, &values)
	value, _ := values[key].(string)
	return value
}

func assignAuthJSON(dst any, src any) error {
	if dst == nil {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func mustAuthJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
