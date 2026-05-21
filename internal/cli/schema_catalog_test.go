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
		"scheduled-tasks",
		"headless-security",
		"browser-mode",
		"browser-profile-status",
		"browser-profile-seed",
		"managed-browser",
		"connection-resolve",
		"daemon-status",
		"daemon-keepalive",
		"daemon-health",
		"daemon-logs",
		"pages",
		"page-cleanup",
		"protocol-examples",
		"storage",
		"workflow-rendered-extract",
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
		"browser-mode":           {"browser_mode", "browser_mode_source", "next_commands"},
		"browser-profile-status": {"browser_mode", "state", "managed_browser", "profile_perm", "metadata_perm"},
		"browser-profile-seed":   {"browser_mode", "state", "seed_strategy", "managed_browser"},
		"managed-browser":        {"browser_mode", "user_data_dir", "profile_seed_strategy", "debugging_port"},
		"connection-resolve":     {"browser_mode", "browser_mode_source", "connection"},
		"daemon-status":          {"daemon"},
		"daemon-keepalive":       {"browser_mode", "connection", "mode", "lock"},
		"cron":                   {"next_commands"},
		"scheduled-tasks":        {"details", "next_commands"},
		"headless-security":      {"browser_mode", "details", "next_commands"},
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

func catalogSchemaHasField(info schemaInfo, name string) bool {
	for _, field := range info.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}
