package cli

import (
	"strings"
	"testing"
)

func TestSchemaCatalogInvariants(t *testing.T) {
	catalog := schemaCatalog()
	if len(catalog) == 0 {
		t.Fatal("schemaCatalog() returned no schemas")
	}
	for key, info := range catalog {
		if info.Name != key {
			t.Fatalf("schemaCatalog()[%q].Name = %q, want key", key, info.Name)
		}
		if info.Description == "" {
			t.Fatalf("schemaCatalog()[%q].Description is empty", key)
		}
		if len(info.Fields) == 0 {
			t.Fatalf("schemaCatalog()[%q].Fields is empty", key)
		}
		seenFields := map[string]bool{}
		for _, field := range info.Fields {
			if field.Name == "" {
				t.Fatalf("schemaCatalog()[%q] has field with empty name", key)
			}
			if seenFields[field.Name] {
				t.Fatalf("schemaCatalog()[%q] has duplicate field %q", key, field.Name)
			}
			seenFields[field.Name] = true
			if field.Required {
				if field.Type == "" {
					t.Fatalf("schemaCatalog()[%q] required field %q has empty type", key, field.Name)
				}
				if field.Description == "" {
					t.Fatalf("schemaCatalog()[%q] required field %q has empty description", key, field.Name)
				}
			}
		}
	}
}

func TestSchemaCatalogCriticalCommands(t *testing.T) {
	catalog := schemaCatalog()
	critical := []string{
		"describe",
		"doctor",
		"doctor-capabilities",
		"error-envelope",
		"cron",
		"cron-profile-seed",
		"cron-task",
		"cron-migrate-pages-polling",
		"scheduled-tasks",
		"scheduled-tasks-details",
		"headless-security",
		"browser-mode",
		"browser-profile-status",
		"browser-profile-seed",
		"profile-seed-status",
		"profile-seed-resource-status",
		"profile-seed-maintenance",
		"profile-seed-maintenance-status",
		"profile-seed-managed-process-state",
		"profile-seed-managed-stop-state",
		"managed-browser",
		"managed-stop",
		"managed-process-evidence",
		"managed-ownership",
		"managed-recovery-state",
		"managed-process-reconcile",
		"managed-process-record",
		"managed-process-signal-failure",
		"resource-preflight",
		"resource-preflight-policy",
		"resource-preflight-check",
		"connection-current",
		"connection-resolve",
		"daemon-status",
		"daemon-keepalive",
		"daemon-maintenance",
		"daemon-maintenance-options",
		"daemon-maintenance-phase",
		"daemon-health",
		"daemon-logs",
		"pages",
		"page-cleanup",
		"stop-state-classify",
		"protocol-examples",
		"storage",
		"rendered-extract-content",
		"rendered-extract-readiness",
		"rendered-extract-quality",
		"workflow-rendered-extract",
		"workflow-reddit-posts",
		"workflow-reddit-collect",
		"workflow-x-collect",
		"workflow-linkedin-collect",
		"workflow-hacker-news-collect",
		"workflow-arxiv-collect",
		"source-collection-coverage",
		"workflow-submit-search",
		"workflow-web-research-serp",
		"workflow-web-research-extract",
		"web-research-query-coverage",
	}
	for _, name := range critical {
		if _, ok := catalog[name]; !ok {
			t.Fatalf("schemaCatalog() missing critical schema %q", name)
		}
	}
}

func TestSchemaCatalogRedditCollectorContract(t *testing.T) {
	workflow := schemaCatalog()["workflow-reddit-posts"]
	if !catalogSchemaFieldContains(workflow, "request", "object", "subreddit", "sort") ||
		!catalogSchemaFieldContains(workflow, "threads", "array<reddit_thread>", "same-subreddit", "t3") ||
		!catalogSchemaFieldContains(workflow, "next_cursor", "string", "never proves", "exhausted") ||
		!catalogSchemaFieldContains(workflow, "workflow", "object", "partial reason", "cleanup") {
		t.Fatalf("Reddit collector schema is incomplete: %+v", workflow)
	}
}

