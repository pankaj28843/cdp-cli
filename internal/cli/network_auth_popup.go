package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

// The browser protocol cannot filter new page targets by opener before pausing
// them. This one-use root handshake avoids browser-wide auto-attach entirely.
type httpAuthPopupSupervisor struct {
	client             browserEventClient
	root, session, key string
	child              *cdp.PageSession
	enabled, paused    bool
	existing           map[string]bool
}

func newHTTPAuthPopupSupervisor(client browserEventClient, root, session string) *httpAuthPopupSupervisor {
	return &httpAuthPopupSupervisor{client: client, root: root, session: session, key: "__cdpPopupAuth" + strconv.FormatInt(time.Now().UnixNano(), 10)}
}

func popupPrearmInstallScript(key string) string {
	k, _ := json.Marshal(key)
	return `(()=>{
 const key=` + string(k) + `;
 if(window.__cdpPopupAuthOwner)return false;
 const original=window.open;
 const state={original,popup:null,pending:false,armed:false,wrapper:null};
 window[key]=state;window.__cdpPopupAuthOwner=key;
 state.wrapper=function cdpPrearmPopup(url,name,features){
   window.open=original;
   if(/(?:^|,)\s*(?:noopener|noreferrer)(?:\s*=\s*(?:yes|1|true))?\s*(?:,|$)/i.test(features||''))throw new Error('Popup opener required');
   state.popup=original.call(window,'about:blank',key,features);
   if(!state.popup)throw new Error('Prearmed popup blocked');
   state.pending=true;
   debugger;
   state.pending=false;
   if(!state.armed){state.popup.close();throw new Error('Popup authentication unavailable');}
   if(name)state.popup.name=name;
   if(url&&url!=='about:blank')state.popup.location.replace(url);
   return state.popup;
 };
 window.open=state.wrapper;
 return true;
})()`
}

func (s *httpAuthPopupSupervisor) enable(ctx context.Context) error {
	var targets struct {
		Targets []cdp.TargetInfo `json:"targetInfos"`
	}
	if err := s.client.Call(ctx, "Target.getTargets", map[string]any{}, &targets); err != nil {
		return fmt.Errorf("snapshot existing targets failed")
	}
	s.existing = make(map[string]bool)
	for _, target := range targets.Targets {
		s.existing[target.TargetID] = true
	}

	if err := s.client.CallSession(ctx, s.session, "Debugger.enable", map[string]any{}, nil); err != nil {
		return err
	}
	s.enabled = true
	var result struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := s.client.CallSession(ctx, s.session, "Runtime.evaluate", map[string]any{"expression": popupPrearmInstallScript(s.key), "returnByValue": true}, &result); err != nil {
		return err
	}
	if !result.Result.Value {
		return fmt.Errorf("popup prearm root already supervised")
	}
	return nil
}

