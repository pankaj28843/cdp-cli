package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	interactionObserverSchemaVersion = "interactions/v1"
	interactionBindingPrefix         = "__cdp_cli_interaction_v1_"
	interactionObserverMarker        = "__cdp_cli_interaction_observer_v1"
)

var interactionKinds = []string{"click", "scroll", "selectionchange", "keydown"}

type interactionObserverOptions struct {
	targetID      string
	urlContains   string
	titleContains string
	targetIndex   int
	match         string
	duration      time.Duration
	maxEvents     int
	readyFile     string
}

func (a *app) newEventsInteractionsCommand() *cobra.Command {
	var options interactionObserverOptions
	cmd := &cobra.Command{
		Use:   "interactions",
		Short: "Observe sanitized DOM interactions as JSONL",
		Long:  "Observe the source collaboration bridge for click, scroll, selection-change, and keydown causes that ordinary CDP events do not expose. Concurrent persistent observers dequeue only their exact session without consuming another observer's events. The observer checks daemon runtime registration and a read-only exact-session heartbeat, and deliberately omits page text, key values, input values, HTML, and raw binding payloads.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runEventsInteractions(cmd, options)
		},
	}
	cmd.Flags().StringVar(&options.targetID, "target", "", "page target id/prefix, or a short numeric 1-based page index")
	cmd.Flags().StringVar(&options.targetID, "target-id", "", "source-compatible alias for --target")
	cmd.Flags().StringVar(&options.urlContains, "url-contains", "", "use the unique page whose URL contains this text")
	cmd.Flags().StringVar(&options.urlContains, "url", "", "source-compatible alias for --url-contains")
	cmd.Flags().StringVar(&options.titleContains, "title-contains", "", "use the unique page whose title contains this text")
	cmd.Flags().IntVar(&options.targetIndex, "target-index", 0, "select a 1-based page target index")
	cmd.Flags().StringVar(&options.match, "match", "", "comma-separated interaction kinds: click,scroll,selectionchange,keydown")
	cmd.Flags().DurationVar(&options.duration, "duration", 30*time.Second, "maximum observation duration; 0 uses the global timeout")
	cmd.Flags().IntVar(&options.maxEvents, "max-events", 100, "maximum sanitized interactions; 0 disables the count limit")
	cmd.Flags().StringVar(&options.readyFile, "ready-file", "", "exclusive owner-only readiness artifact written after binding and listener setup")
	return cmd
}

