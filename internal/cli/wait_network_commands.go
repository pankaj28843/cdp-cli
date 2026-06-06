package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	networkWaitKindRequest  = "request"
	networkWaitKindResponse = "response"
	networkWaitKindIdle     = "network-idle"
)

type networkWaitCriteria struct {
	URL          string
	URLContains  string
	Method       string
	ResourceType string
	Status       int
	StatusMin    int
	StatusMax    int
	StatusSet    bool
	StatusMinSet bool
	StatusMaxSet bool
}

type networkWaitObservation struct {
	Matched       bool
	Event         networkWaitEvent
	LastEvent     *networkWaitEvent
	EventCount    int
	ObservedCount int
}

type networkWaitEvent struct {
	Kind              string  `json:"kind"`
	CDPMethod         string  `json:"cdp_method"`
	RequestID         string  `json:"request_id,omitempty"`
	URL               string  `json:"url,omitempty"`
	Method            string  `json:"method,omitempty"`
	ResourceType      string  `json:"resource_type,omitempty"`
	Status            int     `json:"status,omitempty"`
	StatusText        string  `json:"status_text,omitempty"`
	MimeType          string  `json:"mime_type,omitempty"`
	Failed            bool    `json:"failed"`
	ErrorText         string  `json:"error_text,omitempty"`
	Canceled          bool    `json:"canceled,omitempty"`
	EncodedDataLength float64 `json:"encoded_data_length,omitempty"`
}

type networkIdleOptions struct {
	Idle              time.Duration
	MaxInflight       int
	IgnoreURLContains []string
	Redact            string
}

type networkIdleObservation struct {
	Matched        bool
	EventCount     int
	RequestCount   int
	CompletedCount int
	FailedCount    int
	IgnoredCount   int
	InFlight       map[string]networkWaitEvent
	LastEvent      *networkWaitEvent
	QuietSince     time.Time
	IdleFor        time.Duration
}