func TestSchemaCatalogRedditCollectContract(t *testing.T) {
	workflow := schemaCatalog()["workflow-reddit-collect"]
	if !catalogSchemaFieldContains(workflow, "kind", "string", "subreddit_listing", "thread", "user_profile") ||
		!catalogSchemaFieldContains(workflow, "records", "array<reddit_record>", "canonical identity", "discovery surface") ||
		!catalogSchemaFieldContains(workflow, "coverage", "source_collection_coverage", "possibly missing", "termination evidence") {
		t.Fatalf("Reddit collect schema is incomplete: %+v", workflow)
	}
}

func TestSchemaCatalogDynamicCollectorCoverageContract(t *testing.T) {
	for _, name := range []string{"workflow-x-collect", "workflow-linkedin-collect"} {
		workflow := schemaCatalog()[name]
		if !catalogSchemaFieldContains(workflow, "coverage", "source_collection_coverage", "possibly missing", "termination evidence") {
			t.Fatalf("%s coverage schema is incomplete: %+v", name, workflow)
		}
	}
}

func TestSchemaCatalogNativeCollectorInteractionsContract(t *testing.T) {
	for _, name := range []string{
		"workflow-reddit-posts",
		"workflow-reddit-collect",
		"workflow-x-collect",
		"workflow-linkedin-collect",
		"workflow-hacker-news-collect",
		"workflow-arxiv-collect",
	} {
		workflow := schemaCatalog()[name]
		if !catalogSchemaFieldContains(workflow, "workflow", "object", "interactions") {
			t.Fatalf("%s workflow schema does not document interactions: %+v", name, workflow)
		}
	}
}

func TestSchemaCatalogSourceCollectionCoverageContract(t *testing.T) {
	coverage := schemaCatalog()["source-collection-coverage"]
	if !catalogSchemaFieldContains(coverage, "observed_record_kinds", "array<string>", "observed") ||
		!catalogSchemaFieldContains(coverage, "possibly_missing_record_kinds", "array<string>", "not prove") ||
		!catalogSchemaFieldContains(coverage, "continuation", "string", "continuation surface") ||
		!catalogSchemaFieldContains(coverage, "unresolved_controls", "boolean", "exhausted") ||
		!catalogSchemaFieldContains(coverage, "decode_rejections", "number", "exhausted") ||
		!catalogSchemaFieldContains(coverage, "termination_evidence", "array<string>", "terminal") {
		t.Fatalf("source collection coverage schema is incomplete: %+v", coverage)
	}
}

func TestSchemaCatalogXCollectContract(t *testing.T) {
	workflow := schemaCatalog()["workflow-x-collect"]
	if !catalogSchemaFieldContains(workflow, "kind", "string", "post_thread", "profile_posts") ||
		!catalogSchemaFieldContains(workflow, "records", "array<x_record>", "canonical status", "root status") ||
		!catalogSchemaFieldContains(workflow, "workflow", "object", "hard 500", "partial reason") {
		t.Fatalf("X collect schema is incomplete: %+v", workflow)
	}
}

func TestSchemaCatalogLinkedInCollectContract(t *testing.T) {
	workflow := schemaCatalog()["workflow-linkedin-collect"]
	if !catalogSchemaFieldContains(workflow, "kind", "string", "activity_thread", "company_posts") ||
		!catalogSchemaFieldContains(workflow, "records", "array<linkedin_record>", "activity", "comment") ||
		!catalogSchemaFieldContains(workflow, "workflow", "object", "hard 500", "partial reason") {
		t.Fatalf("LinkedIn collect schema is incomplete: %+v", workflow)
	}
}

