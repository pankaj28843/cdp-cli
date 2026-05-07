package cli

import (
	"context"
	"fmt"
	"strings"

	"encoding/base64"
	"encoding/json"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
	"github.com/spf13/cobra"
)

func (a *app) newCDPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "protocol",
		Aliases: []string{"cdp"},
		Short:   "Discover and execute raw CDP methods",
	}
	cmd.AddCommand(a.newProtocolMetadataCommand())
	cmd.AddCommand(a.newProtocolDomainsCommand())
	cmd.AddCommand(a.newProtocolSearchCommand())
	cmd.AddCommand(a.newProtocolDescribeCommand())
	cmd.AddCommand(a.newProtocolExamplesCommand())
	cmd.AddCommand(a.newProtocolCompatCommand())
	cmd.AddCommand(a.newProtocolExecCommand())
	return cmd
}

func (a *app) newProtocolMetadataCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "metadata",
		Short: "Print CDP protocol metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			domains := cdp.SummarizeDomains(protocol.Domains)
			data := map[string]any{
				"ok": true,
				"protocol": map[string]any{
					"version":      protocol.Version,
					"domain_count": len(domains),
					"domains":      domains,
					"source":       protocol.Source,
				},
			}
			human := fmt.Sprintf("CDP %s.%s, %d domains", protocol.Version.Major, protocol.Version.Minor, len(domains))
			return a.render(ctx, human, data)
		},
	}
}

func (a *app) newProtocolDomainsCommand() *cobra.Command {
	var experimentalOnly bool
	var deprecatedOnly bool
	cmd := &cobra.Command{
		Use:   "domains",
		Short: "List CDP domains",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			domains := cdp.SummarizeDomains(protocol.Domains)
			domains = filterDomainSummaries(domains, experimentalOnly, deprecatedOnly)
			var lines []string
			for _, domain := range domains {
				lines = append(lines, fmt.Sprintf("%s\tcommands=%d\tevents=%d", domain.Name, domain.CommandCount, domain.EventCount))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":           true,
				"domain_count": len(domains),
				"domains":      domains,
				"source":       protocol.Source,
			})
		},
	}
	cmd.Flags().BoolVar(&experimentalOnly, "experimental", false, "only return experimental domains")
	cmd.Flags().BoolVar(&deprecatedOnly, "deprecated", false, "only return deprecated domains")
	return cmd
}

func filterDomainSummaries(domains []cdp.DomainSummary, experimentalOnly, deprecatedOnly bool) []cdp.DomainSummary {
	if !experimentalOnly && !deprecatedOnly {
		return domains
	}
	filtered := make([]cdp.DomainSummary, 0, len(domains))
	for _, domain := range domains {
		if experimentalOnly && !domain.Experimental {
			continue
		}
		if deprecatedOnly && !domain.Deprecated {
			continue
		}
		filtered = append(filtered, domain)
	}
	return filtered
}

func (a *app) newProtocolSearchCommand() *cobra.Command {
	var limit int
	var kind string
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search CDP domains, methods, events, and types",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			results := cdp.SearchProtocol(protocol, args[0], limit)
			results = cdp.FilterSearchResultsByKind(results, kind)
			var lines []string
			for _, result := range results {
				lines = append(lines, fmt.Sprintf("%s\t%s", result.Kind, result.Path))
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":      true,
				"query":   args[0],
				"matches": results,
				"source":  protocol.Source,
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 25, "maximum number of search results")
	cmd.Flags().StringVar(&kind, "kind", "", "only return matches of this kind: domain, command, event, or type")
	return cmd
}

func (a *app) newProtocolDescribeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <Domain.entity>",
		Short: "Describe a CDP domain, command, event, or type schema",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			desc, ok := cdp.DescribeEntity(protocol, args[0])
			if !ok {
				return commandError(
					"unknown_protocol_entity",
					"usage",
					fmt.Sprintf("unknown protocol entity %q", args[0]),
					ExitUsage,
					[]string{"cdp protocol search <query> --json", "cdp protocol domains --json"},
				)
			}
			human := fmt.Sprintf("%s\t%s", desc.Kind, desc.Path)
			return a.render(ctx, human, map[string]any{
				"ok":     true,
				"entity": desc,
				"source": protocol.Source,
			})
		},
	}
}

