package cli

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/config"
)

func TestReadWebResearchQueries(t *testing.T) {
	const exactDate = "cdr:1,cd_min:07/01/2026,cd_max:07/01/2026"
	path := filepath.Join(t.TempDir(), "queries.txt")
	contents := "\n  # comment\nplain query\n exact query \t " + exactDate + " \nempty filter\t\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write query file: %v", err)
	}

	got, err := readWebResearchQueries(path)
	if err != nil {
		t.Fatalf("readWebResearchQueries() error = %v", err)
	}
	want := []webResearchQuery{
		{Text: "plain query"},
		{Text: "exact query", TimeFilter: exactDate},
		{Text: "empty filter"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readWebResearchQueries() = %#v, want %#v", got, want)
	}
}

func TestReadWebResearchQueriesRejectsMalformedRows(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		wantLine string
		wantText string
	}{
		{name: "missing query", contents: "valid\n\tcdr:1,cd_min:07/01/2026,cd_max:07/01/2026\n", wantLine: "line 2", wantText: "query column must not be empty"},
		{name: "third column", contents: "query\tqdr:m\textra\n", wantLine: "line 1", wantText: "found more than two columns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "queries.txt")
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatalf("write query file: %v", err)
			}
			_, err := readWebResearchQueries(path)
			if err == nil {
				t.Fatal("readWebResearchQueries() error = nil")
			}
			var commandErr *CommandError
			if !errors.As(err, &commandErr) {
				t.Fatalf("error type = %T, want *CommandError", err)
			}
			if commandErr.ExitCode != ExitUsage || !strings.Contains(commandErr.Message, tt.wantLine) || !strings.Contains(commandErr.Message, tt.wantText) {
				t.Fatalf("command error = %+v", commandErr)
			}
			if len(commandErr.RemediationCommands) != 1 || !strings.Contains(commandErr.RemediationCommands[0], "cdr:1,cd_min:07/01/2026,cd_max:07/01/2026") {
				t.Fatalf("remediation commands = %#v", commandErr.RemediationCommands)
			}
		})
	}
}

func TestWebResearchSearchURLByEngine(t *testing.T) {
	const exactDate = "cdr:1,cd_min:07/01/2026,cd_max:07/01/2026"
	tests := []struct {
		engine        string
		wantHost      string
		wantPageKey   string
		wantPageValue string
	}{
		{engine: "google", wantHost: "www.google.com", wantPageKey: "start", wantPageValue: "10"},
		{engine: "bing", wantHost: "www.bing.com", wantPageKey: "first", wantPageValue: "11"},
		{engine: "brave", wantHost: "search.brave.com", wantPageKey: "offset", wantPageValue: "10"},
		{engine: "duckduckgo", wantHost: "duckduckgo.com", wantPageKey: "s", wantPageValue: "10"},
		{engine: "kagi", wantHost: "kagi.com", wantPageKey: "page", wantPageValue: "2"},
	}
	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			rawURL := webResearchSearchURL(tt.engine, "agentic engineering", exactDate, 2)
			parsed, err := url.Parse(rawURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if parsed.Hostname() != tt.wantHost {
				t.Fatalf("host = %q, want %q", parsed.Hostname(), tt.wantHost)
			}
			if got := parsed.Query().Get("q"); got != "agentic engineering" {
				t.Fatalf("q = %q", got)
			}
			if got := parsed.Query().Get(tt.wantPageKey); got != tt.wantPageValue {
				t.Fatalf("%s = %q, want %q in %s", tt.wantPageKey, got, tt.wantPageValue, rawURL)
			}
			if tt.engine == "google" && parsed.Query().Get("tbs") != exactDate {
				t.Fatalf("google tbs was not preserved exactly: %s", rawURL)
			}
			if tt.engine == "google" && (parsed.Query().Get("udm") != "" || parsed.Query().Get("aep") != "") {
				t.Fatalf("default Google URL = %s, want ordinary SERP navigation", rawURL)
			}
			if tt.engine != "google" && parsed.Query().Get("tbs") != "" {
				t.Fatalf("%s unexpectedly received Google tbs: %s", tt.engine, rawURL)
			}
		})
	}
}

func TestWebResearchSupportedSERPSet(t *testing.T) {
	for _, engine := range []string{"google", "bing", "brave", "duckduckgo", "kagi"} {
		if !isWebResearchSupportedSERP(engine) {
			t.Fatalf("engine %q should be supported", engine)
		}
	}
	if isWebResearchSupportedSERP("yahoo") {
		t.Fatalf("unexpected supported engine")
	}
}

