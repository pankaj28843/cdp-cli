package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestStopStateClassifyBuiltIns(t *testing.T) {
	tests := []struct {
		name          string
		input         stopStateInput
		wantState     string
		wantClass     string
		wantHuman     bool
		wantStop      bool
		wantNoMatched bool
	}{
		{
			name:      "login required",
			input:     stopStateInput{Text: "Please sign in to continue with this account."},
			wantState: "login_required",
			wantClass: "auth",
			wantHuman: true,
			wantStop:  true,
		},
		{
			name:      "access denied",
			input:     stopStateInput{Title: "Access Denied"},
			wantState: "access_denied",
			wantClass: "access_denied",
			wantStop:  true,
		},
		{
			name:      "unusual traffic",
			input:     stopStateInput{Text: "Our systems have detected unusual traffic from your computer network."},
			wantState: "unusual_traffic",
			wantClass: "bot_check",
			wantHuman: true,
			wantStop:  true,
		},
		{
			name:      "permission required",
			input:     stopStateInput{Text: "Browser permission required before continuing."},
			wantState: "permission_required",
			wantClass: "permission",
			wantHuman: true,
			wantStop:  true,
		},
		{
			name:      "human required",
			input:     stopStateInput{Text: "Manual action required before this workflow can continue."},
			wantState: "human_required",
			wantClass: "human_required",
			wantHuman: true,
			wantStop:  true,
		},
		{
			name:      "payment boundary",
			input:     stopStateInput{Text: "Review fare, then continue to payment."},
			wantState: "payment_or_booking_boundary",
			wantClass: "payment",
			wantHuman: true,
			wantStop:  true,
		},
		{
			name:      "personal data prompt",
			input:     stopStateInput{Text: "Enter passenger details and passport number."},
			wantState: "personal_data_required",
			wantClass: "personal_data",
			wantHuman: true,
			wantStop:  true,
		},
		{
			name:          "ordinary travel text",
			input:         stopStateInput{Text: "Flight rows are visible with prices, stops, baggage, and duration."},
			wantNoMatched: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := classifyStopState(tt.input, nil)
			if !got.OK || !got.ClassificationOK {
				t.Fatalf("classifyStopState(%q) ok = %v classification_ok = %v, want true", tt.name, got.OK, got.ClassificationOK)
			}
			if tt.wantNoMatched {
				if got.Status != "ok" || got.StopState != "" || got.StopStateClass != "" || got.AgentShouldStop {
					t.Fatalf("classifyStopState(%q) = %+v, want non-blocking ok", tt.name, got)
				}
				return
			}
			if got.Status != "blocked" || got.StopState != tt.wantState || got.StopStateClass != tt.wantClass || got.AgentShouldStop != tt.wantStop || got.HumanRequired != tt.wantHuman {
				t.Fatalf("classifyStopState(%q) = %+v, want state=%s class=%s stop=%v human=%v", tt.name, got, tt.wantState, tt.wantClass, tt.wantStop, tt.wantHuman)
			}
			if got.Evidence == nil || got.Evidence.Pattern == "" || got.Evidence.Source == "" || got.MatchedRule == nil {
				t.Fatalf("classifyStopState(%q) evidence = %+v matched_rule = %+v, want bounded match evidence", tt.name, got.Evidence, got.MatchedRule)
			}
			if len(got.NextCommands) == 0 || len(got.Remediation) == 0 {
				t.Fatalf("classifyStopState(%q) next/remediation commands empty: next=%v remediation=%v", tt.name, got.NextCommands, got.Remediation)
			}
		})
	}
}

func TestStopStateClassifyConfiguredRules(t *testing.T) {
	tests := []struct {
		name string
		opts stopStateRuleOptions
		in   stopStateInput
		want string
	}{
		{
			name: "text contains",
			opts: stopStateRuleOptions{TextContains: []string{"google_page_error=Something went wrong"}},
			in:   stopStateInput{Text: "Oops, something went wrong. Reload."},
			want: "google_page_error",
		},
		{
			name: "title contains",
			opts: stopStateRuleOptions{TitleContains: []string{"app_blocked=Temporary block"}},
			in:   stopStateInput{Title: "Temporary Block"},
			want: "app_blocked",
		},
		{
			name: "url contains",
			opts: stopStateRuleOptions{URLContains: []string{"sorry_page=/sorry/"}},
			in:   stopStateInput{URL: "https://www.example.test/sorry/index"},
			want: "sorry_page",
		},
		{
			name: "text regex",
			opts: stopStateRuleOptions{TextRegex: []string{`preflight_search_timeout=No results returned.*reload`}},
			in:   stopStateInput{Text: "No results returned. Please reload and try again."},
			want: "preflight_search_timeout",
		},
		{
			name: "title regex",
			opts: stopStateRuleOptions{TitleRegex: []string{`title_block=Blocked\s+Page`}},
			in:   stopStateInput{Title: "Blocked Page"},
			want: "title_block",
		},
		{
			name: "url regex",
			opts: stopStateRuleOptions{URLRegex: []string{`route_error=/travel/flights/search/.+error=1`}},
			in:   stopStateInput{URL: "https://www.example.test/travel/flights/search/foo?error=1"},
			want: "route_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules, err := parseConfiguredStopStateRules(tt.opts)
			if err != nil {
				t.Fatalf("parseConfiguredStopStateRules(%q): %v", tt.name, err)
			}
			got, _ := classifyStopState(tt.in, rules)
			if got.Status != "blocked" || got.StopState != tt.want || got.StopStateClass != "custom" || got.MatchedRule == nil || got.MatchedRule.BuiltIn {
				t.Fatalf("classify configured %q = %+v, want custom state %q", tt.name, got, tt.want)
			}
			if len(got.ConfiguredRules) == 0 {
				t.Fatalf("classify configured %q configured_rules empty", tt.name)
			}
		})
	}
}

