package wrapper

import (
	"reflect"
	"testing"
)

func TestTranslateChatGPTPreservesLegacyIntent(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "conversation help",
			args: []string{"conversations", "--help"},
			want: []string{
				"workflow", "agent", "chatgpt", "conversations", "--help",
			},
		},
		{
			name: "doctor",
			args: []string{"doctor", "--json"},
			want: []string{
				"workflow", "agent", "chatgpt", "doctor", "--json",
			},
		},
		{
			name: "auth status becomes browser-free doctor",
			args: []string{"auth", "status", "--json"},
			want: []string{
				"workflow", "agent", "chatgpt", "doctor", "--json",
			},
		},
		{
			name: "auth refresh drops only obsolete wait",
			args: []string{
				"auth", "refresh", "--wait-seconds", "10", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt",
				"auth", "refresh", "--json",
			},
		},
		{
			name: "capability refresh retains runtime discovery",
			args: []string{
				"capabilities", "refresh", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt",
				"capabilities", "refresh", "--json",
			},
		},
		{
			name: "file ask preserves explicit highest selection",
			args: []string{
				"ask", "--stdin", "--file", "./probe.txt",
				"--thinking", "highest",
				"--minimum-thinking", "extra-high",
				"--model", "highest", "--json",
				"--timeout", "180",
			},
			want: []string{
				"--timeout", "3m0s",
				"workflow", "agent", "chatgpt", "ask",
				"--file", "./probe.txt",
				"--thinking", "highest",
				"--minimum-thinking", "extra-high",
				"--model", "highest",
				"--stdin", "--json",
			},
		},
		{
			name: "headed browser mode is implicit",
			args: []string{
				"ask", "--stdin",
				"--browser-mode", "headed", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt", "ask",
				"--stdin", "--json",
			},
		},
		{
			name: "selection equals syntax passes through",
			args: []string{
				"ask", "--stdin", "--reasoning=pro",
				"--model=GPT-5.6 Sol", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt", "ask",
				"--reasoning=pro", "--model=GPT-5.6 Sol",
				"--stdin", "--json",
			},
		},
		{
			name: "verified image tool passes through",
			args: []string{
				"ask", "--stdin", "--tool=create-image",
				"--thinking", "pro", "--model", "GPT-5.6 Sol", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt", "ask",
				"--tool=create-image", "--thinking", "pro",
				"--model", "GPT-5.6 Sol", "--stdin", "--json",
			},
		},
		{
			name: "verified image tool with separate value passes through",
			args: []string{
				"ask", "--stdin", "--tool", "create-image",
				"--thinking", "pro", "--model", "GPT-5.6 Sol", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt", "ask",
				"--tool", "create-image", "--thinking", "pro",
				"--model", "GPT-5.6 Sol", "--stdin", "--json",
			},
		},
		{
			name: "verified provider tool values pass through",
			args: []string{
				"ask", "--stdin", "--tool", "visualize",
				"--thinking", "pro", "--model", "GPT-5.6 Sol", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt", "ask",
				"--tool", "visualize", "--thinking", "pro",
				"--model", "GPT-5.6 Sol", "--stdin", "--json",
			},
		},
		{
			name: "unflagged ask injects no entitlement default",
			args: []string{"ask", "--stdin", "--json"},
			want: []string{
				"workflow", "agent", "chatgpt", "ask",
				"--stdin", "--json",
			},
		},
		{
			name: "ask joins prompt words",
			args: []string{
				"ask", "Review", "this", "boundary", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt", "ask",
				"--json", "Review this boundary",
			},
		},
		{
			name: "visible continuation",
			args: []string{
				"conversations", "continue", "conversation-1",
				"--stdin", "--json", "--timeout", "90",
			},
			want: []string{
				"--timeout", "1m30s",
				"workflow", "agent", "chatgpt", "conversations",
				"continue", "conversation-1", "--stdin", "--json",
			},
		},
		{
			name: "await timeout also bounds exact provider wait",
			args: []string{
				"conversations", "await", "conversation-1",
				"--json", "--timeout", "40m",
			},
			want: []string{
				"--timeout", "40m30s",
				"workflow", "agent", "chatgpt", "conversations",
				"await", "conversation-1", "--json",
				"--wait", "40m0s",
			},
		},
		{
			name: "explicit await wait remains authoritative",
			args: []string{
				"conversations", "await", "conversation-1",
				"--wait", "10m", "--timeout", "40m",
			},
			want: []string{
				"--timeout", "40m0s",
				"workflow", "agent", "chatgpt", "conversations",
				"await", "conversation-1", "--wait", "10m",
			},
		},
		{
			name: "artifact download",
			args: []string{
				"conversations", "download-artifact",
				"conversation-1", "--filename", "report.csv",
				"--output", "./report.csv", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt", "conversations",
				"download-artifact", "conversation-1",
				"--filename", "report.csv",
				"--output", "./report.csv", "--json",
			},
		},
		{
			name: "all attachment export is exact passthrough",
			args: []string{
				"conversations", "download-attachments",
				"conversation-1", "--output-dir", "./designs",
				"--json", "--timeout", "5m",
			},
			want: []string{
				"--timeout", "5m0s",
				"workflow", "agent", "chatgpt", "conversations",
				"download-attachments", "conversation-1",
				"--output-dir", "./designs", "--json",
			},
		},
		{
			name: "research remains explicit",
			args: []string{
				"research", "Research", "this", "--browser-export",
				"--json", "--timeout=300",
			},
			want: []string{
				"--timeout", "5m0s",
				"workflow", "agent", "chatgpt", "research",
				"--browser-export", "--json", "Research this",
			},
		},
		{
			name: "research export remains explicit",
			args: []string{
				"conversations", "export-research",
				"conversation-1", "--json",
			},
			want: []string{
				"workflow", "agent", "chatgpt", "conversations",
				"export-research", "conversation-1", "--json",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TranslateChatGPT(test.args)
			if err != nil {
				t.Fatalf("TranslateChatGPT: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf(
					"TranslateChatGPT(%q) = %q, want %q",
					test.args,
					got,
					test.want,
				)
			}
		})
	}
}