func (s *httpAuthPopupSupervisor) attach(ctx context.Context, event cdp.Event) error {
	if event.SessionID != s.session || event.Method != "Debugger.paused" {
		return nil
	}
	var p struct {
		Frames []struct {
			ID   string `json:"callFrameId"`
			Name string `json:"functionName"`
		} `json:"callFrames"`
	}
	if json.Unmarshal(event.Params, &p) != nil || len(p.Frames) == 0 || p.Frames[0].ID == "" {
		return fmt.Errorf("invalid root pause event")
	}
	if p.Frames[0].Name != "cdpPrearmPopup" {
		return fmt.Errorf("unexpected owned-root debugger pause")
	}
	s.paused = true
	key, _ := json.Marshal(s.key)
	var proof struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := s.client.CallSession(ctx, s.session, "Debugger.evaluateOnCallFrame", map[string]any{"callFrameId": p.Frames[0].ID, "expression": "Boolean(window[" + string(key) + "]?.pending && window[" + string(key) + "]?.popup && !window[" + string(key) + "].popup.closed)", "returnByValue": true}, &proof); err != nil || !proof.Result.Value {
		return fmt.Errorf("popup ownership pause proof failed")
	}
	if s.child != nil {
		return fmt.Errorf("popup prearm already consumed")
	}
	var targets struct {
		Targets []struct {
			cdp.TargetInfo
			Opener string `json:"openerId"`
		} `json:"targetInfos"`
	}
	if err := s.client.Call(ctx, "Target.getTargets", map[string]any{}, &targets); err != nil {
		return fmt.Errorf("discover owned blank popup failed")
	}
	candidates := []string{}
	for _, target := range targets.Targets {
		if target.Type == "page" && target.Opener == s.root && target.URL == "about:blank" && !s.existing[target.TargetID] {
			candidates = append(candidates, target.TargetID)
		}
	}
	if len(candidates) != 1 {
		return fmt.Errorf("owned blank popup count is not one")
	}
	child, err := cdp.AttachToTargetWithClient(ctx, s.client, candidates[0], nil)
	if err != nil {
		return fmt.Errorf("attach owned blank popup failed")
	}
	s.child = child
	var owned struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := s.client.CallSession(ctx, child.SessionID, "Runtime.evaluate", map[string]any{"expression": "Boolean(window.opener && window.opener[" + string(key) + "]?.popup === window)", "returnByValue": true}, &owned); err != nil || !owned.Result.Value {
		return fmt.Errorf("blank popup window ownership proof failed")
	}
	if err := s.client.CallSession(ctx, child.SessionID, "Fetch.enable", map[string]any{"handleAuthRequests": true, "patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}}}, nil); err != nil {
		return fmt.Errorf("enable popup HTTP authentication failed")
	}
	var armed struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := s.client.CallSession(ctx, s.session, "Debugger.evaluateOnCallFrame", map[string]any{"callFrameId": p.Frames[0].ID, "expression": "window[" + string(key) + "].armed=true", "returnByValue": true}, &armed); err != nil || !armed.Result.Value {
		return fmt.Errorf("confirm popup authentication readiness failed")
	}
	if err := s.client.CallSession(ctx, s.session, "Debugger.resume", map[string]any{}, nil); err != nil {
		return fmt.Errorf("resume prearmed popup root failed")
	}
	s.paused = false
	return nil
}

func (s *httpAuthPopupSupervisor) cleanup() bool {
	if !s.enabled && s.child == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	complete := true
	key, _ := json.Marshal(s.key)
	if s.enabled {
		// Unarmed cancellation closes only the window created by our own hook.
		script := `(()=>{const key=` + string(key) + `,s=window[key];if(!s)return; if(window.open===s.wrapper)window.open=s.original; if(s.pending&&!s.armed&&s.popup&&!s.popup.closed)s.popup.close(); delete window[key];if(window.__cdpPopupAuthOwner===key)delete window.__cdpPopupAuthOwner;})()`
		if s.client.CallSession(ctx, s.session, "Runtime.evaluate", map[string]any{"expression": script}, nil) != nil {
			complete = false
		}
		if s.paused {
			if s.client.CallSession(ctx, s.session, "Debugger.resume", map[string]any{}, nil) != nil {
				complete = false
			}
			s.paused = false
		}
		if s.client.CallSession(ctx, s.session, "Debugger.disable", map[string]any{}, nil) != nil {
			complete = false
		}
		s.enabled = false
	}
	if s.child != nil && !s.childGone(ctx) {
		if s.client.CallSession(ctx, s.child.SessionID, "Fetch.disable", map[string]any{}, nil) != nil {
			complete = false
		}
		if !closeHTTPAuthSession(s.child.Close) {
			complete = false
		}
	}
	s.child = nil
	return complete
}

type popupAuthEvent struct {
	event   cdp.Event
	session string
	err     error
}

func (s *httpAuthPopupSupervisor) collect(ctx context.Context, duration time.Duration, origin, scheme, userEnv, passEnv string, lookup func(string) (string, bool)) (map[string]int, error) {
	state := newHTTPAuthState(origin, scheme, userEnv, passEnv, lookup)
	state.counts["popups_armed"] = 0
	eventCtx, cancel := context.WithTimeout(ctx, duration)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	events := make(chan popupAuthEvent, 16)
	startReader := func(session string) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				event, err := s.client.ReadSessionEvent(eventCtx, session)
				select {
				case events <- popupAuthEvent{event: event, session: session, err: err}:
				case <-eventCtx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}()
	}
	startReader(s.session)
	childReader := false
	for {
		select {
		case <-eventCtx.Done():
			if ctx.Err() != nil {
				return state.counts, fmt.Errorf("popup authentication observation canceled")
			}
			return state.counts, nil
		case item := <-events:
			if item.err != nil {
				if eventCtx.Err() != nil || errors.Is(item.err, context.DeadlineExceeded) {
					continue
				}
				// Successful OAuth callbacks normally close the child.
				if s.child != nil && item.session == s.child.SessionID && s.childGone(eventCtx) {
					continue
				}
				return state.counts, fmt.Errorf("popup authentication event observation failed")
			}
			if item.event.SessionID != s.session && (s.child == nil || item.event.SessionID != s.child.SessionID) {
				continue
			}
			if item.event.Method == "Debugger.paused" {
				if err := s.attach(eventCtx, item.event); err != nil {
					return state.counts, err
				}
				if s.child != nil && !childReader {
					startReader(s.child.SessionID)
					childReader = true
					state.counts["popups_armed"] = 1
				}
			} else if err := state.handle(eventCtx, s.client, item.event.SessionID, item.event); err != nil {
				return state.counts, err
			}
		}
	}
}

// Only authoritative target absence permits a naturally closed OAuth child.
func (s *httpAuthPopupSupervisor) childGone(ctx context.Context) bool {
	var targets struct {
		Targets []cdp.TargetInfo `json:"targetInfos"`
	}
	if err := s.client.Call(ctx, "Target.getTargets", map[string]any{}, &targets); err != nil {
		return false
	}
	for _, target := range targets.Targets {
		if target.TargetID == s.child.TargetID {
			return false
		}
	}
	return true
}
