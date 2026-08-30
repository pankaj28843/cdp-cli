package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type eventStreamOptions struct {
	targetID      string
	urlContains   string
	titleContains string
	targetIndex   int
	enable        string
	match         string
	duration      time.Duration
	maxEvents     int
	readyFile     string
}

const (
	eventStreamCallTimeout              = 10 * time.Second
	eventStreamReadTimeout              = 500 * time.Millisecond
	eventStreamLivenessPollInterval     = 15 * time.Second
	eventStreamLivenessFailureThreshold = 2
)

func (a *app) newEventsStreamCommand() *cobra.Command {
	var options eventStreamOptions
	cmd := &cobra.Command{
		Use:   "stream",
		Short: "Stream session-scoped CDP events as JSONL",
		Long:  "Stream session-scoped CDP events as JSONL. The stream checks whether its daemon runtime registration is still current before periodically sending a read-only Runtime.evaluate heartbeat on the already attached exact page session; a definitive runtime replacement retires immediately, while ambiguous state follows the two-strike heartbeat policy.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runEventsStream(cmd, options)
		},
	}
	cmd.Flags().StringVar(&options.targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&options.urlContains, "url-contains", "", "use the unique page whose URL contains this text")
	cmd.Flags().StringVar(&options.titleContains, "title-contains", "", "use the unique page whose title contains this text")
	cmd.Flags().IntVar(&options.targetIndex, "target-index", 0, "select a 1-based page target index")
	cmd.Flags().StringVar(&options.enable, "enable", "page,network,runtime,log", "comma-separated CDP target domains to enable (for example page,network,runtime,log,DOM,Performance)")
	cmd.Flags().StringVar(&options.match, "match", "", "comma-separated event method names to keep")
	cmd.Flags().DurationVar(&options.duration, "duration", 0, "maximum stream duration; 0 waits for count, EOF, cancellation, or the global timeout")
	cmd.Flags().IntVar(&options.maxEvents, "max-events", 0, "maximum matching events; 0 disables the count limit")
	cmd.Flags().StringVar(&options.readyFile, "ready-file", "", "exclusive owner-only readiness artifact written after exact-target attach and domain enable")
	return cmd
}

