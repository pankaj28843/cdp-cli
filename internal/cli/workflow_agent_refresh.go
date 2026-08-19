package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
	"github.com/pankaj28843/cdp-cli/internal/webagent/m365"
	"github.com/spf13/cobra"
)

const aggregateRefreshSchemaVersion = "webagent-aggregate-refresh/v1"

var aggregateRefreshProviders = []webagent.Provider{
	webagent.ProviderChatGPT,
	webagent.ProviderM365,
}

type aggregateRefreshData struct {
	SchemaVersion   string                    `json:"schema_version"`
	Operation       webagent.Operation        `json:"operation"`
	Requested       []webagent.Provider       `json:"requested"`
	Results         []aggregateProviderResult `json:"results"`
	RefreshInterval string                    `json:"refresh_interval"`
	CompletedAt     string                    `json:"completed_at"`
}

type aggregateProviderResult struct {
	Provider webagent.Provider `json:"provider"`
	Status   string            `json:"status"`
	Reason   string            `json:"reason,omitempty"`
	Result   *webagent.Result  `json:"result,omitempty"`
}

func (a *app) newWorkflowAgentAggregateAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authenticated provider auth as one isolated refresh operation",
		Long: "Refresh the registered online-provider auth evidence through one provider-neutral boundary. " +
			"Each provider is attempted independently; one provider failure never prevents the others from running. " +
			"Provider-specific commands remain available for diagnostics and recovery.",
		Example: "  cdp workflow agent auth refresh --json\n" +
			"  cdp workflow agent auth refresh --provider m365 --json",
	}
	cmd.AddCommand(a.newWorkflowAgentAggregateRefreshCommand(webagent.OperationAuthRefresh))
	return cmd
}

func (a *app) newWorkflowAgentAggregateCapabilitiesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Manage provider capability evidence as one isolated refresh operation",
		Long: "Refresh the registered online-provider capability evidence through one provider-neutral boundary. " +
			"Each provider is attempted independently and the aggregate returns per-provider outcomes.",
		Example: "  cdp workflow agent capabilities refresh --json\n" +
			"  cdp workflow agent capabilities refresh --provider chatgpt --json",
	}
	cmd.AddCommand(a.newWorkflowAgentAggregateRefreshCommand(webagent.OperationCapabilities))
	return cmd
}

func (a *app) newWorkflowAgentAggregateRefreshCommand(operation webagent.Operation) *cobra.Command {
	var requested []string
	use := "refresh"
	short := "Refresh all registered online-provider evidence"
	long := "Run bounded provider refreshes sequentially through the shared web-agent boundary. " +
		"The command records every provider outcome, continues after an individual failure, and is safe to call from " +
		"one periodic maintenance loop rather than installing one scheduler per provider."
	if operation == webagent.OperationCapabilities {
		short = "Refresh all registered online-provider capability evidence"
		long = "Run bounded provider capability refreshes sequentially through the shared web-agent boundary. " +
			"The command records every provider outcome and continues after an individual failure."
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Example: func() string {
			if operation == webagent.OperationCapabilities {
				return "  cdp workflow agent capabilities refresh --json\n" +
					"  cdp workflow agent capabilities refresh --provider m365 --json"
			}
			return "  cdp workflow agent auth refresh --json\n" +
				"  cdp workflow agent auth refresh --provider m365 --json"
		}(),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.commandContextWithDefault(cmd, 2*time.Minute)
			defer cancel()
			providers, err := parseAggregateProviders(requested)
			if err != nil {
				return err
			}
			if !a.selectHeadedProviderRuntime() {
				return a.renderWebAgentResult(
					ctx,
					"aggregate refresh: headed browser required",
					aggregateRefreshFailure(a.build.Commit, operation, providers,
						"provider-neutral refresh requires the headed browser runtime"),
				)
			}
			result := a.runAggregateRefresh(ctx, operation, providers)
			human := fmt.Sprintf("aggregate %s: %v", operation, result.State)
			return a.renderWebAgentResult(ctx, human, result)
		},
	}
	cmd.Flags().StringSliceVar(&requested, "provider", nil, "provider to refresh; repeat or comma-separate (default: chatgpt,m365)")
	return cmd
}

func parseAggregateProviders(requested []string) ([]webagent.Provider, error) {
	if len(requested) == 0 {
		return append([]webagent.Provider{}, aggregateRefreshProviders...), nil
	}
	seen := make(map[webagent.Provider]bool, len(requested))
	providers := make([]webagent.Provider, 0, len(requested))
	for _, raw := range requested {
		provider, ok := webagent.ParseProvider(strings.ToLower(strings.TrimSpace(raw)))
		if !ok {
			return nil, commandError(
				"unknown_provider",
				"usage",
				fmt.Sprintf("unknown web-agent provider %q", raw),
				ExitUsage,
				[]string{"cdp workflow agent providers --json"},
			)
		}
		if seen[provider] {
			continue
		}
		seen[provider] = true
		providers = append(providers, provider)
	}
	return providers, nil
}

