package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestSelectHeadedProviderRuntimeOverridesAmbientDefaultOnly(t *testing.T) {
	t.Run("ambient default", func(t *testing.T) {
		a := &app{opts: options{}}
		a.newRoot()
		t.Setenv("CDP_BROWSER_MODE", "headless")
		if !a.selectHeadedProviderRuntime() {
			t.Fatal("ambient headless default was not overridden")
		}
		if got := a.browserModeName(); got != "headed" {
			t.Fatalf("provider browser mode = %q, want headed", got)
		}
	})

	t.Run("explicit headless", func(t *testing.T) {
		a := &app{opts: options{}}
		root := a.newRoot()
		if err := root.PersistentFlags().Set("browser-mode", "headless"); err != nil {
			t.Fatalf("set browser mode: %v", err)
		}
		if a.selectHeadedProviderRuntime() {
			t.Fatal("explicit headless mode was silently overridden")
		}
	})
}

func TestRenderWebAgentFailurePreservesEnvelopeAndNonzeroExit(t *testing.T) {
	var out bytes.Buffer
	a := &app{
		out:  &out,
		err:  &bytes.Buffer{},
		opts: options{json: true},
	}
	result := webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationDoctor,
		State:         webagent.StateFailed,
		Stage:         webagent.StagePlanned,
		Error: &webagent.OperationError{
			Code:      "claude_auth_missing",
			ErrClass:  "auth",
			Message:   "Claude auth evidence is missing",
			RetrySafe: true,
		},
		Data: map[string]any{"auth_state": "missing"},
		Evidence: webagent.Evidence{
			RunID:       "wa-render-failure",
			BuildCommit: "abc123",
			BrowserMode: "none",
			ReadMode:    "local_metadata",
		},
		Cleanup:      webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{"cdp workflow agent claude auth refresh --json"},
	}

	err := a.renderWebAgentResult(context.Background(), "Claude auth missing", result)
	var rendered *renderedResultExit
	if !errors.As(err, &rendered) {
		t.Fatalf("render error = %v, want renderedResultExit", err)
	}
	if rendered.ExitCode != ExitPermission {
		t.Fatalf("exit code = %d, want %d", rendered.ExitCode, ExitPermission)
	}
	var got webagent.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode rendered result: %v\n%s", err, out.String())
	}
	if got.OK || got.Error == nil || got.Error.Code != "claude_auth_missing" {
		t.Fatalf("rendered result = %+v", got)
	}
}
