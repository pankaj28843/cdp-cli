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
		"daemon-restart": {
			Name:        "daemon-restart",
			Description: "Stop the existing daemon runtime if present, then start a daemon-backed browser connection.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when restart completed."},
				{Name: "daemon", Type: "daemon_status", Required: true, Description: "Current daemon status after restart."},
				{Name: "start", Type: "daemon_start", Required: true, Description: "Start metadata including connection mode and keepalive state."},
				{Name: "restart", Type: "daemon_restart", Required: true, Description: "Stop metadata including whether a previous daemon process was stopped."},
				{Name: "browser", Type: "browser_probe", Required: true, Description: "Browser probe or daemon-backed browser availability metadata."},
			},
		},
		"daemon-keepalive": {
			Name:        "daemon-keepalive",
			Description: "Cron-safe daemon health check and repair result.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when keepalive completed or skipped due to an existing lock."},
				{Name: "connection", Type: "string", Required: true, Description: "Connection name used for the keepalive lock."},
				{Name: "mode", Type: "string", Required: true, Description: "Connection mode such as auto_connect or browser_url."},
				{Name: "state", Type: "string", Required: true, Description: "Keepalive state: healthy, started, repaired, passive, or locked."},
				{Name: "action", Type: "string", Required: true, Description: "Action taken: none, started, repaired, or skipped."},
				{Name: "locked", Type: "boolean", Required: true, Description: "True when another keepalive process already owns the lock."},
				{Name: "daemon", Type: "daemon_status", Required: false, Description: "Daemon status when checked or after repair."},
				{Name: "start", Type: "daemon_start", Required: false, Description: "Daemon start metadata when keepalive started or repaired it."},
				{Name: "chrome", Type: "chrome_keepalive", Required: false, Description: "Chrome launch/check metadata for auto-connect repair."},
				{Name: "lock", Type: "lock_metadata", Required: true, Description: "Keepalive lock metadata."},
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
		"page-select": {
			Name:        "page-select",
			Description: "Selected default page target for the effective browser connection.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the page selection was saved."},
				{Name: "selected_page", Type: "page_selection", Required: true, Description: "Connection-scoped selected target id, url, title, and timestamp."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "state_path", Type: "string", Required: true, Description: "Local state file path where the selection was saved."},
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
		"wait": {
			Name:        "wait",
			Description: "Page condition wait result for text or selector checks.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the condition matched before timeout."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "wait", Type: "wait_result", Required: true, Description: "Kind, condition, match status, count, elapsed time, and poll interval."},
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
		"network": {
			Name:        "network",
			Description: "Network requests captured from a page target.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when network capture completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "requests", Type: "array<network_request>", Required: true, Description: "Request rows with id, URL, method, status, failure state, and size metadata."},
				{Name: "network", Type: "network_summary", Required: true, Description: "Capture metadata including count, wait, limit, filters, and truncation state."},
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
		"workflow-hacker-news": {
			Name:        "workflow-hacker-news",
			Description: "Focused Hacker News front-page story and page-organization summary.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when at least one story row was found."},
				{Name: "url", Type: "string", Required: true, Description: "Requested Hacker News URL."},
				{Name: "target", Type: "page", Required: true, Description: "Created page target metadata."},
				{Name: "organization", Type: "object", Required: true, Description: "Stable selectors and layout notes for the table-based HN page."},
				{Name: "stories", Type: "array<hacker_news_story>", Required: true, Description: "Visible story rows with rank, title, site, score, age, author, and comments."},
				{Name: "workflow", Type: "workflow_summary", Required: true, Description: "Workflow name, count, wait, and limit metadata."},
			},
		},
		"workflow-console-errors": {
			Name:        "workflow-console-errors",
			Description: "Focused console error and warning summary.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when workflow collection completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "messages", Type: "array<console_message>", Required: true, Description: "Error and warning console/log messages."},
				{Name: "workflow", Type: "workflow_summary", Required: true, Description: "Workflow name, count, wait, truncation, and suggested next commands."},
			},
		},
		"workflow-network-failures": {
			Name:        "workflow-network-failures",
			Description: "Focused failed and HTTP error network request summary.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when workflow collection completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "requests", Type: "array<network_request>", Required: true, Description: "Failed request rows."},
				{Name: "workflow", Type: "workflow_summary", Required: true, Description: "Workflow name, count, wait, truncation, and suggested next commands."},
			},
		},
		"workflow-page-load": {
			Name:        "workflow-page-load",
			Description: "Page-load evidence bundle with console, network, storage-key, performance, and navigation signals.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the workflow completed, even if individual collectors are partial."},
				{Name: "target", Type: "page", Required: true, Description: "Selected or created page target metadata."},
				{Name: "requests", Type: "array<network_request>", Required: true, Description: "Network requests observed after collectors were attached."},
				{Name: "messages", Type: "array<console_message>", Required: true, Description: "Console and log messages observed after collectors were attached."},
				{Name: "storage", Type: "storage_keys", Required: false, Description: "Cookie, localStorage, and sessionStorage key names without values."},
				{Name: "performance", Type: "performance_metrics", Required: false, Description: "Performance.getMetrics output after Performance.enable."},
				{Name: "navigation", Type: "navigation_history", Required: false, Description: "Page navigation history after the load window."},
				{Name: "workflow", Type: "workflow_summary", Required: true, Description: "Trigger, requested URL, wait, truncation, artifact, and partial collector metadata."},
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
				{Name: "artifact", Type: "artifact", Required: false, Description: "Artifact metadata when --save writes a base64 data field to disk."},
				{Name: "artifacts", Type: "array<artifact>", Required: false, Description: "Artifact list for agent workflows."},
			},
		},
		"protocol-examples": {
			Name:        "protocol-examples",
			Description: "Generated raw CDP exec examples for a protocol command.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when examples were generated."},
				{Name: "entity", Type: "protocol_entity", Required: true, Description: "Protocol command metadata and raw schema."},
				{Name: "examples", Type: "array<protocol_exec_example>", Required: true, Description: "Runnable cdp protocol exec command examples with scope and params."},
				{Name: "source", Type: "string", Required: true, Description: "Protocol source endpoint."},
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
