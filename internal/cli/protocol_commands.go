package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"encoding/base64"
	"encoding/json"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

var fetchOfficialProtocol = cdp.FetchOfficialProtocol

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
	cmd.PersistentFlags().BoolVar(&a.opts.protocolOfficial, "official", false, "use the official browser+JavaScript tip-of-tree schema for discovery and --validate; requires network, not a browser daemon")
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
			human := formatProtocolDescription(desc)
			return a.render(ctx, human, map[string]any{
				"ok":     true,
				"entity": desc,
				"source": protocol.Source,
			})
		},
	}
}

type protocolDescriptionField struct {
	Name        string                     `json:"name"`
	Type        string                     `json:"type"`
	Ref         string                     `json:"$ref"`
	Description string                     `json:"description"`
	Optional    bool                       `json:"optional"`
	Items       *protocolDescriptionField  `json:"items"`
	Parameters  []protocolDescriptionField `json:"parameters"`
	Returns     []protocolDescriptionField `json:"returns"`
}

func formatProtocolDescription(desc cdp.EntityDescription) string {
	lines := []string{fmt.Sprintf("%s\t%s", desc.Kind, desc.Path)}
	if description := strings.Join(strings.Fields(desc.Description), " "); description != "" {
		lines = append(lines, description)
	}
	flags := make([]string, 0, 2)
	if desc.Experimental {
		flags = append(flags, "experimental")
	}
	if desc.Deprecated {
		flags = append(flags, "deprecated")
	}
	if len(flags) > 0 {
		lines = append(lines, "Flags: "+strings.Join(flags, ", "))
	}

	var schema protocolDescriptionField
	if len(desc.Schema) == 0 || json.Unmarshal(desc.Schema, &schema) != nil {
		return strings.Join(lines, "\n")
	}
	if desc.Kind == "type" && (schema.Type != "" || schema.Ref != "") {
		lines = append(lines, "", "Type: "+formatProtocolDescriptionType(schema))
	}
	lines = appendProtocolDescriptionFields(lines, "Parameters:", schema.Parameters, true)
	lines = appendProtocolDescriptionFields(lines, "Returns:", schema.Returns, false)
	return strings.Join(lines, "\n")
}

func appendProtocolDescriptionFields(lines []string, heading string, fields []protocolDescriptionField, labelOptionality bool) []string {
	if len(fields) == 0 {
		return lines
	}
	lines = append(lines, "", heading)
	for _, field := range fields {
		line := fmt.Sprintf("  %s: %s", field.Name, formatProtocolDescriptionType(field))
		if labelOptionality {
			if field.Optional {
				line += " (optional)"
			} else {
				line += " (required)"
			}
		}
		lines = append(lines, line)
		if description := strings.Join(strings.Fields(field.Description), " "); description != "" {
			lines = append(lines, "    "+description)
		}
	}
	return lines
}

