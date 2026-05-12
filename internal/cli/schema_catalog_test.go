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
