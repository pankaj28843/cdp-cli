package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"strings"
	"testing"
	"time"
)

type authFake struct {
	networkControlFakeClient
	responses []map[string]any
}

func (f *authFake) CallSession(ctx context.Context, session, method string, params any, result any) error {
	if method == "Fetch.continueWithAuth" {
		r := map[string]any{}
		for k, v := range params.(map[string]any)["authChallengeResponse"].(map[string]any) {
			r[k] = v
		}
		f.responses = append(f.responses, r)
	}
	return f.networkControlFakeClient.CallSession(ctx, session, method, params, result)
}
func TestHTTPAuthOriginValidation(t *testing.T) {
	for _, s := range []string{"https://example.test/path", "https://user:pass@example.test", "ftp://example.test", "https://example.test?x", "https://example.test#x", "http://example.test", "http://192.0.2.1", "https://example.test:99999"} {
		if _, err := httpAuthOrigin(s); err == nil {
			t.Fatalf("accepted invalid origin %q", s)
		}
	}
	a, err := httpAuthOrigin("https://EXAMPLE.test:443")
	if err != nil || a != "https://example.test" {
		t.Fatalf("canonical origin=%q err=%v", a, err)
	}
}
func TestHTTPAuthFailsClosedAndBoundsRetries(t *testing.T) {
	t.Setenv("CDP_TEST_AUTH_USER", "synthetic-user")
	t.Setenv("CDP_TEST_AUTH_PASS", "synthetic-password")
	f := &authFake{}
	for _, tc := range []struct{ id, origin, source, scheme, url string }{
		{"one", "https://example.test", "Server", "basic", "https://example.test/a"},
		{"one", "https://example.test", "Server", "basic", "https://example.test/a"},
		{"two", "https://other.test", "Server", "basic", "https://other.test/a"},
		{"three", "https://example.test", "Proxy", "basic", "https://example.test/a"},
		{"four", "https://example.test", "Server", "digest", "https://example.test/a"},
		{"five", "https://example.test", "Server", "basic", "https://other.test/a"},
	} {
		raw, _ := json.Marshal(map[string]any{"requestId": tc.id, "request": map[string]any{"url": tc.url}, "authChallenge": map[string]any{"origin": tc.origin, "source": tc.source, "scheme": tc.scheme}})
		f.events = append(f.events, cdp.Event{SessionID: "session", Method: "Fetch.authRequired", Params: raw})
	}
	lookups := 0
	lookup := func(name string) (string, bool) {
		lookups++
		if name == "CDP_TEST_AUTH_USER" {
			return "synthetic-user", true
		}
		return "synthetic-password", true
	}
	counts, err := collectHTTPAuth(context.Background(), f, "session", time.Millisecond, "https://example.test", "basic", "CDP_TEST_AUTH_USER", "CDP_TEST_AUTH_PASS", lookup)
	if err != nil || counts["provided"] != 1 || counts["canceled"] != 5 || lookups != 2 {
		t.Fatalf("counts=%v lookups=%d err=%v", counts, lookups, err)
	}
	for i, r := range f.responses {
		if i > 0 && (r["response"] != "CancelAuth" || r["username"] != nil || r["password"] != nil) {
			t.Fatalf("unsafe cancellation response %d", i)
		}
	}
}

func TestHTTPAuthMissingEnvironmentCancelsWithoutDisclosure(t *testing.T) {
	f := &authFake{}
	raw := json.RawMessage(`{"requestId":"one","request":{"url":"https://example.test/a"},"authChallenge":{"origin":"https://example.test","source":"Server","scheme":"basic"}}`)
	f.events = []cdp.Event{{SessionID: "session", Method: "Fetch.authRequired", Params: raw}}
	_, err := collectHTTPAuth(context.Background(), f, "session", time.Millisecond, "https://example.test", "basic", "UNSET_USER", "UNSET_PASS", func(string) (string, bool) { return "", false })
	if err == nil || len(f.responses) != 1 || f.responses[0]["response"] != "CancelAuth" {
		t.Fatal("missing credentials must cancel and fail")
	}
}
func TestHTTPAuthGlobalAttemptBudgetAndRequestContinuation(t *testing.T) {
	f := &authFake{}
	for i := 0; i < 20; i++ {
		raw, _ := json.Marshal(map[string]any{"requestId": fmt.Sprint(i), "request": map[string]any{"url": "https://example.test/a"}, "authChallenge": map[string]any{"origin": "https://example.test", "source": "Server", "scheme": "Basic"}})
		f.events = append(f.events, cdp.Event{SessionID: "session", Method: "Fetch.authRequired", Params: raw})
	}
	f.events = append(f.events, cdp.Event{SessionID: "session", Method: "Fetch.requestPaused", Params: json.RawMessage(`{"requestId":"paused"}`)})
	counts, err := collectHTTPAuth(context.Background(), f, "session", time.Millisecond, "https://example.test", "basic", "SYNTH_USER", "SYNTH_PASS", func(string) (string, bool) { return "synthetic", true })
	if err != nil || counts["provided"] != 16 || counts["canceled"] != 4 || counts["continued"] != 1 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
}
func TestHTTPAuthTransportErrorsDoNotEchoSecrets(t *testing.T) {
	f := &authFake{networkControlFakeClient: networkControlFakeClient{failOnce: map[string]bool{"Fetch.continueWithAuth": true}}}
	f.events = []cdp.Event{{SessionID: "session", Method: "Fetch.authRequired", Params: json.RawMessage(`{"requestId":"one","request":{"url":"https://example.test"},"authChallenge":{"origin":"https://example.test","source":"Server","scheme":"basic"}}`)}}
	_, err := collectHTTPAuth(context.Background(), f, "session", time.Millisecond, "https://example.test", "basic", "SYNTH_USER", "SYNTH_PASS", func(string) (string, bool) { return "synthetic-secret", true })
	if err == nil || strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("unsafe failure: %v", err)
	}
}

