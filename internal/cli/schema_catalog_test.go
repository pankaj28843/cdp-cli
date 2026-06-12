package cli

import "testing"

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
		"workflow-rendered-extract",
		"workflow-submit-search",
		"workflow-web-research-serp",
		"workflow-web-research-extract",
	}
	for _, name := range critical {
		if _, ok := catalog[name]; !ok {
			t.Fatalf("schemaCatalog() missing critical schema %q", name)
		}
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
