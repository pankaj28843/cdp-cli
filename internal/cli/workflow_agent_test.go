package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/alex"
	"github.com/pankaj28843/cdp-cli/internal/webagent/claude"
	"github.com/pankaj28843/cdp-cli/internal/webagent/gemini"
)

func TestWorkflowAgentProvidersJSONNeedsNoBrowser(t *testing.T) {
	var out, errOut bytes.Buffer
	build := cli.BuildInfo{Commit: "test-commit"}

	code := cli.Execute(context.Background(), []string{"workflow", "agent", "providers", "--json"}, &out, &errOut, build)
	if code != cli.ExitOK {
		t.Fatalf("workflow agent providers exit = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got webagent.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("providers output is invalid JSON: %v", err)
	}
	if !got.OK ||
		got.SchemaVersion != webagent.OperationSchemaVersion ||
		got.Provider != webagent.ProviderCatalog ||
		got.Operation != webagent.OperationProviders ||
		got.State != webagent.StateReady ||
		got.Stage != webagent.StageMetadata ||
		got.Action != nil ||
		got.Cleanup.State != webagent.CleanupNotRequired ||
		got.Evidence.BuildCommit != "test-commit" {
		t.Fatalf("providers result = %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("providers result validation: %v", err)
	}
	encodedData, err := json.Marshal(got.Data)
	if err != nil {
		t.Fatalf("marshal providers data: %v", err)
	}
	var catalog webagent.CatalogData
	if err := json.Unmarshal(encodedData, &catalog); err != nil {
		t.Fatalf("decode providers data: %v", err)
	}
	if catalog.SchemaVersion != webagent.CapabilitySchemaVersion || len(catalog.Providers) != 7 {
		t.Fatalf("providers catalog = %+v", catalog)
	}
}

func TestWorkflowAgentAdmissionResolutionRequiresExactSettledRecoveryEvidence(t *testing.T) {
	stateDir := t.TempDir()
	runID := "run-admission-unknown"
	gate, err := admission.New(admission.Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
	lease, err := gate.Acquire(context.Background(), admission.Request{
		Provider: "chatgpt", Operation: "ask", RunID: runID,
	})
	if err != nil {
		t.Fatalf("admission Acquire: %v", err)
	}
	if err := lease.Release(admission.Release{Outcome: admission.OutcomeUnknown}); err != nil {
		t.Fatalf("release unknown admission: %v", err)
	}
	var statusOut, statusErr bytes.Buffer
	code := cli.Execute(
		context.Background(),
		[]string{"--state-dir", stateDir, "workflow", "agent", "admission", "status", "chatgpt", "--json"},
		&statusOut,
		&statusErr,
		cli.BuildInfo{},
	)
	if code != cli.ExitOK {
		t.Fatalf("admission status exit=%d stdout=%s stderr=%s", code, statusOut.String(), statusErr.String())
	}
	var status struct {
		OK                 bool `json:"ok"`
		ResolutionRequired bool `json:"resolution_required"`
	}
	if err := json.Unmarshal(statusOut.Bytes(), &status); err != nil {
		t.Fatalf("decode admission status: %v", err)
	}
	if !status.OK || !status.ResolutionRequired {
		t.Fatalf("admission status = %+v, want quarantined unknown run", status)
	}

	var missingOut, missingErr bytes.Buffer
	code = cli.Execute(
		context.Background(),
		[]string{"--state-dir", stateDir, "workflow", "agent", "admission", "resolve", "chatgpt", runID, "--json"},
		&missingOut,
		&missingErr,
		cli.BuildInfo{},
	)
	if code != cli.ExitUsage || !strings.Contains(missingOut.String(), "admission_acknowledgement_required") {
		t.Fatalf("unacknowledged resolution exit=%d stdout=%s stderr=%s", code, missingOut.String(), missingErr.String())
	}

	var noEvidenceOut, noEvidenceErr bytes.Buffer
	code = cli.Execute(
		context.Background(),
		[]string{"--state-dir", stateDir, "workflow", "agent", "admission", "resolve", "chatgpt", runID, "--acknowledge-unknown", "--json"},
		&noEvidenceOut,
		&noEvidenceErr,
		cli.BuildInfo{},
	)
	if code != cli.ExitCheckFailed || !strings.Contains(noEvidenceOut.String(), "admission_recovery_evidence_missing") {
		t.Fatalf("evidence-free resolution exit=%d stdout=%s stderr=%s", code, noEvidenceOut.String(), noEvidenceErr.String())
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	journal, err := browserflow.NewFileJournal(stateDir)
	if err != nil {
		t.Fatalf("browserflow.NewFileJournal: %v", err)
	}
	if err := journal.Create(context.Background(), browserflow.Record{
		SchemaVersion:      browserflow.RecoverySchemaVersion,
		RunID:              runID,
		Provider:           "chatgpt",
		Operation:          "ask",
		Phase:              browserflow.PhaseClosed,
		ActionName:         "send",
		Dispatch:           browserflow.DispatchUnknown,
		ActionAttemptCount: 1,
		RawInputCount:      1,
		PendingPersisted:   true,
		TargetID:           "owned-target",
		Cleanup:            browserflow.CleanupClosed,
		CreatedAt:          now,
		UpdatedAt:          now,
	}); err != nil {
		t.Fatalf("create settled recovery record: %v", err)
	}

	var resolveOut, resolveErr bytes.Buffer
	code = cli.Execute(
		context.Background(),
		[]string{"--state-dir", stateDir, "workflow", "agent", "admission", "resolve", "chatgpt", runID, "--acknowledge-unknown", "--json"},
		&resolveOut,
		&resolveErr,
		cli.BuildInfo{},
	)
	if code != cli.ExitOK {
		t.Fatalf("admission resolve exit=%d stdout=%s stderr=%s", code, resolveOut.String(), resolveErr.String())
	}
	var resolved struct {
		OK                 bool              `json:"ok"`
		Outcome            admission.Outcome `json:"outcome"`
		ResolutionRequired bool              `json:"resolution_required"`
	}
	if err := json.Unmarshal(resolveOut.Bytes(), &resolved); err != nil {
		t.Fatalf("decode admission resolution: %v", err)
	}
	if !resolved.OK || resolved.Outcome != admission.OutcomeAcknowledged || resolved.ResolutionRequired {
		t.Fatalf("admission resolution = %+v, want exact acknowledged release", resolved)
	}
}

func TestWorkflowAgentAdmissionResolutionAcceptsExactAlexDirectActionEvidence(t *testing.T) {
	stateDir := t.TempDir()
	runID := "run-alex-unknown"
	gate, err := admission.New(admission.Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("admission.New: %v", err)
	}
	lease, err := gate.Acquire(context.Background(), admission.Request{
		Provider: "alex", Operation: "ask", RunID: runID,
	})
	if err != nil {
		t.Fatalf("admission Acquire: %v", err)
	}
	if err := lease.Release(admission.Release{Outcome: admission.OutcomeUnknown}); err != nil {
		t.Fatalf("release unknown admission: %v", err)
	}
	alexStore, err := alex.NewStore(stateDir)
	if err != nil {
		t.Fatalf("alex.NewStore: %v", err)
	}
	if err := alexStore.SaveAskRecord(context.Background(), alex.AskRecord{
		SchemaVersion:     alex.AskRecordSchemaVersion,
		RunID:             runID,
		PromptFingerprint: strings.Repeat("a", 64),
		CourseID:          "system-design-interview",
		ChapterID:         "design-a-rate-limiter",
		State:             "unknown",
		AttemptCount:      1,
		RawInputCount:     1,
		PendingPersisted:  true,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("save Ask Alex action evidence: %v", err)
	}

	var statusOut, statusErr bytes.Buffer
	code := cli.Execute(
		context.Background(),
		[]string{"--state-dir", stateDir, "workflow", "agent", "admission", "status", "alex", "--json"},
		&statusOut,
		&statusErr,
		cli.BuildInfo{},
	)
	if code != cli.ExitOK {
		t.Fatalf("Ask Alex admission status exit=%d stdout=%s stderr=%s", code, statusOut.String(), statusErr.String())
	}
	if strings.Contains(statusOut.String(), "recovery inspect") ||
		strings.Contains(statusOut.String(), "recovery close") ||
		!strings.Contains(statusOut.String(), "admission resolve alex") {
		t.Fatalf("Ask Alex direct-action recovery commands = %s", statusOut.String())
	}

	var out, errOut bytes.Buffer
	code = cli.Execute(
		context.Background(),
		[]string{"--state-dir", stateDir, "workflow", "agent", "admission", "resolve", "alex", runID, "--acknowledge-unknown", "--json"},
		&out,
		&errOut,
		cli.BuildInfo{},
	)
	if code != cli.ExitOK {
		t.Fatalf("Ask Alex admission resolve exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var resolved struct {
		OK           bool   `json:"ok"`
		Outcome      string `json:"outcome"`
		EvidenceKind string `json:"evidence_kind"`
		Cleanup      string `json:"cleanup"`
	}
	if err := json.Unmarshal(out.Bytes(), &resolved); err != nil {
		t.Fatalf("decode Ask Alex admission resolution: %v", err)
	}
	if !resolved.OK ||
		resolved.Outcome != string(admission.OutcomeAcknowledged) ||
		resolved.EvidenceKind != "direct_http_action_record" ||
		resolved.Cleanup != string(browserflow.CleanupNotRequired) {
		t.Fatalf("Ask Alex admission resolution = %+v", resolved)
	}
}

func TestWorkflowAgentProviderCapabilitiesAreExplicit(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "agent", "claude", "capabilities", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("claude capabilities exit = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got webagent.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("claude capabilities output is invalid JSON: %v", err)
	}
	if got.Provider != webagent.ProviderClaude || got.Operation != webagent.OperationCapabilities {
		t.Fatalf("claude capabilities result = %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("claude capabilities result validation: %v", err)
	}
	encodedData, err := json.Marshal(got.Data)
	if err != nil {
		t.Fatalf("marshal capabilities data: %v", err)
	}
	var capabilities webagent.Capabilities
	if err := json.Unmarshal(encodedData, &capabilities); err != nil {
		t.Fatalf("decode capabilities data: %v", err)
	}
	if capabilities.ImplementationStatus != "partial" || len(capabilities.Operations) == 0 {
		t.Fatalf("claude capabilities = %+v", capabilities)
	}
	implemented := map[webagent.Operation]bool{
		webagent.OperationCapabilities:        true,
		webagent.OperationDoctor:              true,
		webagent.OperationAuthRefresh:         true,
		webagent.OperationAsk:                 true,
		webagent.OperationConversationsList:   true,
		webagent.OperationConversationsDetail: true,
		webagent.OperationConversationsAwait:  true,
		webagent.OperationConversationsDelete: true,
		webagent.OperationCalibrate:           true,
	}
	for _, capability := range capabilities.Operations {
		if capability.Status == webagent.CapabilityUnsupported {
			if capability.Supported || capability.UnavailableBy == "" {
				t.Fatalf("unsupported operation = %+v", capability)
			}
			continue
		}
		if implemented[capability.Operation] {
			if !capability.Supported || capability.Status != webagent.CapabilityImplemented {
				t.Fatalf("implemented operation = %+v", capability)
			}
			continue
		}
		if capability.Supported || capability.Status != webagent.CapabilityPlanned || capability.UnavailableBy == "" {
			t.Fatalf("planned operation = %+v", capability)
		}
	}
}

func TestWorkflowAgentClaudeDoctorNeedsNoBrowser(t *testing.T) {
	stateDir := t.TempDir()
	store, err := claude.NewStore(stateDir)
	if err != nil {
		t.Fatalf("claude.NewStore: %v", err)
	}
	now := time.Now().UTC()
	template := claude.AuthTemplate{
		SchemaVersion:    claude.AuthTemplateSchemaVersion,
		Method:           "GET",
		Origin:           claude.Origin,
		OrganizationID:   "org-test",
		ListURL:          claude.Origin + "/api/organizations/org-test/chat_conversations_v2?limit=30&starred=false&consistency=eventual",
		Headers:          map[string]string{"accept": "application/json"},
		Cookies:          map[string]string{"sessionKey": "private-test-session"},
		BrowserUserAgent: "Browser/Test",
		CapturedAt:       now.Format(time.RFC3339Nano),
		Source:           "headed-cdp-observed-list-request",
	}
	if err := store.Save(context.Background(), template); err != nil {
		t.Fatalf("save Claude auth template: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(
		context.Background(),
		[]string{"--state-dir", stateDir, "workflow", "agent", "claude", "doctor", "--json"},
		&out,
		&errOut,
		cli.BuildInfo{Commit: "test-commit"},
	)
	if code != cli.ExitOK {
		t.Fatalf("Claude doctor exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result webagent.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode Claude doctor: %v", err)
	}
	if !result.OK ||
		result.Operation != webagent.OperationDoctor ||
		result.Evidence.BrowserMode != "none" ||
		result.Evidence.ReadMode != "owner_only_local_state" ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("Claude doctor = %+v", result)
	}
	if strings.Contains(out.String(), "private-test-session") || strings.Contains(out.String(), "org-test") {
		t.Fatalf("Claude doctor leaked private auth material: %s", out.String())
	}
}

func TestWorkflowAgentClaudeCalibrationStatusNeedsNoBrowser(t *testing.T) {
	stateDir := t.TempDir()
	store, err := claude.NewCalibrationStore(stateDir)
	if err != nil {
		t.Fatalf("claude.NewCalibrationStore: %v", err)
	}
	if err := store.Save(context.Background(), claude.CalibrationStateRecord{
		SchemaVersion:     claude.CalibrationStateSchemaVersion,
		RunID:             "wa-calibration-cli-test",
		State:             "deleted",
		PromptFingerprint: strings.Repeat("a", 64),
		TargetID:          "target-test",
		ConversationID:    "conversation-test",
		SendDispatch:      browserflow.DispatchPerformed,
		DeleteDispatch:    browserflow.DispatchPerformed,
		Postcondition:     "redirected_to_new_without_conversation_id",
		TargetClosed:      true,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("save Claude calibration state: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(
		context.Background(),
		[]string{
			"--state-dir", stateDir,
			"workflow", "agent", "claude", "calibration", "status",
			"--json",
		},
		&out,
		&errOut,
		cli.BuildInfo{Commit: "test-commit"},
	)
	if code != cli.ExitOK {
		t.Fatalf(
			"Claude calibration status exit=%d stdout=%s stderr=%s",
			code,
			out.String(),
			errOut.String(),
		)
	}
	var result webagent.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode Claude calibration status: %v", err)
	}
	if !result.OK ||
		result.Operation != webagent.OperationCalibrate ||
		result.Evidence.BrowserMode != "none" ||
		result.Evidence.ReadMode != "owner_only_local_state" ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("Claude calibration status = %+v", result)
	}
	encodedData, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatalf("marshal Claude calibration status data: %v", err)
	}
	var data claude.CalibrationStatusData
	if err := json.Unmarshal(encodedData, &data); err != nil {
		t.Fatalf("decode Claude calibration status data: %v", err)
	}
	if data.ConversationPresent || data.RecoveryRequired || !data.TargetClosed {
		t.Fatalf("Claude calibration status data = %+v", data)
	}
}

func TestWorkflowAgentClaudeAuthRefreshRejectsHeadlessBeforeBrowserAccess(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(
		context.Background(),
		[]string{"--browser-mode", "headless", "workflow", "agent", "claude", "auth", "refresh", "--json"},
		&out,
		&errOut,
		cli.BuildInfo{Commit: "test-commit"},
	)
	if code != cli.ExitUsage {
		t.Fatalf("headless Claude auth exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result webagent.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode headless Claude auth: %v", err)
	}
	if result.OK ||
		result.Operation != webagent.OperationAuthRefresh ||
		result.Error == nil ||
		result.Error.Code != "claude_headed_browser_required" ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("headless Claude auth = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("headless Claude auth validation: %v", err)
	}
}

func TestWorkflowAgentGeminiCapabilitiesAndDoctorNeedNoBrowser(t *testing.T) {
	stateDir := t.TempDir()
	store, err := gemini.NewStore(stateDir)
	if err != nil {
		t.Fatalf("gemini.NewStore: %v", err)
	}
	now := time.Now().UTC()
	if err := store.SaveAuth(context.Background(), gemini.AuthState{
		SchemaVersion:         gemini.AuthStateSchemaVersion,
		CapturedAt:            now.Format(time.RFC3339Nano),
		SignedIn:              true,
		SessionCookieObserved: true,
		Source:                "headed-cdp-safe-auth-evidence",
	}); err != nil {
		t.Fatalf("save Gemini auth state: %v", err)
	}
	if err := store.SaveRuntime(context.Background(), gemini.RuntimeCapabilities{
		SchemaVersion:         gemini.RuntimeCapabilitiesSchemaVersion,
		CapturedAt:            now.Format(time.RFC3339Nano),
		CurrentMode:           "Flash",
		ModeOptions:           []string{"Flash", "Pro"},
		FileUploadControl:     "observed",
		FileUploadAction:      "unsupported",
		ExplicitModeSelection: "request_shape_unobserved",
		Source:                "headed-cdp-rendered-controls",
	}); err != nil {
		t.Fatalf("save Gemini runtime capabilities: %v", err)
	}

	for _, args := range [][]string{
		{
			"--state-dir", stateDir,
			"workflow", "agent", "gemini", "capabilities", "--json",
		},
		{
			"--state-dir", stateDir,
			"workflow", "agent", "gemini", "doctor", "--json",
		},
	} {
		var out, errOut bytes.Buffer
		code := cli.Execute(
			context.Background(),
			args,
			&out,
			&errOut,
			cli.BuildInfo{Commit: "test-commit"},
		)
		if code != cli.ExitOK {
			t.Fatalf(
				"Gemini browser-free command exit=%d stdout=%s stderr=%s",
				code,
				out.String(),
				errOut.String(),
			)
		}
		var result webagent.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("decode Gemini browser-free result: %v", err)
		}
		if !result.OK ||
			result.Evidence.BrowserMode != "none" ||
			result.Cleanup.State != webagent.CleanupNotRequired {
			t.Fatalf("Gemini browser-free result = %+v", result)
		}
	}
}

func TestWorkflowAgentGeminiCalibrationStatusNeedsNoBrowser(t *testing.T) {
	stateDir := t.TempDir()
	store, err := gemini.NewCalibrationStore(stateDir)
	if err != nil {
		t.Fatalf("gemini.NewCalibrationStore: %v", err)
	}
	if err := store.Save(context.Background(), gemini.CalibrationStateRecord{
		SchemaVersion:     gemini.CalibrationStateSchemaVersion,
		RunID:             "wa-gemini-calibration-cli-test",
		State:             "deleted",
		PromptFingerprint: strings.Repeat("c", 64),
		TargetID:          "target-test",
		ConversationID:    "1234567890abcdef",
		SendDispatch:      browserflow.DispatchPerformed,
		DeleteDispatch:    browserflow.DispatchPerformed,
		Postcondition:     "redirected_to_app_without_conversation_id",
		TargetClosed:      true,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("save Gemini calibration state: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(
		context.Background(),
		[]string{
			"--state-dir", stateDir,
			"workflow", "agent", "gemini", "calibration", "status",
			"--json",
		},
		&out,
		&errOut,
		cli.BuildInfo{Commit: "test-commit"},
	)
	if code != cli.ExitOK {
		t.Fatalf(
			"Gemini calibration status exit=%d stdout=%s stderr=%s",
			code,
			out.String(),
			errOut.String(),
		)
	}
	var result webagent.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode Gemini calibration status: %v", err)
	}
	var data gemini.CalibrationStatusData
	encoded, _ := json.Marshal(result.Data)
	if err := json.Unmarshal(encoded, &data); err != nil {
		t.Fatalf("decode Gemini calibration status data: %v", err)
	}
	if !result.OK ||
		result.Evidence.BrowserMode != "none" ||
		data.RecoveryRequired ||
		data.ConversationPresent ||
		!data.TargetClosed {
		t.Fatalf("Gemini calibration status = %+v data=%+v", result, data)
	}
}

func TestWorkflowAgentGeminiAuthRefreshRejectsHeadlessBeforeBrowserAccess(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(
		context.Background(),
		[]string{
			"--browser-mode", "headless",
			"workflow", "agent", "gemini", "auth", "refresh", "--json",
		},
		&out,
		&errOut,
		cli.BuildInfo{Commit: "test-commit"},
	)
	if code != cli.ExitUsage {
		t.Fatalf(
			"headless Gemini auth exit=%d stdout=%s stderr=%s",
			code,
			out.String(),
			errOut.String(),
		)
	}
	var result webagent.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode headless Gemini auth: %v", err)
	}
	if result.OK ||
		result.Operation != webagent.OperationAuthRefresh ||
		result.Error == nil ||
		result.Error.Code != "gemini_headed_browser_required" ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("headless Gemini auth = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("headless Gemini auth validation: %v", err)
	}
}

func TestDescribeWorkflowAgentIncludesSchemaExamples(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "workflow agent", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe workflow agent exit = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	for _, want := range []string{
		"workflow agent providers",
		"workflow agent claude capabilities",
		"workflow agent claude doctor",
		"workflow agent claude auth",
		"workflow agent gemini capabilities",
		"workflow agent gemini capabilities refresh",
		"workflow agent gemini ask",
		"workflow agent recovery",
		"schema webagent-operation",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("describe workflow agent missing %q:\n%s", want, out.String())
		}
	}
}

func TestWebAgentSchemaCommands(t *testing.T) {
	for _, name := range []string{
		"webagent-operation",
		"webagent-capabilities",
		"webagent-provider-catalog",
		"webagent-operation-capability",
		"webagent-action",
		"webagent-error",
		"webagent-conversation",
		"webagent-target",
		"webagent-evidence",
		"webagent-cleanup",
	} {
		var out, errOut bytes.Buffer
		code := cli.Execute(context.Background(), []string{"schema", name, "--json"}, &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("schema %s exit = %d, want %d; stdout=%s stderr=%s", name, code, cli.ExitOK, out.String(), errOut.String())
		}
		if !strings.Contains(out.String(), `"name": "`+name+`"`) {
			t.Fatalf("schema %s output does not contain its name:\n%s", name, out.String())
		}
	}
}

func TestWorkflowAgentRecoveryInspectReadsExactSafeRecord(t *testing.T) {
	stateDir := t.TempDir()
	journal, err := browserflow.NewFileJournal(stateDir)
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	now := time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	record := browserflow.Record{
		SchemaVersion: browserflow.RecoverySchemaVersion,
		RunID:         "run-inspect",
		Provider:      "claude",
		Operation:     "auth.refresh",
		Phase:         browserflow.PhasePlanned,
		Cleanup:       browserflow.CleanupNotRequired,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := journal.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(
		context.Background(),
		[]string{"--state-dir", stateDir, "workflow", "agent", "recovery", "inspect", "run-inspect", "--json"},
		&out,
		&errOut,
		cli.BuildInfo{},
	)
	if code != cli.ExitOK {
		t.Fatalf("recovery inspect exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		OK            bool               `json:"ok"`
		SchemaVersion string             `json:"schema_version"`
		Record        browserflow.Record `json:"record"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode recovery inspect: %v", err)
	}
	if !got.OK ||
		got.SchemaVersion != browserflow.RecoverySchemaVersion ||
		got.Record.RunID != "run-inspect" ||
		got.Record.Phase != browserflow.PhasePlanned {
		t.Fatalf("recovery inspect = %+v", got)
	}
}