func TestSchemaCatalogHackerNewsAndArxivCollectContracts(t *testing.T) {
	catalog := schemaCatalog()
	if workflow := catalog["workflow-hacker-news-collect"]; !catalogSchemaFieldContains(workflow, "kind", "string", "thread") ||
		!catalogSchemaFieldContains(workflow, "records", "array<hacker_news_record>", "story", "comment") ||
		!catalogSchemaFieldContains(workflow, "coverage", "source_collection_coverage", "possibly missing", "termination evidence") ||
		!catalogSchemaFieldContains(workflow, "workflow", "object", "hard 500", "partial reason") {
		t.Fatalf("Hacker News collect schema is incomplete: %+v", workflow)
	}
	if workflow := catalog["workflow-arxiv-collect"]; !catalogSchemaFieldContains(workflow, "paper", "arxiv_paper", "version-pinned") ||
		!catalogSchemaFieldContains(workflow, "references", "array<arxiv_reference>", "exact paper identity") ||
		!catalogSchemaFieldContains(workflow, "coverage", "source_collection_coverage", "possibly missing", "termination evidence") ||
		!catalogSchemaFieldContains(workflow, "workflow", "object", "hard 500", "partial reason") {
		t.Fatalf("arXiv collect schema is incomplete: %+v", workflow)
	}
}

func TestSchemaCatalogWebResearchQueryContract(t *testing.T) {
	catalog := schemaCatalog()
	workflow := catalog["workflow-web-research-serp"]
	query := catalog["web-research-query"]

	if !catalogSchemaFieldContains(workflow, "queries", "array<web_research_query>", "query<TAB>", "applied only to Google", "cdr:1,cd_min:07/01/2026,cd_max:07/01/2026") {
		t.Fatalf("workflow schema does not expose the custom-date query contract: %+v", workflow)
	}
	if !catalogSchemaFieldContains(query, "query", "string", "non-empty") {
		t.Fatalf("query schema does not expose the required query column: %+v", query)
	}
	if !catalogSchemaFieldContains(query, "time_filter", "string", "Google tbs", "ignored by other engines", "cdr:1,cd_min:07/01/2026,cd_max:07/01/2026") {
		t.Fatalf("query schema does not expose the optional Google time filter: %+v", query)
	}
}

