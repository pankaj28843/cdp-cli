package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	popupWaitKind            = "popup"
	popupWaitCDPMethodCreate = "Target.targetCreated"
	popupWaitCDPMethodChange = "Target.targetInfoChanged"
)

type popupWaitCriteria struct {
	OpenerID    string `json:"opener_id,omitempty"`
	URLContains string `json:"url_contains,omitempty"`
	Title       string `json:"title,omitempty"`
}

type popupWaitTarget struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Title            string `json:"title,omitempty"`
	URL              string `json:"url,omitempty"`
	Attached         bool   `json:"attached"`
	BrowserContextID string `json:"browser_context_id,omitempty"`
	OpenerID         string `json:"opener_id,omitempty"`
	CanAccessOpener  bool   `json:"can_access_opener,omitempty"`
	OpenerFrameID    string `json:"opener_frame_id,omitempty"`
	ParentID         string `json:"parent_id,omitempty"`
}

type popupWaitEvent struct {
	Target        popupWaitTarget `json:"target"`
	CDPMethod     string          `json:"cdp_method"`
	NewTarget     bool            `json:"new_target"`
	OpenerMatched bool            `json:"opener_matched"`
}

type popupWaitObservation struct {
	Matched       bool
	EventCount    int
	ObservedCount int
	Event         *popupWaitEvent
	LastEvent     *popupWaitEvent
}

func (a *app) newWaitPopupCommand() *cobra.Command {
	var targetID string
	var pageURLContains string
	var titleContains string
	var matchURL string
	var matchTitle string
	cmd := &cobra.Command{
		Use:   "popup",
		Short: "Wait for a page popup or new tab opened by the selected page",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, closeClient, err := a.browserEventCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					a.connectionRemediationCommands(),
				)
			}
			defer closeClient(ctx)

			opener, err := a.resolvePageTargetWithClient(ctx, client, targetID, pageURLContains, titleContains)
			if err != nil {
				return err
			}
			criteria := popupWaitCriteria{
				OpenerID:    opener.TargetID,
				URLContains: strings.TrimSpace(matchURL),
				Title:       strings.TrimSpace(matchTitle),
			}
			baselineTargets, err := popupWaitListTargets(ctx, client)
			if err != nil {
				return popupWaitConnectionError(opener.TargetID, err)
			}

			start := time.Now()
			observation, teardown, err := waitForPopupEvent(ctx, client, baselineTargets, criteria)
			elapsed := time.Since(start)
			if teardown != nil {
				teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer teardownCancel()
				_ = teardown(teardownCtx)
			}
			report := popupWaitReport(observation, criteria, opener, elapsed, a.effectiveNetworkWaitTimeout(), len(baselineTargets))
			if err != nil {
				return popupWaitError(ctx, opener.TargetID, criteria, report, err)
			}
			return a.render(ctx, fmt.Sprintf("matched popup\t%s", observation.Event.Target.ID), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "opener page target id or unique prefix")
	cmd.Flags().StringVar(&pageURLContains, "url-contains", "", "use the first opener page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first opener page whose title contains this text")
	cmd.Flags().StringVar(&matchURL, "match-url", "", "substring that the popup URL must contain")
	cmd.Flags().StringVar(&matchTitle, "match-title", "", "substring that the popup title must contain")
	return cmd
}

func popupWaitListTargets(ctx context.Context, client cdp.CommandClient) ([]popupWaitTarget, error) {
	var result struct {
		TargetInfos []struct {
			TargetID         string `json:"targetId"`
			Type             string `json:"type"`
			Title            string `json:"title"`
			URL              string `json:"url"`
			Attached         bool   `json:"attached"`
			BrowserContextID string `json:"browserContextId"`
			OpenerID         string `json:"openerId"`
			CanAccessOpener  bool   `json:"canAccessOpener"`
			OpenerFrameID    string `json:"openerFrameId"`
			ParentID         string `json:"parentId"`
		} `json:"targetInfos"`
	}
	if err := client.Call(ctx, "Target.getTargets", map[string]any{}, &result); err != nil {
		return nil, err
	}
	targets := make([]popupWaitTarget, 0, len(result.TargetInfos))
	for _, target := range result.TargetInfos {
		targets = append(targets, popupWaitTarget{
			ID:               target.TargetID,
			Type:             target.Type,
			Title:            target.Title,
			URL:              target.URL,
			Attached:         target.Attached,
			BrowserContextID: target.BrowserContextID,
			OpenerID:         target.OpenerID,
			CanAccessOpener:  target.CanAccessOpener,
			OpenerFrameID:    target.OpenerFrameID,
			ParentID:         target.ParentID,
		})
	}
	return targets, nil
}

