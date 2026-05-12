package cli

import (
	"strings"
	"testing"
)

func TestCommandExamplesHighRiskPaths(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{path: "cdp version", want: []string{"version --json"}},
		{path: "cdp daemon start", want: []string{"--auto-connect"}},
		{path: "cdp daemon restart", want: []string{"--autoConnect"}},
		{path: "cdp daemon keepalive", want: []string{"--display"}},
		{path: "cdp daemon logs", want: []string{"--tail"}},
		{path: "cdp pages", want: []string{"--title-contains"}},
		{path: "cdp page cleanup", want: []string{"--close"}},
		{path: "cdp eval", want: []string{"--title-contains"}},
		{path: "cdp html", want: []string{"--diagnose-empty"}},
		{path: "cdp screenshot", want: []string{"--element"}},
		{path: "cdp screenshot render", want: []string{"--serve"}},
		{path: "cdp storage cookies set", want: []string{"--name"}},
		{path: "cdp storage indexeddb put", want: []string{"@tmp/value.json"}},
		{path: "cdp storage cache put", want: []string{"--content-type"}},
		{path: "cdp storage service-workers unregister", want: []string{"--scope"}},
		{path: "cdp protocol exec", want: []string{"--target"}},
		{path: "cdp protocol examples", want: []string{"Page.captureScreenshot"}},
		{path: "cdp workflow page-load", want: []string{"--reload"}},
		{path: "cdp workflow rendered-extract", want: []string{"--serp google"}},
		{path: "cdp workflow web-research serp", want: []string{"--result-pages 3"}},
		{path: "cdp workflow web-research extract", want: []string{"--parallel 4", "--parallel 10"}},
		{path: "cdp workflow feeds", want: []string{"--wait-load"}},
		{path: "cdp workflow visible-posts", want: []string{"visible-posts"}},
		{path: "cdp workflow hacker-news", want: []string{"hacker-news"}},
		{path: "cdp workflow verify", want: []string{"workflow verify"}},
		{path: "cdp workflow debug-bundle", want: []string{"debug-bundle"}},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			examples := commandExamples(tt.path)
			if len(examples) == 0 {
				t.Fatalf("commandExamples(%q) returned no examples", tt.path)
			}
			for _, want := range tt.want {
				if !examplesContain(examples, want) {
					t.Fatalf("commandExamples(%q) = %#v, want an example containing %q", tt.path, examples, want)
				}
			}
		})
	}
}

func examplesContain(examples []string, needle string) bool {
	for _, example := range examples {
		if strings.Contains(example, needle) {
			return true
		}
	}
	return false
}
