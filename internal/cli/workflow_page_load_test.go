package cli

import "testing"

func TestClassifyPageLoadContentEvidenceBlocked(t *testing.T) {
	state := classifyPageLoadContentEvidence(
		"https://www.reddit.com/r/OpenAI/search/?q=OpenClaw",
		"page-1",
		403,
		"You've been blocked by network security.",
	)
	if state.Class != "blocked" || state.Actionable || state.MainStatus != 403 || state.FinalURL == "" || state.Warning == "" || len(state.NextCommands) == 0 {
		t.Fatalf("blocked state = %+v, want non-actionable blocked classification", state)
	}
	if !hasPageLoadSignal(state.Signals, "main_document_http_403") || !hasPageLoadSignal(state.Signals, "block_text") {
		t.Fatalf("blocked signals = %+v, want http and block-text signals", state.Signals)
	}
}

func TestClassifyPageLoadContentEvidenceLoginAndCookieWall(t *testing.T) {
	state := classifyPageLoadContentEvidence(
		"https://x.com/i/flow/login?redirect_after_login=%2Fsearch%3Fq%3DOpenClaw",
		"page-1",
		200,
		"Did someone say cookies? Accept all cookies Refuse non-essential cookies Sign in to X",
	)
	if state.Class != "login_wall" || state.Actionable || state.MainStatus != 200 || state.Warning == "" {
		t.Fatalf("login wall state = %+v, want non-actionable login wall classification", state)
	}
	if !hasPageLoadSignal(state.Signals, "login_wall") || !hasPageLoadSignal(state.Signals, "cookie_wall") {
		t.Fatalf("login wall signals = %+v, want login and cookie wall signals", state.Signals)
	}
}

func hasPageLoadSignal(signals []string, want string) bool {
	for _, signal := range signals {
		if signal == want {
			return true
		}
	}
	return false
}