func waitForPopupEvent(ctx context.Context, client browserEventClient, baselineTargets []popupWaitTarget, criteria popupWaitCriteria) (popupWaitObservation, func(context.Context) error, error) {
	teardown, err := enablePopupTargetDiscovery(ctx, client)
	if err != nil {
		return popupWaitObservation{}, nil, err
	}
	observation, err := collectPopupEvent(ctx, client, baselineTargets, criteria)
	return observation, teardown, err
}

func enablePopupTargetDiscovery(ctx context.Context, client cdp.CommandClient) (func(context.Context) error, error) {
	if err := client.Call(ctx, "Target.setDiscoverTargets", map[string]any{"discover": true}, nil); err != nil {
		return nil, err
	}
	teardown := func(teardownCtx context.Context) error {
		return client.Call(teardownCtx, "Target.setDiscoverTargets", map[string]any{"discover": false}, nil)
	}
	return teardown, nil
}

func collectPopupEvent(ctx context.Context, client browserEventClient, baselineTargets []popupWaitTarget, criteria popupWaitCriteria) (popupWaitObservation, error) {
	baseline := map[string]bool{}
	for _, target := range baselineTargets {
		baseline[target.ID] = true
	}

	observation := popupWaitObservation{}
	observe := func(event cdp.Event) bool {
		popupEvent, ok := popupWaitEventFromCDP(event)
		if !ok {
			return false
		}
		if popupEvent.Target.Type != "page" {
			return false
		}
		observation.EventCount++
		popupEvent.NewTarget = !baseline[popupEvent.Target.ID]
		popupEvent.OpenerMatched = criteria.OpenerID != "" && popupEvent.Target.OpenerID == criteria.OpenerID
		if !popupEvent.NewTarget {
			return false
		}
		if criteria.OpenerID != "" && popupEvent.Target.OpenerID != "" && popupEvent.Target.OpenerID != criteria.OpenerID {
			return false
		}
		observation.ObservedCount++
		observation.LastEvent = &popupEvent
		if !popupWaitMatches(popupEvent, criteria) {
			return false
		}
		observation.Event = &popupEvent
		observation.Matched = true
		return true
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return observation, err
	}
	for _, event := range events {
		if observe(event) {
			return observation, nil
		}
	}
	for {
		event, err := client.ReadEvent(ctx)
		if err != nil {
			return observation, err
		}
		if observe(event) {
			return observation, nil
		}
	}
}

func popupWaitEventFromCDP(event cdp.Event) (popupWaitEvent, bool) {
	if event.Method != popupWaitCDPMethodCreate && event.Method != popupWaitCDPMethodChange {
		return popupWaitEvent{}, false
	}
	var params struct {
		TargetInfo struct {
			TargetID         string `json:"targetId"`
			Type             string `json:"type"`
			Title            string `json:"title"`
			URL              string `json:"url"`
			Attached         bool   `json:"attached"`
			BrowserContextID string `json:"browserContextId"`
			OpenerID         string `json:"openerId"`
			CanAccessOpener  bool   `json:"canAccessOpener"`
			OpenerFrameID    string `json:"openerFrameId"`
			ParentID         string `json:"parentId"`
		} `json:"targetInfo"`
	}
	if err := json.Unmarshal(event.Params, &params); err != nil {
		return popupWaitEvent{}, false
	}
	if params.TargetInfo.TargetID == "" {
		return popupWaitEvent{}, false
	}
	return popupWaitEvent{
		Target: popupWaitTarget{
			ID:               params.TargetInfo.TargetID,
			Type:             params.TargetInfo.Type,
			Title:            params.TargetInfo.Title,
			URL:              params.TargetInfo.URL,
			Attached:         params.TargetInfo.Attached,
			BrowserContextID: params.TargetInfo.BrowserContextID,
			OpenerID:         params.TargetInfo.OpenerID,
			CanAccessOpener:  params.TargetInfo.CanAccessOpener,
			OpenerFrameID:    params.TargetInfo.OpenerFrameID,
			ParentID:         params.TargetInfo.ParentID,
		},
		CDPMethod: event.Method,
	}, true
}

