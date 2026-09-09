package wrapper

import (
	"reflect"
	"testing"
)

func TestTranslateClaudePreservesLegacyIntent(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "nested help",
			args: []string{"conversations", "--help"},
			want: []string{
				"workflow", "agent", "claude", "conversations", "--help",
			},
		},
		{
			name: "doctor",
			args: []string{"doctor", "--json"},
			want: []string{"workflow", "agent", "claude", "doctor", "--json"},
		},
		{
			name: "auth status becomes browser-free doctor",
			args: []string{"auth", "status", "--json"},
			want: []string{"workflow", "agent", "claude", "doctor", "--json"},
		},
		{
			name: "auth refresh drops obsolete wait",
			args: []string{"auth", "refresh", "--wait-seconds", "10", "--json"},
			want: []string{"workflow", "agent", "claude", "auth", "refresh", "--json"},
		},
		{
			name: "capability refresh becomes executable contract",
			args: []string{"capabilities", "refresh", "--dry-run", "--json"},
			want: []string{"workflow", "agent", "claude", "capabilities", "--json"},
		},
		{
			name: "list seconds timeout becomes Go duration",
			args: []string{"conversations", "list", "--limit", "20", "--timeout", "30", "--json"},
			want: []string{"--timeout", "30s", "workflow", "agent", "claude", "conversations", "list", "--limit", "20", "--json"},
		},
		{
			name: "ask joins legacy prompt words",
			args: []string{"ask", "Review", "this", "boundary", "--json", "--timeout", "180"},
			want: []string{"--timeout", "3m0s", "workflow", "agent", "claude", "ask", "--json", "Review this boundary"},
		},
		{
			name: "ask prompt flag",
			args: []string{"ask", "--prompt", "Review exactly.", "--json"},
			want: []string{"workflow", "agent", "claude", "ask", "--json", "Review exactly."},
		},
		{
			name: "ask stdin",
			args: []string{"ask", "--stdin", "--json", "--timeout=180"},
			want: []string{"--timeout", "3m0s", "workflow", "agent", "claude", "ask", "--stdin", "--json"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TranslateClaude(test.args)
			if err != nil {
				t.Fatalf("TranslateClaude: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("TranslateClaude(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestTranslateClaudeRejectsInvalidTimeout(t *testing.T) {
	if _, err := TranslateClaude([]string{"ask", "--stdin", "--timeout", "0"}); err == nil {
		t.Fatal("TranslateClaude accepted a zero timeout")
	}
}

func TestTranslateGeminiPreservesLegacyIntent(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "doctor",
			args: []string{"doctor", "--json"},
			want: []string{"workflow", "agent", "gemini", "doctor", "--json"},
		},
		{
			name: "auth status becomes browser-free doctor",
			args: []string{"auth", "status", "--json"},
			want: []string{"workflow", "agent", "gemini", "doctor", "--json"},
		},
		{
			name: "auth refresh drops obsolete wait",
			args: []string{"auth", "refresh", "--wait-seconds=10", "--json"},
			want: []string{"workflow", "agent", "gemini", "auth", "refresh", "--json"},
		},
		{
			name: "capability status is browser free",
			args: []string{"capabilities", "status", "--json"},
			want: []string{"workflow", "agent", "gemini", "capabilities", "--json"},
		},
		{
			name: "capability refresh retains runtime discovery",
			args: []string{"capabilities", "refresh", "--dry-run", "--json"},
			want: []string{"workflow", "agent", "gemini", "capabilities", "refresh", "--json"},
		},
		{
			name: "list",
			args: []string{"conversations", "list", "--limit", "20", "--json"},
			want: []string{"workflow", "agent", "gemini", "conversations", "list", "--limit", "20", "--json"},
		},
		{
			name: "detail",
			args: []string{"conversations", "detail", "conversation-1", "--json"},
			want: []string{"workflow", "agent", "gemini", "conversations", "detail", "conversation-1", "--json"},
		},
		{
			name: "await",
			args: []string{"conversations", "await", "conversation-1", "--json", "--timeout=90"},
			want: []string{"--timeout", "1m30s", "workflow", "agent", "gemini", "conversations", "await", "conversation-1", "--json"},
		},
		{
			name: "delete",
			args: []string{"conversations", "delete", "conversation-1", "--json"},
			want: []string{"workflow", "agent", "gemini", "conversations", "delete", "conversation-1", "--json"},
		},
		{
			name: "ask prompt flag",
			args: []string{"ask", "--prompt=Review exactly.", "--json", "--timeout", "180"},
			want: []string{"--timeout", "3m0s", "workflow", "agent", "gemini", "ask", "--json", "Review exactly."},
		},
		{
			name: "ask stdin",
			args: []string{"ask", "--stdin", "--json"},
			want: []string{"workflow", "agent", "gemini", "ask", "--stdin", "--json"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TranslateGemini(test.args)
			if err != nil {
				t.Fatalf("TranslateGemini: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("TranslateGemini(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestTranslateGeminiRejectsInvalidTimeout(t *testing.T) {
	if _, err := TranslateGemini([]string{"ask", "--stdin", "--timeout", "nan"}); err == nil {
		t.Fatal("TranslateGemini accepted a non-finite timeout")
	}
}

func TestTranslateGrokPreservesLegacyIntent(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "doctor",
			args: []string{"doctor", "--json"},
			want: []string{"workflow", "agent", "grok", "doctor", "--json"},
		},
		{
			name: "auth status becomes browser-free doctor",
			args: []string{"auth", "status", "--json"},
			want: []string{"workflow", "agent", "grok", "doctor", "--json"},
		},
		{
			name: "capability refresh retains runtime discovery",
			args: []string{"capabilities", "refresh", "--dry-run", "--json"},
			want: []string{"workflow", "agent", "grok", "capabilities", "refresh", "--json"},
		},
		{
			name: "stored detail",
			args: []string{"conversations", "detail", "conversation-1", "--json"},
			want: []string{"workflow", "agent", "grok", "conversations", "detail", "conversation-1", "--json"},
		},
		{
			name: "ask stdin with timeout",
			args: []string{"ask", "--stdin", "--json", "--timeout", "180"},
			want: []string{"--timeout", "3m0s", "workflow", "agent", "grok", "ask", "--stdin", "--json"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TranslateGrok(test.args)
			if err != nil {
				t.Fatalf("TranslateGrok: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf(
					"TranslateGrok(%q) = %q, want %q",
					test.args,
					got,
					test.want,
				)
			}
		})
	}
}

func TestTranslatePerplexityPreservesLegacyIntent(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "doctor",
			args: []string{"doctor", "--json"},
			want: []string{"workflow", "agent", "perplexity", "doctor", "--json"},
		},
		{
			name: "capability refresh retains runtime discovery",
			args: []string{"capabilities", "refresh", "--dry-run", "--json"},
			want: []string{"workflow", "agent", "perplexity", "capabilities", "refresh", "--json"},
		},
		{
			name: "candidate detail with timeout",
			args: []string{"conversations", "detail", "conversation-1", "--timeout", "30", "--json"},
			want: []string{"--timeout", "30s", "workflow", "agent", "perplexity", "conversations", "detail", "conversation-1", "--json"},
		},
		{
			name: "ask stdin",
			args: []string{"ask", "--stdin", "--json", "--timeout=180"},
			want: []string{"--timeout", "3m0s", "workflow", "agent", "perplexity", "ask", "--stdin", "--json"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TranslatePerplexity(test.args)
			if err != nil {
				t.Fatalf("TranslatePerplexity: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf(
					"TranslatePerplexity(%q) = %q, want %q",
					test.args,
					got,
					test.want,
				)
			}
		})
	}
}

func TestProviderWrappersRejectCalibration(t *testing.T) {
	translators := []func([]string) ([]string, error){
		TranslateAlex,
		TranslateChatGPT,
		TranslateClaude,
		TranslateGemini,
		TranslateGrok,
		TranslatePerplexity,
		TranslateTripadvisor,
	}
	for _, translate := range translators {
		for _, command := range []string{"calibration", "calibrate"} {
			if _, err := translate([]string{command, "run", "--json"}); err == nil {
				t.Fatalf("%s command unexpectedly succeeded", command)
			}
		}
	}
}