func TestSchemaCatalogRenderedExtractAndQueryCoverageContracts(t *testing.T) {
	catalog := schemaCatalog()
	content := catalog["rendered-extract-content"]
	readiness := catalog["rendered-extract-readiness"]
	quality := catalog["rendered-extract-quality"]
	rendered := catalog["workflow-rendered-extract"]
	serp := catalog["workflow-web-research-serp"]
	coverage := catalog["web-research-query-coverage"]
	extract := catalog["workflow-web-research-extract"]

	if !catalogSchemaFieldContains(rendered, "content", "rendered_extract_content", "Source-profile") ||
		!catalogSchemaFieldContains(rendered, "workflow", "workflow_summary", "navigation", "content_extractor") ||
		!catalogSchemaFieldContains(content, "profile", "string", "arxiv", "hacker-news", "x-profile", "reddit-user-profile", "linkedin-company-posts") ||
		!catalogSchemaFieldContains(content, "planned_strategy", "string", "semantic-dom", "discussion-tree") ||
		!catalogSchemaFieldContains(content, "strategy", "string", "Effective", "fallbacks", "legacy-html") ||
		!catalogSchemaFieldContains(content, "planned_representation", "string", "before navigation") ||
		!catalogSchemaFieldContains(content, "representation", "string", "Effective", "rendered-html") ||
		!catalogSchemaFieldContains(content, "representation_rewritten", "boolean", "PDF", "TeX-source") ||
		!catalogSchemaFieldContains(content, "native_succeeded", "boolean", "semantic Markdown") ||
		!catalogSchemaFieldContains(content, "fallback_reason", "string", "mismatch", "capture") ||
		!catalogSchemaFieldContains(content, "item_count", "number", "social root", "discussion") ||
		!catalogSchemaFieldContains(content, "discussion_limit", "number", "500") ||
		!catalogSchemaFieldContains(content, "discussion_status", "string", "ceiling", "partial", "unknown", "invalid") ||
		!catalogSchemaFieldContains(content, "discussion_interactions", "number", "load-more") ||
		!catalogSchemaFieldContains(content, "representations", "object", "html", "pdf", "source", "abstract") {
		t.Fatalf("rendered content-profile schema is incomplete: workflow=%+v content=%+v", rendered, content)
	}
	if !catalogSchemaFieldContains(readiness, "outcome", "string", "immediate", "wait_expired") ||
		!catalogSchemaFieldContains(readiness, "settle", "duration", "fingerprint") ||
		!catalogSchemaFieldContains(readiness, "thresholds_met", "boolean", "every enabled") ||
		!catalogSchemaFieldContains(readiness, "capture_consistent", "boolean", "post-capture") ||
		!catalogSchemaFieldContains(readiness, "network_idle_seen", "boolean", "Always false") {
		t.Fatalf("rendered readiness schema is incomplete: %+v", readiness)
	}
	if !catalogSchemaFieldContains(quality, "passed", "boolean", "readiness completed", "every enabled", "consistency") ||
		!catalogSchemaFieldContains(quality, "thresholds_passed", "boolean", "every enabled") ||
		!catalogSchemaFieldContains(quality, "readiness_passed", "boolean", "wait_expired") ||
		!catalogSchemaFieldContains(quality, "capture_consistency_checked", "boolean", "post-capture") ||
		!catalogSchemaFieldContains(quality, "capture_consistent", "boolean", "fingerprints matched") ||
		!catalogSchemaFieldContains(quality, "thresholds", "object", "zero disables") {
		t.Fatalf("rendered quality schema is incomplete: %+v", quality)
	}
	if !catalogSchemaFieldContains(serp, "query_coverage", "array<web_research_query_coverage>", "Input-order") ||
		!catalogSchemaFieldContains(coverage, "duplicate_candidates", "number", "within this query", "earlier query") ||
		!catalogSchemaFieldContains(coverage, "omitted_by_cap", "number", "global candidate cap") {
		t.Fatalf("query coverage schema is incomplete: workflow=%+v coverage=%+v", serp, coverage)
	}
	for _, field := range []string{
		"selector_match_count",
		"selected_text_length",
		"selected_html_length",
		"selected_word_count",
		"body_text_length",
		"body_html_length",
		"element_count",
		"navigated_from_about_blank",
		"load_seen",
		"dom_stable_seen",
		"text_stable_seen",
		"html_stable_seen",
		"content_grew_seen",
		"stable_polls",
		"poll_count",
		"useful_content_seen",
		"error",
	} {
		if !catalogSchemaHasField(readiness, field) {
			t.Fatalf("rendered readiness schema is missing emitted field %q: %+v", field, readiness)
		}
	}
	if !catalogSchemaFieldContains(extract, "ok", "boolean", "passed every enabled quality gate") {
		t.Fatalf("web research extract schema does not expose quality-gated success: %+v", extract)
	}
}