func (a *app) runEventsStream(cmd *cobra.Command, options eventStreamOptions) error {
	if !a.opts.json {
		return commandError("json_required", "usage", "cdp events stream emits JSONL; pass --json", ExitUsage, []string{"cdp events stream --duration 30s --json"})
	}
	if a.opts.jq != "" {
		return commandError("jq_unsupported", "usage", "--jq cannot filter a multi-record JSONL stream; filter records downstream", ExitUsage, []string{"cdp events stream --json | jq -c 'select(.type == \"event\")'"})
	}
	if options.duration < 0 || options.maxEvents < 0 {
		return commandError("usage", "usage", "--duration and --max-events must be non-negative", ExitUsage, []string{"cdp events stream --duration 30s --max-events 50 --json"})
	}
	if err := validatePageTargetIndexSelector(cmd, options.targetID, options.urlContains, options.titleContains, options.targetIndex); err != nil {
		return err
	}
	if err := validateEventDomains(options.enable); err != nil {
		return err
	}
	subscriptions, err := parseEventSubscriptions(options.match)
	if err != nil {
		return err
	}

	setupCtx, setupCancel := a.eventsStreamSetupContext(cmd)
	defer setupCancel()
	client, session, target, err := a.attachPageEventSessionWithIndex(setupCtx, options.targetID, options.urlContains, options.titleContains, options.targetIndex)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(cleanupCtx)
	}()

	enabledDomains, err := parseEventDomains(options.enable)
	if err != nil {
		return err
	}
	for _, domain := range enabledDomains.names() {
		if err := enableEventDomain(setupCtx, client, session.SessionID, domain); err != nil {
			return commandError("collector_enable_failed", "connection", fmt.Sprintf("enable %s for target %s: %v", domain, target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
		}
	}
	removeReady, err := publishCollectorReadiness(options.readyFile, target.TargetID, session.SessionID, enabledDomains.names())
	if err != nil {
		return collectorReadinessError(err)
	}
	defer removeReady()

	writer := eventStreamWriter{out: a.out}
	if err := writer.write(map[string]any{
		"ok":     true,
		"type":   "ready",
		"target": pageRow(target),
		"stream": eventStreamMetadata(target, session.SessionID, options, subscriptions, enabledDomains),
	}); err != nil {
		return fmt.Errorf("write event stream readiness: %w", err)
	}

	streamCtx, cancelStream := a.eventsStreamContext(cmd)
	defer cancelStream()
	eventCount := 0
	foreignEventsDropped := 0
	emitEvent := func(event cdp.Event) (bool, error) {
		if event.SessionID != "" && event.SessionID != session.SessionID {
			foreignEventsDropped++
			return false, nil
		}
		if !subscriptions.matches(event.Method) {
			return false, nil
		}
		if err := writer.write(map[string]any{"ok": true, "type": "event", "event": event}); err != nil {
			return false, fmt.Errorf("write event stream event: %w", err)
		}
		eventCount++
		return options.maxEvents > 0 && eventCount >= options.maxEvents, nil
	}
	var livenessStop *eventStreamLivenessResult
	finish := func(reason string, exit int) error {
		streamMetadata := eventStreamMetadata(target, session.SessionID, options, subscriptions, enabledDomains)
		if livenessStop != nil {
			streamMetadata["liveness"] = eventStreamLivenessMetadata(livenessStop)
		}
		if err := writer.write(map[string]any{
			"ok":                     true,
			"type":                   "stopped",
			"reason":                 reason,
			"event_count":            eventCount,
			"foreign_events_dropped": foreignEventsDropped,
			"truncated":              options.maxEvents > 0 && eventCount >= options.maxEvents,
			"stream":                 streamMetadata,
		}); err != nil {
			return fmt.Errorf("write event stream stop: %w", err)
		}
		if exit != ExitOK {
			return &renderedResultExit{ExitCode: exit}
		}
		return nil
	}
	finishFailure := func(err error) error {
		streamErr := eventStreamFailure(err)
		if writeErr := writer.write(eventStreamErrorRecord(streamErr)); writeErr != nil {
			return fmt.Errorf("write event stream error: %w", writeErr)
		}
		return finish("error", streamErr.ExitCode)
	}

	drainCtx, cancelDrain := eventStreamCallContext(streamCtx)
	initialEvents, err := client.DrainEvents(drainCtx)
	cancelDrain()
	if err != nil {
		return finishFailure(fmt.Errorf("drain initial events: %w", err))
	}
	for _, event := range initialEvents {
		limit, emitErr := emitEvent(event)
		if emitErr != nil {
			return emitErr
		}
		if limit {
			return finish("max_events", ExitOK)
		}
	}

	registrationCheck := a.eventStreamRuntimeRegistrationCheck(client)
	livenessCh := pumpEventStreamLivenessWithRegistration(streamCtx, client, session.SessionID, eventStreamLivenessPollInterval, eventStreamLivenessFailureThreshold, registrationCheck)
	eventCh := pumpEventStream(streamCtx, client)
	commandCh := readEventStreamCommands(streamCtx, cmd.InOrStdin())
	var durationCh <-chan time.Time
	var durationTimer *time.Timer
	if options.duration > 0 {
		durationTimer = time.NewTimer(options.duration)
		defer durationTimer.Stop()
		durationCh = durationTimer.C
	}

	for {
		select {
		case <-streamCtx.Done():
			reason, exit := eventStreamContextStop(streamCtx)
			return finish(reason, exit)
		case <-durationCh:
			return finish("duration", ExitOK)
		case result, ok := <-livenessCh:
			if !ok {
				livenessCh = nil
				continue
			}
			if result.retired {
				livenessStop = &result
				return finish("liveness", ExitOK)
			}
		case input, ok := <-commandCh:
			if !ok {
				commandCh = nil
				continue
			}
			if input.err != nil {
				if input.fatal {
					return finishFailure(input.err)
				}
				if err := writer.write(eventStreamErrorRecord(input.err)); err != nil {
					return err
				}
				continue
			}
			if input.eof {
				return finish("stdin_eof", ExitOK)
			}
			if input.operation == '+' {
				domain := eventMethodDomain(input.method)
				canonicalDomain, ok := canonicalEventDomain(domain)
				if !ok {
					streamErr := commandError("subscription_enable_failed", "usage", fmt.Sprintf("invalid event domain %q", domain), ExitUsage, []string{"cdp protocol domains --json"})
					if writeErr := writer.write(eventStreamErrorRecord(streamErr)); writeErr != nil {
						return writeErr
					}
					continue
				}
				if !enabledDomains.contains(canonicalDomain) {
					if err := enableEventDomain(streamCtx, client, session.SessionID, canonicalDomain); err != nil {
						streamErr := commandError("subscription_enable_failed", "connection", fmt.Sprintf("enable %s for target %s: %v", canonicalDomain, target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
						if writeErr := writer.write(eventStreamErrorRecord(streamErr)); writeErr != nil {
							return writeErr
						}
						continue
					}
					enabledDomains.add(canonicalDomain)
				}
				changed := subscriptions.add(input.method)
				if err := writer.write(map[string]any{"ok": true, "type": "subscription", "operation": "add", "method": input.method, "active": true, "changed": changed}); err != nil {
					return err
				}
				continue
			}
			changed := subscriptions.remove(input.method)
			if err := writer.write(map[string]any{"ok": true, "type": "subscription", "operation": "remove", "method": input.method, "active": subscriptions.contains(input.method), "changed": changed}); err != nil {
				return err
			}
		case result, ok := <-eventCh:
			if !ok {
				if streamCtx.Err() != nil {
					reason, exit := eventStreamContextStop(streamCtx)
					return finish(reason, exit)
				}
				return finishFailure(errors.New("event reader stopped without a context error"))
			}
			if result.err != nil {
				if streamCtx.Err() != nil {
					reason, exit := eventStreamContextStop(streamCtx)
					return finish(reason, exit)
				}
				return finishFailure(result.err)
			}
			limit, emitErr := emitEvent(result.event)
			if emitErr != nil {
				return emitErr
			}
			if limit {
				return finish("max_events", ExitOK)
			}
		}
	}
}

func (a *app) eventsStreamSetupContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return a.commandContextWithDefault(cmd, 30*time.Second)
}

func (a *app) eventsStreamContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return a.commandContext(cmd)
}

func eventStreamContextStop(ctx context.Context) (string, int) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout", ExitTimeout
	}
	return "canceled", ExitOK
}

