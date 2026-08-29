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
	eventStreamCallTimeout = 10 * time.Second
	eventStreamReadTimeout = 500 * time.Millisecond
)

func (a *app) newEventsStreamCommand() *cobra.Command {
	var options eventStreamOptions
	cmd := &cobra.Command{
		Use:   "stream",
		Short: "Stream session-scoped CDP events as JSONL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runEventsStream(cmd, options)
		},
	}
	cmd.Flags().StringVar(&options.targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&options.urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&options.titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().IntVar(&options.targetIndex, "target-index", 0, "select a 1-based page target index")
	cmd.Flags().StringVar(&options.enable, "enable", "page,network,runtime,log", "comma-separated domains to enable: page,network,runtime,log")
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
	if options.duration < 0 || options.maxEvents < 0 || options.targetIndex < 0 {
		return commandError("usage", "usage", "--duration, --max-events, and --target-index must be non-negative", ExitUsage, []string{"cdp events stream --duration 30s --max-events 50 --json"})
	}
	if cmd.Flags().Changed("target-index") && options.targetIndex == 0 {
		return commandError("invalid_target_index", "usage", "--target-index must be greater than zero", ExitUsage, []string{"cdp pages --json"})
	}
	if options.targetIndex > 0 && (strings.TrimSpace(options.targetID) != "" || strings.TrimSpace(options.urlContains) != "" || strings.TrimSpace(options.titleContains) != "") {
		return commandError("invalid_target_selector", "usage", "--target-index cannot be combined with --target, --url-contains, or --title-contains", ExitUsage, []string{"cdp pages --json"})
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

	enabledDomains := parseCSVSet(options.enable)
	for _, domain := range setKeys(enabledDomains) {
		if err := enableEventDomain(setupCtx, client, session.SessionID, domain); err != nil {
			return commandError("collector_enable_failed", "connection", fmt.Sprintf("enable %s for target %s: %v", domain, target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
		}
	}
	removeReady, err := publishCollectorReadiness(options.readyFile, target.TargetID, session.SessionID, setKeys(enabledDomains))
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
	finish := func(reason string, exit int) error {
		if err := writer.write(map[string]any{
			"ok":                     true,
			"type":                   "stopped",
			"reason":                 reason,
			"event_count":            eventCount,
			"foreign_events_dropped": foreignEventsDropped,
			"truncated":              options.maxEvents > 0 && eventCount >= options.maxEvents,
			"stream":                 eventStreamMetadata(target, session.SessionID, options, subscriptions, enabledDomains),
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
				if !enabledDomains[domain] {
					if err := enableEventDomain(streamCtx, client, session.SessionID, domain); err != nil {
						streamErr := commandError("subscription_enable_failed", "connection", fmt.Sprintf("enable %s for target %s: %v", domain, target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
						if writeErr := writer.write(eventStreamErrorRecord(streamErr)); writeErr != nil {
							return writeErr
						}
						continue
					}
					enabledDomains[domain] = true
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
	return strings.ToLower(domain)
}

func eventDomainEnableMethod(domain string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(domain)) {
	case "page":
		return "Page.enable", true
	case "network":
		return "Network.enable", true
	case "runtime":
		return "Runtime.enable", true
	case "log":
		return "Log.enable", true
	default:
		return "", false
	}
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
	for _, domain := range setKeys(parseCSVSet(value)) {
		if _, ok := eventDomainEnableMethod(domain); !ok {
			return commandError("usage", "usage", fmt.Sprintf("unsupported --enable domain %q", domain), ExitUsage, []string{"cdp events stream --enable page,network,runtime,log --json"})
		}
	}
	return nil
}

func eventStreamMetadata(target cdp.TargetInfo, sessionID string, options eventStreamOptions, subscriptions eventSubscriptionSet, enabledDomains map[string]bool) map[string]any {
	return map[string]any{
		"duration":        durationString(options.duration),
		"max_events":      options.maxEvents,
		"session_bound":   sessionID != "",
		"target_index":    options.targetIndex,
		"target_id":       target.TargetID,
		"all_events":      subscriptions.all,
		"subscriptions":   subscriptions.methods(),
		"enabled_domains": setKeys(enabledDomains),
	}
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