func popupWaitMatches(event popupWaitEvent, criteria popupWaitCriteria) bool {
	if criteria.URLContains != "" && !strings.Contains(strings.ToLower(event.Target.URL), strings.ToLower(criteria.URLContains)) {
		return false
	}
	if criteria.Title != "" && !strings.Contains(strings.ToLower(event.Target.Title), strings.ToLower(criteria.Title)) {
		return false
	}
	return true
}

func popupWaitReport(observation popupWaitObservation, criteria popupWaitCriteria, opener cdp.TargetInfo, elapsed time.Duration, timeout time.Duration, baselineCount int) map[string]any {
	wait := map[string]any{
		"kind":           popupWaitKind,
		"matched":        observation.Matched,
		"criteria":       criteria,
		"cdp_methods":    []string{popupWaitCDPMethodCreate, popupWaitCDPMethodChange},
		"elapsed_ms":     elapsed.Milliseconds(),
		"timeout":        durationString(timeout),
		"source":         "cdp-browser-events",
		"scope":          "Target.setDiscoverTargets with baseline target filtering",
		"baseline_count": baselineCount,
		"event_count":    observation.EventCount,
		"observed_count": observation.ObservedCount,
		"evidence": map[string]any{
			"headers": false,
			"bodies":  false,
			"bounded": true,
		},
	}
	report := map[string]any{
		"ok":            observation.Matched,
		"target":        pageRow(opener),
		"opener":        pageRow(opener),
		"wait":          wait,
		"next_commands": popupWaitNextCommands(observation.Event),
	}
	if observation.Event != nil {
		report["popup"] = observation.Event
		wait["event"] = observation.Event
		if criteria.OpenerID != "" && observation.Event.Target.OpenerID == "" {
			wait["warnings"] = []string{"matched popup target did not include opener_id; another new page could match if multiple pages open concurrently"}
		}
	}
	if observation.LastEvent != nil {
		report["last_event"] = observation.LastEvent
		wait["last_event"] = observation.LastEvent
	}
	return report
}

func popupWaitError(ctx context.Context, openerID string, criteria popupWaitCriteria, report map[string]any, err error) error {
	if popupWaitDeadlineExceeded(ctx, err) || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return commandErrorWithData(
			"timeout",
			"timeout",
			fmt.Sprintf("wait popup did not observe a matching popup for opener %s: %v", openerID, context.DeadlineExceeded),
			ExitTimeout,
			popupWaitRemediations(criteria),
			report,
		)
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return err
	}
	return popupWaitConnectionError(openerID, err)
}

func popupWaitDeadlineExceeded(ctx context.Context, err error) bool {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if err == nil {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, context.DeadlineExceeded.Error()) || strings.Contains(errText, "i/o timeout")
}

func popupWaitConnectionError(openerID string, err error) error {
	return commandError("connection_failed", "connection", fmt.Sprintf("wait popup opener %s: %v", openerID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
}

func popupWaitRemediations(criteria popupWaitCriteria) []string {
	waitCommand := "cdp wait popup"
	if criteria.URLContains != "" {
		waitCommand += " --match-url " + shellQuote(criteria.URLContains)
	}
	if criteria.Title != "" {
		waitCommand += " --match-title " + shellQuote(criteria.Title)
	}
	return []string{
		waitCommand + " --timeout 15s --json",
		"cdp protocol exec Target.getTargets --json",
		"cdp pages --json",
	}
}

func popupWaitNextCommands(event *popupWaitEvent) []string {
	if event != nil && event.Target.ID != "" {
		return []string{
			"cdp page select --target " + shellQuote(event.Target.ID) + " --json",
			"cdp wait load-state load --target " + shellQuote(event.Target.ID) + " --json",
			"cdp snapshot --target " + shellQuote(event.Target.ID) + " --json",
		}
	}
	return []string{
		"cdp pages --json",
		"cdp protocol exec Target.getTargets --json",
	}
}