func TestTranslateChatGPTRejectsMissingSelectionValues(t *testing.T) {
	for _, flag := range []string{
		"--thinking",
		"--reasoning",
		"--intelligence",
		"--minimum-thinking",
		"--model",
		"--tool",
	} {
		if _, err := TranslateChatGPT(
			[]string{"ask", "--stdin", flag},
		); err == nil {
			t.Fatalf("TranslateChatGPT accepted missing %s value", flag)
		}
	}
}

func TestTranslateProviderRejectsHeadlessBrowserMode(t *testing.T) {
	translators := []struct {
		name      string
		translate func([]string) ([]string, error)
	}{
		{"claude", TranslateClaude},
		{"chatgpt", TranslateChatGPT},
		{"gemini", TranslateGemini},
		{"grok", TranslateGrok},
		{"perplexity", TranslatePerplexity},
		{"tripadvisor", TranslateTripadvisor},
	}
	for _, translator := range translators {
		for _, mode := range []string{
			"--browser-mode headless",
			"--browser-mode=headless",
			"--browserMode=headless",
		} {
			t.Run(translator.name+"/"+mode, func(t *testing.T) {
				args := append([]string{"ask", "--stdin"}, fields(mode)...)
				if _, err := translator.translate(args); err == nil {
					t.Fatalf("accepted headless browser mode: %q", args)
				}
			})
		}
	}
}

func TestTranslateChatGPTRejectsDryRunBeforeExecution(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "refresh", "--dry-run", "--json"},
		{"capabilities", "refresh", "--dry-run", "--json"},
	} {
		if _, err := TranslateChatGPT(args); err == nil {
			t.Fatalf(
				"TranslateChatGPT(%q) accepted a state-changing dry-run downgrade",
				args,
			)
		}
	}
}
