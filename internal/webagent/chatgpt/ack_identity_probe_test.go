package chatgpt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
)

type stalledAckClient struct{ *testsupport.Browser }

func (c stalledAckClient) CallSession(ctx context.Context, sessionID, method string,
	params any, result any) error {
	if method == "Runtime.evaluate" {
		<-ctx.Done()
		return ctx.Err()
	}
	return c.Browser.CallSession(ctx, sessionID, method, params, result)
}

func (c stalledAckClient) Call(ctx context.Context, method string,
	params any, result any) error {
	if method == "Target.getTargetInfo" {
		<-ctx.Done()
		return ctx.Err()
	}
	return c.Browser.Call(ctx, method, params, result)
}

func TestAckIdentityProbeRequiresExactRouteAndPrompt(t *testing.T) {
	fingerprint := fingerprintPrompt("Exact submitted prompt")
	base := renderedObservation{RouteMatches: true,
		ConversationID: "synthetic-conversation", UserMessageCount: 1,
		PromptCandidates: []string{"Exact submitted prompt"}}
	if !validAckIdentity(base, fingerprint) {
		t.Fatal("exact route and prompt rejected")
	}
	for _, change := range []func(*renderedObservation){
		func(v *renderedObservation) { v.RouteMatches = false },
		func(v *renderedObservation) { v.ConversationID = "invalid/id" },
		func(v *renderedObservation) { v.UserMessageCount = 0 },
		func(v *renderedObservation) { v.UserMessageCount = 2 },
		func(v *renderedObservation) { v.PromptCandidates = []string{"Another prompt"} },
	} {
		candidate := base
		change(&candidate)
		if validAckIdentity(candidate, fingerprint) {
			t.Fatalf("ambiguous identity accepted: %+v", candidate)
		}
	}
}

func TestAckObservationErrorClassesDoNotLeakMessages(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{&evaluationFailure{kind: "javascript_exception"}, "javascript_exception"},
		{context.DeadlineExceeded, "context_deadline"},
		{errors.New("secret URL and page text"), "cdp_evaluate_error"},
	} {
		if got := classifyAckObservationError(test.err); got != test.want {
			t.Fatalf("error class = %q, want %q", got, test.want)
		}
	}
}

func TestAckIdentityProbeBoundsStalledEvaluate(t *testing.T) {
	client := stalledAckClient{Browser: testsupport.NewBrowser("owned-probe")}
	session, err := cdp.AttachToTargetWithClient(context.Background(), client,
		"owned-probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, matched := probeAckIdentity(context.Background(), session,
		fingerprintPrompt("Exact submitted prompt")); matched {
		t.Fatal("stalled evaluation proved identity")
	}
	if elapsed := time.Since(started); elapsed < 5*time.Second || elapsed > 8*time.Second {
		t.Fatalf("stalled evaluate duration %v, want bounded near 6s", elapsed)
	}
}

func TestAckTargetDiagnosticBoundsStalledCDP(t *testing.T) {
	client := stalledAckClient{Browser: testsupport.NewBrowser("owned-probe")}
	started := time.Now()
	if got := probeAckTargetPresence(context.Background(), client,
		"owned-probe"); got != "unknown" {
		t.Fatalf("stalled target diagnostic = %v, want unknown", got)
	}
	if elapsed := time.Since(started); elapsed < time.Second || elapsed > 4*time.Second {
		t.Fatalf("stalled target diagnostic duration %v, want bounded near 2s", elapsed)
	}
}
