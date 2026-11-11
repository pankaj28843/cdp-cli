package perplexity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestPreSendReadinessFailureReportsOnlySanitizedDiagnostics(t *testing.T) {
	const secret = "secret-cookie-and-private-page-text"
	for _, operation := range []string{"auth", "ask"} {
		t.Run(operation, func(t *testing.T) {
			stateDir := t.TempDir()
			client := testsupport.NewBrowser("user-page")
			client.Evaluate = func(string, *testsupport.Browser) (any, error) {
				return nil, errors.New(secret)
			}
			engine, journal, err := testsupport.NewRuntime(stateDir, client)
			if err != nil {
				t.Fatalf("NewRuntime: %v", err)
			}
			store, err := NewStore(stateDir)
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			browser := BrowserConfig{Client: client, Engine: engine, Journal: journal}
			var result webagent.Result
			if operation == "auth" {
				result = RefreshAuth(context.Background(), AuthRefreshConfig{
					BrowserConfig:      browser,
					Store:              store,
					ObservationTimeout: 60 * time.Millisecond,
				})
			} else {
				result = Ask(context.Background(), AskConfig{
					BrowserConfig:   browser,
					Store:           store,
					Timeout:         time.Second,
					ComposerTimeout: 60 * time.Millisecond,
					PollInterval:    time.Millisecond,
				}, "A harmless synthetic question")
			}
			want := "perplexity_auth_readiness_failed"
			if operation == "ask" {
				want = "perplexity_composer_readiness_failed"
			}
			if result.Error == nil || result.Error.Code != want {
				t.Fatalf("error = %+v, want %q", result.Error, want)
			}
			payload, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal result data: %v", err)
			}
			if strings.Contains(string(payload), secret) {
				t.Fatal("private observation error escaped into result")
			}
			var envelope struct {
				Data struct {
					ReadinessDiagnostic struct {
						Attempt                int    `json:"attempt"`
						Stage                  string `json:"stage"`
						ValidObservations      int    `json:"valid_observations"`
						StageValidObservations int    `json:"stage_valid_observations"`
						ErrorCategory          string `json:"error_category"`
					} `json:"readiness_diagnostic"`
				} `json:"data"`
			}
			if err := json.Unmarshal(payload, &envelope); err != nil {
				t.Fatalf("decode result data: %v", err)
			}
			data := envelope.Data
			if data.ReadinessDiagnostic.Attempt != 3 ||
				data.ReadinessDiagnostic.Stage != "hard_reload" ||
				data.ReadinessDiagnostic.ErrorCategory != "observation_error" ||
				data.ReadinessDiagnostic.ValidObservations < 0 ||
				data.ReadinessDiagnostic.StageValidObservations < 0 {
				t.Fatalf("missing bounded readiness diagnostic: %+v", data.ReadinessDiagnostic)
			}
			_, _, _, _, _, sends, targets := client.Snapshot()
			if sends != 0 {
				t.Fatalf("unexpected Send count: %d", sends)
			}
			if _, present := targets["owned-1"]; present {
				t.Fatal("owned target was not closed")
			}
		})
	}
}