func TestHTTPAuthLoopbackFixtures(t *testing.T) {
	for _, s := range []string{"http://localhost", "http://127.0.0.1", "http://[::1]"} {
		if _, err := httpAuthOrigin(s); err != nil {
			t.Fatalf("loopback %q rejected: %v", s, err)
		}
	}
}

type authEarlyDeadlineFake struct{ networkControlFakeClient }

func (f *authEarlyDeadlineFake) ReadSessionEvent(context.Context, string) (cdp.Event, error) {
	return cdp.Event{}, context.DeadlineExceeded
}
func TestHTTPAuthDaemonDeadlineBeforeLocalTimer(t *testing.T) {
	counts, err := collectHTTPAuth(context.Background(), &authEarlyDeadlineFake{}, "session", time.Minute, "https://example.test", "basic", "SYNTH_USER", "SYNTH_PASS", func(string) (string, bool) { t.Fatal("unexpected credential lookup"); return "", false })
	if err != nil || counts["provided"] != 0 {
		t.Fatalf("normal bounded daemon expiry: counts=%v err=%v", counts, err)
	}
}

type authSessionOnlyFake struct {
	networkControlFakeClient
	used bool
	t    *testing.T
}

func (f *authSessionOnlyFake) ReadEvent(context.Context) (cdp.Event, error) {
	f.t.Fatal("global event queue must not be used")
	return cdp.Event{}, context.Canceled
}
func (f *authSessionOnlyFake) ReadSessionEvent(_ context.Context, session string) (cdp.Event, error) {
	if session != "owned-session" {
		f.t.Fatal("wrong session reader")
	}
	f.used = true
	return cdp.Event{}, context.DeadlineExceeded
}
func TestHTTPAuthConsumesOnlyOwnedSessionEvents(t *testing.T) {
	f := &authSessionOnlyFake{t: t}
	_, err := collectHTTPAuth(context.Background(), f, "owned-session", time.Second, "https://example.test", "basic", "SYNTH_USER", "SYNTH_PASS", func(string) (string, bool) { t.Fatal("unexpected lookup"); return "", false })
	if err != nil || !f.used {
		t.Fatalf("session reader used=%v err=%v", f.used, err)
	}
}

func TestHTTPAuthRejectsForeignAndSessionlessEvents(t *testing.T) {
	f := &authFake{}
	raw := json.RawMessage(`{"requestId":"one","request":{"url":"https://example.test/a"},"authChallenge":{"origin":"https://example.test","source":"Server","scheme":"basic"}}`)
	f.events = []cdp.Event{{SessionID: "foreign", Method: "Fetch.authRequired", Params: raw}, {Method: "Fetch.authRequired", Params: raw}, {SessionID: "foreign", Method: "Fetch.requestPaused", Params: raw}}
	counts, err := collectHTTPAuth(context.Background(), f, "session", time.Millisecond, "https://example.test", "basic", "SYNTH_USER", "SYNTH_PASS", func(string) (string, bool) { t.Fatal("foreign credential lookup"); return "", false })
	if err != nil || len(f.methodSnapshot()) != 0 || counts["provided"] != 0 {
		t.Fatalf("foreign event actions=%v err=%v", counts, err)
	}
}

func TestHTTPAuthSessionCleanupHasIndependentDeadline(t *testing.T) {
	if !closeHTTPAuthSession(func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second || ctx.Err() != nil {
			t.Fatal("cleanup lacks independent bounded context")
		}
		return nil
	}) {
		t.Fatal("successful close failed")
	}
	if closeHTTPAuthSession(func(context.Context) error { return errors.New("synthetic secret diagnostic") }) {
		t.Fatal("failed detach reported complete")
	}
}

