package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestWindowMarkerConfigAndScriptAreStableAndSafe(t *testing.T) {
	config, err := newWindowMarkerConfig(`agent "one"`)
	if err != nil {
		t.Fatalf("newWindowMarkerConfig: %v", err)
	}
	if config.SchemaVersion != windowMarkerSchemaVersion || !config.Enabled {
		t.Fatalf("config metadata = %+v", config)
	}
	if config.Color != deriveWindowMarkerColor(config.Name) {
		t.Fatalf("color = %q, want derived color %q", config.Color, deriveWindowMarkerColor(config.Name))
	}
	if config.HostID == "" {
		t.Fatal("config has no host identity")
	}

	script := buildWindowMarkerScript(config)
	for _, want := range []string{
		"(() => {",
		`agent \"one\"`,
		config.Color,
		config.HostID,
		"attachShadow",
		`mode:'closed'`,
		"pointer-events:none",
		"data-cdp-cli-window-marker-disabled",
		"indexOf(PREFIX)",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("marker script does not contain %q: %s", want, script)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(script), "})();") {
		t.Fatalf("marker script is not a closed IIFE: %s", script)
	}

	removal := buildWindowMarkerRemovalScript(config)
	for _, want := range []string{config.HostID, "PREFIX", "setAttribute", ".remove()"} {
		if !strings.Contains(removal, want) {
			t.Fatalf("marker removal script does not contain %q: %s", want, removal)
		}
	}
}

func TestWindowMarkerNameValidationRejectsUnsafeOrEmptyNames(t *testing.T) {
	for _, name := range []string{"", "   ", "agent\nwindow", "agent\x00window"} {
		if _, err := newWindowMarkerConfig(name); err == nil {
			t.Fatalf("newWindowMarkerConfig(%q) accepted unsafe name", name)
		}
	}
	if _, err := newWindowMarkerConfig("agent-window"); err != nil {
		t.Fatalf("newWindowMarkerConfig(valid): %v", err)
	}
}

type markerCall struct {
	method    string
	sessionID string
	params    any
}

type fakeWindowMarkerTransport struct {
	mu       sync.Mutex
	handlers map[string]cdp.EventHandler
	calls    []markerCall
	failures map[string]int
	callHook func(string)
}

func (f *fakeWindowMarkerTransport) Call(_ context.Context, method string, params any, _ any) error {
	f.mu.Lock()
	f.calls = append(f.calls, markerCall{method: method, params: params})
	hook := f.callHook
	f.mu.Unlock()
	if hook != nil {
		hook(method)
	}
	return nil
}

func (f *fakeWindowMarkerTransport) CallSession(_ context.Context, sessionID, method string, params any, result any) error {
	f.mu.Lock()
	f.calls = append(f.calls, markerCall{method: method, sessionID: sessionID, params: params})
	if f.failures != nil && f.failures[method] > 0 {
		f.failures[method]--
		f.mu.Unlock()
		return errors.New("synthetic marker session failure")
	}
	f.mu.Unlock()
	if method == "Page.addScriptToEvaluateOnNewDocument" && result != nil {
		encoded, _ := json.Marshal(map[string]string{"identifier": "script-" + sessionID})
		_ = json.Unmarshal(encoded, result)
	}
	return nil
}

func (f *fakeWindowMarkerTransport) SubscribeEvent(method string, handler cdp.EventHandler) func() {
	f.mu.Lock()
	if f.handlers == nil {
		f.handlers = map[string]cdp.EventHandler{}
	}
	f.handlers[method] = handler
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.handlers, method)
		f.mu.Unlock()
	}
}

func (f *fakeWindowMarkerTransport) emit(method string, params any) bool {
	raw, _ := json.Marshal(params)
	f.mu.Lock()
	handler := f.handlers[method]
	f.mu.Unlock()
	if handler == nil {
		return false
	}
	return handler(cdp.Event{Method: method, Params: raw})
}

func (f *fakeWindowMarkerTransport) callsSnapshot() []markerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]markerCall(nil), f.calls...)
}

func TestWindowMarkerControllerCoversCurrentAndFuturePagesAndDisablesCleanly(t *testing.T) {
	transport := &fakeWindowMarkerTransport{}
	detachEventsConsumed := 0
	transport.callHook = func(method string) {
		if method == "Target.detachFromTarget" && transport.emit("Target.detachedFromTarget", map[string]any{"sessionId": "session-one"}) {
			detachEventsConsumed++
		}
	}
	controller := newWindowMarkerController(t.TempDir(), "headed", transport)

	status, err := controller.Enable(context.Background(), "agent")
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if status.State != "enabled" || !status.Enabled {
		t.Fatalf("enable status = %+v", status)
	}
	if !transport.emit("Target.attachedToTarget", map[string]any{
		"sessionId":  "session-one",
		"targetInfo": map[string]any{"targetId": "page-one", "type": "page"},
	}) {
		t.Fatal("page attach was not consumed")
	}
	if !transport.emit("Target.attachedToTarget", map[string]any{
		"sessionId":  "session-two",
		"targetInfo": map[string]any{"targetId": "page-two", "type": "page"},
	}) {
		t.Fatal("future page attach was not consumed")
	}
	waitForMarkerCalls(t, transport, 6)
	status = controller.Status()
	if status.ActiveSessionCount != 2 || status.SetupFailureCount != 0 {
		t.Fatalf("active marker status = %+v", status)
	}

	if _, err := controller.Disable(context.Background()); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	status = controller.Status()
	if status.State != "disabled" || status.ActiveSessionCount != 0 || status.HostIDPresent {
		t.Fatalf("disabled marker status = %+v", status)
	}
	calls := transport.callsSnapshot()
	if !hasMarkerCall(calls, "Target.setAutoAttach", "") || !hasMarkerCall(calls, "Page.removeScriptToEvaluateOnNewDocument", "session-one") || !hasMarkerCall(calls, "Page.removeScriptToEvaluateOnNewDocument", "session-two") || !hasMarkerTargetCall(calls, "Target.detachFromTarget", "session-one") || !hasMarkerTargetCall(calls, "Target.detachFromTarget", "session-two") {
		t.Fatalf("disable calls = %+v", calls)
	}
	removeIndex := markerCallIndex(calls, "Page.removeScriptToEvaluateOnNewDocument", "session-one")
	detachIndex := markerTargetCallIndex(calls, "Target.detachFromTarget", "session-one")
	autoAttachOffIndex := markerCallIndexWithAutoAttach(calls, false)
	if removeIndex < 0 || detachIndex < 0 || autoAttachOffIndex < 0 || !(removeIndex < detachIndex && detachIndex < autoAttachOffIndex) {
		t.Fatalf("disable teardown order = remove %d, detach %d, auto-attach-off %d; calls = %+v", removeIndex, detachIndex, autoAttachOffIndex, calls)
	}
	if detachEventsConsumed == 0 {
		t.Fatal("marker teardown detach event was not consumed while handlers were still subscribed")
	}
}

