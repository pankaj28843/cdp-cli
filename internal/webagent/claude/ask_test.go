package claude

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestAskSubmitsOnceAcknowledgesReadsAndClosesExactTarget(t *testing.T) {
	const prompt = "Review the private Claude transaction boundary"
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	client.ackConversationID = "conversation-1"
	client.ackStreaming = true
	config := newAskTestConfig(t, stateDir, client)
	config.HTTPClient = terminalDetailClient(prompt)

	result := Ask(context.Background(), config, prompt)
	if !result.OK ||
		result.State != webagent.StateTerminal ||
		result.Stage != webagent.StageClosed ||
		result.Action == nil ||
		result.Action.Dispatch != webagent.DispatchPerformed ||
		result.Action.AttemptCount != 1 ||
		result.Action.RawInputCount != 1 ||
		result.Action.RetrySafe ||
		!result.Action.PendingPersisted ||
		result.Conversation == nil ||
		result.Conversation.ID != "conversation-1" ||
		result.Evidence.Target == nil ||
		!result.Evidence.Target.Closed ||
		result.Cleanup.State != webagent.CleanupClosed {
		t.Fatalf("Ask result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Ask result validation: %v", err)
	}
	data, ok := result.Data.(AskData)
	if !ok ||
		data.Text != "Useful review" ||
		data.CompletionState != "terminal" ||
		data.ReadMode != "observed_stable_http" ||
		data.PromptFingerprint != fingerprintPrompt(prompt) ||
		data.DetailReadAttempts != 1 {
		t.Fatalf("Ask data = %#v", result.Data)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal Ask result: %v", err)
	}
	if strings.Contains(string(encoded), prompt) ||
		strings.Contains(string(encoded), "private thinking") {
		t.Fatalf("prompt or thinking leaked into Ask result: %s", encoded)
	}
	if client.insertedPrompt != prompt ||
		client.callCount("Input.insertText") != 1 ||
		client.callCount("Input.dispatchKeyEvent") != 2 ||
		client.callCount("Target.createTarget") != 1 ||
		client.callCount("Target.closeTarget") != 1 {
		t.Fatalf("Ask CDP counts=%+v inserted=%q", client.countSnapshot(), client.insertedPrompt)
	}
	if client.hasTarget("owned-1") || !client.hasTarget("user-page") {
		t.Fatalf("Ask targets: owned=%v user=%v", client.hasTarget("owned-1"), client.hasTarget("user-page"))
	}

	record, err := config.Journal.Load(context.Background(), result.Evidence.RunID)
	if err != nil {
		t.Fatalf("load Ask recovery record: %v", err)
	}
	if record.Phase != browserflow.PhaseClosed ||
		record.Dispatch != browserflow.DispatchPerformed ||
		record.ConversationID != "conversation-1" ||
		record.ActionAttemptCount != 1 ||
		record.RawInputCount != 1 {
		t.Fatalf("Ask recovery record = %+v", record)
	}
	raw, err := os.ReadFile(filepath.Join(
		stateDir,
		"webagent",
		"recovery",
		result.Evidence.RunID+".json",
	))
	if err != nil {
		t.Fatalf("read Ask recovery state: %v", err)
	}
	if strings.Contains(string(raw), prompt) || strings.Contains(string(raw), "Useful review") {
		t.Fatalf("prompt or answer leaked into recovery state: %s", raw)
	}
}

func TestAskUsesLiveComposerAndRenderedDetailWhenTemplateCacheIsUnavailable(
	t *testing.T,
) {
	const prompt = "Review without trusting cached auth age"
	for _, test := range []struct {
		name    string
		missing bool
		expired bool
	}{
		{name: "missing", missing: true},
		{name: "expired", expired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			client := newAuthFakeClient("user-page")
			client.ackConversationID = "conversation-rendered"
			client.renderedDetailText = "Rendered terminal answer."
			client.renderedDetailPrompt = prompt
			config := newAskTestConfig(t, stateDir, client)
			if test.missing {
				if err := os.Remove(config.Store.Path()); err != nil {
					t.Fatalf("remove cached template: %v", err)
				}
			}
			if test.expired {
				config.Now = func() time.Time {
					return readTestNow().Add(2 * DefaultAuthTTL)
				}
			}

			result := Ask(context.Background(), config, prompt)
			data, _ := result.Data.(AskData)
			if !result.OK ||
				result.State != webagent.StateTerminal ||
				data.ReadMode != "headed_browser" ||
				data.Text != "Rendered terminal answer." ||
				client.callCount("Input.dispatchKeyEvent") != 2 {
				t.Fatalf(
					"result=%+v data=%+v calls=%+v",
					result,
					data,
					client.countSnapshot(),
				)
			}
		})
	}
}

func TestAskRecoversLiveComposerAfterHardReloadBeforePromptMutation(t *testing.T) {
	const prompt = "Review after recovered composer readiness"
	stateDir := t.TempDir()
	client := newAuthFakeClient("user-page")
	client.composerReady = false
	client.composerReadyAfterReload = 2
	client.ackConversationID = "conversation-recovered"
	config := newAskTestConfig(t, stateDir, client)
	config.ComposerTimeout = time.Second
	config.HTTPClient = terminalDetailClient(prompt)

	result := Ask(context.Background(), config, prompt)
	if !result.OK ||
		result.Action == nil ||
		result.Action.RawInputCount != 1 ||
		client.callCount("Input.insertText") != 1 ||
		len(client.reloadIgnoreCache) != 2 ||
		client.reloadIgnoreCache[0] ||
		!client.reloadIgnoreCache[1] {
		t.Fatalf(
			"result=%+v reloads=%v calls=%+v",
			result,
			client.reloadIgnoreCache,
			client.countSnapshot(),
		)
	}
}