func TestSchemaCatalogBrowserModeContracts(t *testing.T) {
	catalog := schemaCatalog()
	cases := map[string][]string{
		"browser-mode":               {"browser_mode", "browser_mode_source", "next_commands"},
		"browser-preflight":          {"browser_mode", "state", "resource_preflight", "health"},
		"browser-profile-status":     {"browser_mode", "state_dir", "state", "managed_browser", "profile_perm", "metadata_perm", "seed_strategy", "resource_preflight", "last_seed", "seed_status_path"},
		"browser-profile-seed":       {"browser_mode", "state_dir", "state", "seed_strategy", "seed_age_seconds", "seed_interval_seconds", "managed_browser", "maintenance", "resource_preflight", "last_seed", "seed_status_path"},
		"profile-seed-status":        {"schema_version", "browser_mode", "status", "state", "seed_strategy", "seed_action", "checked_at", "fresh", "resource_preflight", "maintenance"},
		"profile-seed-maintenance":   {"was_running", "managed_process_sweep", "managed_stop", "healed", "managed_browser"},
		"managed-browser":            {"browser_mode", "user_data_dir", "profile_seed_strategy", "debugging_port", "default_profile_copied", "copied_file_count"},
		"managed-stop":               {"checked", "stopped", "skipped", "process_evidence", "safety_checks"},
		"managed-process-evidence":   {"pid", "root_pid", "role", "profile_matched", "debugging_port_match"},
		"managed-ownership":          {"checked", "owned", "safety_checks", "reasons"},
		"managed-recovery-state":     {"connections_removed", "stale_locks", "runtime_artifacts_cleared"},
		"managed-process-reconcile":  {"checked", "state", "browser_mode", "registered_count", "live_count", "stale_count", "reaped_count", "records", "signal_failures", "next_commands"},
		"resource-preflight":         {"checked", "browser_mode", "state", "status", "heavy_work_allowed", "policy", "checks", "reasons", "next_commands"},
		"resource-preflight-check":   {"name", "status", "live_count", "stale_count", "tab_count", "window_count", "retryable", "next_command"},
		"connection-current":         {"browser_mode", "browser_mode_source", "connection", "effective_connection", "connection_matches_effective"},
		"connection-resolve":         {"browser_mode", "browser_mode_source", "connection"},
		"daemon-status":              {"daemon"},
		"daemon-keepalive":           {"browser_mode", "connection", "mode", "lock"},
		"daemon-maintenance":         {"schema_version", "browser_mode", "state", "dry_run", "run_id", "started_at", "finished_at", "locked", "lock", "options", "phases", "artifacts", "warnings", "next_commands"},
		"daemon-maintenance-options": {"profile_seed_strategy", "profile_seed_if_older_than", "profile_seed_if_older_than_seconds", "health_check", "cleanup_close", "lock_timeout", "stale_lock_after"},
		"daemon-maintenance-phase":   {"order", "name", "status", "required", "mutates", "heavy_work", "resource_gated", "started_at", "finished_at", "result"},
		"cron":                       {"tasks", "managed_processes", "last_run_artifacts", "next_commands"},
		"cron-task":                  {"id", "browser_mode", "launch_capable", "requires_managed_process_sweep", "managed_process_sweep_installed", "status"},
		"scheduled-tasks-details":    {"expected_managed_task_ids", "installed_managed_task_ids", "missing_managed_task_ids", "tasks", "has_managed_process_sweep", "has_headless_launch_without_managed_process_sweep", "last_run_artifacts", "managed_processes"},
		"cron-migrate-pages-polling": {"action", "dry_run", "applied", "candidate_count", "managed_keepalive_installed", "next_commands"},
		"scheduled-tasks":            {"details", "next_commands"},
		"headless-security":          {"browser_mode", "details", "next_commands"},
		"stop-state-classify":        {"stop_state", "stop_state_class", "agent_should_stop", "next_commands"},
	}
	for schemaName, fieldNames := range cases {
		info, ok := catalog[schemaName]
		if !ok {
			t.Fatalf("schemaCatalog() missing %q", schemaName)
		}
		for _, fieldName := range fieldNames {
			if !catalogSchemaHasField(info, fieldName) {
				t.Fatalf("schemaCatalog()[%q] missing field %q", schemaName, fieldName)
			}
		}
	}
}

func TestSchemaCatalogDaemonMaintenanceUsesPhaseResults(t *testing.T) {
	info := schemaCatalog()["daemon-maintenance"]
	for _, fieldName := range []string{"resource_preflight", "managed_process_sweep", "profile_seed", "health_check", "cleanup"} {
		if catalogSchemaHasField(info, fieldName) {
			t.Fatalf("schemaCatalog()[%q] has stale top-level phase result field %q", info.Name, fieldName)
		}
	}
	phase := schemaCatalog()["daemon-maintenance-phase"]
	if !catalogSchemaHasField(phase, "result") {
		t.Fatalf("schemaCatalog()[%q] missing phase result field", phase.Name)
	}
}

func catalogSchemaHasField(info schemaInfo, name string) bool {
	for _, field := range info.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func catalogSchemaFieldContains(info schemaInfo, name, fieldType string, fragments ...string) bool {
	for _, field := range info.Fields {
		if field.Name != name || field.Type != fieldType {
			continue
		}
		for _, fragment := range fragments {
			if !strings.Contains(field.Description, fragment) {
				return false
			}
		}
		return true
	}
	return false
}
