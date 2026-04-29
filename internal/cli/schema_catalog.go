package cli

import "sort"

type schemaInfo struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Fields      []schemaField `json:"fields"`
}

type schemaField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

func schemaCatalog() map[string]schemaInfo {
	return map[string]schemaInfo{
		"describe": {
			Name:        "describe",
			Description: "Command-tree metadata for agent discovery.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when metadata was generated."},
				{Name: "commands", Type: "command", Required: true, Description: "Recursive command tree."},
				{Name: "globals", Type: "array<string>", Required: true, Description: "Global flags accepted by every command."},
			},
		},
		"doctor": {
			Name:        "doctor",
			Description: "Local readiness checks.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when no check failed."},
				{Name: "checks", Type: "array<check>", Required: true, Description: "Readiness checks with name, status, and message."},
			},
		},
		"pages": {
			Name:        "pages",
			Description: "Open page targets from the selected browser connection.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when page targets were listed."},
				{Name: "pages", Type: "array<page>", Required: true, Description: "Page rows with id, type, title, url, and attachment state."},
			},
		},
		"targets": {
			Name:        "targets",
			Description: "Browser targets from the selected browser connection.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when targets were listed."},
				{Name: "targets", Type: "array<target>", Required: true, Description: "Target rows with id, type, title, url, and attachment state."},
			},
		},
		"protocol-metadata": {
			Name:        "protocol-metadata",
			Description: "Summarized live CDP protocol metadata.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when protocol metadata was fetched."},
				{Name: "protocol", Type: "protocol_summary", Required: true, Description: "Version, domain count, and compact domain summaries."},
			},
		},
		"protocol-domains": {
			Name:        "protocol-domains",
			Description: "Compact list of live CDP domains.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when protocol domains were fetched."},
				{Name: "domain_count", Type: "number", Required: true, Description: "Number of domains returned by the browser."},
				{Name: "domains", Type: "array<domain_summary>", Required: true, Description: "Compact domain summaries."},
			},
		},
		"protocol-search": {
			Name:        "protocol-search",
			Description: "Search results across live CDP domains, commands, events, and types.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when protocol search completed."},
				{Name: "query", Type: "string", Required: true, Description: "Search query."},
				{Name: "matches", Type: "array<protocol_match>", Required: true, Description: "Matching protocol entities."},
			},
		},
		"error-envelope": {
			Name:        "error-envelope",
			Description: "Stable JSON shape emitted when a command fails with --json or --jq.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "Always false for errors."},
				{Name: "code", Type: "string", Required: true, Description: "Stable machine-readable error code."},
				{Name: "err_class", Type: "string", Required: true, Description: "Broader class for policy decisions."},
				{Name: "message", Type: "string", Required: true, Description: "Concise human-readable failure detail."},
				{Name: "remediation_commands", Type: "array<string>", Required: false, Description: "Safe commands an agent can run next."},
			},
		},
		"exit-codes": {
			Name:        "exit-codes",
			Description: "Process exit codes and their semantic names.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the table was generated."},
				{Name: "exit_codes", Type: "array<exit_code>", Required: true, Description: "Rows with code, name, class, and meaning."},
			},
		},
		"version": {
			Name:        "version",
			Description: "Build metadata for the cdp binary.",
			Fields: []schemaField{
				{Name: "version", Type: "string", Required: true, Description: "Version label set at build time."},
				{Name: "commit", Type: "string", Required: true, Description: "Source commit set at build time."},
				{Name: "date", Type: "string", Required: true, Description: "Build date set at build time."},
			},
		},
	}
}

func schemaNames() []string {
	catalog := schemaCatalog()
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
