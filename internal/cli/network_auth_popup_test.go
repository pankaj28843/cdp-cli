package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type popupFake struct {
	networkControlFakeClient
	targets string
	proof   bool
}

func (f *popupFake) Call(_ context.Context, method string, _ any, result any) error {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	f.mu.Unlock()
	switch method {
	case "Target.getTargets":
		return json.Unmarshal([]byte(`{"targetInfos":`+f.targets+`}`), result)
	case "Target.attachToTarget":
		return json.Unmarshal([]byte(`{"sessionId":"child-session"}`), result)
	}
	return nil
}
func (f *popupFake) CallSession(ctx context.Context, session, method string, params, result any) error {
	if err := f.networkControlFakeClient.CallSession(ctx, session, method, params, result); err != nil {
		return err
	}
	if result != nil && (method == "Runtime.evaluate" || method == "Debugger.evaluateOnCallFrame") {
		raw := `{"result":{"value":false}}`
		if f.proof {
			raw = `{"result":{"value":true}}`
		}
		return json.Unmarshal([]byte(raw), result)
	}
	return nil
}
func popupPause() cdp.Event {
	return cdp.Event{SessionID: "root-session", Method: "Debugger.paused", Params: json.RawMessage(`{"callFrames":[{"callFrameId":"frame","functionName":"cdpPrearmPopup"}]}`)}
}
func ownedPopupFake() *popupFake {
	return &popupFake{proof: true, targets: `[{"targetId":"child","type":"page","openerId":"root","url":"about:blank"}]`}
}
func TestHTTPAuthPopupSetupPrecedesResume(t *testing.T) {
	f := ownedPopupFake()
	s := newHTTPAuthPopupSupervisor(f, "root", "root-session")
	if err := s.attach(context.Background(), popupPause()); err != nil {
		t.Fatal(err)
	}
	methods := strings.Join(f.methodSnapshot(), ",")
	if !strings.Contains(methods, "Fetch.enable,Debugger.evaluateOnCallFrame,Debugger.resume") {
		t.Fatalf("unsafe order: %s", methods)
	}
	if !s.cleanup() {
		t.Fatal("cleanup failed")
	}
}
func TestHTTPAuthPopupOwnershipFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, targets   string
		proof, existing bool
	}{
		{"foreign", `[{"targetId":"child","type":"page","openerId":"other","url":"about:blank"}]`, true, false},
		{"existing", ownedPopupFake().targets, true, true},
		{"wrong window", ownedPopupFake().targets, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &popupFake{targets: tc.targets, proof: tc.proof}
			s := newHTTPAuthPopupSupervisor(f, "root", "root-session")
			s.existing = map[string]bool{"child": tc.existing}
			if s.attach(context.Background(), popupPause()) == nil {
				t.Fatal("unowned popup accepted")
			}
			for _, m := range f.methodSnapshot() {
				if m == "Fetch.enable" || m == "Debugger.resume" {
					t.Fatal("unowned popup armed/resumed")
				}
			}
		})
	}
}
func TestHTTPAuthPopupSetupFailureNeverResumesWithoutAuth(t *testing.T) {
	f := ownedPopupFake()
	f.failOnce = map[string]bool{"Fetch.enable": true}
	s := newHTTPAuthPopupSupervisor(f, "root", "root-session")
	s.enabled = true
	if s.attach(context.Background(), popupPause()) == nil {
		t.Fatal("failed setup succeeded")
	}
	for _, m := range f.methodSnapshot() {
		if m == "Debugger.resume" {
			t.Fatal("resumed without auth")
		}
	}
	if !s.cleanup() {
		t.Fatal("failed setup cleanup")
	}
}
func TestHTTPAuthPopupCancellationAndForeignEvents(t *testing.T) {
	f := ownedPopupFake()
	s := newHTTPAuthPopupSupervisor(f, "root", "root-session")
	e := popupPause()
	e.SessionID = "foreign"
	if err := s.attach(context.Background(), e); err != nil || len(f.methodSnapshot()) != 0 {
		t.Fatal("foreign root touched")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.collect(ctx, time.Second, "https://example.test", "basic", "USER", "PASS", func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("cancellation succeeded")
	}
}
func TestHTTPAuthPopupFlagRejectsReload(t *testing.T) {
	cmd := (&app{}).newNetworkAuthCommand()
	cmd.SetArgs([]string{"--popup-prearm", "--reload", "--origin", "https://example.test", "--target", "synthetic", "--username-env", "USER", "--password-env", "PASS"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("incompatible options accepted")
	}
}
func TestHTTPAuthPopupTransportFailureRequiresTargetAbsence(t *testing.T) {
	f := ownedPopupFake()
	s := newHTTPAuthPopupSupervisor(f, "root", "root-session")
	if err := s.attach(context.Background(), popupPause()); err != nil {
		t.Fatal(err)
	}
	if s.childGone(context.Background()) {
		t.Fatal("live child treated as gone")
	}
	f.targets = `[]`
	if !s.childGone(context.Background()) {
		t.Fatal("closed child not recognized")
	}
}

func TestHTTPAuthPopupSharesCredentialBudgetAcrossSessions(t *testing.T) {
	f := &authFake{}
	state := newHTTPAuthState("https://example.test", "basic", "USER", "PASS", func(string) (string, bool) { return "synthetic", true })
	for i := 0; i < 20; i++ {
		session := "root"
		if i%2 == 0 {
			session = "child"
		}
		// Request IDs may repeat in distinct sessions.
		raw, _ := json.Marshal(map[string]any{"requestId": strings.Repeat("x", i/2+1), "request": map[string]any{"url": "https://example.test/a"}, "authChallenge": map[string]any{"origin": "https://example.test", "source": "Server", "scheme": "Basic"}})
		if err := state.handle(context.Background(), f, session, cdp.Event{Method: "Fetch.authRequired", Params: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if state.counts["provided"] != 16 || state.counts["canceled"] != 4 {
		t.Fatalf("unshared budget: %v", state.counts)
	}
}
