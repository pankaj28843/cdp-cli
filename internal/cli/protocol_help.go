package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

// newProtocolHelpCommand is the source-compatible protocol-discovery spelling
// used by chrome-agent. It deliberately reuses the daemon-backed protocol
// metadata path instead of creating a second browser transport.
func (a *app) newProtocolHelpCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "help [Domain | Domain.method]",
		Short: "Discover CDP domains, commands, events, and signatures",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isProtocolHelpInvocation(args) {
				return runCobraHelpTopic(cmd.Root(), args)
			}
			query := ""
			if len(args) == 1 {
				query = strings.TrimSpace(args[0])
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			protocol, err := a.fetchProtocol(ctx)
			if err != nil {
				// chrome-agent's argument-free help degrades to static usage when
				// there is no browser to query. Keep structured callers typed and
				// actionable; only plain, argument-free help uses the fallback.
				if query == "" && !a.opts.json && a.opts.jq == "" && !a.opts.protocolOfficial {
					return cmd.Root().Help()
				}
				return err
			}

			human, data, err := protocolHelpResult(protocol, query)
			if err != nil {
				return err
			}
			return a.render(ctx, human, data)
		},
	}
	cmd.Flags().BoolVar(&a.opts.protocolOfficial, "official", false, "use the official browser+JavaScript tip-of-tree schema; requires network, not a browser daemon")
	return cmd
}

// Cobra normally owns the root help command. This command is installed as
// that owner so the source-compatible `cdp help Page.navigate` route does not
// create a duplicate help entry. Lowercase command paths still retain the
// conventional `cdp help <command>` behavior.
func isProtocolHelpInvocation(args []string) bool {
	if len(args) == 0 {
		return true
	}
	if len(args) != 1 {
		return false
	}
	query := strings.TrimSpace(args[0])
	if strings.Contains(query, ".") {
		return true
	}
	return query != "" && query[0] >= 'A' && query[0] <= 'Z'
}

func runCobraHelpTopic(root *cobra.Command, args []string) error {
	target := root
	if len(args) > 0 {
		found, _, err := root.Find(args)
		if err != nil || found == nil {
			return commandError(
				"unknown_help_topic",
				"usage",
				fmt.Sprintf("unknown help topic %q", strings.Join(args, " ")),
				ExitUsage,
				[]string{"cdp --help", "cdp describe --json"},
			)
		}
		target = found
	}
	return target.Help()
}

func protocolHelpResult(protocol cdp.Protocol, query string) (string, map[string]any, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		summaries := cdp.SummarizeDomains(protocol.Domains)
		lines := make([]string, 0, len(protocol.Domains))
		for _, domain := range protocol.Domains {
			line := domain.Domain + protocolHelpFlags(domain.Experimental, domain.Deprecated)
			if description := protocolHelpOneLine(domain.Description); description != "" {
				line += ": " + description
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), map[string]any{
			"ok":      true,
			"mode":    "domains",
			"domains": summaries,
			"source":  protocol.Source,
		}, nil
	}

	if domain := protocolHelpDomain(protocol, query); domain != nil {
		desc, _ := cdp.DescribeEntity(protocol, domain.Domain)
		commands := protocolHelpRawItems(domain.Commands)
		events := protocolHelpRawItems(domain.Events)
		return formatProtocolHelpDomain(*domain), map[string]any{
			"ok":       true,
			"mode":     "domain",
			"query":    query,
			"entity":   desc,
			"commands": commands,
			"events":   events,
			"source":   protocol.Source,
		}, nil
	}

	desc, ok := cdp.DescribeEntity(protocol, query)
	if !ok {
		return "", nil, commandError(
			"unknown_protocol_entity",
			"usage",
			fmt.Sprintf("unknown protocol entity %q", query),
			ExitUsage,
			[]string{"cdp help --json", "cdp protocol search " + query + " --json", "cdp protocol domains --json"},
		)
	}
	return formatProtocolHelpEntity(desc), map[string]any{
		"ok":     true,
		"mode":   "entity",
		"query":  query,
		"entity": desc,
		"source": protocol.Source,
	}, nil
}

func protocolHelpDomain(protocol cdp.Protocol, query string) *cdp.Domain {
	for index := range protocol.Domains {
		if strings.EqualFold(protocol.Domains[index].Domain, query) {
			return &protocol.Domains[index]
		}
	}
	return nil
}

func protocolHelpRawItems(raw json.RawMessage) []json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	return items
}

type protocolHelpItem struct {
	Name         string                     `json:"name"`
	Description  string                     `json:"description,omitempty"`
	Experimental bool                       `json:"experimental,omitempty"`
	Deprecated   bool                       `json:"deprecated,omitempty"`
	Parameters   []protocolDescriptionField `json:"parameters,omitempty"`
	Returns      []protocolDescriptionField `json:"returns,omitempty"`
}

func protocolHelpItems(raw json.RawMessage) []protocolHelpItem {
	items := protocolHelpRawItems(raw)
	result := make([]protocolHelpItem, 0, len(items))
	for _, item := range items {
		var decoded protocolHelpItem
		if err := json.Unmarshal(item, &decoded); err == nil && decoded.Name != "" {
			result = append(result, decoded)
		}
	}
	return result
}

func formatProtocolHelpDomain(domain cdp.Domain) string {
	lines := []string{"Domain: " + domain.Domain}
	if description := strings.TrimSpace(domain.Description); description != "" {
		lines = append(lines, description)
	}

	commands := protocolHelpItems(domain.Commands)
	if len(commands) > 0 {
		lines = append(lines, "", "Commands:")
		for _, item := range commands {
			lines = append(lines, formatProtocolHelpItem(domain.Domain, item))
		}
	}

	events := protocolHelpItems(domain.Events)
	if len(events) > 0 {
		lines = append(lines, "", "Events:")
		for _, item := range events {
			lines = append(lines, formatProtocolHelpItem(domain.Domain, item))
		}
	}
	return strings.Join(lines, "\n")
}

func formatProtocolHelpItem(domain string, item protocolHelpItem) string {
	lines := []string{"  " + domain + "." + item.Name + protocolHelpFlags(item.Experimental, item.Deprecated)}
	if description := protocolHelpOneLine(item.Description); description != "" {
		lines = append(lines, "    "+description)
	}
	return strings.Join(lines, "\n")
}

func formatProtocolHelpEntity(desc cdp.EntityDescription) string {
	lines := []string{desc.Path}
	if description := protocolHelpOneLine(desc.Description); description != "" {
		lines = append(lines, description)
	}
	if flags := protocolHelpFlags(desc.Experimental, desc.Deprecated); flags != "" {
		lines = append(lines, strings.TrimSpace(flags))
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

func protocolHelpFlags(experimental, deprecated bool) string {
	flags := make([]string, 0, 2)
	if experimental {
		flags = append(flags, "experimental")
	}
	if deprecated {
		flags = append(flags, "deprecated")
	}
	if len(flags) == 0 {
		return ""
	}
	return " (" + strings.Join(flags, ", ") + ")"
}

func protocolHelpOneLine(description string) string {
	return strings.Join(strings.Fields(description), " ")
}
