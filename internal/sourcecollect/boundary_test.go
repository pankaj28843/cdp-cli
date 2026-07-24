package sourcecollect

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCollectorsDoNotDependOnCLIOrBrowserRuntime(t *testing.T) {
	for _, platform := range []string{"arxiv", "hackernews", "linkedin", "reddit", "x"} {
		entries, err := os.ReadDir(platform)
		if err != nil {
			t.Fatalf("read %s: %v", platform, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(platform, entry.Name())
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, spec := range file.Imports {
				assertCollectorImportAllowed(t, path, spec)
			}
		}
	}
}

func assertCollectorImportAllowed(t *testing.T, path string, spec *ast.ImportSpec) {
	t.Helper()
	importPath, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		t.Fatalf("unquote import in %s: %v", path, err)
	}
	allowed := map[string]bool{
		"encoding/json": true,
		"fmt":           true,
		"net/url":       true,
		"regexp":        true,
		"strconv":       true,
		"strings":       true,
	}
	if !allowed[importPath] {
		t.Errorf("%s imports %q; collector policy is restricted to pure decoding and URL/identity dependencies", path, importPath)
	}
}