type eventStreamWriter struct {
	out io.Writer
}

func (w eventStreamWriter) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal JSONL record: %w", err)
	}
	if _, err := fmt.Fprintln(w.out, string(data)); err != nil {
		return err
	}
	return nil
}

type eventStreamEventResult struct {
	event cdp.Event
	err   error
}

type eventStreamLivenessResult struct {
	retired             bool
	consecutiveFailures int
	reason              string
	source              string
}

func pumpEventStreamLiveness(ctx context.Context, client browserEventClient, sessionID string, pollInterval time.Duration, failureThreshold int) <-chan eventStreamLivenessResult {
	return pumpEventStreamLivenessWithRegistration(ctx, client, sessionID, pollInterval, failureThreshold, nil)
}

func pumpEventStreamLivenessWithRegistration(ctx context.Context, client browserEventClient, sessionID string, pollInterval time.Duration, failureThreshold int, registrationCheck eventStreamRuntimeRegistrationCheck) <-chan eventStreamLivenessResult {
	results := make(chan eventStreamLivenessResult, 1)
	go func() {
		defer close(results)
		if pollInterval <= 0 {
			pollInterval = eventStreamLivenessPollInterval
		}
		if failureThreshold <= 0 {
			failureThreshold = eventStreamLivenessFailureThreshold
		}
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		consecutiveFailures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if registrationCheck != nil {
					checkCtx, cancelCheck := eventStreamCallContext(ctx)
					status, err := registrationCheck(checkCtx)
					cancelCheck()
					if ctx.Err() != nil {
						return
					}
					if err == nil && status == eventStreamRuntimeRegistrationRetired {
						result := eventStreamLivenessResult{
							retired: true,
							reason:  "runtime_retired",
							source:  "runtime_registration",
						}
						select {
						case results <- result:
						case <-ctx.Done():
						}
						return
					}
				}
				callCtx, cancel := eventStreamCallContext(ctx)
				err := client.CallSession(callCtx, sessionID, "Runtime.evaluate", map[string]any{
					"expression":    "void 0",
					"returnByValue": true,
				}, nil)
				cancel()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					consecutiveFailures++
					if consecutiveFailures < failureThreshold {
						continue
					}
					result := eventStreamLivenessResult{
						retired:             true,
						consecutiveFailures: consecutiveFailures,
						reason:              "exact_session_unhealthy",
					}
					select {
					case results <- result:
					case <-ctx.Done():
					}
					return
				}
				consecutiveFailures = 0
			}
		}
	}()
	return results
}