func TestParseWebResearchGoogleAIPolicy(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: webResearchGoogleAIDefault},
		{input: " AUTO ", want: "auto"},
		{input: "mode", want: "mode"},
		{input: "off", want: "off"},
	}
	for _, tt := range tests {
		got, err := parseWebResearchGoogleAIPolicy(tt.input)
		if err != nil {
			t.Fatalf("parseWebResearchGoogleAIPolicy(%q) error = %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("parseWebResearchGoogleAIPolicy(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
	if _, err := parseWebResearchGoogleAIPolicy("summarize"); err == nil || !strings.Contains(err.Error(), "unsupported Google AI response policy") {
		t.Fatalf("invalid Google AI policy error = %v, want classified policy error", err)
	}
}

func TestWebResearchSearchURLWithGoogleAI(t *testing.T) {
	autoURL := webResearchSearchURLWithGoogleAI("google", "agentic engineering", "", 1, "auto")
	autoParsed, err := url.Parse(autoURL)
	if err != nil {
		t.Fatalf("parse auto URL: %v", err)
	}
	if autoParsed.Query().Get("udm") != "" || autoParsed.Query().Get("aep") != "" {
		t.Fatalf("auto Google URL = %s, want standard SERP without AI Mode selectors", autoURL)
	}

	modeURL := webResearchSearchURLWithGoogleAI("google", "agentic engineering", "", 1, "mode")
	modeParsed, err := url.Parse(modeURL)
	if err != nil {
		t.Fatalf("parse AI Mode URL: %v", err)
	}
	if modeParsed.Query().Get("udm") != "50" || modeParsed.Query().Get("aep") != "1" {
		t.Fatalf("AI Mode URL = %s, want udm=50 and aep=1", modeURL)
	}

	otherURL := webResearchSearchURLWithGoogleAI("bing", "agentic engineering", "", 1, "mode")
	otherParsed, err := url.Parse(otherURL)
	if err != nil {
		t.Fatalf("parse non-Google URL: %v", err)
	}
	if otherParsed.Query().Get("udm") != "" || otherParsed.Query().Get("aep") != "" {
		t.Fatalf("non-Google URL = %s, Google AI policy must be ignored", otherURL)
	}
}

func TestResolveWebResearchGoogleAIPolicy(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		explicit  bool
		cfg       config.Config
		want      webResearchGoogleAIPolicyResolution
	}{
		{
			name:      "safe inline default",
			requested: webResearchGoogleAIDefault,
			want: webResearchGoogleAIPolicyResolution{
				Policy: webResearchGoogleAIAuto, Source: webResearchGoogleAIPolicySourceDefault,
			},
		},
		{
			name:      "configured exclusive mode",
			requested: webResearchGoogleAIDefault,
			cfg: config.Config{Agents: config.AgentConfig{Google: config.GoogleAgentConfig{
				ExclusiveAIMode: true,
			}}},
			want: webResearchGoogleAIPolicyResolution{
				Policy: webResearchGoogleAIMode, Source: webResearchGoogleAIPolicySourceConfig, Exclusive: true,
			},
		},
		{
			name:      "explicit auto overrides exclusive config",
			requested: webResearchGoogleAIAuto,
			explicit:  true,
			cfg: config.Config{Agents: config.AgentConfig{Google: config.GoogleAgentConfig{
				ExclusiveAIMode: true,
			}}},
			want: webResearchGoogleAIPolicyResolution{
				Policy: webResearchGoogleAIAuto, Source: webResearchGoogleAIPolicySourceFlag,
			},
		},
		{
			name:      "explicit off overrides exclusive config",
			requested: webResearchGoogleAIOff,
			explicit:  true,
			cfg: config.Config{Agents: config.AgentConfig{Google: config.GoogleAgentConfig{
				ExclusiveAIMode: true,
			}}},
			want: webResearchGoogleAIPolicyResolution{
				Policy: webResearchGoogleAIOff, Source: webResearchGoogleAIPolicySourceFlag,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWebResearchGoogleAIPolicy(tt.requested, tt.explicit, tt.cfg)
			if err != nil {
				t.Fatalf("resolveWebResearchGoogleAIPolicy() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveWebResearchGoogleAIPolicy() = %#v, want %#v", got, tt.want)
			}
		})
	}
	if _, err := resolveWebResearchGoogleAIPolicy("unsupported", true, config.Config{}); err == nil {
		t.Fatal("explicit unsupported policy returned nil error")
	}
}