func formatProtocolDescriptionType(field protocolDescriptionField) string {
	if field.Type == "array" {
		if field.Items == nil {
			return "array"
		}
		return "array<" + formatProtocolDescriptionType(*field.Items) + ">"
	}
	if field.Type != "" {
		return field.Type
	}
	if field.Ref != "" {
		return field.Ref
	}
	return "object"
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
				lines = append(lines, example.Command)
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

type protocolExecExample struct {
	Scope          string         `json:"scope"`
	Command        string         `json:"command"`
	Params         string         `json:"params"`
	RequiredParams []string       `json:"required_params"`
	OptionalParams []string       `json:"optional_params"`
	ParamsSample   map[string]any `json:"params_sample"`
	ScopeNote      string         `json:"scope_note"`
	Notes          []string       `json:"notes"`
}

func protocolExecExamples(desc cdp.EntityDescription) []protocolExecExample {
	params := sampleProtocolParams(desc.Schema)
	paramsJSON, _ := json.Marshal(params)
	requiredParams, optionalParams := protocolParamNames(desc.Schema)
	scope := protocolCommandScope(desc.Domain)
	command := fmt.Sprintf("cdp protocol exec %s --params '%s' --json", desc.Path, paramsJSON)
	scopeNote := "Browser-scoped command; do not pass --target."
	if scope == "target" {
		command = fmt.Sprintf("cdp protocol exec %s --target <target-id> --params '%s' --json", desc.Path, paramsJSON)
		scopeNote = "Target-scoped command; pass --target or a unique page selector flag."
	}
	notes := []string{"params_sample includes required parameters only; add optional_params when needed."}
	if len(requiredParams) == 0 {
		notes = []string{"This command has no required params in the live protocol schema."}
	}
	return []protocolExecExample{{
		Scope:          scope,
		Command:        command,
		Params:         string(paramsJSON),
		RequiredParams: requiredParams,
		OptionalParams: optionalParams,
		ParamsSample:   params,
		ScopeNote:      scopeNote,
		Notes:          notes,
	}}
}

func protocolParamNames(schema json.RawMessage) ([]string, []string) {
	var command struct {
		Parameters []struct {
			Name     string `json:"name"`
			Optional bool   `json:"optional"`
		} `json:"parameters"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &command) != nil {
		return nil, nil
	}
	var required []string
	var optional []string
	for _, param := range command.Parameters {
		if param.Optional {
			optional = append(optional, param.Name)
			continue
		}
		required = append(required, param.Name)
	}
	return required, optional
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
		warning := "Live browser protocol can differ from static tip-of-tree documentation"
		if a.opts.protocolOfficial {
			warning = "Official tip-of-tree protocol can differ from the selected live Chrome version"
		}
		return a.render(ctx, fmt.Sprintf("compat\t%d checks", len(checks)), map[string]any{"ok": true, "protocol_version": protocol.Version, "schema_source": protocol.Source, "required": checks, "warnings": []string{warning}})
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
	var targetType string
	var urlContains string
	var titleContains string
	var targetIndex int
	var savePath string
	var validate bool
	cmd := &cobra.Command{
		Use:   "exec <Domain.method> [JSON_PARAMS]",
		Short: "Execute a raw browser-scoped or target-scoped CDP method",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 && cmd.Flags().Changed("params") {
				return commandError(
					"conflicting_params",
					"usage",
					"positional JSON params cannot be combined with --params",
					ExitUsage,
					[]string{"cdp protocol exec " + args[0] + " --params '{}' --json"},
				)
			}
			if targetIndex < 0 || (cmd.Flags().Changed("target-index") && targetIndex == 0) {
				return commandError("invalid_target_index", "usage", "--target-index must be greater than zero", ExitUsage, []string{"cdp pages --json"})
			}
			if targetIndex > 0 && (targetID != "" || urlContains != "" || titleContains != "" || strings.TrimSpace(targetType) != "") {
				return commandError("invalid_target_selector", "usage", "--target-index cannot be combined with --target, --url-contains, --title-contains, or --target-type", ExitUsage, []string{"cdp pages --json"})
			}
			if a.opts.protocolOfficial && !validate {
				return commandError(
					"official_protocol_requires_validation",
					"usage",
					"--official selects validation metadata only for protocol exec; add --validate or remove --official",
					ExitUsage,
					[]string{"cdp protocol exec " + args[0] + " --validate --official --json", "cdp protocol describe " + args[0] + " --official --json"},
				)
			}
			rawParams := json.RawMessage(params)
			if len(args) == 2 {
				rawParams = json.RawMessage(args[1])
			}
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
			var paramsObject map[string]json.RawMessage
			if err := json.Unmarshal(rawParams, &paramsObject); err != nil || paramsObject == nil {
				return commandError(
					"invalid_json",
					"usage",
					"--params must be a JSON object",
					ExitUsage,
					[]string{"cdp protocol exec Browser.getVersion --params '{}' --json"},
				)
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			targetScoped := targetIndex > 0 || targetID != "" || urlContains != "" || titleContains != "" || strings.TrimSpace(targetType) != ""
			if validate {
				scope := "browser"
				if targetScoped {
					scope = "target"
				}
				if err := a.validateProtocolExecParams(ctx, args[0], rawParams, scope); err != nil {
					return err
				}
			}
			if targetScoped {
				var session *cdp.PageSession
				var target cdp.TargetInfo
				var err error
				if targetIndex > 0 {
					session, target, err = a.attachPageSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
				} else if strings.TrimSpace(targetType) == "" {
					session, target, err = a.attachPageSession(ctx, targetID, urlContains, titleContains)
				} else {
					session, target, err = a.attachProtocolTargetSession(ctx, targetID, urlContains, titleContains, targetType)
				}
				if err != nil {
					return err
				}
				defer session.Close(ctx)

				result, err := session.Exec(ctx, args[0], rawParams)
				if err != nil {
					if protocolErr := protocolCommandError(args[0], "target", target.TargetID, err); protocolErr != nil {
						return protocolErr
					}
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
				if protocolErr := protocolCommandError(args[0], "browser", "", err); protocolErr != nil {
					return protocolErr
				}
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
	cmd.Flags().StringVar(&targetID, "target", "", "target id or unique prefix for target-scoped execution")
	cmd.Flags().StringVar(&targetType, "target-type", "", "target type to include when selecting a target, such as page or service_worker")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "filter targets by URL substring; combines with ID/title/type filters and must leave one target")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "filter targets by title substring; combines with ID/URL/type filters and must leave one target")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index for target-scoped execution")
	cmd.Flags().StringVar(&savePath, "save", "", "write a base64 result data field to this artifact path")
	cmd.Flags().BoolVar(&validate, "validate", false, "validate method, browser/target scope, and params against selected protocol metadata before executing")
	return cmd
}

func protocolCommandError(method, scope, targetID string, err error) error {
	var protocolErr *cdp.ProtocolError
	if !errors.As(err, &protocolErr) {
		return nil
	}
	if strings.TrimSpace(protocolErr.Method) != "" {
		method = protocolErr.Method
	}
	data := map[string]any{
		"scope":            scope,
		"method":           method,
		"protocol_code":    protocolErr.Code,
		"protocol_message": protocolErr.Message,
	}
	if targetID != "" {
		data["target_id"] = targetID
	}
	return commandErrorWithData(
		"cdp_command_failed",
		"protocol",
		fmt.Sprintf("Chrome rejected %s: %s (%d)", method, protocolErr.Message, protocolErr.Code),
		ExitCheckFailed,
		[]string{
			"cdp protocol describe " + method + " --json",
			"cdp protocol examples " + method + " --json",
		},
		data,
	)
}

func (a *app) attachProtocolTargetSession(ctx context.Context, targetID, urlContains, titleContains, targetType string) (*cdp.PageSession, cdp.TargetInfo, error) {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return nil, cdp.TargetInfo{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			a.connectionRemediationCommands(),
		)
	}
	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("list targets: %v", err),
			ExitConnection,
			[]string{"cdp targets --json", "cdp doctor --json"},
		)
	}
	target, err := resolveProtocolTarget(targets, targetID, urlContains, titleContains, targetType)
	if err != nil {
		_ = closeClient(ctx)
		return nil, cdp.TargetInfo{}, err
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, closeClient)
	if err != nil {
		_ = closeClient(ctx)
		return nil, target, commandError(
			"connection_failed",
			"connection",
			fmt.Sprintf("attach target %s: %v", target.TargetID, err),
			ExitConnection,
			[]string{"cdp targets --json", "cdp protocol describe Target.attachToTarget --json"},
		)
	}
	return session, target, nil
}

func resolveProtocolTarget(targets []cdp.TargetInfo, targetID, urlContains, titleContains, targetType string) (cdp.TargetInfo, error) {
	targetID = strings.TrimSpace(targetID)
	urlContains = strings.TrimSpace(urlContains)
	titleContains = strings.TrimSpace(titleContains)
	targetType = strings.TrimSpace(targetType)
	matches := make([]cdp.TargetInfo, 0, len(targets))
	available := make([]cdp.TargetInfo, 0, len(targets))
	for _, target := range targets {
		if targetType != "" && target.Type != targetType {
			continue
		}
		available = append(available, target)
		if targetID != "" && !targetIDMatchesPrefix(target.TargetID, targetID) {
			continue
		}
		if urlContains != "" && !strings.Contains(strings.ToLower(target.URL), strings.ToLower(urlContains)) {
			continue
		}
		if titleContains != "" && !strings.Contains(strings.ToLower(target.Title), strings.ToLower(titleContains)) {
			continue
		}
		matches = append(matches, target)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	label := "target"
	if targetType != "" {
		label = targetType + " target"
	}
	if targetID != "" {
		label = fmt.Sprintf("%s %q", label, targetID)
	}
	if urlContains != "" {
		label += fmt.Sprintf(" with URL containing %q", urlContains)
	}
	if titleContains != "" {
		label += fmt.Sprintf(" with title containing %q", titleContains)
	}
	if len(matches) == 0 {
		return cdp.TargetInfo{}, commandErrorWithData(
			"target_not_found",
			"usage",
			"no "+label+" matched",
			ExitUsage,
			protocolTargetRemediation(targetType),
			availableTargetEvidence(available),
		)
	}
	return cdp.TargetInfo{}, commandErrorWithData(
		"ambiguous_target",
		"usage",
		fmt.Sprintf("%s matched %d targets; pass a longer --target or add --url-contains/--title-contains", label, len(matches)),
		ExitUsage,
		protocolTargetRemediation(targetType),
		ambiguousTargetEvidence(matches),
	)
}

func protocolTargetRemediation(targetType string) []string {
	commands := []string{"cdp targets --json"}
	if targetType = strings.TrimSpace(targetType); targetType != "" {
		commands = append(commands, fmt.Sprintf("cdp protocol exec Runtime.evaluate --target-type %s --target <target-id> --json", targetType))
	}
	return commands
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
	if a.opts.protocolOfficial {
		protocol, err := fetchOfficialProtocol(ctx)
		if err != nil {
			return cdp.Protocol{}, commandError(
				"official_protocol_fetch_failed",
				"connection",
				fmt.Sprintf("fetch official protocol metadata: %v", err),
				ExitConnection,
				[]string{"cdp --timeout 30s protocol domains --official --json", "cdp protocol domains --json"},
			)
		}
		return protocol, nil
	}
	client, err := a.daemonRuntimeClient(ctx)
	if err != nil {
		return cdp.Protocol{}, commandError(
			"connection_not_configured",
			"connection",
			err.Error(),
			ExitConnection,
			a.connectionRemediationCommands(),
		)
	}
	protocol, err := client.FetchProtocol(ctx)
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