func (a *app) newProtocolExamplesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "examples <Domain.method>",
		Short: "Generate example cdp protocol exec commands",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				return err
			}
			desc, ok := cdp.DescribeEntity(protocol, args[0])
			if !ok || desc.Kind != "command" {
				return commandError(
					"unknown_protocol_entity",
					"usage",
					fmt.Sprintf("unknown protocol command %q", args[0]),
					ExitUsage,
					[]string{"cdp protocol search <query> --kind command --json", "cdp protocol domains --json"},
				)
			}
			examples := protocolExecExamples(desc)
			lines := make([]string, 0, len(examples))
			for _, example := range examples {
				lines = append(lines, example["command"])
			}
			return a.render(ctx, strings.Join(lines, "\n"), map[string]any{
				"ok":       true,
				"entity":   desc,
				"examples": examples,
				"source":   protocol.Source,
			})
		},
	}
}

func protocolExecExamples(desc cdp.EntityDescription) []map[string]string {
	params := sampleProtocolParams(desc.Schema)
	paramsJSON, _ := json.Marshal(params)
	scope := protocolCommandScope(desc.Domain)
	command := fmt.Sprintf("cdp protocol exec %s --params '%s' --json", desc.Path, paramsJSON)
	if scope == "target" {
		command = fmt.Sprintf("cdp protocol exec %s --target <target-id> --params '%s' --json", desc.Path, paramsJSON)
	}
	return []map[string]string{{
		"scope":   scope,
		"command": command,
		"params":  string(paramsJSON),
	}}
}

func protocolCommandScope(domain string) string {
	switch domain {
	case "Browser", "Target", "Schema", "SystemInfo":
		return "browser"
	default:
		return "target"
	}
}

