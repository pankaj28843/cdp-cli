package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
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
	if catalog.SchemaVersion != webagent.CapabilitySchemaVersion || len(catalog.Providers) != 8 {
		t.Fatalf("providers catalog = %+v", catalog)
	}
}

func TestWorkflowAgentProvidersPolicyProjection(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"agents":{"disabled_providers":["chatgpt"]}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	for _, test := range []struct {
		name            string
		includeDisabled bool
		wantChatGPT     bool
		wantReason      string
	}{
		{name: "ordinary omits disabled", wantChatGPT: false},
		{name: "diagnostic explains disabled", includeDisabled: true, wantChatGPT: true, wantReason: "disabled_by_config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"--config", configPath, "workflow", "agent", "providers", "--json"}
			if test.includeDisabled {
				args = []string{"--config", configPath, "workflow", "agent", "providers", "--include-disabled", "--json"}
			}
			var out, errOut bytes.Buffer
			code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{Commit: "test-commit"})
			if code != cli.ExitOK {
				t.Fatalf("exit = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			var result webagent.Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			data, err := json.Marshal(result.Data)
			if err != nil {
				t.Fatalf("marshal catalog: %v", err)
			}
			var catalog webagent.CatalogData
			if err := json.Unmarshal(data, &catalog); err != nil {
				t.Fatalf("decode catalog: %v", err)
			}
			for _, provider := range catalog.Providers {
				if provider.Provider != webagent.ProviderChatGPT {
					continue
				}
				if !test.wantChatGPT {
					t.Fatalf("ordinary catalog included disabled ChatGPT: %+v", provider)
				}
				if provider.Reason != test.wantReason || provider.Availability != "disabled" {
					t.Fatalf("diagnostic ChatGPT = %+v", provider)
				}
				test.wantChatGPT = false
			}
			if test.wantChatGPT {
				t.Fatal("diagnostic catalog omitted disabled ChatGPT")
			}
		})
	}
}

func TestWorkflowAgentDisabledProviderStopsBeforeBrowser(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"agents":{"disabled_providers":["chatgpt"]}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"--config", configPath,
		"workflow", "agent", "chatgpt", "doctor", "--json",
	}, &out, &errOut, cli.BuildInfo{Commit: "test-commit"})
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var result webagent.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, out.String())
	}
	if result.OK || result.Error == nil || result.Error.Code != "provider_disabled" || result.Error.Reason != "disabled_by_config" || result.Operation != webagent.OperationDoctor {
		t.Fatalf("disabled provider result = %+v", result)
	}
	if strings.Contains(out.String(), "headed browser") {
		t.Fatalf("disabled provider reached browser readiness path: %s", out.String())
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

func TestWorkflowAgentChatGPTAskRejectsHeadlessBeforeBrowserAccess(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(
		context.Background(),
		[]string{
			"--browser-mode", "headless",
			"workflow", "agent", "chatgpt", "ask", "test prompt", "--json",
		},
		&out,
		&errOut,
		cli.BuildInfo{Commit: "test-commit"},
	)
	if code != cli.ExitUsage {
		t.Fatalf(
			"headless ChatGPT ask exit=%d stdout=%s stderr=%s",
			code,
			out.String(),
			errOut.String(),
		)
	}
	var result webagent.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode headless ChatGPT ask: %v", err)
	}
	if result.OK ||
		result.Operation != webagent.OperationAsk ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_headed_browser_required" ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("headless ChatGPT ask = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("headless ChatGPT ask validation: %v", err)
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
		"workflow agent auth refresh",
		"workflow agent capabilities refresh",
		"workflow agent claude capabilities",
		"workflow agent claude doctor",
		"workflow agent claude auth",
		"workflow agent gemini capabilities",
		"workflow agent gemini capabilities refresh",
		"workflow agent gemini ask",
		"schema webagent-operation",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("describe workflow agent missing %q:\n%s", want, out.String())
		}
	}
}

func TestWorkflowAgentHelpDocumentsGoogleAIPolicy(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"workflow", "agent", "--help"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("workflow agent help exit = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	for _, want := range []string{
		"agents.google.exclusive_ai_mode",
		"corporate/Zscaler",
		"--google-ai auto|mode|off",
		"exclusive Google AI Mode",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("workflow agent help missing %q:\n%s", want, out.String())
		}
	}
}

func TestWorkflowAgentHelpHidesBrowserModeSelectors(t *testing.T) {
	for _, args := range [][]string{
		{"workflow", "agent", "chatgpt", "ask", "--help"},
		{"workflow", "agent", "chatgpt", "conversations", "download-attachments", "--help"},
	} {
		var out, errOut bytes.Buffer
		code := cli.Execute(context.Background(), args, &out, &errOut, cli.BuildInfo{})
		if code != cli.ExitOK {
			t.Fatalf("workflow agent help exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		if strings.Contains(out.String(), "--browser-mode") ||
			strings.Contains(out.String(), "--browserMode") {
			t.Fatalf("workflow agent help exposed browser mode selectors for %q:\n%s", args, out.String())
		}
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"pages", "--help"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("pages help exit=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "--browser-mode") {
		t.Fatalf("direct cdp pages help lost browser-mode diagnostics:\n%s", out.String())
	}
}

func TestWebAgentSchemaCommands(t *testing.T) {
	for _, name := range []string{
		"webagent-operation",
		"webagent-capabilities",
		"webagent-provider-catalog",
		"webagent-aggregate-refresh",
		"webagent-aggregate-refresh-result",
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