func (a *app) newWaitRequestCommand() *cobra.Command {
	var targetID string
	var pageURLContains string
	var titleContains string
	var url string
	var matchURL string
	var method string
	var resourceType string
	var redact string
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Wait for a matching Network.requestWillBeSent event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			criteria := networkWaitCriteria{
				URL:          url,
				URLContains:  matchURL,
				Method:       method,
				ResourceType: resourceType,
			}
			if err := normalizeNetworkWaitCriteria(&criteria); err != nil {
				return err
			}
			redactor, err := networkWaitRedactor(redact)
			if err != nil {
				return err
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, session, target, err := a.attachPageEventSession(ctx, targetID, pageURLContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			observation, err := waitForNetworkEvent(ctx, client, session.SessionID, networkWaitKindRequest, criteria)
			elapsed := time.Since(start)
			report := networkWaitReport(networkWaitKindRequest, criteria, observation, elapsed, a.effectiveNetworkWaitTimeout(), redact, redactor)
			report["target"] = pageRow(target)
			if err != nil {
				return networkWaitError(ctx, session.TargetID, networkWaitKindRequest, criteria, report, err)
			}
			return a.render(ctx, networkWaitHuman(networkWaitKindRequest, observation.Event), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&pageURLContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&url, "url", "", "exact request URL to match")
	cmd.Flags().StringVar(&matchURL, "match-url", "", "substring that the request URL must contain")
	cmd.Flags().StringVar(&method, "method", "", "HTTP method to match, such as GET or POST")
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "CDP resource type to match, such as Document, Fetch, XHR, or Script")
	cmd.Flags().StringVar(&redact, "redact", "safe", "redaction preset for returned URLs: safe or none")
	return cmd
}

func (a *app) newWaitNetworkIdleCommand() *cobra.Command {
	var targetID string
	var pageURLContains string
	var titleContains string
	var idle time.Duration
	var maxInflight int
	var ignoreURLContains []string
	var redact string
	cmd := &cobra.Command{
		Use:   "network-idle",
		Short: "Wait until observed network traffic is quiet",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := networkIdleOptions{
				Idle:              idle,
				MaxInflight:       maxInflight,
				IgnoreURLContains: ignoreURLContains,
				Redact:            redact,
			}
			if err := normalizeNetworkIdleOptions(&opts); err != nil {
				return err
			}
			redactor, err := networkWaitRedactor(opts.Redact)
			if err != nil {
				return err
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, session, target, err := a.attachPageEventSession(ctx, targetID, pageURLContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			observation, err := waitForNetworkIdle(ctx, client, session.SessionID, opts)
			elapsed := time.Since(start)
			report := networkIdleReport(observation, opts, elapsed, a.effectiveNetworkWaitTimeout(), redactor)
			report["target"] = pageRow(target)
			if err != nil {
				return networkIdleError(ctx, session.TargetID, opts, report, err)
			}
			return a.render(ctx, fmt.Sprintf("matched network-idle\tinflight=%d\tidle=%s", len(observation.InFlight), durationString(opts.Idle)), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&pageURLContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().DurationVar(&idle, "idle", 500*time.Millisecond, "quiet network window required before succeeding")
	cmd.Flags().IntVar(&maxInflight, "max-inflight", 0, "maximum tracked in-flight requests allowed during the idle window")
	cmd.Flags().StringArrayVar(&ignoreURLContains, "ignore-url-contains", nil, "ignore requests whose URL contains this substring; may be repeated")
	cmd.Flags().StringVar(&redact, "redact", "safe", "redaction preset for returned URLs: safe or none")
	return cmd
}

func (a *app) newWaitResponseCommand() *cobra.Command {
	var targetID string
	var pageURLContains string
	var titleContains string
	var url string
	var matchURL string
	var method string
	var resourceType string
	var status int
	var statusMin int
	var statusMax int
	var redact string
	cmd := &cobra.Command{
		Use:   "response",
		Short: "Wait for a matching Network.responseReceived event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			criteria := networkWaitCriteria{
				URL:          url,
				URLContains:  matchURL,
				Method:       method,
				ResourceType: resourceType,
				Status:       status,
				StatusMin:    statusMin,
				StatusMax:    statusMax,
				StatusSet:    cmd.Flags().Changed("status"),
				StatusMinSet: cmd.Flags().Changed("status-min"),
				StatusMaxSet: cmd.Flags().Changed("status-max"),
			}
			if err := normalizeNetworkWaitCriteria(&criteria); err != nil {
				return err
			}
			redactor, err := networkWaitRedactor(redact)
			if err != nil {
				return err
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, session, target, err := a.attachPageEventSession(ctx, targetID, pageURLContains, titleContains)
			if err != nil {
				return err
			}
			defer session.Close(ctx)

			start := time.Now()
			observation, err := waitForNetworkEvent(ctx, client, session.SessionID, networkWaitKindResponse, criteria)
			elapsed := time.Since(start)
			report := networkWaitReport(networkWaitKindResponse, criteria, observation, elapsed, a.effectiveNetworkWaitTimeout(), redact, redactor)
			report["target"] = pageRow(target)
			if err != nil {
				return networkWaitError(ctx, session.TargetID, networkWaitKindResponse, criteria, report, err)
			}
			return a.render(ctx, networkWaitHuman(networkWaitKindResponse, observation.Event), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&pageURLContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&url, "url", "", "exact response URL to match")
	cmd.Flags().StringVar(&matchURL, "match-url", "", "substring that the response URL must contain")
	cmd.Flags().StringVar(&method, "method", "", "HTTP method of the request to match when it was observed")
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "CDP resource type to match, such as Document, Fetch, XHR, or Script")
	cmd.Flags().IntVar(&status, "status", 0, "exact HTTP status to match")
	cmd.Flags().IntVar(&statusMin, "status-min", 0, "minimum HTTP status to match")
	cmd.Flags().IntVar(&statusMax, "status-max", 0, "maximum HTTP status to match")
	cmd.Flags().StringVar(&redact, "redact", "safe", "redaction preset for returned URLs: safe or none")
	return cmd
}

func normalizeNetworkWaitCriteria(criteria *networkWaitCriteria) error {
	criteria.URL = strings.TrimSpace(criteria.URL)
	criteria.URLContains = strings.TrimSpace(criteria.URLContains)
	criteria.Method = strings.ToUpper(strings.TrimSpace(criteria.Method))
	criteria.ResourceType = strings.TrimSpace(criteria.ResourceType)
	if criteria.StatusSet && !validHTTPStatus(criteria.Status) {
		return commandError("usage", "usage", "--status must be between 100 and 999", ExitUsage, []string{"cdp wait response --status 200 --json"})
	}
	if criteria.StatusMinSet && !validHTTPStatus(criteria.StatusMin) {
		return commandError("usage", "usage", "--status-min must be between 100 and 999", ExitUsage, []string{"cdp wait response --status-min 200 --json"})
	}
	if criteria.StatusMaxSet && !validHTTPStatus(criteria.StatusMax) {
		return commandError("usage", "usage", "--status-max must be between 100 and 999", ExitUsage, []string{"cdp wait response --status-max 399 --json"})
	}
	if criteria.StatusMinSet && criteria.StatusMaxSet && criteria.StatusMin > criteria.StatusMax {
		return commandError("usage", "usage", "--status-min must be less than or equal to --status-max", ExitUsage, []string{"cdp wait response --status-min 200 --status-max 399 --json"})
	}
	return nil
}

func normalizeNetworkIdleOptions(opts *networkIdleOptions) error {
	if opts.Idle <= 0 {
		return commandError("usage", "usage", "--idle must be positive", ExitUsage, []string{"cdp wait network-idle --idle 500ms --json"})
	}
	if opts.MaxInflight < 0 {
		return commandError("usage", "usage", "--max-inflight must be non-negative", ExitUsage, []string{"cdp wait network-idle --max-inflight 0 --json"})
	}
	filtered := opts.IgnoreURLContains[:0]
	for _, item := range opts.IgnoreURLContains {
		item = strings.TrimSpace(item)
		if item != "" {
			filtered = append(filtered, item)
		}
	}
	opts.IgnoreURLContains = filtered
	opts.Redact = artifacts.NormalizeMode(opts.Redact)
	if opts.Redact != artifacts.ModeSafe && opts.Redact != artifacts.ModeNone {
		return commandError("usage", "usage", "--redact must be safe or none", ExitUsage, []string{"cdp wait network-idle --redact safe --json"})
	}
	return nil
}

func validHTTPStatus(status int) bool {
	return status >= 100 && status <= 999
}

func networkWaitRedactor(redact string) (*artifacts.Redactor, error) {
	redact = artifacts.NormalizeMode(redact)
	if redact != artifacts.ModeSafe && redact != artifacts.ModeNone {
		return nil, commandError("usage", "usage", "--redact must be safe or none", ExitUsage, []string{"cdp wait response --redact safe --json"})
	}
	return artifacts.NewRedactor(redact), nil
}

func waitForNetworkEvent(ctx context.Context, client browserEventClient, sessionID string, kind string, criteria networkWaitCriteria) (networkWaitObservation, error) {
	if kind != networkWaitKindRequest && kind != networkWaitKindResponse {
		return networkWaitObservation{}, commandError("usage", "usage", fmt.Sprintf("unsupported network wait kind %q", kind), ExitUsage, []string{"cdp wait request --json", "cdp wait response --json"})
	}
	if err := setupNetworkEventWait(ctx, client, sessionID); err != nil {
		return networkWaitObservation{}, err
	}
	return collectNetworkEvent(ctx, client, sessionID, kind, criteria)
}

func setupNetworkEventWait(ctx context.Context, client browserEventClient, sessionID string) error {
	return client.CallSession(ctx, sessionID, "Network.enable", map[string]any{}, nil)
}

func collectNetworkEvent(ctx context.Context, client browserEventClient, sessionID string, kind string, criteria networkWaitCriteria) (networkWaitObservation, error) {
	if kind != networkWaitKindRequest && kind != networkWaitKindResponse {
		return networkWaitObservation{}, commandError("usage", "usage", fmt.Sprintf("unsupported network wait kind %q", kind), ExitUsage, []string{"cdp wait request --json", "cdp wait response --json"})
	}
	recordsByID := map[string]*networkRequest{}
	observe := func(event cdp.Event, observation *networkWaitObservation) bool {
		if event.SessionID != "" && event.SessionID != sessionID {
			return false
		}
		update, ok := networkRequestFromEvent(event)
		if !ok || update.ID == "" {
			return false
		}
		observation.EventCount++
		record, ok := recordsByID[update.ID]
		if !ok {
			copyUpdate := update
			recordsByID[update.ID] = &copyUpdate
			record = &copyUpdate
		} else {
			mergeNetworkRequest(record, update)
		}
		if event.Method != networkWaitCDPMethod(kind) {
			return false
		}
		observation.ObservedCount++
		candidate := networkWaitEventFromRequest(kind, event.Method, *record)
		observation.LastEvent = &candidate
		if !networkWaitMatches(kind, *record, criteria) {
			return false
		}
		observation.Matched = true
		observation.Event = candidate
		return true
	}

	var observation networkWaitObservation
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return observation, err
	}
	for _, event := range events {
		if observe(event, &observation) {
			return observation, nil
		}
	}
	for {
		event, err := client.ReadEvent(ctx)
		if err != nil {
			return observation, err
		}
		if observe(event, &observation) {
			return observation, nil
		}
	}
}

func waitForNetworkIdle(ctx context.Context, client browserEventClient, sessionID string, opts networkIdleOptions) (networkIdleObservation, error) {
	if err := client.CallSession(ctx, sessionID, "Network.enable", map[string]any{}, nil); err != nil {
		return networkIdleObservation{}, err
	}

	observation := networkIdleObservation{InFlight: map[string]networkWaitEvent{}}
	recordsByID := map[string]*networkRequest{}
	ignoredIDs := map[string]bool{}

	observe := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		update, ok := networkRequestFromEvent(event)
		if !ok || update.ID == "" {
			return
		}
		observation.EventCount++
		record, ok := recordsByID[update.ID]
		if !ok {
			copyUpdate := update
			recordsByID[update.ID] = &copyUpdate
			record = &copyUpdate
		} else {
			mergeNetworkRequest(record, update)
		}

		switch event.Method {
		case "Network.requestWillBeSent":
			if networkIdleURLIgnored(record.URL, opts.IgnoreURLContains) {
				ignoredIDs[record.ID] = true
				delete(observation.InFlight, record.ID)
				observation.IgnoredCount++
				return
			}
			delete(ignoredIDs, record.ID)
			observation.RequestCount++
			waitEvent := networkWaitEventFromRequest(networkWaitKindForCDPMethod(event.Method), event.Method, *record)
			observation.InFlight[record.ID] = waitEvent
			observation.LastEvent = &waitEvent
		case "Network.responseReceived":
			if ignoredIDs[record.ID] {
				return
			}
			waitEvent := networkWaitEventFromRequest(networkWaitKindForCDPMethod(event.Method), event.Method, *record)
			if _, ok := observation.InFlight[record.ID]; ok {
				observation.InFlight[record.ID] = waitEvent
			}
			observation.LastEvent = &waitEvent
		case "Network.loadingFinished", "Network.loadingFailed":
			if ignoredIDs[record.ID] {
				return
			}
			if event.Method == "Network.loadingFailed" {
				observation.FailedCount++
			} else {
				observation.CompletedCount++
			}
			delete(observation.InFlight, record.ID)
			waitEvent := networkWaitEventFromRequest(networkWaitKindForCDPMethod(event.Method), event.Method, *record)
			observation.LastEvent = &waitEvent
		}
	}

	events, err := client.DrainEvents(ctx)
	if err != nil {
		return observation, err
	}
	for _, event := range events {
		observe(event)
	}
	quietSince := time.Time{}
	refreshQuiet := func(now time.Time) {
		if len(observation.InFlight) <= opts.MaxInflight {
			if quietSince.IsZero() {
				quietSince = now
			}
			observation.QuietSince = quietSince
			observation.IdleFor = now.Sub(quietSince)
			return
		}
		quietSince = time.Time{}
		observation.QuietSince = time.Time{}
		observation.IdleFor = 0
	}

	refreshQuiet(time.Now())
	for {
		if !quietSince.IsZero() {
			remaining := opts.Idle - time.Since(quietSince)
			if remaining <= 0 {
				observation.Matched = true
				observation.IdleFor = opts.Idle
				return observation, nil
			}
			eventCtx, cancel := context.WithTimeout(ctx, remaining)
			event, err := client.ReadEvent(eventCtx)
			cancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
					observation.Matched = true
					observation.IdleFor = opts.Idle
					return observation, nil
				}
				return observation, err
			}
			observe(event)
			refreshQuiet(time.Now())
			continue
		}

		event, err := client.ReadEvent(ctx)
		if err != nil {
			return observation, err
		}
		observe(event)
		refreshQuiet(time.Now())
	}
}

func networkIdleURLIgnored(rawURL string, substrings []string) bool {
	for _, substring := range substrings {
		if strings.Contains(rawURL, substring) {
			return true
		}
	}
	return false
}

func networkWaitCDPMethod(kind string) string {
	switch kind {
	case networkWaitKindRequest:
		return "Network.requestWillBeSent"
	case networkWaitKindResponse:
		return "Network.responseReceived"
	default:
		return ""
	}
}

func networkWaitKindForCDPMethod(method string) string {
	switch method {
	case "Network.requestWillBeSent":
		return networkWaitKindRequest
	case "Network.responseReceived":
		return networkWaitKindResponse
	case "Network.loadingFinished":
		return "loading-finished"
	case "Network.loadingFailed":
		return "loading-failed"
	default:
		return ""
	}
}

func networkWaitMatches(kind string, req networkRequest, criteria networkWaitCriteria) bool {
	if criteria.URL != "" && req.URL != criteria.URL {
		return false
	}
	if criteria.URLContains != "" && !strings.Contains(req.URL, criteria.URLContains) {
		return false
	}
	if criteria.Method != "" && strings.ToUpper(req.Method) != criteria.Method {
		return false
	}
	if criteria.ResourceType != "" && !strings.EqualFold(req.ResourceType, criteria.ResourceType) {
		return false
	}
	if kind == networkWaitKindResponse {
		if criteria.StatusSet && req.Status != criteria.Status {
			return false
		}
		if criteria.StatusMinSet && req.Status < criteria.StatusMin {
			return false
		}
		if criteria.StatusMaxSet && req.Status > criteria.StatusMax {
			return false
		}
	}
	return true
}

func networkWaitEventFromRequest(kind string, cdpMethod string, req networkRequest) networkWaitEvent {
	return networkWaitEvent{
		Kind:              kind,
		CDPMethod:         cdpMethod,
		RequestID:         req.ID,
		URL:               req.URL,
		Method:            req.Method,
		ResourceType:      req.ResourceType,
		Status:            req.Status,
		StatusText:        req.StatusText,
		MimeType:          req.MimeType,
		Failed:            req.Failed,
		ErrorText:         req.ErrorText,
		Canceled:          req.Canceled,
		EncodedDataLength: req.EncodedDataLength,
	}
}

func networkIdleReport(observation networkIdleObservation, opts networkIdleOptions, elapsed time.Duration, timeout time.Duration, redactor *artifacts.Redactor) map[string]any {
	inFlight := networkIdleInFlightEvents(observation, redactor)
	wait := map[string]any{
		"kind":                networkWaitKindIdle,
		"matched":             observation.Matched,
		"idle":                durationString(opts.Idle),
		"max_inflight":        opts.MaxInflight,
		"elapsed_ms":          elapsed.Milliseconds(),
		"timeout":             durationString(timeout),
		"source":              "cdp-network-events",
		"scope":               "events observed after Network.enable",
		"event_count":         observation.EventCount,
		"request_count":       observation.RequestCount,
		"completed_count":     observation.CompletedCount,
		"failed_count":        observation.FailedCount,
		"ignored_count":       observation.IgnoredCount,
		"in_flight_count":     len(inFlight),
		"in_flight":           inFlight,
		"ignore_url_contains": opts.IgnoreURLContains,
		"redact":              opts.Redact,
		"idle_for_ms":         observation.IdleFor.Milliseconds(),
		"warnings": []string{
			"network-idle is a quiescence signal, not proof that the app is ready; prefer URL, text, selector, assertion, or response waits for success checks",
		},
		"evidence": map[string]any{
			"headers": false,
			"bodies":  false,
			"bounded": true,
		},
	}
	report := map[string]any{
		"ok":   observation.Matched,
		"wait": wait,
	}
	if observation.LastEvent != nil {
		last := redactNetworkWaitEvent(*observation.LastEvent, redactor, "last_event.url")
		report["last_event"] = last
		wait["last_event"] = last
	}
	return report
}

func networkIdleInFlightEvents(observation networkIdleObservation, redactor *artifacts.Redactor) []networkWaitEvent {
	if len(observation.InFlight) == 0 {
		return []networkWaitEvent{}
	}
	ids := make([]string, 0, len(observation.InFlight))
	for id := range observation.InFlight {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]networkWaitEvent, 0, len(ids))
	for _, id := range ids {
		out = append(out, redactNetworkWaitEvent(observation.InFlight[id], redactor, "in_flight.url"))
	}
	return out
}