func TestWindowMarkerControllerRehydratesPersistedConfiguration(t *testing.T) {
	stateDir := t.TempDir()
	firstTransport := &fakeWindowMarkerTransport{}
	first := newWindowMarkerController(stateDir, "headed", firstTransport)
	if _, err := first.Enable(context.Background(), "agent"); err != nil {
		t.Fatalf("initial Enable: %v", err)
	}
	if err := first.close(context.Background()); err != nil {
		t.Fatalf("close first controller: %v", err)
	}

	secondTransport := &fakeWindowMarkerTransport{}
	second := newWindowMarkerController(stateDir, "headed", secondTransport)
	if err := second.rehydrate(context.Background()); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	if !secondTransport.emit("Target.attachedToTarget", map[string]any{
		"sessionId":  "session-rehydrated",
		"targetInfo": map[string]any{"targetId": "page-rehydrated", "type": "page"},
	}) {
		t.Fatal("rehydrated page attach was not consumed")
	}
	waitForMarkerCalls(t, secondTransport, 4)
	if got := second.Status(); got.State != "enabled" || got.ActiveSessionCount != 1 || got.Name != "agent" {
		t.Fatalf("rehydrated status = %+v", got)
	}
}

func TestWindowMarkerControllerRetainsPartialScriptSetupForDisable(t *testing.T) {
	transport := &fakeWindowMarkerTransport{failures: map[string]int{"Runtime.evaluate": 1}}
	controller := newWindowMarkerController(t.TempDir(), "headed", transport)
	if _, err := controller.Enable(context.Background(), "agent"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !transport.emit("Target.attachedToTarget", map[string]any{
		"sessionId":  "session-partial",
		"targetInfo": map[string]any{"targetId": "page-partial", "type": "page"},
	}) {
		t.Fatal("partial page attach was not consumed")
	}
	waitForMarkerCalls(t, transport, 4)
	if got := controller.Status(); got.SetupFailureCount != 1 {
		t.Fatalf("partial setup status = %+v, want one failure", got)
	}
	if _, err := controller.Disable(context.Background()); err != nil {
		t.Fatalf("Disable after partial setup: %v", err)
	}
	if !hasMarkerCall(transport.callsSnapshot(), "Page.removeScriptToEvaluateOnNewDocument", "session-partial") {
		t.Fatalf("partial setup cleanup calls = %+v, want navigation script removal", transport.callsSnapshot())
	}
}

func waitForMarkerCalls(t *testing.T, transport *fakeWindowMarkerTransport, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(transport.callsSnapshot()) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("marker calls = %d, want at least %d: %+v", len(transport.callsSnapshot()), want, transport.callsSnapshot())
}

func hasMarkerCall(calls []markerCall, method, sessionID string) bool {
	for _, call := range calls {
		if call.method == method && call.sessionID == sessionID {
			return true
		}
	}
	return false
}

func hasMarkerTargetCall(calls []markerCall, method, sessionID string) bool {
	for _, call := range calls {
		params, ok := call.params.(map[string]any)
		if ok && call.method == method && params["sessionId"] == sessionID {
			return true
		}
	}
	return false
}

func markerCallIndex(calls []markerCall, method, sessionID string) int {
	for index, call := range calls {
		if call.method == method && call.sessionID == sessionID {
			return index
		}
	}
	return -1
}

func markerTargetCallIndex(calls []markerCall, method, sessionID string) int {
	for index, call := range calls {
		params, ok := call.params.(map[string]any)
		if ok && call.method == method && params["sessionId"] == sessionID {
			return index
		}
	}
	return -1
}

func markerCallIndexWithAutoAttach(calls []markerCall, enabled bool) int {
	for index, call := range calls {
		if call.method != "Target.setAutoAttach" {
			continue
		}
		params, ok := call.params.(map[string]any)
		if ok && params["autoAttach"] == enabled {
			return index
		}
	}
	return -1
}

func TestWindowMarkerStateRejectsTamperedEnabledConfiguration(t *testing.T) {
	path := windowMarkerStatePath(t.TempDir(), "headed")
	config, err := newWindowMarkerConfig("agent")
	if err != nil {
		t.Fatal(err)
	}
	config.Color = "#000000"
	if err := saveWindowMarkerConfig(path, config); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadWindowMarkerConfig(path); err == nil || !strings.Contains(err.Error(), "color") {
		t.Fatalf("load tampered config error = %v", err)
	}
}
