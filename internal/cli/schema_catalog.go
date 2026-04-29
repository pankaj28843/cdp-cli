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
				{Name: "commands", Type: "command", Required: true, Description: "Recursive command tree with command-local flags and examples."},
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
		"doctor-capabilities": {
			Name:        "doctor-capabilities",
			Description: "Implemented and planned cdp-cli capability areas.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when capability metadata was generated."},
				{Name: "capabilities", Type: "array<capability>", Required: true, Description: "Capability rows with name, status, and related commands."},
			},
		},
		"connection-add": {
			Name:        "connection-add",
			Description: "Saved browser connection metadata after adding or updating a named connection.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the connection was saved."},
				{Name: "connection", Type: "connection", Required: true, Description: "Saved connection name, mode, browser URL metadata, project scope, and auto-connect settings."},
				{Name: "state_path", Type: "string", Required: true, Description: "Local state file path where connection metadata was saved."},
			},
		},
		"connection-list": {
			Name:        "connection-list",
			Description: "Saved browser connections visible to the current profile and state directory.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when connection metadata was listed."},
				{Name: "connections", Type: "array<connection>", Required: true, Description: "Saved connections with name, mode, project, and selection metadata."},
				{Name: "current", Type: "string", Required: false, Description: "Currently selected connection name when one is selected."},
			},
		},
		"connection-select": {
			Name:        "connection-select",
			Description: "Selected connection metadata for subsequent browser commands.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the selected connection was updated."},
				{Name: "connection", Type: "connection", Required: true, Description: "Selected connection metadata."},
				{Name: "state_path", Type: "string", Required: true, Description: "Local state file path where selection metadata was saved."},
			},
		},
		"connection-current": {
			Name:        "connection-current",
			Description: "Current connection selected from local connection memory.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when current connection metadata was read."},
				{Name: "connection", Type: "connection", Required: false, Description: "Current connection metadata when a connection is selected."},
			},
		},
		"connection-remove": {
			Name:        "connection-remove",
			Description: "Connection removal result.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the remove operation completed."},
				{Name: "removed", Type: "string", Required: true, Description: "Name of the removed connection."},
				{Name: "connections", Type: "array<connection>", Required: true, Description: "Remaining saved connections."},
			},
		},
		"connection-prune": {
			Name:        "connection-prune",
			Description: "Connection prune result for stale local connection metadata.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when prune completed or dry-run metadata was generated."},
				{Name: "removed", Type: "array<string>", Required: true, Description: "Connection names removed or that would be removed during a dry run."},
				{Name: "dry_run", Type: "boolean", Required: true, Description: "True when no state mutation was performed."},
			},
		},
		"connection-resolve": {
			Name:        "connection-resolve",
			Description: "Effective browser connection after applying explicit, project, current, and environment selection rules.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when an effective connection was resolved."},
				{Name: "source", Type: "string", Required: true, Description: "Selection source such as explicit, project, selected, environment, or none."},
				{Name: "connection", Type: "connection", Required: false, Description: "Resolved connection metadata when available."},
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
		"daemon-logs": {
			Name:        "daemon-logs",
			Description: "Daemon JSONL log entries from the local state directory.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the log file was read or found absent."},
				{Name: "log", Type: "object", Required: true, Description: "Log path, requested tail, and returned count."},
				{Name: "entries", Type: "array<daemon_log_entry>", Required: true, Description: "Log entries with time, level, event, message, and pid."},
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
		"page-cleanup": {
			Name:        "page-cleanup",
			Description: "Cron-friendly dry-run or close result for inactive page targets.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when cleanup evaluation completed."},
				{Name: "cleanup", Type: "page_cleanup_summary", Required: true, Description: "Dry-run flag, filters, selected page, candidate count, closed count, and next commands."},
				{Name: "candidates", Type: "array<page_cleanup_candidate>", Required: true, Description: "Page targets considered with visibility state, hidden flag, keep reason, and close error."},
				{Name: "closed", Type: "array<page_cleanup_candidate>", Required: true, Description: "Candidates closed when --close is set."},
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
		"click": {
			Name:        "click",
			Description: "Mouse click operation against the first matching element.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when click command completed."},
				{Name: "action", Type: "string", Required: true, Description: "Action name, typically clicked when an element is found."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "click", Type: "click_result", Required: true, Description: "Selector, matched count, clicked boolean, and error metadata when applicable."},
			},
		},
		"fill": {
			Name:        "fill",
			Description: "Set the value of the first matching form control.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when fill command completed."},
				{Name: "action", Type: "string", Required: true, Description: "Action name, typically filled when an element is successfully updated."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "fill", Type: "fill_result", Required: true, Description: "Selector, matched count, filled boolean, and previous/current values."},
			},
		},
		"type": {
			Name:        "type",
			Description: "Emit key events against the first matching control to simulate typing.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when type command completed."},
				{Name: "action", Type: "string", Required: true, Description: "Action name, typically typed when an element is successfully updated."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "type", Type: "type_result", Required: true, Description: "Selector, matched count, typed string, previous value, and success flag."},
			},
		},
		"press": {
			Name:        "press",
			Description: "Dispatch keyboard events for a key on the focused element or selector.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when press command completed."},
				{Name: "action", Type: "string", Required: true, Description: "Action name, typically pressed when key dispatch succeeds."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "press", Type: "press_result", Required: true, Description: "Selector, key name, matched count, and dispatch status."},
			},
		},
		"hover": {
			Name:        "hover",
			Description: "Dispatch pointer hover events over the first matching element.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when hover command completed."},
				{Name: "action", Type: "string", Required: true, Description: "Action name, typically hovered when pointer events are dispatched."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "hover", Type: "hover_result", Required: true, Description: "Selector, matched count, hovered flag, and hovered coordinates."},
			},
		},
		"drag": {
			Name:        "drag",
			Description: "Drag the first matching element by a delta using pointer events.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when drag command completed."},
				{Name: "action", Type: "string", Required: true, Description: "Action name, typically dragged when pointer events are dispatched."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "drag", Type: "drag_result", Required: true, Description: "Selector, matched count, drag success flag, delta, and coordinates."},
			},
		},
		"frames": {
			Name:        "frames",
			Description: "List the frame tree for the selected target.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when frame listing completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "frames", Type: "array<frame_summary>", Required: true, Description: "Flattened frame metadata including id, URL, parent id, and child count."},
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
				{Name: "messages", Type: "array<console_message>", Required: true, Description: "Console/log entries with id, source, type or level, text, timestamp, optional location, stack_trace, args, and exception details."},
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
		"network-capture": {
			Name:        "network-capture",
			Description: "Full local network metadata capture with headers, bodies, timing, initiators, redaction, and artifact output.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when network capture completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "requests", Type: "array<network_capture_request>", Required: true, Description: "Full request/response records keyed by CDP request id."},
				{Name: "capture", Type: "network_capture_summary", Required: true, Description: "Capture options, redaction mode, warning, and collector errors."},
				{Name: "artifact", Type: "artifact", Required: false, Description: "JSON artifact metadata when --out is used."},
				{Name: "artifacts", Type: "array<artifact>", Required: false, Description: "Artifact list for agent workflows."},
			},
		},
		"storage": {
			Name:        "storage",
			Description: "Application storage inspection and Web Storage, cookie, or Cache Storage mutation result.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the storage command completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata for browser-backed commands."},
				{Name: "storage", Type: "storage_result", Required: true, Description: "Storage area snapshot, operation result, or command metadata."},
				{Name: "collector_errors", Type: "array<collector_error>", Required: false, Description: "Non-fatal collector errors for optional areas such as quota."},
			},
		},
		"storage-cache": {
			Name:        "storage-cache",
			Description: "Cache Storage list/get/put/delete/clear result.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the Cache Storage command completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "storage", Type: "cache_storage_result", Required: true, Description: "Cache names, request rows, response metadata, body truncation metadata, and mutation booleans."},
			},
		},
		"storage-indexeddb": {
			Name:        "storage-indexeddb",
			Description: "IndexedDB list/get/put/delete/clear result.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the IndexedDB command completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "storage", Type: "indexeddb_result", Required: true, Description: "Database/store metadata, record values, mutation booleans, and counts."},
			},
		},
		"storage-service-workers": {
			Name:        "storage-service-workers",
			Description: "Service worker registration list/unregister result.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the service worker command completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "storage", Type: "service_worker_result", Required: true, Description: "Registration scopes, script URLs, lifecycle states, and unregister results."},
			},
		},
		"storage-snapshot": {
			Name:        "storage-snapshot",
			Description: "Local forensic storage snapshot with optional redaction and artifact output.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the storage snapshot completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "snapshot", Type: "storage_snapshot", Required: true, Description: "Origin, localStorage, sessionStorage, cookies, IndexedDB metadata, Cache Storage request metadata, service worker registrations, and quota data; --redact safe replaces storage and cookie values with <redacted>."},
				{Name: "storage", Type: "storage_snapshot_summary", Required: true, Description: "Snapshot options, redaction mode, warning, and collector errors."},
				{Name: "artifact", Type: "artifact", Required: false, Description: "JSON artifact metadata when --out is used."},
				{Name: "artifacts", Type: "array<artifact>", Required: false, Description: "Artifact list for agent workflows."},
			},
		},
		"storage-diff": {
			Name:        "storage-diff",
			Description: "Difference between two storage snapshot artifacts.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when both snapshots were read and compared."},
				{Name: "left", Type: "string", Required: true, Description: "Left/before snapshot path."},
				{Name: "right", Type: "string", Required: true, Description: "Right/after snapshot path."},
				{Name: "has_diff", Type: "boolean", Required: true, Description: "True when any added, removed, or changed storage entries were found."},
				{Name: "diff", Type: "storage_diff", Required: true, Description: "Added, removed, and changed entries grouped by storage area."},
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
		"workflow-debug-bundle": {
			Name:        "workflow-debug-bundle",
			Description: "Comprehensive workflow evidence bundle including console events, network events, snapshot, screenshot, and artifact references.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when bundle collection completed."},
				{Name: "target", Type: "page", Required: true, Description: "Created or selected page target metadata."},
				{Name: "requests", Type: "array<network_request>", Required: true, Description: "Network requests observed during the collect window."},
				{Name: "messages", Type: "array<console_message>", Required: true, Description: "Console/log messages observed during the collect window."},
				{Name: "snapshot", Type: "snapshot", Required: true, Description: "Visible text snapshot captured after collection started."},
				{Name: "evidence", Type: "object", Required: true, Description: "Summary counts, truncation state, and screenshot/compatibility flags."},
				{Name: "artifact", Type: "artifact", Required: false, Description: "JSON bundle artifact when --out-dir is used."},
				{Name: "artifacts", Type: "array<artifact>", Required: false, Description: "Per-artifact references for workflow-replayable output."},
				{Name: "workflow", Type: "workflow_summary", Required: true, Description: "Workflow name, requested URL, counts, next commands, and collector status."},
			},
		},
		"workflow-a11y": {
			Name:        "workflow-a11y",
			Description: "Focused accessibility signal collection with basic console/network evidence.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the accessibility workflow completed."},
				{Name: "target", Type: "page", Required: true, Description: "Selected page target metadata."},
				{Name: "requests", Type: "array<network_request>", Required: true, Description: "Failed network request rows observed during the signal window."},
				{Name: "messages", Type: "array<console_message>", Required: true, Description: "Console/log errors and warnings observed during the signal window."},
				{Name: "a11y", Type: "object", Required: true, Description: "Accessibility signal counts and suggested follow-up commands."},
				{Name: "workflow", Type: "workflow_summary", Required: true, Description: "Workflow name, counts, wait, truncation, and collector metadata."},
			},
		},
		"workflow-perf": {
			Name:        "workflow-perf",
			Description: "Collected performance metrics and trace artifact from a page-load window.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the workflow completed, even if some collectors were partial."},
				{Name: "target", Type: "page", Required: true, Description: "Created page target metadata."},
				{Name: "performance", Type: "performance_metrics", Required: true, Description: "Performance.getMetrics output after Performance.enable."},
				{Name: "artifact", Type: "artifact", Required: false, Description: "Optional JSON performance trace artifact when --trace is used."},
				{Name: "artifacts", Type: "array<artifact>", Required: false, Description: "Artifact list for agent workflows."},
				{Name: "workflow", Type: "workflow_summary", Required: true, Description: "Workflow name, requested URL, wait, metric count, and next commands."},
			},
		},
		"workflow-verify": {
			Name:        "workflow-verify",
			Description: "Focused URL verification evidence with console and failed network requests.",
			Fields: []schemaField{
				{Name: "ok", Type: "boolean", Required: true, Description: "True when the verification workflow completed."},
				{Name: "target", Type: "page", Required: true, Description: "Created page target metadata."},
				{Name: "requests", Type: "array<network_request>", Required: true, Description: "Failed/errored network requests captured during verification."},
				{Name: "messages", Type: "array<console_message>", Required: true, Description: "Error and warning console/log messages captured during verification."},
				{Name: "artifact", Type: "artifact", Required: false, Description: "Optional JSON report artifact when --out is used."},
				{Name: "artifacts", Type: "array<artifact>", Required: false, Description: "Artifact list for agent workflows."},
				{Name: "workflow", Type: "workflow_summary", Required: true, Description: "Trigger, requested URL, wait, truncation, and next commands."},
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
		"protocol-compat": {Name: "protocol-compat", Description: "Live CDP compatibility report for required protocol paths.", Fields: []schemaField{{Name: "ok", Type: "boolean", Required: true, Description: "True when compatibility metadata was generated."}, {Name: "protocol_version", Type: "object", Required: true, Description: "Live protocol version metadata."}, {Name: "schema_source", Type: "string", Required: true, Description: "Protocol schema source used for checks."}, {Name: "required", Type: "array<protocol_compat_check>", Required: true, Description: "Requested protocol paths and availability."}}},
		"a11y":            {Name: "a11y", Description: "Accessibility tree query result.", Fields: []schemaField{{Name: "ok", Type: "boolean", Required: true, Description: "True when accessibility data was collected."}, {Name: "nodes", Type: "array<a11y_node>", Required: false, Description: "Accessibility nodes."}, {Name: "truncated", Type: "boolean", Required: false, Description: "True when output was bounded."}}},
		"emulation":       {Name: "emulation", Description: "Target emulation result.", Fields: []schemaField{{Name: "ok", Type: "boolean", Required: true, Description: "True when emulation command completed."}, {Name: "emulation", Type: "object", Required: true, Description: "Applied emulation metadata and cleanup command."}}},
		"events-tap":      {Name: "events-tap", Description: "Bounded raw CDP event stream.", Fields: []schemaField{{Name: "ok", Type: "boolean", Required: true, Description: "True when event collection completed."}, {Name: "events", Type: "array<cdp_event>", Required: true, Description: "Captured CDP events."}, {Name: "tap", Type: "object", Required: true, Description: "Collection bounds and truncation metadata."}}},
		"workflow-feeds":  {Name: "workflow-feeds", Description: "RSS, Atom, and JSON Feed discovery workflow.", Fields: []schemaField{{Name: "ok", Type: "boolean", Required: true, Description: "True when feeds were discovered."}, {Name: "workflow", Type: "object", Required: true, Description: "Workflow cleanup metadata."}, {Name: "feeds", Type: "array<feed_link>", Required: true, Description: "Discovered feed links."}}},
		"perf-summary":    {Name: "perf-summary", Description: "Lightweight performance metric summary.", Fields: []schemaField{{Name: "ok", Type: "boolean", Required: true, Description: "True when metrics were collected."}, {Name: "metrics", Type: "object", Required: true, Description: "Performance metrics summary."}}},
		"memory":          {Name: "memory", Description: "Memory counters or heap artifact result.", Fields: []schemaField{{Name: "ok", Type: "boolean", Required: true, Description: "True when memory command completed."}, {Name: "memory", Type: "object", Required: false, Description: "Memory counters."}, {Name: "artifact", Type: "artifact", Required: false, Description: "Heap snapshot artifact metadata."}}},

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
