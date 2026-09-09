package wrapper

import (
	"reflect"
	"testing"
)

func TestTranslateTripadvisorPreservesRenderedIntent(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "nested conversation help",
			args: []string{"conversations", "--help"},
			want: fields(
				"workflow agent tripadvisor conversations --help",
			),
		},
		{
			name: "nested auth help",
			args: []string{"auth", "--help"},
			want: fields(
				"workflow agent tripadvisor auth --help",
			),
		},
		{
			name: "nested capability help",
			args: []string{"capabilities", "--help"},
			want: fields(
				"workflow agent tripadvisor capabilities --help",
			),
		},
		{
			name: "doctor",
			args: []string{"doctor", "--json"},
			want: fields(
				"workflow agent tripadvisor doctor --json",
			),
		},
		{
			name: "auth status is local doctor",
			args: []string{"auth", "status", "--json"},
			want: fields(
				"workflow agent tripadvisor doctor --json",
			),
		},
		{
			name: "auth refresh drops obsolete wait",
			args: []string{
				"auth", "refresh",
				"--wait-seconds", "10",
				"--json",
			},
			want: fields(
				"workflow agent tripadvisor auth refresh --json",
			),
		},
		{
			name: "capability status advertises operations",
			args: []string{"capabilities", "status", "--json"},
			want: fields(
				"workflow agent tripadvisor capabilities --json",
			),
		},
		{
			name: "conservative list",
			args: []string{
				"conversations", "list",
				"--limit", "10", "--json",
			},
			want: fields(
				"workflow agent tripadvisor conversations list --limit 10 --json",
			),
		},
		{
			name: "detail timeout",
			args: []string{
				"conversations", "detail", "conversation-id",
				"--timeout", "30", "--json",
			},
			want: fields(
				"--timeout 30s workflow agent tripadvisor conversations detail conversation-id --json",
			),
		},
		{
			name: "await",
			args: []string{
				"conversations", "await", "conversation-id",
				"--timeout=180", "--json",
			},
			want: fields(
				"--timeout 3m0s workflow agent tripadvisor conversations await conversation-id --json",
			),
		},
		{
			name: "stdin ask",
			args: []string{
				"ask", "--stdin", "--json", "--timeout", "180",
			},
			want: fields(
				"--timeout 3m0s workflow agent tripadvisor ask --stdin --json",
			),
		},
		{
			name: "joined prompt",
			args: []string{
				"ask", "Critique", "this", "itinerary", "--json",
			},
			want: []string{
				"workflow", "agent", "tripadvisor", "ask",
				"--json", "Critique this itinerary",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TranslateTripadvisor(test.args)
			if err != nil {
				t.Fatalf("TranslateTripadvisor: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf(
					"TranslateTripadvisor(%q) = %q, want %q",
					test.args,
					got,
					test.want,
				)
			}
		})
	}
}

func TestTranslateTripadvisorRejectsUnprovenMutations(t *testing.T) {
	for _, args := range [][]string{
		{"capabilities", "refresh", "--json"},
		{"conversations", "delete", "conversation-id", "--json"},
	} {
		if _, err := TranslateTripadvisor(args); err == nil {
			t.Fatalf(
				"TranslateTripadvisor(%q) unexpectedly succeeded",
				args,
			)
		}
	}
}