func pumpEventStream(ctx context.Context, client browserEventClient) <-chan eventStreamEventResult {
	results := make(chan eventStreamEventResult, 1)
	go func() {
		defer close(results)
		for {
			readCtx, cancelRead := context.WithTimeout(ctx, eventStreamReadTimeout)
			event, err := client.ReadEvent(readCtx)
			cancelRead()
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				continue
			}
			result := eventStreamEventResult{event: event, err: err}
			select {
			case results <- result:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return results
}

type eventStreamInput struct {
	operation byte
	method    string
	eof       bool
	err       error
	fatal     bool
}

func readEventStreamCommands(ctx context.Context, reader io.Reader) <-chan eventStreamInput {
	inputs := make(chan eventStreamInput, 32)
	go func() {
		defer close(inputs)
		if reader == nil {
			select {
			case inputs <- eventStreamInput{eof: true}:
			case <-ctx.Done():
			}
			return
		}
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 1024), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			operation, method, err := parseEventStreamCommand(line)
			if err != nil {
				select {
				case inputs <- eventStreamInput{err: err}:
				case <-ctx.Done():
					return
				}
				continue
			}
			select {
			case inputs <- eventStreamInput{operation: operation, method: method}:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case inputs <- eventStreamInput{err: fmt.Errorf("read subscription commands: %w", err), fatal: true}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case inputs <- eventStreamInput{eof: true}:
		case <-ctx.Done():
		}
	}()
	return inputs
}

func parseEventStreamCommand(line string) (byte, string, error) {
	if len(line) < 2 || (line[0] != '+' && line[0] != '-') {
		return 0, "", commandError("invalid_subscription", "usage", fmt.Sprintf("subscription %q must use +Method or -Method", line), ExitUsage, []string{"printf '+Page.loadEventFired\\n' | cdp events stream --json"})
	}
	method := strings.TrimSpace(line[1:])
	if !validEventMethod(method) {
		return 0, "", commandError("invalid_subscription", "usage", fmt.Sprintf("invalid CDP event method %q", method), ExitUsage, []string{"printf '+Page.loadEventFired\\n' | cdp events stream --json"})
	}
	return line[0], method, nil
}

type eventSubscriptionSet struct {
	all      bool
	included map[string]string
	excluded map[string]string
}

func parseEventSubscriptions(value string) (eventSubscriptionSet, error) {
	set := eventSubscriptionSet{
		all:      strings.TrimSpace(value) == "",
		included: map[string]string{},
		excluded: map[string]string{},
	}
	for _, raw := range strings.Split(value, ",") {
		method := strings.TrimSpace(raw)
		if method == "" {
			continue
		}
		if !validEventMethod(method) {
			return eventSubscriptionSet{}, commandError("invalid_event_match", "usage", fmt.Sprintf("invalid CDP event method %q", method), ExitUsage, []string{"cdp events stream --match Page.loadEventFired --json"})
		}
		set.add(method)
	}
	return set, nil
}

func (s eventSubscriptionSet) add(method string) bool {
	key := strings.ToLower(method)
	if s.all {
		if _, exists := s.excluded[key]; exists {
			delete(s.excluded, key)
			return true
		}
		return false
	}
	if _, exists := s.included[key]; exists {
		return false
	}
	s.included[key] = method
	return true
}

func (s eventSubscriptionSet) remove(method string) bool {
	key := strings.ToLower(method)
	if s.all {
		if _, exists := s.excluded[key]; exists {
			return false
		}
		s.excluded[key] = method
		return true
	}
	if _, exists := s.included[key]; !exists {
		return false
	}
	delete(s.included, key)
	return true
}

func (s eventSubscriptionSet) contains(method string) bool {
	key := strings.ToLower(method)
	if s.all {
		_, excluded := s.excluded[key]
		return !excluded
	}
	_, included := s.included[key]
	return included
}

func (s eventSubscriptionSet) matches(method string) bool {
	return s.contains(method)
}

func (s eventSubscriptionSet) methods() []string {
	methods := make([]string, 0, len(s.included)+len(s.excluded))
	for _, method := range s.included {
		methods = append(methods, method)
	}
	for _, method := range s.excluded {
		methods = append(methods, "-"+method)
	}
	sort.Strings(methods)
	return methods
}

func validEventMethod(method string) bool {
	domain, event, ok := strings.Cut(strings.TrimSpace(method), ".")
	return ok && validIdentifier(domain) && validIdentifier(event)
}

func validIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (index > 0 && char >= '0' && char <= '9') || char == '_' {
			continue
		}
		return false
	}
	return true
}