func (a *app) runEventsInteractions(cmd *cobra.Command, options interactionObserverOptions) error {
	if !a.opts.json {
		return commandError("json_required", "usage", "cdp events interactions emits JSONL; pass --json", ExitUsage, []string{"cdp events interactions --target-index 1 --max-events 1 --json"})
	}
	if a.opts.jq != "" {
		return commandError("jq_unsupported", "usage", "--jq cannot filter a multi-record JSONL stream; filter records downstream", ExitUsage, []string{"cdp events interactions --json | jq -c 'select(.type == \"interaction\")'"})
	}
	if options.duration < 0 || options.maxEvents < 0 {
		return commandError("usage", "usage", "--duration and --max-events must be non-negative", ExitUsage, []string{"cdp events interactions --duration 30s --max-events 50 --json"})
	}
	if err := normalizeSourceAttachStopNumericTarget(cmd, &options.targetID, &options.targetIndex); err != nil {
		return err
	}
	if err := validatePageTargetIndexSelector(cmd, options.targetID, options.urlContains, options.titleContains, options.targetIndex); err != nil {
		return err
	}
	kinds, err := parseInteractionKinds(options.match)
	if err != nil {
		return err
	}

	setupCtx, setupCancel := a.eventsStreamSetupContext(cmd)
	defer setupCancel()
	client, session, target, err := a.attachPageEventSessionWithIndex(setupCtx, options.targetID, options.urlContains, options.titleContains, options.targetIndex)
	if err != nil {
		return err
	}

	bindingName := ""
	scriptID := ""
	var cleanupOnce sync.Once
	var cleanupResult interactionCleanupResult
	var cleanupErr error
	runCleanup := func() (interactionCleanupResult, error) {
		cleanupOnce.Do(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cleanupResult, cleanupErr = cleanupInteractionObserver(cleanupCtx, client, session.SessionID, bindingName, scriptID)
		})
		return cleanupResult, cleanupErr
	}
	defer func() {
		_, _ = runCleanup()
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(closeCtx)
	}()

	if err := enableEventDomain(setupCtx, client, session.SessionID, "runtime"); err != nil {
		return commandError("observer_enable_failed", "connection", fmt.Sprintf("enable Runtime for target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	if err := enableEventDomain(setupCtx, client, session.SessionID, "page"); err != nil {
		return commandError("observer_enable_failed", "connection", fmt.Sprintf("enable Page for target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	if _, err := client.DrainSessionEvents(setupCtx, session.SessionID); err != nil {
		return commandError("observer_drain_failed", "connection", fmt.Sprintf("drain setup events for target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp doctor --json"})
	}

	bindingName, err = newInteractionBindingName()
	if err != nil {
		return commandError("observer_binding_name_failed", "internal", fmt.Sprintf("create an interaction binding name: %v", err), ExitInternal, []string{"cdp doctor --json"})
	}
	if err := client.CallSession(setupCtx, session.SessionID, "Runtime.addBinding", map[string]any{"name": bindingName}, nil); err != nil {
		return commandError("observer_binding_failed", "connection", fmt.Sprintf("register interaction binding for target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp protocol search addBinding --kind command --json", "cdp doctor --json"})
	}

	script := interactionObserverScript(bindingName)
	var scriptResult struct {
		Identifier string `json:"identifier"`
	}
	if err := client.CallSession(setupCtx, session.SessionID, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": script}, &scriptResult); err != nil {
		return commandError("observer_script_failed", "connection", fmt.Sprintf("install interaction listener for target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp protocol search addScriptToEvaluateOnNewDocument --kind command --json", "cdp doctor --json"})
	}
	if strings.TrimSpace(scriptResult.Identifier) == "" {
		return commandError("observer_script_failed", "connection", "Page.addScriptToEvaluateOnNewDocument returned no script identifier", ExitConnection, []string{"cdp doctor --json"})
	}
	scriptID = scriptResult.Identifier
	var evaluationResult struct {
		ExceptionDetails json.RawMessage `json:"exceptionDetails,omitempty"`
	}
	if err := client.CallSession(setupCtx, session.SessionID, "Runtime.evaluate", map[string]any{"expression": script}, &evaluationResult); err != nil {
		return commandError("observer_current_document_failed", "connection", fmt.Sprintf("install interaction listener in target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
	}
	if len(evaluationResult.ExceptionDetails) > 0 && string(evaluationResult.ExceptionDetails) != "null" {
		return commandError("observer_current_document_failed", "check_failed", fmt.Sprintf("install interaction listener in target %s returned an execution exception", target.TargetID), ExitCheckFailed, []string{"cdp pages --json", "cdp doctor --json"})
	}
	initialEvents, err := client.DrainSessionEvents(setupCtx, session.SessionID)
	if err != nil {
		return commandError("observer_drain_failed", "connection", fmt.Sprintf("drain interaction setup events for target %s: %v", target.TargetID, err), ExitConnection, []string{"cdp doctor --json"})
	}

	removeReady, err := publishCollectorReadiness(options.readyFile, target.TargetID, session.SessionID, []string{"page", "runtime"})
	if err != nil {
		return collectorReadinessError(err)
	}
	defer removeReady()

	writer := eventStreamWriter{out: a.out}
	metadata := interactionObserverMetadata(target, options, kinds)
	if err := writer.write(map[string]any{"ok": true, "type": "ready", "target": pageRow(target), "observer": metadata}); err != nil {
		return fmt.Errorf("write interaction observer readiness: %w", err)
	}

	streamCtx, cancelStream := a.eventsStreamContext(cmd)
	defer cancelStream()
	if options.duration > 0 {
		var cancelDuration context.CancelFunc
		streamCtx, cancelDuration = context.WithTimeout(streamCtx, options.duration)
		defer cancelDuration()
	}

	eventCount := 0
	foreignEventsDropped := 0
	ignoredBindingEvents := 0
	var livenessStop *eventStreamLivenessResult
	emit := func(event cdp.Event) (bool, error) {
		if event.SessionID != session.SessionID {
			foreignEventsDropped++
			return false, nil
		}
		if event.Method != "Runtime.bindingCalled" {
			return false, nil
		}
		var params struct {
			Name    string `json:"name"`
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil || params.Name != bindingName {
			return false, nil
		}
		interaction, ok := sanitizeInteractionPayload(params.Payload)
		if !ok {
			ignoredBindingEvents++
			return false, nil
		}
		if !kinds[interaction.Type] {
			return false, nil
		}
		if err := writer.write(map[string]any{
			"ok":          true,
			"type":        "interaction",
			"target":      pageRow(target),
			"event":       map[string]any{"method": "Runtime.bindingCalled"},
			"interaction": interaction.Data,
		}); err != nil {
			return false, fmt.Errorf("write interaction event: %w", err)
		}
		eventCount++
		return options.maxEvents > 0 && eventCount >= options.maxEvents, nil
	}
	finish := func(reason string, exit int) error {
		cleanup, cleanupFailure := runCleanup()
		if livenessStop != nil {
			metadata["liveness"] = eventStreamLivenessMetadata(livenessStop)
		}
		stop := map[string]any{
			"ok":                     true,
			"type":                   "stopped",
			"reason":                 reason,
			"event_count":            eventCount,
			"foreign_events_dropped": foreignEventsDropped,
			"ignored_binding_events": ignoredBindingEvents,
			"truncated":              options.maxEvents > 0 && eventCount >= options.maxEvents,
			"observer":               metadata,
			"cleanup":                cleanup,
		}
		if cleanupFailure != nil {
			stop["cleanup_error"] = cleanupFailure.Error()
			if exit == ExitOK {
				exit = ExitConnection
			}
		}
		if err := writer.write(stop); err != nil {
			return fmt.Errorf("write interaction observer stop: %w", err)
		}
		if exit != ExitOK {
			return &renderedResultExit{ExitCode: exit}
		}
		return nil
	}
	for _, event := range initialEvents {
		limit, emitErr := emit(event)
		if emitErr != nil {
			return emitErr
		}
		if limit {
			return finish("max_events", ExitOK)
		}
	}

	registrationCheck := a.eventStreamRuntimeRegistrationCheck(client)
	livenessCh := pumpEventStreamLivenessWithRegistration(streamCtx, client, session.SessionID, eventStreamLivenessPollInterval, eventStreamLivenessFailureThreshold, registrationCheck)
	eventCh := pumpEventStream(streamCtx, client, session.SessionID)
	for {
		select {
		case <-streamCtx.Done():
			reason, exit := eventStreamContextStop(streamCtx)
			return finish(reason, exit)
		case result, ok := <-livenessCh:
			if !ok {
				livenessCh = nil
				continue
			}
			if result.retired {
				livenessStop = &result
				return finish("liveness", ExitOK)
			}
		case result, ok := <-eventCh:
			if !ok {
				if streamCtx.Err() != nil {
					reason, exit := eventStreamContextStop(streamCtx)
					return finish(reason, exit)
				}
				return finish("event_reader_stopped", ExitConnection)
			}
			if result.err != nil {
				if streamCtx.Err() != nil {
					reason, exit := eventStreamContextStop(streamCtx)
					return finish(reason, exit)
				}
				streamErr := eventStreamFailure(result.err)
				if writeErr := writer.write(eventStreamErrorRecord(streamErr)); writeErr != nil {
					return fmt.Errorf("write interaction observer error: %w", writeErr)
				}
				return finish("error", streamErr.ExitCode)
			}
			limit, emitErr := emit(result.event)
			if emitErr != nil {
				return emitErr
			}
			if limit {
				return finish("max_events", ExitOK)
			}
		}
	}
}

type interactionCleanupResult struct {
	CurrentDocumentRemoved bool `json:"current_document_removed"`
	FutureDocumentRemoved  bool `json:"future_document_removed"`
	BindingRemoved         bool `json:"binding_removed"`
}

func cleanupInteractionObserver(ctx context.Context, client browserEventClient, sessionID, bindingName, scriptID string) (interactionCleanupResult, error) {
	var result interactionCleanupResult
	var errs []error
	if strings.TrimSpace(bindingName) != "" {
		if err := client.CallSession(ctx, sessionID, "Runtime.evaluate", map[string]any{"expression": interactionObserverCleanupScript(bindingName)}, nil); err != nil {
			errs = append(errs, fmt.Errorf("remove current-document interaction listeners: %w", err))
		} else {
			result.CurrentDocumentRemoved = true
		}
	}
	if strings.TrimSpace(scriptID) != "" {
		if err := client.CallSession(ctx, sessionID, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{"identifier": scriptID}, nil); err != nil {
			errs = append(errs, fmt.Errorf("remove future-document interaction listener: %w", err))
		} else {
			result.FutureDocumentRemoved = true
		}
	}
	if strings.TrimSpace(bindingName) != "" {
		if err := client.CallSession(ctx, sessionID, "Runtime.removeBinding", map[string]any{"name": bindingName}, nil); err != nil {
			errs = append(errs, fmt.Errorf("remove interaction binding: %w", err))
		} else {
			result.BindingRemoved = true
		}
	}
	return result, errors.Join(errs...)
}

func newInteractionBindingName() (string, error) {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return interactionBindingPrefix + hex.EncodeToString(bytes[:]), nil
}

func parseInteractionKinds(raw string) (map[string]bool, error) {
	selected := map[string]bool{}
	parts := splitCSV(raw)
	if len(parts) == 0 {
		for _, kind := range interactionKinds {
			selected[kind] = true
		}
		return selected, nil
	}
	for _, part := range parts {
		kind := strings.ToLower(strings.TrimSpace(part))
		if !containsInteractionKind(kind) {
			return nil, commandError("invalid_interaction_kind", "usage", fmt.Sprintf("unsupported interaction kind %q", part), ExitUsage, []string{"cdp events interactions --match click,scroll --json"})
		}
		selected[kind] = true
	}
	return selected, nil
}

func interactionObserverMetadata(target cdp.TargetInfo, options interactionObserverOptions, kinds map[string]bool) map[string]any {
	selected := make([]string, 0, len(kinds))
	for kind := range kinds {
		selected = append(selected, kind)
	}
	sort.Strings(selected)
	return map[string]any{
		"schema_version":             interactionObserverSchemaVersion,
		"session_bound":              true,
		"event_dequeue":              "exact_session",
		"target_id":                  target.TargetID,
		"target_index":               options.targetIndex,
		"match":                      selected,
		"duration":                   durationString(options.duration),
		"max_events":                 options.maxEvents,
		"binding_installed":          true,
		"current_document_installed": true,
		"future_documents_installed": true,
		"sanitized_payload":          true,
		"liveness":                   eventStreamLivenessMetadata(nil),
	}
}

type sanitizedInteraction struct {
	Type string
	Data map[string]any
}

type interactionPayload struct {
	Type         string                    `json:"type"`
	X            *float64                  `json:"x,omitempty"`
	Y            *float64                  `json:"y,omitempty"`
	Button       *int                      `json:"button,omitempty"`
	Detail       *int                      `json:"detail,omitempty"`
	ScrollX      *float64                  `json:"scroll_x,omitempty"`
	ScrollY      *float64                  `json:"scroll_y,omitempty"`
	HasSelection *bool                     `json:"has_selection,omitempty"`
	Collapsed    *bool                     `json:"collapsed,omitempty"`
	KeyKind      string                    `json:"key_kind,omitempty"`
	Alt          bool                      `json:"alt,omitempty"`
	Ctrl         bool                      `json:"ctrl,omitempty"`
	Meta         bool                      `json:"meta,omitempty"`
	Shift        bool                      `json:"shift,omitempty"`
	Repeat       bool                      `json:"repeat,omitempty"`
	Target       *interactionTargetPayload `json:"target,omitempty"`
}

type interactionTargetPayload struct {
	Tag      string `json:"tag"`
	Editable bool   `json:"editable"`
}

func sanitizeInteractionPayload(raw string) (sanitizedInteraction, bool) {
	var payload interactionPayload
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || strings.TrimSpace(payload.Type) == "" {
		return sanitizedInteraction{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return sanitizedInteraction{}, false
	}
	if !interactionKindAllowed(payload.Type) || !validInteractionTarget(payload.Target) {
		return sanitizedInteraction{}, false
	}
	data := map[string]any{"type": payload.Type}
	if payload.Target != nil {
		data["target"] = map[string]any{"tag": payload.Target.Tag, "editable": payload.Target.Editable}
	}
	switch payload.Type {
	case "click":
		if payload.X == nil || payload.Y == nil || payload.Button == nil || payload.Detail == nil || !finiteInteractionNumber(*payload.X) || !finiteInteractionNumber(*payload.Y) || *payload.Button < 0 || *payload.Button > 5 || *payload.Detail < 0 || *payload.Detail > 100 {
			return sanitizedInteraction{}, false
		}
		data["x"], data["y"] = *payload.X, *payload.Y
		data["button"], data["detail"] = *payload.Button, *payload.Detail
	case "scroll":
		if payload.ScrollX == nil || payload.ScrollY == nil || !finiteInteractionNumber(*payload.ScrollX) || !finiteInteractionNumber(*payload.ScrollY) {
			return sanitizedInteraction{}, false
		}
		data["scroll_x"], data["scroll_y"] = *payload.ScrollX, *payload.ScrollY
	case "selectionchange":
		if payload.HasSelection == nil || payload.Collapsed == nil {
			return sanitizedInteraction{}, false
		}
		data["has_selection"], data["collapsed"] = *payload.HasSelection, *payload.Collapsed
	case "keydown":
		if payload.KeyKind != "printable" && payload.KeyKind != "control" && payload.KeyKind != "unknown" {
			return sanitizedInteraction{}, false
		}
		data["key_kind"] = payload.KeyKind
		data["modifiers"] = map[string]any{"alt": payload.Alt, "ctrl": payload.Ctrl, "meta": payload.Meta, "shift": payload.Shift, "repeat": payload.Repeat}
	}
	return sanitizedInteraction{Type: payload.Type, Data: data}, true
}

func validInteractionTarget(target *interactionTargetPayload) bool {
	if target == nil {
		return true
	}
	if len(target.Tag) == 0 || len(target.Tag) > 32 {
		return false
	}
	for _, char := range target.Tag {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

func interactionKindAllowed(kind string) bool {
	return containsInteractionKind(kind)
}

func finiteInteractionNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= 1e9
}

func containsInteractionKind(value string) bool {
	for _, kind := range interactionKinds {
		if kind == value {
			return true
		}
	}
	return false
}

func interactionObserverScript(bindingName string) string {
	bindingJSON, _ := json.Marshal(bindingName)
	markerJSON, _ := json.Marshal(interactionObserverMarker)
	return fmt.Sprintf(`(() => {
  const bindingName = %s;
  const marker = %s;
  const registry = globalThis[marker] || (globalThis[marker] = {});
  if (registry[bindingName]) return;
  const targetMetadata = (node) => {
    const element = node && node.nodeType === 1 ? node : node && node.parentElement;
    if (!element) return undefined;
    const tag = typeof element.tagName === "string" ? element.tagName.toLowerCase() : "unknown";
    return {tag, editable: Boolean(element.isContentEditable || element.tagName === "INPUT" || element.tagName === "TEXTAREA")};
  };
  const finite = (value) => Number.isFinite(value) ? Math.round(value * 100) / 100 : 0;
  const report = (payload) => {
    try {
      const binding = globalThis[bindingName];
      if (typeof binding === "function") binding(JSON.stringify(payload));
    } catch (_) {}
  };
  const state = {listeners: [], cleanup() {
    for (const listener of state.listeners) {
      try { listener.target.removeEventListener(listener.type, listener.handler, listener.options); } catch (_) {}
    }
    delete registry[bindingName];
    if (Object.keys(registry).length === 0) {
      try { delete globalThis[marker]; } catch (_) {}
    }
    try { delete globalThis[bindingName]; } catch (_) {}
  }};
  const listen = (target, type, handler, options) => {
    target.addEventListener(type, handler, options);
    state.listeners.push({target, type, handler, options});
  };
  listen(globalThis, "click", (event) => report({type: "click", x: finite(event.clientX), y: finite(event.clientY), button: Number(event.button) || 0, detail: Number(event.detail) || 0, target: targetMetadata(event.target)}), true);
  listen(document, "scroll", (event) => {
    const target = event.target;
    const documentTarget = target === document || target === document.documentElement || target === document.body || target === globalThis;
    report({type: "scroll", scroll_x: finite(documentTarget ? globalThis.scrollX : target.scrollLeft), scroll_y: finite(documentTarget ? globalThis.scrollY : target.scrollTop), target: targetMetadata(documentTarget ? document.documentElement : target)});
  }, true);
  listen(document, "selectionchange", () => {
    const selection = typeof globalThis.getSelection === "function" ? globalThis.getSelection() : null;
    report({type: "selectionchange", has_selection: Boolean(selection && !selection.isCollapsed), collapsed: Boolean(!selection || selection.isCollapsed), target: targetMetadata(selection && selection.anchorNode)});
  }, true);
  listen(document, "keydown", (event) => {
    const keyKind = typeof event.key === "string" && event.key.length === 1 ? "printable" : (event.key ? "control" : "unknown");
    report({type: "keydown", key_kind: keyKind, alt: Boolean(event.altKey), ctrl: Boolean(event.ctrlKey), meta: Boolean(event.metaKey), shift: Boolean(event.shiftKey), repeat: Boolean(event.repeat), target: targetMetadata(event.target)});
  }, true);
  registry[bindingName] = state;
})()`, string(bindingJSON), string(markerJSON))
}

func interactionObserverCleanupScript(bindingName string) string {
	bindingJSON, _ := json.Marshal(bindingName)
	markerJSON, _ := json.Marshal(interactionObserverMarker)
	return fmt.Sprintf(`(() => {
  const marker = %s;
  const bindingName = %s;
  const registry = globalThis[marker];
  const state = registry && registry[bindingName];
  if (state && typeof state.cleanup === "function") state.cleanup();
})()`, string(markerJSON), string(bindingJSON))
}