func TestHTTPAuthSchemeDefaultsToBasic(t *testing.T) {
	cmd := (&app{}).newNetworkAuthCommand()
	scheme, err := cmd.Flags().GetString("scheme")
	if err != nil || scheme != "basic" {
		t.Fatalf("scheme default=%q err=%v, want basic", scheme, err)
	}
}

func TestHTTPAuthNTLMOptInPreservesBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, selected, challenge, source, challengeOrigin, requestOrigin string
		accepted                                                          bool
	}{
		{"basic default rejects NTLM", "basic", "NTLM", "Server", "https://example.test", "https://example.test", false},
		{"explicit NTLM", "ntlm", "NTLM", "Server", "https://example.test", "https://example.test", true},
		{"challenge casing", "ntlm", "nTlM", "Server", "https://example.test", "https://example.test", true},
		{"NTLM rejects Basic", "ntlm", "Basic", "Server", "https://example.test", "https://example.test", false},
		{"NTLM rejects proxy", "ntlm", "NTLM", "Proxy", "https://example.test", "https://example.test", false},
		{"NTLM rejects missing source", "ntlm", "NTLM", "", "https://example.test", "https://example.test", false},
		{"NTLM rejects challenge origin", "ntlm", "NTLM", "Server", "https://other.test", "https://example.test", false},
		{"NTLM rejects request origin", "ntlm", "NTLM", "Server", "https://example.test", "https://other.test", false},
		{"NTLM rejects Negotiate", "ntlm", "Negotiate", "Server", "https://example.test", "https://example.test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &authFake{}
			raw, _ := json.Marshal(map[string]any{"requestId": "one", "request": map[string]any{"url": tc.requestOrigin + "/a"}, "authChallenge": map[string]any{"origin": tc.challengeOrigin, "source": tc.source, "scheme": tc.challenge}})
			// Repeated challenge must always be canceled, including NTLM.
			f.events = []cdp.Event{{SessionID: "session", Method: "Fetch.authRequired", Params: raw}, {SessionID: "session", Method: "Fetch.authRequired", Params: raw}}
			names := []string{}
			counts, err := collectHTTPAuth(context.Background(), f, "session", time.Millisecond, "https://example.test", tc.selected, "SYNTH_USER", "SYNTH_PASS", func(name string) (string, bool) {
				names = append(names, name)
				if name == "SYNTH_USER" {
					return `FIXTURE\user`, true
				}
				return "synthetic-password", true
			})
			want := 0
			if tc.accepted {
				want = 1
			}
			if err != nil || counts["provided"] != want || counts["canceled"] != 2-want || len(names) != 2*want {
				t.Fatalf("counts=%v lookups=%d err=%v", counts, len(names), err)
			}
			if tc.accepted && (names[0] != "SYNTH_USER" || names[1] != "SYNTH_PASS" || f.responses[0]["username"] != `FIXTURE\user`) {
				t.Fatal("NTLM must use named variables and preserve opaque domain-qualified username")
			}
			for i, r := range f.responses {
				if !tc.accepted || i > 0 {
					if r["response"] != "CancelAuth" || r["username"] != nil || r["password"] != nil {
						t.Fatal("rejected challenge disclosed credentials")
					}
				}
			}
		})
	}
}

func TestHTTPAuthRejectsUnsupportedSchemeBeforeAttach(t *testing.T) {
	cmd := (&app{}).newNetworkAuthCommand()
	for name, value := range map[string]string{"origin": "https://example.test", "target": "synthetic-target", "username-env": "SYNTH_USER", "password-env": "SYNTH_PASS", "scheme": "negotiate"} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("unsupported scheme must fail before browser attach")
	}
}

func TestHTTPAuthNTLMMissingEnvironmentFailsClosed(t *testing.T) {
	f := &authFake{}
	f.events = []cdp.Event{{SessionID: "session", Method: "Fetch.authRequired", Params: json.RawMessage(`{"requestId":"one","request":{"url":"https://example.test/a"},"authChallenge":{"origin":"https://example.test","source":"Server","scheme":"NTLM"}}`)}}
	_, err := collectHTTPAuth(context.Background(), f, "session", time.Millisecond, "https://example.test", "ntlm", "SYNTH_USER", "SYNTH_PASS", func(string) (string, bool) { return "", false })
	if err == nil || len(f.responses) != 1 || f.responses[0]["response"] != "CancelAuth" || f.responses[0]["username"] != nil {
		t.Fatal("NTLM missing credentials must cancel without disclosure")
	}
}
