package gemini

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestOwnerOnlyStateRoundTripAfterLiveContract(t *testing.T) {
	stateDir := t.TempDir()
	store, err := NewStore(stateDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Date(2026, 7, 25, 20, 0, 0, 0, time.UTC)
	if err := store.SaveAuth(context.Background(), AuthState{
		SchemaVersion:         AuthStateSchemaVersion,
		CapturedAt:            now.Format(time.RFC3339Nano),
		SignedIn:              true,
		SessionCookieObserved: true,
		Source:                "headed-cdp-safe-auth-evidence",
	}); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}
	if err := store.SaveRuntime(context.Background(), RuntimeCapabilities{
		SchemaVersion:         RuntimeCapabilitiesSchemaVersion,
		CapturedAt:            now.Format(time.RFC3339Nano),
		CurrentMode:           "Flash",
		ModeOptions:           []string{"Flash", "Pro"},
		FileUploadControl:     "observed",
		FileUploadAction:      "unsupported",
		DeepResearchSelected:  false,
		ExplicitModeSelection: "request_shape_unobserved",
		Source:                "headed-cdp-rendered-controls",
	}); err != nil {
		t.Fatalf("SaveRuntime: %v", err)
	}

	if status := store.AuthStatus(
		context.Background(),
		now.Add(time.Minute),
		DefaultAuthTTL,
	); !status.Ready || status.State != "ready" {
		t.Fatalf("AuthStatus = %+v", status)
	}
	runtime := store.RuntimeStatus(
		context.Background(),
		now.Add(time.Minute),
		DefaultCapabilitiesTTL,
	)
	if !runtime.Ready ||
		runtime.CurrentMode != "Flash" ||
		len(runtime.ModeOptions) != 2 {
		t.Fatalf("RuntimeStatus = %+v", runtime)
	}

	for _, relative := range []string{
		RelativeAuthStatePath,
		RelativeCapabilitiesPath,
	} {
		path := filepath.Join(stateDir, filepath.FromSlash(relative))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", relative, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions = %o", relative, info.Mode().Perm())
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		for _, forbidden := range []string{
			"cookie_value",
			"authorization",
			"csrf",
			"prompt",
			"answer",
		} {
			if strings.Contains(strings.ToLower(string(raw)), forbidden) {
				t.Fatalf("%s contains forbidden field %q", relative, forbidden)
			}
		}
	}
}

func TestExactCapturedPromptFingerprintRequiresStrictCopyEvidence(t *testing.T) {
	prompt := "Review this design.\n\tpreserve := \"two  spaces\""
	want := fingerprintPrompt(prompt)
	observation := promptCaptureObservation{
		Prompt:               prompt,
		QueryCount:           1,
		CopyButtonCount:      1,
		ClipboardIntercepted: true,
		Captured:             true,
	}
	if got := exactCapturedPromptFingerprint(&observation); got != want {
		t.Fatalf("exact captured prompt fingerprint = %q, want %q", got, want)
	}

	observation.Prompt = strings.Replace(prompt, "two  spaces", "two spaces", 1)
	if got := exactCapturedPromptFingerprint(&observation); got == want {
		t.Fatal("interior whitespace mutation retained exact prompt identity")
	}

	observation.Prompt = "Review this design.  \nnext line"
	trailingBeforeNewline := exactCapturedPromptFingerprint(&observation)
	observation.Prompt = "Review this design.\nnext line"
	if got := exactCapturedPromptFingerprint(&observation); got == trailingBeforeNewline {
		t.Fatal("whitespace before an interior newline lost exact prompt identity")
	}

	observation.Prompt = prompt
	observation.CopyButtonCount = 2
	if got := exactCapturedPromptFingerprint(&observation); got != "" {
		t.Fatalf("ambiguous Copy prompt controls produced fingerprint %q", got)
	}
}