func sampleProtocolParams(schema json.RawMessage) map[string]any {
	var command struct {
		Parameters []struct {
			Name     string   `json:"name"`
			Type     string   `json:"type"`
			Ref      string   `json:"$ref"`
			Optional bool     `json:"optional"`
			Enum     []string `json:"enum"`
		} `json:"parameters"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &command) != nil {
		return map[string]any{}
	}
	params := map[string]any{}
	for _, param := range command.Parameters {
		if param.Optional {
			continue
		}
		params[param.Name] = sampleProtocolValue(param.Type, param.Ref, param.Enum)
	}
	return params
}

func sampleProtocolValue(paramType, ref string, enum []string) any {
	if len(enum) > 0 {
		return enum[0]
	}
	if ref != "" {
		return "<" + ref + ">"
	}
	switch paramType {
	case "boolean":
		return true
	case "integer", "number":
		return 0
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	default:
		return "<string>"
	}
}

func (a *app) newProtocolCompatCommand() *cobra.Command {
	var requires, workflow string
	cmd := &cobra.Command{Use: "compat", Short: "Report live CDP compatibility for methods and workflows", RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := a.browserCommandContext(cmd)
		defer cancel()
		protocol, err := a.fetchProtocol(ctx)
		if err != nil {
			return err
		}
		required := splitCSV(requires)
		if workflow != "" {
			required = append(required, workflowProtocolRequirements(workflow)...)
		}
		if len(required) == 0 {
			required = []string{"Target.attachToTarget", "Runtime.evaluate", "Page.navigate"}
		}
		checks := make([]map[string]any, 0, len(required))
		for _, path := range required {
			desc, ok := cdp.DescribeEntity(protocol, path)
			check := map[string]any{"path": path, "available": ok}
			if ok {
				check["kind"] = desc.Kind
				check["experimental"] = desc.Experimental
				check["deprecated"] = desc.Deprecated
			}
			checks = append(checks, check)
		}
		return a.render(ctx, fmt.Sprintf("compat\t%d checks", len(checks)), map[string]any{"ok": true, "protocol_version": protocol.Version, "schema_source": protocol.Source, "required": checks, "warnings": []string{"Live browser protocol can differ from static tot documentation"}})
	}}
	cmd.Flags().StringVar(&requires, "requires", "", "comma-separated Domain.method or Domain.event paths to check")
	cmd.Flags().StringVar(&workflow, "workflow", "", "known workflow requirement set: debug-bundle, responsive-audit, network, console, storage")
	return cmd
}

func workflowProtocolRequirements(workflow string) []string {
	switch strings.ToLower(strings.TrimSpace(workflow)) {
	case "debug-bundle":
		return []string{"Target.attachToTarget", "Page.navigate", "Runtime.enable", "Log.enable", "Network.enable", "Page.captureScreenshot"}
	case "responsive-audit":
		return []string{"Emulation.setDeviceMetricsOverride", "Emulation.clearDeviceMetricsOverride", "Page.reload", "Page.captureScreenshot"}
	case "network":
		return []string{"Network.enable", "Network.loadingFailed", "Network.responseReceived"}
	case "console":
		return []string{"Runtime.enable", "Runtime.exceptionThrown", "Log.entryAdded"}
	case "storage":
		return []string{"Storage.getUsageAndQuota", "Network.getCookies"}
	default:
		return nil
	}
}

func (a *app) validateProtocolExecParams(ctx context.Context, method string, rawParams json.RawMessage, scope string) error {
	protocol, err := a.fetchProtocol(ctx)
	if err != nil {
		return err
	}
	desc, ok := cdp.DescribeEntity(protocol, method)
	if !ok || desc.Kind != "command" {
		return commandError("unknown_protocol_entity", "usage", fmt.Sprintf("unknown protocol command %q", method), ExitUsage, []string{"cdp protocol search <query> --kind command --json"})
	}
	expectedScope := protocolCommandScope(desc.Domain)
	if expectedScope != scope {
		return commandError("cdp_invalid_scope", "usage", fmt.Sprintf("%s is %s-scoped; invocation is %s-scoped", method, expectedScope, scope), ExitUsage, []string{"cdp protocol examples " + method + " --json"})
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return commandError("invalid_json", "usage", "--params must be a JSON object", ExitUsage, []string{"cdp protocol exec " + method + " --params '{}' --json"})
	}
	var schema struct {
		Parameters []struct {
			Name     string   `json:"name"`
			Type     string   `json:"type"`
			Optional bool     `json:"optional"`
			Enum     []string `json:"enum"`
		} `json:"parameters"`
	}
	_ = json.Unmarshal(desc.Schema, &schema)
	known := map[string]struct{}{}
	for _, param := range schema.Parameters {
		known[param.Name] = struct{}{}
		if !param.Optional {
			if _, ok := params[param.Name]; !ok {
				return commandError("cdp_invalid_params", "usage", fmt.Sprintf("missing required parameter %s for %s", param.Name, method), ExitUsage, []string{"cdp protocol examples " + method + " --json"})
			}
		}
	}
	for name := range params {
		if _, ok := known[name]; !ok {
			return commandError("cdp_invalid_params", "usage", fmt.Sprintf("unknown parameter %s for %s", name, method), ExitUsage, []string{"cdp protocol examples " + method + " --json"})
		}
	}
	return nil
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (a *app) newProtocolExecCommand() *cobra.Command {
	var params string
	var targetID string
	var urlContains string
	var titleContains string
	var savePath string
	var validate bool
	cmd := &cobra.Command{
		Use:   "exec <Domain.method>",
		Short: "Execute a raw browser-scoped or target-scoped CDP method",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			rawParams := json.RawMessage(params)
			if len(rawParams) == 0 {
				rawParams = json.RawMessage(`{}`)
			}
			if !json.Valid(rawParams) {
				return commandError(
					"invalid_json",
					"usage",
					"--params must be valid JSON",
					ExitUsage,
					[]string{"cdp protocol exec Browser.getVersion --params '{}' --json"},
				)
			}
			if targetID != "" || urlContains != "" || titleContains != "" {
				session, target, err := a.attachPageSession(ctx, targetID, urlContains, titleContains)
				if err != nil {
					return err
				}
				defer session.Close(ctx)

				result, err := session.Exec(ctx, args[0], rawParams)
				if err != nil {
					return commandError(
						"connection_failed",
						"connection",
						fmt.Sprintf("execute %s in target %s: %v", args[0], target.TargetID, err),
						ExitConnection,
						[]string{"cdp pages --json", "cdp protocol describe " + args[0] + " --json"},
					)
				}
				data := map[string]any{
					"ok":         true,
					"scope":      "target",
					"method":     args[0],
					"target":     pageRow(target),
					"session_id": session.SessionID,
					"result":     result,
				}
				if strings.TrimSpace(savePath) != "" {
					artifact, redactedResult, err := saveProtocolExecArtifact(savePath, result)
					if err != nil {
						return err
					}
					data["result"] = redactedResult
					data["artifact"] = artifact
					data["artifacts"] = []map[string]any{artifact}
				}
				return a.render(ctx, fmt.Sprintf("%s ok", args[0]), data)
			}
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					a.connectionRemediationCommands(),
				)
			}
			defer closeClient(ctx)

			result, err := cdp.ExecWithClient(ctx, client, args[0], rawParams)
			if err != nil {
				return commandError(
					"connection_failed",
					"connection",
					fmt.Sprintf("execute %s: %v", args[0], err),
					ExitConnection,
					[]string{"cdp doctor --json", "cdp protocol describe " + args[0] + " --json"},
				)
			}
			data := map[string]any{
				"ok":     true,
				"scope":  "browser",
				"method": args[0],
				"result": result,
			}
			if strings.TrimSpace(savePath) != "" {
				artifact, redactedResult, err := saveProtocolExecArtifact(savePath, result)
				if err != nil {
					return err
				}
				data["result"] = redactedResult
				data["artifact"] = artifact
				data["artifacts"] = []map[string]any{artifact}
			}
			return a.render(ctx, fmt.Sprintf("%s ok", args[0]), data)
		},
	}
	cmd.Flags().StringVar(&params, "params", "{}", "JSON params object for the CDP method")
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix for target-scoped execution")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text for target-scoped execution")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text for target-scoped execution")
	cmd.Flags().StringVar(&savePath, "save", "", "write a base64 result data field to this artifact path")
	cmd.Flags().BoolVar(&validate, "validate", false, "validate params against live protocol metadata before executing")
	return cmd
}

func saveProtocolExecArtifact(path string, result json.RawMessage) (map[string]any, any, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result, &fields); err != nil {
		return nil, nil, commandError(
			"protocol_result_not_saveable",
			"usage",
			fmt.Sprintf("protocol result is not a JSON object with a base64 data field: %v", err),
			ExitUsage,
			[]string{"cdp protocol exec Page.captureScreenshot --target <target-id> --save tmp/page.png --json"},
		)
	}
	rawData, ok := fields["data"]
	if !ok {
		return nil, nil, commandError(
			"protocol_result_not_saveable",
			"usage",
			"protocol result has no base64 data field to save",
			ExitUsage,
			[]string{"cdp protocol exec Page.captureScreenshot --target <target-id> --save tmp/page.png --json"},
		)
	}
	var encoded string
	if err := json.Unmarshal(rawData, &encoded); err != nil || encoded == "" {
		return nil, nil, commandError(
			"protocol_result_not_saveable",
			"usage",
			"protocol result data field is not a non-empty base64 string",
			ExitUsage,
			[]string{"cdp protocol exec Page.captureScreenshot --target <target-id> --save tmp/page.png --json"},
		)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, nil, commandError(
			"protocol_result_not_saveable",
			"usage",
			fmt.Sprintf("decode protocol result data: %v", err),
			ExitUsage,
			[]string{"cdp protocol exec Page.captureScreenshot --target <target-id> --save tmp/page.png --json"},
		)
	}
	writtenPath, err := writeArtifactFile(path, decoded)
	if err != nil {
		return nil, nil, err
	}
	var redacted map[string]any
	if err := json.Unmarshal(result, &redacted); err != nil {
		return nil, nil, err
	}
	redacted["data"] = map[string]any{
		"omitted": true,
		"reason":  "saved_to_artifact",
	}
	artifact := map[string]any{
		"type":     "protocol-result",
		"path":     writtenPath,
		"bytes":    len(decoded),
		"field":    "data",
		"encoding": "base64",
	}
	return artifact, redacted, nil
}

func (a *app) fetchProtocol(ctx context.Context) (cdp.Protocol, error) {
	runtime, err := a.requiredDaemonRuntime(ctx)
	if err != nil {
		return cdp.Protocol{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			a.connectionRemediationCommands(),
		)
	}
	protocol, err := daemon.RuntimeClient{Runtime: runtime}.FetchProtocol(ctx)
	if err != nil {
		return cdp.Protocol{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("fetch protocol metadata through daemon: %v", err),
			ExitConnection,
			[]string{"cdp doctor --json", "cdp daemon status --json"},
		)
	}
	return protocol, nil
}