func eventMethodDomain(method string) string {
	domain, _, _ := strings.Cut(method, ".")
	return domain
}

func eventDomainEnableMethod(domain string) (string, bool) {
	canonical, ok := canonicalEventDomain(domain)
	if !ok {
		return "", false
	}
	return canonical + ".enable", true
}

func enableEventDomain(ctx context.Context, client browserEventClient, sessionID, domain string) error {
	method, ok := eventDomainEnableMethod(domain)
	if !ok {
		return fmt.Errorf("unsupported event domain %q", domain)
	}
	callCtx, cancel := eventStreamCallContext(ctx)
	defer cancel()
	return client.CallSession(callCtx, sessionID, method, map[string]any{}, nil)
}

func eventStreamCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, eventStreamCallTimeout)
}

func validateEventDomains(value string) error {
	_, err := parseEventDomains(value)
	return err
}

type eventDomainSet map[string]string

func parseEventDomains(value string) (eventDomainSet, error) {
	set := eventDomainSet{}
	for _, raw := range strings.Split(value, ",") {
		domain := strings.TrimSpace(raw)
		if domain == "" {
			continue
		}
		canonical, ok := canonicalEventDomain(domain)
		if !ok {
			return nil, commandError("invalid_event_domain", "usage", fmt.Sprintf("invalid or browser-scoped event domain %q", domain), ExitUsage, []string{"cdp protocol domains --json", "cdp events stream --enable DOM,Performance --json"})
		}
		set.add(canonical)
	}
	return set, nil
}

func (s eventDomainSet) add(domain string) {
	canonical, ok := canonicalEventDomain(domain)
	if !ok {
		return
	}
	s[strings.ToLower(canonical)] = canonical
}

func (s eventDomainSet) contains(domain string) bool {
	canonical, ok := canonicalEventDomain(domain)
	if !ok {
		return false
	}
	_, exists := s[strings.ToLower(canonical)]
	return exists
}

func (s eventDomainSet) names() []string {
	names := make([]string, 0, len(s))
	for _, domain := range s {
		names = append(names, eventDomainDisplayName(domain))
	}
	sort.Slice(names, func(i, j int) bool {
		left, right := strings.ToLower(names[i]), strings.ToLower(names[j])
		if left == right {
			return names[i] < names[j]
		}
		return left < right
	})
	return names
}

func eventDomainDisplayName(domain string) string {
	switch strings.ToLower(domain) {
	case "page", "network", "runtime", "log":
		return strings.ToLower(domain)
	default:
		return domain
	}
}

