package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	dialogWaitKind    = "dialog"
	dialogWaitCDPName = "Page.javascriptDialogOpening"
)

type dialogWaitCriteria struct {
	Type            string `json:"type,omitempty"`
	Message         string `json:"message,omitempty"`
	MessageContains string `json:"message_contains,omitempty"`
}

type dialogWaitOptions struct {
	Criteria   dialogWaitCriteria
	Action     string
	PromptText string
	Redact     string
}

type dialogWaitEvent struct {
	Type              string `json:"type"`
	Message           string `json:"message"`
	DefaultPrompt     string `json:"default_prompt,omitempty"`
	URL               string `json:"url,omitempty"`
	FrameID           string `json:"frame_id,omitempty"`
	HasBrowserHandler bool   `json:"has_browser_handler"`
	CDPMethod         string `json:"cdp_method"`
}

type dialogWaitObservation struct {
	Matched       bool
	EventCount    int
	ObservedCount int
	Event         *dialogWaitEvent
	LastEvent     *dialogWaitEvent
}

func (a *app) newWaitDialogCommand() *cobra.Command {
	var targetID string
	var pageURLContains string
	var titleContains string
	var dialogType string
	var message string
	var messageContains string
	var action string
	var promptText string
	var redact string
	cmd := &cobra.Command{
		Use:   "dialog",
		Short: "Wait for a JavaScript dialog to open",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := dialogWaitOptions{
				Criteria: dialogWaitCriteria{
					Type:            dialogType,
					Message:         message,
					MessageContains: messageContains,
				},
				Action:     action,
				PromptText: promptText,
				Redact:     redact,
			}
			if err := normalizeDialogWaitOptions(&opts); err != nil {
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
			observation, err := waitForDialogEvent(ctx, client, session.SessionID, opts.Criteria)
			elapsed := time.Since(start)
			report := dialogWaitReport(observation, opts, elapsed, a.effectiveNetworkWaitTimeout(), redactor)
			report["target"] = pageRow(target)
			if err != nil {
				return dialogWaitError(ctx, session.TargetID, opts, report, err)
			}
			if opts.Action != "none" {
				accept := opts.Action == "accept"
				params := map[string]any{"accept": accept}
				if accept && opts.PromptText != "" {
					params["promptText"] = opts.PromptText
				}
				if err := client.CallSession(ctx, session.SessionID, "Page.handleJavaScriptDialog", params, nil); err != nil {
					return commandErrorWithData("connection_failed", "connection", fmt.Sprintf("handle dialog target %s: %v", session.TargetID, err), ExitConnection, dialogWaitRemediations(opts), report)
				}
				markDialogHandled(report, opts.Action, accept, opts.PromptText != "")
			}
			return a.render(ctx, fmt.Sprintf("matched dialog\t%s", observation.Event.Type), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&pageURLContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&dialogType, "type", "", "dialog type to match: alert, confirm, prompt, or beforeunload")
	cmd.Flags().StringVar(&message, "message", "", "exact dialog message to match")
	cmd.Flags().StringVar(&messageContains, "message-contains", "", "substring that the dialog message must contain")
	cmd.Flags().StringVar(&action, "action", "none", "handle the matched dialog: none, accept, or dismiss")
	cmd.Flags().StringVar(&promptText, "prompt-text", "", "prompt text to send when --action accept handles a prompt dialog")
	cmd.Flags().StringVar(&redact, "redact", "safe", "redaction preset for returned dialog URL: safe or none")
	return cmd
}

func normalizeDialogWaitOptions(opts *dialogWaitOptions) error {
	opts.Criteria.Type = strings.ToLower(strings.TrimSpace(opts.Criteria.Type))
	opts.Criteria.Message = strings.TrimSpace(opts.Criteria.Message)
	opts.Criteria.MessageContains = strings.TrimSpace(opts.Criteria.MessageContains)
	opts.Action = strings.ToLower(strings.TrimSpace(opts.Action))
	opts.Redact = artifacts.NormalizeMode(opts.Redact)
	switch opts.Criteria.Type {
	case "", "alert", "confirm", "prompt", "beforeunload":
	default:
		return commandError("usage", "usage", "--type must be alert, confirm, prompt, or beforeunload", ExitUsage, []string{"cdp wait dialog --type alert --json"})
	}
	if opts.Criteria.Message != "" && opts.Criteria.MessageContains != "" {
		return commandError("usage", "usage", "--message and --message-contains are mutually exclusive", ExitUsage, []string{"cdp wait dialog --message-contains Saved --json"})
	}
	switch opts.Action {
	case "none", "accept", "dismiss":
	default:
		return commandError("usage", "usage", "--action must be none, accept, or dismiss", ExitUsage, []string{"cdp wait dialog --action dismiss --json"})
	}
	if opts.PromptText != "" && opts.Action != "accept" {
		return commandError("usage", "usage", "--prompt-text requires --action accept", ExitUsage, []string{"cdp wait dialog --action accept --prompt-text yes --json"})
	}
	if opts.Redact != artifacts.ModeSafe && opts.Redact != artifacts.ModeNone {
		return commandError("usage", "usage", "--redact must be safe or none", ExitUsage, []string{"cdp wait dialog --redact safe --json"})
	}
	return nil
}

func waitForDialogEvent(ctx context.Context, client browserEventClient, sessionID string, criteria dialogWaitCriteria) (dialogWaitObservation, error) {
	if err := client.CallSession(ctx, sessionID, "Page.enable", map[string]any{}, nil); err != nil {
		return dialogWaitObservation{}, err
	}
	observation := dialogWaitObservation{}
	observe := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		dialogEvent, ok := dialogWaitEventFromCDP(event)
		if !ok {
			return
		}
		observation.EventCount++
		observation.LastEvent = &dialogEvent
		if !dialogWaitMatches(dialogEvent, criteria) {
			return
		}
		observation.ObservedCount++
		observation.Event = &dialogEvent
		observation.Matched = true
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return observation, err
	}
	for _, event := range events {
		observe(event)
		if observation.Matched {
			return observation, nil
		}
	}
	for {
		event, err := client.ReadEvent(ctx)
		if err != nil {
			return observation, err
		}
		observe(event)
		if observation.Matched {
			return observation, nil
		}
	}
}

func dialogWaitEventFromCDP(event cdp.Event) (dialogWaitEvent, bool) {
	if event.Method != dialogWaitCDPName {
		return dialogWaitEvent{}, false
	}
	var params struct {
		URL               string `json:"url"`
		FrameID           string `json:"frameId"`
		Message           string `json:"message"`
		Type              string `json:"type"`
		HasBrowserHandler bool   `json:"hasBrowserHandler"`
		DefaultPrompt     string `json:"defaultPrompt"`
	}
	if err := json.Unmarshal(event.Params, &params); err != nil {
		return dialogWaitEvent{}, false
	}
	return dialogWaitEvent{
		Type:              params.Type,
		Message:           params.Message,
		DefaultPrompt:     params.DefaultPrompt,
		URL:               params.URL,
		FrameID:           params.FrameID,
		HasBrowserHandler: params.HasBrowserHandler,
		CDPMethod:         event.Method,
	}, true
}

func dialogWaitMatches(event dialogWaitEvent, criteria dialogWaitCriteria) bool {
	if criteria.Type != "" && event.Type != criteria.Type {
		return false
	}
	if criteria.Message != "" && event.Message != criteria.Message {
		return false
	}
	if criteria.MessageContains != "" && !strings.Contains(event.Message, criteria.MessageContains) {
		return false
	}
	return true
}

func dialogWaitReport(observation dialogWaitObservation, opts dialogWaitOptions, elapsed time.Duration, timeout time.Duration, redactor *artifacts.Redactor) map[string]any {
	wait := map[string]any{
		"kind":           dialogWaitKind,
		"matched":        observation.Matched,
		"criteria":       opts.Criteria,
		"cdp_method":     dialogWaitCDPName,
		"elapsed_ms":     elapsed.Milliseconds(),
		"timeout":        durationString(timeout),
		"source":         "cdp-page-events",
		"scope":          "events observed after Page.enable",
		"event_count":    observation.EventCount,
		"observed_count": observation.ObservedCount,
		"evidence": map[string]any{
			"headers": false,
			"bodies":  false,
			"bounded": true,
		},
	}
	if opts.Action == "none" {
		wait["warnings"] = []string{"JavaScript dialogs block page execution until accepted or dismissed; pass --action accept or --action dismiss, or run cdp dialog accept/dismiss after this wait"}
	}
	report := map[string]any{
		"ok":   observation.Matched,
		"wait": wait,
	}
	if observation.Event != nil {
		dialog := redactDialogWaitEvent(*observation.Event, redactor, "dialog.url")
		dialogMap := map[string]any{
			"type":                 dialog.Type,
			"message":              dialog.Message,
			"default_prompt":       dialog.DefaultPrompt,
			"url":                  dialog.URL,
			"frame_id":             dialog.FrameID,
			"has_browser_handler":  dialog.HasBrowserHandler,
			"cdp_method":           dialog.CDPMethod,
			"action":               opts.Action,
			"handled":              false,
			"accepted":             false,
			"prompt_text_supplied": false,
		}
		report["dialog"] = dialogMap
		wait["event"] = dialog
	}
	if observation.LastEvent != nil {
		last := redactDialogWaitEvent(*observation.LastEvent, redactor, "last_event.url")
		report["last_event"] = last
		wait["last_event"] = last
	}
	report["next_commands"] = dialogWaitNextCommands(opts)
	return report
}

func redactDialogWaitEvent(event dialogWaitEvent, redactor *artifacts.Redactor, field string) dialogWaitEvent {
	if event.URL != "" {
		event.URL = redactor.URL(event.URL, field)
	}
	return event
}

func markDialogHandled(report map[string]any, action string, accepted bool, promptTextSupplied bool) {
	dialog, ok := report["dialog"].(map[string]any)
	if !ok {
		return
	}
	dialog["action"] = action
	dialog["handled"] = true
	dialog["accepted"] = accepted
	dialog["prompt_text_supplied"] = promptTextSupplied
	report["next_commands"] = []string{"cdp snapshot --json", "cdp pages --json"}
}

func dialogWaitError(ctx context.Context, targetID string, opts dialogWaitOptions, report map[string]any, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return commandErrorWithData(
			"timeout",
			"timeout",
			fmt.Sprintf("wait dialog did not observe a matching dialog for target %s: %v", targetID, context.DeadlineExceeded),
			ExitTimeout,
			dialogWaitRemediations(opts),
			report,
		)
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return err
	}
	return commandError("connection_failed", "connection", fmt.Sprintf("wait dialog target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
}

func dialogWaitRemediations(opts dialogWaitOptions) []string {
	waitCommand := "cdp wait dialog"
	if opts.Criteria.Type != "" {
		waitCommand += " --type " + shellQuote(opts.Criteria.Type)
	}
	if opts.Criteria.Message != "" {
		waitCommand += " --message " + shellQuote(opts.Criteria.Message)
	}
	if opts.Criteria.MessageContains != "" {
		waitCommand += " --message-contains " + shellQuote(opts.Criteria.MessageContains)
	}
	if opts.Action != "" && opts.Action != "none" {
		waitCommand += " --action " + opts.Action
	}
	return []string{
		waitCommand + " --timeout 15s --json",
		"cdp events tap --enable page --match Page.javascriptDialogOpening --duration 5s --json",
		"cdp dialog dismiss --json",
		"cdp snapshot --json",
	}
}

func dialogWaitNextCommands(opts dialogWaitOptions) []string {
	if opts.Action == "accept" || opts.Action == "dismiss" {
		return []string{"cdp snapshot --json", "cdp pages --json"}
	}
	return []string{
		"cdp dialog accept --json",
		"cdp dialog dismiss --json",
		"cdp events tap --enable page --match Page.javascriptDialogOpening --duration 5s --json",
	}
}