func TestAskPreSendFailureAndPromptBudgetNeverDispatch(t *testing.T) {
	t.Run("composer unavailable", func(t *testing.T) {
		stateDir := t.TempDir()
		client := newAuthFakeClient("user-page")
		client.composerReady = false
		config := newAskTestConfig(t, stateDir, client)
		config.ComposerTimeout = time.Second

		result := Ask(context.Background(), config, "Review")
		if result.OK ||
			result.Error == nil ||
			result.Error.Code != "claude_composer_evidence_not_observed" ||
			result.Action == nil ||
			result.Action.Dispatch != webagent.DispatchNotPerformed ||
			!result.Action.RetrySafe ||
			result.Action.RawInputCount != 0 ||
			result.Cleanup.State != webagent.CleanupClosed ||
			client.callCount("Input.insertText") != 0 ||
			client.callCount("Input.dispatchKeyEvent") != 0 {
			t.Fatalf("composer failure = %+v counts=%+v", result, client.countSnapshot())
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("composer failure validation: %v", err)
		}
	})

	t.Run("prompt too long", func(t *testing.T) {
		stateDir := t.TempDir()
		client := newAuthFakeClient("user-page")
		config := newAskTestConfig(t, stateDir, client)
		result := Ask(context.Background(), config, strings.Repeat("x", MaxPromptCharacters+1))
		if result.OK ||
			result.Error == nil ||
			result.Error.Code != "claude_prompt_too_long" ||
			client.callCount("Target.createTarget") != 0 {
			t.Fatalf("prompt budget result = %+v counts=%+v", result, client.countSnapshot())
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("prompt budget validation: %v", err)
		}
	})
}

func TestAskUnknownDispatchNeverResendsAndAcknowledgementCanResolveIt(t *testing.T) {
	t.Run("unacknowledged", func(t *testing.T) {
		const privateCanary = "PRIVATE-DISPATCH-CANARY"
		stateDir := t.TempDir()
		client := newAuthFakeClient("user-page")
		config := newAskTestConfig(t, stateDir, client)
		config.Timeout = 3 * time.Second
		config.Send = browserflow.DispatchFunc(func(context.Context, *cdp.PageSession) (browserflow.DispatchOutcome, error) {
			return browserflow.DispatchOutcome{
				Dispatch:          browserflow.DispatchUnknown,
				RawInputAttempted: true,
			}, errors.New(privateCanary)
		})

		result := Ask(context.Background(), config, "Review")
		if result.OK ||
			result.Error == nil ||
			result.Error.Code != "claude_submission_unacknowledged" ||
			result.Error.RetrySafe ||
			result.Action == nil ||
			result.Action.Dispatch != webagent.DispatchUnknown ||
			result.Action.RawInputCount != 1 ||
			client.callCount("Input.dispatchKeyEvent") != 0 ||
			result.Cleanup.State != webagent.CleanupClosed {
			t.Fatalf("unknown Ask result = %+v counts=%+v", result, client.countSnapshot())
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal unknown Ask: %v", err)
		}
		if strings.Contains(string(encoded), privateCanary) {
			t.Fatalf("private dispatcher error leaked: %s", encoded)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("unknown Ask validation: %v", err)
		}
	})

	t.Run("same-target acknowledgement resolves unknown", func(t *testing.T) {
		stateDir := t.TempDir()
		client := newAuthFakeClient("user-page")
		client.ackConversationID = "conversation-1"
		config := newAskTestConfig(t, stateDir, client)
		config.HTTPClient = terminalDetailClient("Review")
		config.Send = browserflow.DispatchFunc(func(context.Context, *cdp.PageSession) (browserflow.DispatchOutcome, error) {
			return browserflow.DispatchOutcome{
				Dispatch:          browserflow.DispatchUnknown,
				RawInputAttempted: true,
			}, errors.New("private ambiguity")
		})

		result := Ask(context.Background(), config, "Review")
		if !result.OK ||
			result.Action == nil ||
			result.Action.Dispatch != webagent.DispatchPerformed ||
			result.Error != nil {
			t.Fatalf("acknowledged unknown Ask = %+v", result)
		}
		record, err := config.Journal.Load(context.Background(), result.Evidence.RunID)
		if err != nil {
			t.Fatalf("load acknowledged recovery: %v", err)
		}
		if record.Dispatch != browserflow.DispatchPerformed || record.RetryAt != "" {
			t.Fatalf("acknowledged recovery record = %+v", record)
		}
	})
}

func newAskTestConfig(t *testing.T, stateDir string, client *authFakeClient) AskConfig {
	t.Helper()
	authConfig := newAuthRefreshTestConfig(t, stateDir, client, cdp.BrowserResourceBudgetOptions{
		MaxTabs: 15, MaxTabsSource: "test", MaxWindows: 5, BrowserMode: "headed",
	})
	if err := authConfig.Store.Save(context.Background(), validAuthTemplate(readTestNow())); err != nil {
		t.Fatalf("save Ask auth template: %v", err)
	}
	return AskConfig{
		Client:          client,
		Engine:          authConfig.Engine,
		Journal:         authConfig.Journal,
		Admission:       authConfig.Admission,
		Store:           authConfig.Store,
		BuildCommit:     "test-commit",
		Timeout:         20 * time.Millisecond,
		ComposerTimeout: time.Second,
		PollInterval:    time.Millisecond,
		DetailDelays:    []time.Duration{0},
		Now:             readTestNow,
	}
}

func terminalDetailClient(prompt string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, terminalDetailPayload(prompt)), nil
	})}
}