func networkWaitReport(kind string, criteria networkWaitCriteria, observation networkWaitObservation, elapsed time.Duration, timeout time.Duration, redact string, redactor *artifacts.Redactor) map[string]any {
	wait := map[string]any{
		"kind":           kind,
		"matched":        observation.Matched,
		"criteria":       networkWaitCriteriaReport(criteria),
		"elapsed_ms":     elapsed.Milliseconds(),
		"timeout":        durationString(timeout),
		"source":         "cdp-network-events",
		"cdp_method":     networkWaitCDPMethod(kind),
		"event_count":    observation.EventCount,
		"observed_count": observation.ObservedCount,
		"redact":         artifacts.NormalizeMode(redact),
		"evidence": map[string]any{
			"headers": false,
			"bodies":  false,
			"bounded": true,
		},
	}
	report := map[string]any{
		"ok":   observation.Matched,
		"wait": wait,
	}
	if observation.Matched {
		event := redactNetworkWaitEvent(observation.Event, redactor, "event.url")
		report["event"] = event
		wait["event"] = event
	}
	if observation.LastEvent != nil {
		last := redactNetworkWaitEvent(*observation.LastEvent, redactor, "last_event.url")
		report["last_event"] = last
		wait["last_event"] = last
	}
	return report
}

func networkWaitCriteriaReport(criteria networkWaitCriteria) map[string]any {
	out := map[string]any{}
	if criteria.URL != "" {
		out["url"] = criteria.URL
	}
	if criteria.URLContains != "" {
		out["url_contains"] = criteria.URLContains
	}
	if criteria.Method != "" {
		out["method"] = criteria.Method
	}
	if criteria.ResourceType != "" {
		out["resource_type"] = criteria.ResourceType
	}
	if criteria.StatusSet {
		out["status"] = criteria.Status
	}
	if criteria.StatusMinSet {
		out["status_min"] = criteria.StatusMin
	}
	if criteria.StatusMaxSet {
		out["status_max"] = criteria.StatusMax
	}
	return out
}

