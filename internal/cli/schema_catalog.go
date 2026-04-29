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
		"open": {
			Name:        "open",
			Description: "Page open or navigation result.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the page was opened or navigated."},
				{Name: "action", Type: "string", Required: true, Description: "Either created or navigated."},
				{Name: "page", Type: "page", Required: true, Description: "Page target metadata with id, url, and action fields."},
			},
		},
		"page-action": {
			Name:        "page-action",
			Description: "Page target control result for reload, history navigation, activate, and close.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the page action completed."},
				{Name: "action", Type: "string", Required: true, Description: "Action name such as reloaded, back, forward, activated, or closed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "history", Type: "object", Required: false, Description: "History metadata for back and forward actions."},
			},
		},
		"eval": {
			Name:        "eval",
			Description: "Page-scoped JavaScript evaluation result.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when JavaScript evaluation completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "result", Type: "runtime_object", Required: true, Description: "Runtime object with type, value, and description fields."},
			},
		},
		"text": {
			Name:        "text",
			Description: "Compact visible text extracted from a CSS selector.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when text extraction completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "text", Type: "text_result", Required: true, Description: "Selector, joined text, and per-element text items."},
				{Name: "items", Type: "array<text_item>", Required: true, Description: "Text items duplicated for jq convenience."},
			},
		},
		"html": {
			Name:        "html",
			Description: "Compact HTML extracted from a CSS selector.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when HTML extraction completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "html", Type: "html_result", Required: true, Description: "Selector and truncated HTML items."},
			},
		},
		"dom-query": {
			Name:        "dom-query",
			Description: "DOM node summaries for a CSS selector.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when DOM query completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "query", Type: "dom_query_result", Required: true, Description: "Selector, count, and node summaries."},
				{Name: "nodes", Type: "array<dom_node>", Required: true, Description: "Node summaries duplicated for jq convenience."},
			},
		},
		"css-inspect": {
			Name:        "css-inspect",
			Description: "Computed style and box data for the first matching element.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when CSS inspection completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "inspect", Type: "css_inspect_result", Required: true, Description: "Selector, found flag, styles, and layout box."},
			},
		},
		"layout-overflow": {
			Name:        "layout-overflow",
			Description: "Elements whose scroll dimensions exceed their client boxes.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when overflow scan completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "overflow", Type: "layout_overflow_result", Required: true, Description: "Selector, count, and overflow items."},
				{Name: "items", Type: "array<layout_overflow_item>", Required: true, Description: "Overflow items duplicated for jq convenience."},
			},
		},
		"snapshot": {
			Name:        "snapshot",
			Description: "Visible text extracted from selected page elements.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when snapshot extraction completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "snapshot", Type: "snapshot", Required: true, Description: "Page URL, title, selector, count, and extracted items."},
				{Name: "items", Type: "array<snapshot_item>", Required: true, Description: "Visible text items duplicated for jq convenience."},
			},
		},
		"screenshot": {
			Name:        "screenshot",
			Description: "Page screenshot saved as a local artifact.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when screenshot capture completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "screenshot", Type: "artifact", Required: true, Description: "Path, byte count, format, and capture mode."},
				{Name: "artifacts", Type: "array<artifact>", Required: true, Description: "Artifact references for agent workflows."},
			},
		},
		"console": {
			Name:        "console",
			Description: "Console and browser log messages captured from a page target.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when console capture completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "messages", Type: "array<console_message>", Required: true, Description: "Console/log entries with id, source, type or level, text, timestamp, and optional location."},
				{Name: "console", Type: "console_summary", Required: true, Description: "Capture metadata including count, wait, limit, filters, and truncation state."},
			},
		},
		"workflow-visible-posts": {
			Name:        "workflow-visible-posts",
			Description: "Open a feed page and return visible post-like text items.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when at least one visible post was found."},
				{Name: "url", Type: "string", Required: true, Description: "Requested URL."},
				{Name: "target", Type: "page", Required: true, Description: "Created page target metadata."},
				{Name: "selector", Type: "string", Required: true, Description: "Post container selector used for extraction."},
				{Name: "items", Type: "array<snapshot_item>", Required: true, Description: "Visible post text items."},
			},
		},
		"protocol-metadata": {
			Name:        "protocol-metadata",
			Description: "Summarized CDP protocol metadata.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when protocol metadata was fetched."},
				{Name: "protocol", Type: "protocol_summary", Required: true, Description: "Version, domain count, and compact domain summaries."},
			},
		},
		"protocol-domains": {
			Name:        "protocol-domains",
			Description: "Compact list of CDP domains.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when protocol domains were fetched."},
				{Name: "domain_count", Type: "number", Required: true, Description: "Number of protocol domains returned."},
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
		"protocol-describe": {
			Name:        "protocol-describe",
			Description: "A live CDP domain, command, event, or type schema.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the entity was found."},
				{Name: "entity", Type: "protocol_entity", Required: true, Description: "Entity metadata and raw protocol schema."},
			},
		},
		"protocol-exec": {
			Name:        "protocol-exec",
			Description: "Raw CDP method execution result.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the CDP method completed."},
				{Name: "scope", Type: "string", Required: true, Description: "Either browser or target."},
				{Name: "method", Type: "string", Required: true, Description: "Executed CDP method."},
				{Name: "target", Type: "page", Required: false, Description: "Selected page target for target-scoped execution."},
				{Name: "session_id", Type: "string", Required: false, Description: "Temporary CDP session id used for target-scoped execution."},
				{Name: "result", Type: "object", Required: true, Description: "Raw CDP result payload."},
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
