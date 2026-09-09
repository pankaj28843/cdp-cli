package wrapper

import (
	"reflect"
	"testing"
)

func TestTranslateAlexPreservesSpecialistIntent(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "auth status is local doctor",
			args: []string{"auth", "status", "--json"},
			want: fields("workflow agent alex doctor --json"),
		},
		{
			name: "auth refresh strips legacy headed selector",
			args: []string{
				"auth", "refresh",
				"--browser-mode", "headed",
				"--dry-run", "--json",
			},
			want: fields(
				"workflow agent alex auth refresh --dry-run --json",
			),
		},
		{
			name: "catalog refresh",
			args: []string{"catalog", "refresh", "--json"},
			want: fields("workflow agent alex catalog refresh --json"),
		},
		{
			name: "capabilities status",
			args: []string{"capabilities", "status", "--json"},
			want: fields("workflow agent alex capabilities --json"),
		},
		{
			name: "chapter list",
			args: []string{
				"chapters", "list",
				"--course", "system-design-interview",
				"--json",
			},
			want: fields(
				"workflow agent alex chapters list --course system-design-interview --json",
			),
		},
		{
			name: "content fetch",
			args: []string{
				"content", "fetch",
				"--all-courses", "--limit", "2", "--json",
			},
			want: fields(
				"workflow agent alex content fetch --all-courses --limit 2 --json",
			),
		},
		{
			name: "ask context and timeout",
			args: []string{
				"ask", "compare", "algorithms",
				"--course", "system-design-interview",
				"--chapter-id", "design-a-rate-limiter",
				"--timeout", "45",
				"--json",
			},
			want: []string{
				"--timeout", "45s",
				"workflow", "agent", "alex", "ask",
				"--course", "system-design-interview",
				"--chapter-id", "design-a-rate-limiter",
				"--json",
				"compare algorithms",
			},
		},
		{
			name: "piped ask",
			args: []string{"ask", "--stdin", "--raw", "--json"},
			want: fields(
				"workflow agent alex ask --stdin --raw --json",
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TranslateAlex(test.args)
			if err != nil {
				t.Fatalf("TranslateAlex: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf(
					"TranslateAlex(%q) = %q, want %q",
					test.args,
					got,
					test.want,
				)
			}
		})
	}
}

func TestTranslateAlexRejectsUnsafeCompatibilityDowngrades(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "refresh", "--smoke-e2e"},
		{"auth", "smoke-e2e"},
		{"capabilities", "refresh"},
		{"catalog", "refresh", "--browser-mode", "headless"},
		{"ask", "--prompt", "one", "two"},
	} {
		if _, err := TranslateAlex(args); err == nil {
			t.Fatalf("TranslateAlex(%q) unexpectedly succeeded", args)
		}
	}
}