func redactNetworkWaitEvent(event networkWaitEvent, redactor *artifacts.Redactor, field string) networkWaitEvent {
	event.URL = redactor.URL(event.URL, field)
	return event
}

func networkIdleError(ctx context.Context, targetID string, opts networkIdleOptions, report map[string]any, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return commandErrorWithData(
			"timeout",
			"timeout",
			fmt.Sprintf("wait network-idle did not observe an idle %s window for target %s: %v", durationString(opts.Idle), targetID, context.DeadlineExceeded),
			ExitTimeout,
			networkIdleRemediations(opts),
			report,
		)
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return err
	}
	return commandError(
		"connection_failed",
		"connection",
		fmt.Sprintf("wait network-idle target %s: %v", targetID, err),
		ExitConnection,
		[]string{"cdp pages --json", "cdp doctor --json"},
	)
}

func networkIdleRemediations(opts networkIdleOptions) []string {
	waitCommand := "cdp wait network-idle --idle " + durationString(opts.Idle)
	if opts.MaxInflight != 0 {
		waitCommand += " --max-inflight " + fmt.Sprint(opts.MaxInflight)
	}
	for _, item := range opts.IgnoreURLContains {
		waitCommand += " --ignore-url-contains " + shellQuote(item)
	}
	return []string{
		waitCommand + " --timeout 15s --json",
		"cdp wait response --match-url /api --timeout 15s --json",
		"cdp wait selector main --timeout 15s --json",
		"cdp network --wait 5s --json",
	}
}

