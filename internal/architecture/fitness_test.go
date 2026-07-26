package architecture_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/pankaj28843/cdp-cli/"

type fitnessIssue struct {
	Rule   string
	Path   string
	Detail string
}

func (i fitnessIssue) String() string {
	return fmt.Sprintf("%s: %s: %s", i.Rule, i.Path, i.Detail)
}

func TestRepositoryArchitectureFitness(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	issues, err := inspectArchitecture(root)
	if err != nil {
		t.Fatalf("inspect architecture: %v", err)
	}
	for _, issue := range issues {
		t.Error(issue)
	}
}

func TestArchitectureFitnessRejectsSeededViolations(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		source   string
		wantRule string
	}{
		{
			name:     "cobra outside CLI",
			path:     "internal/browserflow/violation.go",
			source:   "package browserflow\nimport _ \"github.com/spf13/cobra\"\n",
			wantRule: "cobra-boundary",
		},
		{
			name:     "browserflow dependency points outward",
			path:     "internal/browserflow/violation.go",
			source:   "package browserflow\nimport _ \"github.com/pankaj28843/cdp-cli/internal/webagent\"\n",
			wantRule: "browserflow-dependencies",
		},
		{
			name:     "provider imports sibling provider",
			path:     "internal/webagent/claude/violation.go",
			source:   "package claude\nimport _ \"github.com/pankaj28843/cdp-cli/internal/webagent/gemini\"\n",
			wantRule: "provider-isolation",
		},
		{
			name:     "provider dials Chrome directly",
			path:     "internal/webagent/claude/violation.go",
			source:   "package claude\nimport \"context\"\nfunc bad(ctx context.Context) { _ = websocket.Dial(ctx, \"/json/version\", nil) }\n",
			wantRule: "daemon-only-browser-entry",
		},
		{
			name:     "provider bypasses action boundary",
			path:     "internal/webagent/claude/violation.go",
			source:   "package claude\nconst bad = \"Input.dispatchKeyEvent\"\n",
			wantRule: "irreversible-action-boundary",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(test.path))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir fixture: %v", err)
			}
			if err := os.WriteFile(path, []byte(test.source), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			issues, err := inspectArchitecture(root)
			if err != nil {
				t.Fatalf("inspect fixture: %v", err)
			}
			if !hasRule(issues, test.wantRule) {
				t.Fatalf("issues = %+v, want rule %q", issues, test.wantRule)
			}
		})
	}
}

func inspectArchitecture(root string) ([]fitnessIssue, error) {
	var issues []fitnessIssue
	internalRoot := filepath.Join(root, "internal")
	err := filepath.Walk(internalRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		imports, err := parseImports(path, source)
		if err != nil {
			return err
		}
		issues = append(issues, inspectFile(relative, string(source), imports)...)
		return nil
	})
	sort.Slice(issues, func(left, right int) bool {
		if issues[left].Rule != issues[right].Rule {
			return issues[left].Rule < issues[right].Rule
		}
		if issues[left].Path != issues[right].Path {
			return issues[left].Path < issues[right].Path
		}
		return issues[left].Detail < issues[right].Detail
	})
	return issues, err
}

func inspectFile(path, source string, imports []string) []fitnessIssue {
	var issues []fitnessIssue
	inCLI := strings.HasPrefix(path, "internal/cli/")
	inBrowserflow := strings.HasPrefix(path, "internal/browserflow/")
	inAdmission := strings.HasPrefix(path, "internal/admission/")
	provider := providerPackage(path)
	inPolicyPackage := inBrowserflow || inAdmission || strings.HasPrefix(path, "internal/webagent/")

	for _, importPath := range imports {
		if importPath == "github.com/spf13/cobra" && !inCLI {
			issues = append(issues, fitnessIssue{
				Rule: "cobra-boundary", Path: path,
				Detail: "Cobra belongs in internal/cli; move policy and mechanics into a dependency package",
			})
		}
		if inBrowserflow && strings.HasPrefix(importPath, modulePath+"internal/") &&
			importPath != modulePath+"internal/artifacts" &&
			importPath != modulePath+"internal/cdp" {
			issues = append(issues, fitnessIssue{
				Rule: "browserflow-dependencies", Path: path,
				Detail: fmt.Sprintf("browserflow must stay provider-neutral; forbidden import %q", importPath),
			})
		}
		if inAdmission && strings.HasPrefix(importPath, modulePath+"internal/") &&
			importPath != modulePath+"internal/artifacts" {
			issues = append(issues, fitnessIssue{
				Rule: "admission-dependencies", Path: path,
				Detail: fmt.Sprintf("admission may depend only on artifacts; forbidden import %q", importPath),
			})
		}
		if provider != "" {
			importedProvider := providerImport(importPath)
			if importedProvider != "" && importedProvider != provider {
				issues = append(issues, fitnessIssue{
					Rule: "provider-isolation", Path: path,
					Detail: fmt.Sprintf("provider %q must not import sibling provider %q", provider, importedProvider),
				})
			}
			if importPath == modulePath+"internal/cli" {
				issues = append(issues, fitnessIssue{
					Rule: "provider-isolation", Path: path,
					Detail: "provider packages must not import internal/cli",
				})
			}
		}
		if inPolicyPackage && (importPath == "nhooyr.io/websocket" ||
			importPath == modulePath+"internal/browser" ||
			importPath == modulePath+"internal/daemon") {
			issues = append(issues, fitnessIssue{
				Rule: "daemon-only-browser-entry", Path: path,
				Detail: fmt.Sprintf("policy package must receive daemon-backed CDP capabilities; forbidden import %q", importPath),
			})
		}
	}
	if inPolicyPackage {
		for _, token := range []string{"websocket.Dial(", "cdp.Dial(", "webSocketDebuggerUrl", "/json/version"} {
			if strings.Contains(source, token) {
				issues = append(issues, fitnessIssue{
					Rule: "daemon-only-browser-entry", Path: path,
					Detail: fmt.Sprintf("direct Chrome discovery/dial token %q is forbidden", token),
				})
			}
		}
	}
	if provider != "" {
		for _, method := range []string{
			"Input.dispatchKeyEvent",
			"Input.dispatchMouseEvent",
			"Input.insertText",
			"Target.createTarget",
			"Target.closeTarget",
		} {
			if strings.Contains(source, method) {
				issues = append(issues, fitnessIssue{
					Rule: "irreversible-action-boundary", Path: path,
					Detail: fmt.Sprintf("provider must use browserflow's instrumented action/target boundary, not %q", method),
				})
			}
		}
	}
	return issues
}

func parseImports(path string, source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("unquote import in %s: %w", path, err)
		}
		imports = append(imports, value)
	}
	return imports, nil
}

func providerPackage(path string) string {
	const prefix = "internal/webagent/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(path, prefix)
	parts := strings.Split(remainder, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

func providerImport(importPath string) string {
	const prefix = modulePath + "internal/webagent/"
	if !strings.HasPrefix(importPath, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(importPath, prefix)
	return strings.Split(remainder, "/")[0]
}

func hasRule(issues []fitnessIssue, rule string) bool {
	for _, issue := range issues {
		if issue.Rule == rule {
			return true
		}
	}
	return false
}
