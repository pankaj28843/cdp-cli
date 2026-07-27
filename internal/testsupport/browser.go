package testsupport

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

// EvaluateFunc returns the JSON value produced by Runtime.evaluate.
// It runs while Browser's lock is held, so it may inspect or mutate Browser
// fields directly without calling Browser methods.
type EvaluateFunc func(expression string, browser *Browser) (any, error)

// Browser is a deterministic CDP command client for provider transaction tests.
// It intentionally implements only the headed-browser boundary shared by asks.
type Browser struct {
	mu sync.Mutex

	Targets      map[string]cdp.TargetInfo
	Counts       map[string]int
	Trace        []string
	Reloads      []bool
	InsertedText string
	InsertCount  int
	SendCount    int
	nextTargetID int

	Evaluate EvaluateFunc
}

func NewBrowser(existingTargetIDs ...string) *Browser {
	browser := &Browser{
		Targets: map[string]cdp.TargetInfo{},
		Counts:  map[string]int{},
	}
	for _, targetID := range existingTargetIDs {
		browser.Targets[targetID] = cdp.TargetInfo{
			TargetID: targetID,
			Type:     "page",
			URL:      "https://user.test/",
		}
	}
	return browser
}

func (b *Browser) Call(
	_ context.Context,
	method string,
	params any,
	result any,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(method)
	switch method {
	case "Target.getTargets":
		targets := make([]cdp.TargetInfo, 0, len(b.Targets))
		for _, target := range b.Targets {
			targets = append(targets, target)
		}
		return assign(result, map[string]any{"targetInfos": targets})
	case "Browser.getWindowForTarget":
		return assign(result, map[string]any{"windowId": 1})
	case "Target.createTarget":
		b.nextTargetID++
		targetID := fmt.Sprintf("owned-%d", b.nextTargetID)
		b.Targets[targetID] = cdp.TargetInfo{
			TargetID: targetID,
			Type:     "page",
			URL:      stringParam(params, "url"),
		}
		return assign(result, map[string]any{"targetId": targetID})
	case "Target.attachToTarget":
		targetID := stringParam(params, "targetId")
		if _, ok := b.Targets[targetID]; !ok {
			return fmt.Errorf("target %q not found", targetID)
		}
		return assign(result, map[string]any{
			"sessionId": "session-" + targetID,
		})
	case "Browser.getVersion":
		return assign(result, map[string]any{"userAgent": "Browser/Test"})
	case "Target.activateTarget", "Target.detachFromTarget":
		return assign(result, map[string]any{})
	case "Target.closeTarget":
		delete(b.Targets, stringParam(params, "targetId"))
		return assign(result, map[string]any{"success": true})
	default:
		return fmt.Errorf("unexpected browser call %s", method)
	}
}

func (b *Browser) CallSession(
	_ context.Context,
	_ string,
	method string,
	params any,
	result any,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(method)
	switch method {
	case "Network.enable", "Page.enable", "Runtime.enable":
		return assign(result, map[string]any{})
	case "Page.navigate":
		return assign(result, map[string]any{"frameId": "frame-1"})
	case "Page.reload":
		b.Reloads = append(b.Reloads, boolParam(params, "ignoreCache"))
		return assign(result, map[string]any{"frameId": "frame-1"})
	case "Runtime.evaluate":
		value := any(map[string]any{})
		var err error
		if b.Evaluate != nil {
			value, err = b.Evaluate(stringParam(params, "expression"), b)
			if err != nil {
				return err
			}
		}
		return assign(result, map[string]any{
			"result": map[string]any{"type": "object", "value": value},
		})
	case "Input.insertText":
		b.InsertedText = stringParam(params, "text")
		b.InsertCount++
		return assign(result, map[string]any{})
	case "Input.dispatchKeyEvent":
		eventType := stringParam(params, "type")
		if eventType == "keyDown" || eventType == "rawKeyDown" {
			b.SendCount++
		}
		return assign(result, map[string]any{})
	case "Input.dispatchMouseEvent":
		if stringParam(params, "type") == "mousePressed" {
			b.SendCount++
		}
		return assign(result, map[string]any{})
	default:
		return fmt.Errorf("unexpected session call %s", method)
	}
}

func (b *Browser) ReadEvent(ctx context.Context) (cdp.Event, error) {
	<-ctx.Done()
	return cdp.Event{}, ctx.Err()
}

func (b *Browser) Snapshot() (
	counts map[string]int,
	trace []string,
	reloads []bool,
	insertedText string,
	insertCount int,
	sendCount int,
	targets map[string]cdp.TargetInfo,
) {
	b.mu.Lock()
	defer b.mu.Unlock()
	counts = make(map[string]int, len(b.Counts))
	for method, count := range b.Counts {
		counts[method] = count
	}
	trace = append([]string(nil), b.Trace...)
	reloads = append([]bool(nil), b.Reloads...)
	targets = make(map[string]cdp.TargetInfo, len(b.Targets))
	for targetID, target := range b.Targets {
		targets[targetID] = target
	}
	return counts, trace, reloads, b.InsertedText, b.InsertCount, b.SendCount, targets
}

func NewRuntime(
	stateDir string,
	client cdp.CommandClient,
) (*browserflow.Engine, browserflow.Journal, error) {
	journal, err := browserflow.NewFileJournal(stateDir)
	if err != nil {
		return nil, nil, err
	}
	engine, err := browserflow.New(browserflow.Config{
		Client:  client,
		Journal: journal,
		Budget: cdp.BrowserResourceBudgetOptions{
			MaxTabs:       15,
			MaxTabsSource: "test",
			MaxWindows:    5,
			BrowserMode:   "headed",
		},
		CloseTimeout:      20 * time.Millisecond,
		ClosePollInterval: time.Millisecond,
		Now:               FixedNow,
	})
	if err != nil {
		return nil, nil, err
	}
	return engine, journal, nil
}

func FixedNow() time.Time {
	return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
}

func (b *Browser) record(method string) {
	b.Counts[method]++
	b.Trace = append(b.Trace, method)
}

func assign(destination any, value any) error {
	if destination == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, destination)
}

func stringParam(params any, name string) string {
	raw, _ := json.Marshal(params)
	values := map[string]any{}
	_ = json.Unmarshal(raw, &values)
	value, _ := values[name].(string)
	return value
}

func boolParam(params any, name string) bool {
	raw, _ := json.Marshal(params)
	values := map[string]any{}
	_ = json.Unmarshal(raw, &values)
	value, _ := values[name].(bool)
	return value
}