func (a *app) runAggregateRefresh(
	ctx context.Context,
	operation webagent.Operation,
	providers []webagent.Provider,
) webagent.Result {
	data := aggregateRefreshData{
		SchemaVersion:   aggregateRefreshSchemaVersion,
		Operation:       operation,
		Requested:       append([]webagent.Provider{}, providers...),
		Results:         make([]aggregateProviderResult, 0, len(providers)),
		RefreshInterval: "10m",
		CompletedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	succeeded := 0
	attempted := 0
	for _, provider := range providers {
		if !aggregateProviderSupported(provider, operation) {
			data.Results = append(data.Results, aggregateProviderResult{
				Provider: provider,
				Status:   "deferred",
				Reason:   "provider-specific refresh adapter is not registered at this release gate",
			})
			continue
		}
		attempted++
		child := a.refreshAggregateProvider(ctx, provider, operation)
		status := "failed"
		if child.OK {
			status = "ready"
			succeeded++
		}
		data.Results = append(data.Results, aggregateProviderResult{
			Provider: provider,
			Status:   status,
			Result:   &child,
		})
	}
	data.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)

	result := webagent.Result{
		OK:            succeeded == len(providers) && len(providers) > 0,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderCatalog,
		Operation:     operation,
		State:         webagent.StateIncomplete,
		Stage:         webagent.StageClosed,
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       webagent.NewRunID(),
			BuildCommit: normalizedAggregateBuildCommit(a.build.Commit),
			BrowserMode: "headed",
			ReadMode:    "aggregate_provider_refresh",
		},
		Cleanup: webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{
			"cdp workflow agent providers --json",
			"cdp workflow agent auth refresh --json",
			"cdp workflow agent capabilities refresh --json",
		},
	}
	if result.OK {
		result.State = webagent.StateReady
	} else if succeeded > 0 || attempted < len(providers) {
		result.OK = true
		result.State = webagent.StateIncomplete
	} else if succeeded == 0 && attempted == len(providers) && attempted > 0 {
		result.OK = false
		result.State = webagent.StateFailed
		result.Error = &webagent.OperationError{
			Code:      "aggregate_refresh_failed",
			ErrClass:  "provider",
			Message:   "every requested provider refresh failed",
			RetrySafe: true,
		}
	}
	return result
}

func aggregateProviderSupported(provider webagent.Provider, operation webagent.Operation) bool {
	return (provider == webagent.ProviderChatGPT || provider == webagent.ProviderM365) &&
		(operation == webagent.OperationAuthRefresh || operation == webagent.OperationCapabilities)
}

func (a *app) refreshAggregateProvider(
	ctx context.Context,
	provider webagent.Provider,
	operation webagent.Operation,
) webagent.Result {
	if provider == webagent.ProviderChatGPT {
		config, store, unavailable := a.chatgptBrowserOperationConfig(ctx, operation)
		if unavailable != nil {
			return *unavailable
		}
		if operation == webagent.OperationAuthRefresh {
			return chatgpt.RefreshAuth(ctx, chatgpt.AuthRefreshConfig{BrowserConfig: config, Store: store})
		}
		return chatgpt.RefreshCapabilities(ctx, chatgpt.CapabilityRefreshConfig{BrowserConfig: config, Store: store})
	}
	if provider == webagent.ProviderM365 {
		config, store, unavailable := a.m365BrowserOperationConfig(ctx, operation)
		if unavailable != nil {
			return *unavailable
		}
		refreshConfig := m365.AuthRefreshConfig{BrowserConfig: config, Store: store}
		if operation == webagent.OperationAuthRefresh {
			return m365.RefreshAuth(ctx, refreshConfig)
		}
		return m365.RefreshCapabilities(ctx, refreshConfig)
	}
	return m365.UnavailableOperation(
		a.build.Commit,
		operation,
		"provider_refresh_deferred",
		"unsupported",
		fmt.Sprintf("aggregate refresh for %s is deferred until its provider adapter is proven", provider),
	)
}

func aggregateRefreshFailure(
	buildCommit string,
	operation webagent.Operation,
	providers []webagent.Provider,
	message string,
) webagent.Result {
	result := webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderCatalog,
		Operation:     operation,
		State:         webagent.StateFailed,
		Stage:         webagent.StagePlanned,
		Error: &webagent.OperationError{
			Code:      "aggregate_refresh_unavailable",
			ErrClass:  "usage",
			Message:   message,
			RetrySafe: true,
		},
		Data: aggregateRefreshData{
			SchemaVersion:   aggregateRefreshSchemaVersion,
			Operation:       operation,
			Requested:       append([]webagent.Provider{}, providers...),
			Results:         []aggregateProviderResult{},
			RefreshInterval: "10m",
			CompletedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		},
		Evidence: webagent.Evidence{
			RunID:       webagent.NewRunID(),
			BuildCommit: normalizedAggregateBuildCommit(buildCommit),
			BrowserMode: "none",
			ReadMode:    "aggregate_provider_refresh",
		},
		Cleanup: webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{
			"cdp --browser-mode headed daemon status --json",
			"cdp workflow agent providers --json",
		},
	}
	return result
}

func normalizedAggregateBuildCommit(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value)
}
