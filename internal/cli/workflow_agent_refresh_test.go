package cli

import (
	"context"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/providerpolicy"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestAggregateRefreshRoutesImplementedProvidersAndContinuesAfterFailure(t *testing.T) {
	// Missing HOME fails each real adapter before any browser connection.
	t.Setenv("HOME", "")
	a := &app{}
	a.newRoot()
	for _, operation := range []webagent.Operation{webagent.OperationAuthRefresh, webagent.OperationCapabilities} {
		t.Run(string(operation), func(t *testing.T) {
			providers := []webagent.Provider{webagent.ProviderChatGPT, webagent.ProviderGemini, webagent.ProviderGrok, webagent.ProviderM365, webagent.ProviderPerplexity}
			if operation == webagent.OperationAuthRefresh {
				providers = append(providers, webagent.ProviderClaude, webagent.ProviderAlex, webagent.ProviderTripadvisor)
			}
			result := a.runAggregateRefresh(context.Background(), operation, providers, providerpolicy.Default())
			data := result.Data.(aggregateRefreshData)
			if result.OK || result.Error == nil || result.Error.Code != "aggregate_refresh_failed" || len(data.Results) != len(providers) {
				t.Fatalf("aggregate did not attempt every provider: %+v", result)
			}
			for i, entry := range data.Results {
				if entry.Provider != providers[i] || entry.Status != "failed" || entry.Result == nil || entry.Result.Provider != providers[i] || entry.Result.Operation != operation || entry.Result.Error == nil || entry.Result.Error.Code != string(providers[i])+"_state_unavailable" {
					t.Fatalf("provider adapter was not invoked: %+v", entry)
				}
			}
		})
	}
}

func TestAggregateRefreshDefersUnimplementedRuntimeCapabilities(t *testing.T) {
	providers := []webagent.Provider{webagent.ProviderClaude, webagent.ProviderAlex, webagent.ProviderTripadvisor}
	result := (&app{}).runAggregateRefresh(context.Background(), webagent.OperationCapabilities, providers, providerpolicy.Default())
	data := result.Data.(aggregateRefreshData)
	for _, entry := range data.Results {
		if entry.Status != "deferred" || entry.Result != nil {
			t.Fatalf("unimplemented runtime discovery was attempted: %+v", entry)
		}
	}
}
