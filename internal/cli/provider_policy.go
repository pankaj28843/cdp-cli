package cli

import (
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/config"
	"github.com/pankaj28843/cdp-cli/internal/providerpolicy"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/spf13/cobra"
)

const providerPolicySchemaVersion = "cdp-provider-policy/v1"

type providerPolicyResultData struct {
	SchemaVersion string `json:"schema_version"`
	Provider      string `json:"provider"`
	DisplayName   string `json:"display_name"`
	Enabled       bool   `json:"enabled"`
	Reason        string `json:"reason"`
	ConfigKey     string `json:"config_key"`
}

func (a *app) providerPolicy() (providerpolicy.Policy, error) {
	cfg, err := config.Load(a.opts.config)
	if err != nil {
		return providerpolicy.Policy{}, err
	}
	return providerpolicy.New(cfg.Agents.DisabledProviders)
}

func (a *app) guardWorkflowAgentProvider(cmd *cobra.Command) error {
	provider, ok := workflowAgentProvider(cmd)
	if !ok {
		return nil
	}
	policy, err := a.providerPolicy()
	if err != nil {
		return commandError(
			"invalid_config",
			"usage",
			err.Error(),
			ExitUsage,
			[]string{"cdp workflow agent providers --json"},
		)
	}
	decision := policy.Decision(string(provider))
	if decision.Reason != providerpolicy.ReasonDisabledByConfig {
		return nil
	}
	ctx, cancel := a.commandContext(cmd)
	defer cancel()
	return a.renderWebAgentResult(
		ctx,
		fmt.Sprintf("%s is disabled by cdp-cli policy", decision.DisplayName),
		providerDisabledResult(provider, workflowAgentOperation(cmd), decision),
	)
}

func workflowAgentProvider(cmd *cobra.Command) (webagent.Provider, bool) {
	for current := cmd; current != nil; current = current.Parent() {
		parent := current.Parent()
		if parent == nil || parent.Name() != "agent" {
			continue
		}
		workflow := parent.Parent()
		if workflow == nil || workflow.Name() != "workflow" {
			continue
		}
		provider, ok := webagent.ParseProvider(current.Name())
		return provider, ok
	}
	return "", false
}

func workflowAgentOperation(cmd *cobra.Command) webagent.Operation {
	provider, ok := workflowAgentProvider(cmd)
	if !ok {
		return webagent.OperationCapabilities
	}
	parts := make([]string, 0, 3)
	for current := cmd; current != nil; current = current.Parent() {
		parts = append(parts, current.Name())
		if current.Name() == string(provider) {
			break
		}
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	path := strings.Join(parts[1:], " ")
	switch path {
	case "capabilities", "capabilities refresh":
		return webagent.OperationCapabilities
	case "doctor":
		return webagent.OperationDoctor
	case "auth refresh":
		return webagent.OperationAuthRefresh
	case "transcribe":
		return webagent.OperationTranscribe
	case "ask":
		return webagent.OperationAsk
	case "research":
		return webagent.OperationResearch
	case "conversations list":
		return webagent.OperationConversationsList
	case "conversations continue":
		return webagent.OperationConversationsContinue
	case "conversations detail":
		return webagent.OperationConversationsDetail
	case "conversations await":
		return webagent.OperationConversationsAwait
	case "conversations delete":
		return webagent.OperationConversationsDelete
	case "conversations download-artifact":
		return webagent.OperationArtifactDownload
	case "conversations download-attachments":
		return webagent.OperationAttachmentsDownload
	case "conversations export-research":
		return webagent.OperationResearchExport
	default:
		return webagent.OperationCapabilities
	}
}

func providerDisabledResult(provider webagent.Provider, operation webagent.Operation, decision providerpolicy.Decision) webagent.Result {
	reason := string(decision.Reason)
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      provider,
		Operation:     operation,
		State:         webagent.StateFailed,
		Stage:         webagent.StagePlanned,
		Error: &webagent.OperationError{
			Code:      "provider_disabled",
			ErrClass:  "usage",
			Message:   fmt.Sprintf("%s is disabled by agents.disabled_providers", decision.DisplayName),
			Reason:    reason,
			RetrySafe: false,
		},
		Data: providerPolicyResultData{
			SchemaVersion: providerPolicySchemaVersion,
			Provider:      string(decision.ID),
			DisplayName:   decision.DisplayName,
			Enabled:       false,
			Reason:        reason,
			ConfigKey:     "agents.disabled_providers",
		},
		Evidence: webagent.Evidence{
			RunID:       webagent.NewRunID(),
			BuildCommit: "unknown",
			BrowserMode: "none",
			ReadMode:    "local_provider_policy",
		},
		Cleanup: webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{
			"cdp workflow agent providers --json",
			"cdp workflow agent providers --include-disabled --json",
			"Remove the provider from agents.disabled_providers in the cdp-cli config",
		},
	}
}