func networkWaitError(ctx context.Context, targetID string, kind string, criteria networkWaitCriteria, report map[string]any, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return commandErrorWithData(
			"timeout",
			"timeout",
			fmt.Sprintf("wait %s did not observe matching %s for target %s: %v", kind, networkWaitCDPMethod(kind), targetID, context.DeadlineExceeded),
			ExitTimeout,
			networkWaitRemediations(kind, criteria),
			report,
		)
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return err
	}
	return commandError(
		"connection_failed",
		"connection",
		fmt.Sprintf("wait %s target %s: %v", kind, targetID, err),
		ExitConnection,
		[]string{"cdp pages --json", "cdp doctor --json"},
	)
}

func networkWaitRemediations(kind string, criteria networkWaitCriteria) []string {
	waitCommand := "cdp wait " + kind
	if criteria.URL != "" {
		waitCommand += " --url " + shellQuote(criteria.URL)
	}
	if criteria.URLContains != "" {
		waitCommand += " --match-url " + shellQuote(criteria.URLContains)
	}
	if criteria.Method != "" {
		waitCommand += " --method " + shellQuote(criteria.Method)
	}
	if criteria.ResourceType != "" {
		waitCommand += " --resource-type " + shellQuote(criteria.ResourceType)
	}
	if kind == networkWaitKindResponse {
		if criteria.StatusSet {
			waitCommand += " --status " + fmt.Sprint(criteria.Status)
		}
		if criteria.StatusMinSet {
			waitCommand += " --status-min " + fmt.Sprint(criteria.StatusMin)
		}
		if criteria.StatusMaxSet {
			waitCommand += " --status-max " + fmt.Sprint(criteria.StatusMax)
		}
	}
	return []string{
		waitCommand + " --timeout 15s --json",
		"cdp network --wait 5s --json",
		"cdp events tap --enable network --match " + networkWaitCDPMethod(kind) + " --duration 5s --json",
	}
}

func networkWaitHuman(kind string, event networkWaitEvent) string {
	status := ""
	if kind == networkWaitKindResponse && event.Status > 0 {
		status = "\t" + fmt.Sprint(event.Status)
	}
	return fmt.Sprintf("matched %s\t%s\t%s%s", kind, event.Method, event.URL, status)
}

func (a *app) effectiveNetworkWaitTimeout() time.Duration {
	if a.opts.timeout > 0 {
		return a.opts.timeout
	}
	return 10 * time.Second
}