func TestStopStateClassifyCommandJSON(t *testing.T) {
	got := runStopStateClassify(t,
		"stop-state", "classify",
		"--text", "Please sign in to continue.",
		"--json",
	)
	if !got.OK || got.Status != "blocked" || got.StopState != "login_required" || got.StopStateClass != "auth" || !got.AgentShouldStop || !got.HumanRequired {
		t.Fatalf("stop-state classify output = %+v, want login-required stop state", got)
	}
	if got.Input["text_present"] != true || got.Input["text_bytes"] == nil {
		t.Fatalf("stop-state classify input summary = %+v, want bounded text metadata", got.Input)
	}
	if !stopStateTestContainsString(got.NextCommands, "cdp --browser-mode headed daemon status --json") {
		t.Fatalf("next_commands = %v, want auth-safe headed status command", got.NextCommands)
	}
}

func TestStopStateClassifyCommandCustomRuleJSON(t *testing.T) {
	got := runStopStateClassify(t,
		"stop-state", "classify",
		"--text", "Oops, something went wrong. Try again later.",
		"--rule-text-contains", "google_page_error=Something went wrong",
		"--json",
	)
	if got.StopState != "google_page_error" || got.StopStateClass != "custom" || got.MatchedRule == nil || got.MatchedRule.Pattern != "Something went wrong" {
		t.Fatalf("custom stop-state output = %+v, want explicit app-specific state", got)
	}
	if len(got.ConfiguredRules) != 1 || got.ConfiguredRules[0].State != "google_page_error" {
		t.Fatalf("configured_rules = %+v, want explicit custom rule echoed", got.ConfiguredRules)
	}
}

func TestStopStateClassifyInvalidRegexJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{
		"stop-state", "classify",
		"--text", "blocked",
		"--rule-text-regex", "app_block=(",
		"--json",
	}, &out, &errOut, BuildInfo{})
	if code != ExitUsage {
		t.Fatalf("stop-state invalid regex exit = %d, want %d; stderr=%s stdout=%s", code, ExitUsage, errOut.String(), out.String())
	}
	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		ErrClass            string   `json:"err_class"`
		Message             string   `json:"message"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid regex output is invalid JSON: %v; stdout=%s", err, out.String())
	}
	if got.OK || got.Code != "invalid_stop_state_rule" || got.ErrClass != "usage" || !strings.Contains(got.Message, "invalid text regex") || !stopStateTestContainsString(got.RemediationCommands, "cdp stop-state classify --rule-text-regex app_block='blocked|unavailable' --json") {
		t.Fatalf("invalid regex envelope = %+v, want usage error with remediation", got)
	}
}

func TestStopStateForCommandError(t *testing.T) {
	tests := []struct {
		name      string
		err       *CommandError
		wantState string
		wantClass string
	}{
		{
			name:      "permission",
			err:       &CommandError{Code: "permission_pending", Class: "permission"},
			wantState: "permission_pending",
			wantClass: "permission",
		},
		{
			name:      "resource budget",
			err:       &CommandError{Code: "browser_resource_budget_exceeded", Class: "resource_budget"},
			wantState: "browser_resource_budget_exceeded",
			wantClass: "resource_budget",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stopStateForCommandError(tt.err)
			if got == nil || got.StopState != tt.wantState || got.StopStateClass != tt.wantClass || !got.AgentShouldStop || len(got.NextCommands) == 0 {
				t.Fatalf("stopStateForCommandError(%q) = %+v, want state=%s class=%s stop", tt.name, got, tt.wantState, tt.wantClass)
			}
		})
	}
}

func runStopStateClassify(t *testing.T, args ...string) stopStateResult {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), args, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("Execute(%v) exit = %d, want %d; stderr=%s stdout=%s", args, code, ExitOK, errOut.String(), out.String())
	}
	var got stopStateResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("Execute(%v) output is invalid JSON: %v; stdout=%s", args, err, out.String())
	}
	return got
}

func stopStateTestContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