var canonicalEventDomains = map[string]string{
	"animation":            "Animation",
	"audits":               "Audits",
	"autofill":             "Autofill",
	"backgroundservice":    "BackgroundService",
	"bluetoothemulation":   "BluetoothEmulation",
	"browser":              "Browser",
	"cast":                 "Cast",
	"console":              "Console",
	"css":                  "CSS",
	"database":             "Database",
	"deviceaccess":         "DeviceAccess",
	"deviceorientation":    "DeviceOrientation",
	"dom":                  "DOM",
	"domdebugger":          "DOMDebugger",
	"domsnapshot":          "DOMSnapshot",
	"domstorage":           "DOMStorage",
	"emulation":            "Emulation",
	"eventbreakpoints":     "EventBreakpoints",
	"extensions":           "Extensions",
	"fedcm":                "FedCm",
	"fetch":                "Fetch",
	"filesystem":           "FileSystem",
	"headlessexperimental": "HeadlessExperimental",
	"heapprofiler":         "HeapProfiler",
	"indexeddb":            "IndexedDB",
	"input":                "Input",
	"inspector":            "Inspector",
	"io":                   "IO",
	"layertree":            "LayerTree",
	"log":                  "Log",
	"media":                "Media",
	"memory":               "Memory",
	"network":              "Network",
	"overlay":              "Overlay",
	"page":                 "Page",
	"performance":          "Performance",
	"performancetimeline":  "PerformanceTimeline",
	"preload":              "Preload",
	"profiler":             "Profiler",
	"pwa":                  "PWA",
	"runtime":              "Runtime",
	"security":             "Security",
	"servicenetworking":    "ServiceNetworking",
	"serviceworker":        "ServiceWorker",
	"storage":              "Storage",
	"systeminfo":           "SystemInfo",
	"target":               "Target",
	"tethering":            "Tethering",
	"tracing":              "Tracing",
	"webaudio":             "WebAudio",
	"webauthn":             "WebAuthn",
	"schema":               "Schema",
}

var browserScopedEventDomains = map[string]bool{
	"browser":    true,
	"schema":     true,
	"systeminfo": true,
	"target":     true,
	"tethering":  true,
}

func canonicalEventDomain(domain string) (string, bool) {
	trimmed := strings.TrimSpace(domain)
	if !validIdentifier(trimmed) {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if canonical, known := canonicalEventDomains[lower]; known {
		if browserScopedEventDomains[lower] {
			return "", false
		}
		return canonical, true
	}
	return trimmed, true
}

func eventStreamMetadata(target cdp.TargetInfo, sessionID string, options eventStreamOptions, subscriptions eventSubscriptionSet, enabledDomains eventDomainSet) map[string]any {
	return map[string]any{
		"duration":        durationString(options.duration),
		"max_events":      options.maxEvents,
		"session_bound":   sessionID != "",
		"target_index":    options.targetIndex,
		"target_id":       target.TargetID,
		"all_events":      subscriptions.all,
		"subscriptions":   subscriptions.methods(),
		"enabled_domains": enabledDomains.names(),
		"liveness":        eventStreamLivenessMetadata(nil),
	}
}

func eventStreamLivenessMetadata(stop *eventStreamLivenessResult) map[string]any {
	metadata := map[string]any{
		"enabled":           true,
		"heartbeat":         "Runtime.evaluate",
		"read_only":         true,
		"poll_interval":     eventStreamLivenessPollInterval.String(),
		"failure_threshold": eventStreamLivenessFailureThreshold,
	}
	if stop != nil {
		metadata["state"] = "retired"
		metadata["reason"] = stop.reason
		metadata["consecutive_failures"] = stop.consecutiveFailures
		if strings.TrimSpace(stop.source) != "" {
			metadata["source"] = stop.source
		}
	}
	return metadata
}

func eventStreamFailure(err error) *CommandError {
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		return commandErr
	}
	return &CommandError{
		Code:                "event_stream_failed",
		Class:               "connection",
		Message:             fmt.Sprintf("event stream: %v", err),
		ExitCode:            ExitConnection,
		RemediationCommands: []string{"cdp pages --json", "cdp doctor --json"},
		Err:                 err,
	}
}

func eventStreamErrorRecord(err error) map[string]any {
	commandErr := eventStreamFailure(err)
	return map[string]any{
		"ok":                   false,
		"type":                 "error",
		"code":                 commandErr.Code,
		"err_class":            commandErr.Class,
		"message":              commandErr.Error(),
		"remediation_commands": commandErr.RemediationCommands,
	}
}