func TestConversationListSignatureDeduplicatesOnlySafeIDs(t *testing.T) {
	signature, count := conversationListSignature([]renderedConversation{
		{ID: "1234567890abcdef"},
		{ID: "1234567890abcdef"},
		{ID: "fedcba0987654321"},
		{ID: "unsafe"},
	})
	if count != 2 ||
		signature != "1234567890abcdef\nfedcba0987654321" {
		t.Fatalf("signature = %q, count = %d", signature, count)
	}
}

func TestConversationListAtEndRequiresReadyBottom(t *testing.T) {
	for name, test := range map[string]struct {
		scroller listScrollerObservation
		want     bool
	}{
		"not ready": {
			scroller: listScrollerObservation{
				ScrollTop:    900,
				ScrollHeight: 1000,
				ClientHeight: 100,
			},
		},
		"not at bottom": {
			scroller: listScrollerObservation{
				Ready:        true,
				ScrollTop:    800,
				ScrollHeight: 1000,
				ClientHeight: 100,
			},
		},
		"at bottom": {
			scroller: listScrollerObservation{
				Ready:        true,
				ScrollTop:    900,
				ScrollHeight: 1000,
				ClientHeight: 100,
			},
			want: true,
		},
		"within layout tolerance": {
			scroller: listScrollerObservation{
				Ready:        true,
				ScrollTop:    898.5,
				ScrollHeight: 1000,
				ClientHeight: 100,
			},
			want: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := conversationListAtEnd(test.scroller); got != test.want {
				t.Fatalf("conversationListAtEnd() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCalibrationFailureRemainsNonRetryableAfterSend(t *testing.T) {
	data := CalibrationData{
		SchemaVersion:     CalibrationSchemaVersion,
		CompletionState:   "delete_not_performed",
		PromptFingerprint: strings.Repeat("a", 64),
		ReadMode:          "headed_browser",
		SendAction: &webagent.ActionEvidence{
			Dispatch:         webagent.DispatchPerformed,
			AttemptCount:     1,
			RawInputCount:    1,
			RetrySafe:        false,
			PendingPersisted: true,
		},
		DeleteAction: &webagent.ActionEvidence{
			Dispatch:      webagent.DispatchNotPerformed,
			AttemptCount:  1,
			RawInputCount: 0,
			RetrySafe:     true,
		},
		Metadata: map[string]any{},
	}
	result := calibrationFailure(
		"wa-test",
		CalibrationConfig{
			BrowserConfig: BrowserConfig{BuildCommit: "test"},
		},
		webagent.StagePrepared,
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		data.DeleteAction,
		conversationRef("1234567890abcdef"),
		"gemini_calibration_delete_not_performed",
		"provider",
		"Gemini calibration delete was not performed; do not rerun calibration",
		"",
		data,
		[]string{"cdp workflow agent gemini calibration cleanup --json"},
	)
	if result.Action != data.SendAction ||
		result.Error == nil ||
		result.Error.RetrySafe {
		t.Fatalf("calibration failure = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(raw), calibrationPrompt) {
		t.Fatal("calibration prompt leaked into result")
	}
}

func TestCalibrationStatusDerivesClosedDeletedState(t *testing.T) {
	stateDir := t.TempDir()
	store, err := NewCalibrationStore(stateDir)
	if err != nil {
		t.Fatalf("NewCalibrationStore: %v", err)
	}
	record := CalibrationStateRecord{
		SchemaVersion:     CalibrationStateSchemaVersion,
		RunID:             "wa-calibration-test",
		State:             "deleted",
		PromptFingerprint: strings.Repeat("b", 64),
		TargetID:          "target-test",
		ConversationID:    "1234567890abcdef",
		SendDispatch:      browserflow.DispatchPerformed,
		DeleteDispatch:    browserflow.DispatchPerformed,
		Postcondition:     deletePostconditionProof,
		TargetClosed:      true,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	result := CalibrationStatus(
		context.Background(),
		store,
		nil,
		"test",
	)
	data, ok := result.Data.(CalibrationStatusData)
	if !result.OK ||
		!ok ||
		data.RecoveryRequired ||
		data.ConversationPresent ||
		!data.TargetClosed {
		t.Fatalf("CalibrationStatus = %+v", result)
	}
}
