package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"encoding/json"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type consoleMessage struct {
	ID               int                 `json:"id"`
	Source           string              `json:"source"`
	Type             string              `json:"type,omitempty"`
	Level            string              `json:"level,omitempty"`
	Text             string              `json:"text"`
	Timestamp        float64             `json:"timestamp,omitempty"`
	URL              string              `json:"url,omitempty"`
	LineNumber       int                 `json:"line_number,omitempty"`
	ColumnNumber     int                 `json:"column_number,omitempty"`
	ScriptID         string              `json:"script_id,omitempty"`
	NetworkRequestID string              `json:"network_request_id,omitempty"`
	Args             []consoleMessageArg `json:"args,omitempty"`
	Exception        *consoleMessageArg  `json:"exception,omitempty"`
	StackTrace       json.RawMessage     `json:"stack_trace,omitempty"`
}

type consoleMessageArg struct {
	Type                string          `json:"type"`
	Subtype             string          `json:"subtype,omitempty"`
	ClassName           string          `json:"class_name,omitempty"`
	Description         string          `json:"description,omitempty"`
	Value               json.RawMessage `json:"value,omitempty"`
	UnserializableValue string          `json:"unserializable_value,omitempty"`
}

func collectConsoleMessages(ctx context.Context, client browserEventClient, sessionID string, wait time.Duration, limit int, errorsOnly bool, typeSet map[string]bool) ([]consoleMessage, bool, error) {
	if err := client.CallSession(ctx, sessionID, "Runtime.enable", map[string]any{}, nil); err != nil {
		return nil, false, err
	}
	if err := client.CallSession(ctx, sessionID, "Log.enable", map[string]any{}, nil); err != nil {
		return nil, false, err
	}

	var messages []consoleMessage
	truncated := false
	addEventMessages := func(events []cdp.Event) {
		for _, event := range events {
			if event.SessionID != "" && event.SessionID != sessionID {
				continue
			}
			msg, ok := consoleMessageFromEvent(event)
			if !ok || !keepConsoleMessage(msg, errorsOnly, typeSet) {
				continue
			}
			if limit > 0 && len(messages) >= limit {
				truncated = true
				continue
			}
			msg.ID = len(messages)
			messages = append(messages, msg)
		}
	}
	events, err := client.DrainEvents(ctx)
	if err != nil {
		return nil, false, err
	}
	addEventMessages(events)

	if wait > 0 {
		eventCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				return nil, false, err
			}
			addEventMessages([]cdp.Event{event})
		}
	}

	return messages, truncated, nil
}

func consoleMessageFromEvent(event cdp.Event) (consoleMessage, bool) {
	switch event.Method {
	case "Runtime.consoleAPICalled":
		var params struct {
			Type       string              `json:"type"`
			Args       []consoleMessageArg `json:"args"`
			Timestamp  float64             `json:"timestamp"`
			StackTrace json.RawMessage     `json:"stackTrace"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return consoleMessage{}, false
		}
		return consoleMessage{
			Source:     "runtime",
			Type:       params.Type,
			Level:      runtimeConsoleLevel(params.Type),
			Text:       consoleArgsText(params.Args),
			Timestamp:  params.Timestamp,
			Args:       params.Args,
			StackTrace: params.StackTrace,
		}, true
	case "Runtime.exceptionThrown":
		var params struct {
			Timestamp        float64 `json:"timestamp"`
			ExceptionDetails struct {
				Text         string            `json:"text"`
				URL          string            `json:"url"`
				LineNumber   int               `json:"lineNumber"`
				ColumnNumber int               `json:"columnNumber"`
				ScriptID     string            `json:"scriptId"`
				StackTrace   json.RawMessage   `json:"stackTrace"`
				Exception    consoleMessageArg `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return consoleMessage{}, false
		}
		text := consoleExceptionText(params.ExceptionDetails.Text, params.ExceptionDetails.Exception)
		exception := params.ExceptionDetails.Exception
		return consoleMessage{
			Source:       "runtime",
			Type:         "exception",
			Level:        "error",
			Text:         text,
			Timestamp:    params.Timestamp,
			URL:          params.ExceptionDetails.URL,
			LineNumber:   params.ExceptionDetails.LineNumber,
			ColumnNumber: params.ExceptionDetails.ColumnNumber,
			ScriptID:     params.ExceptionDetails.ScriptID,
			Exception:    &exception,
			StackTrace:   params.ExceptionDetails.StackTrace,
		}, true
	case "Log.entryAdded":
		var params struct {
			Entry struct {
				Source           string              `json:"source"`
				Level            string              `json:"level"`
				Text             string              `json:"text"`
				Timestamp        float64             `json:"timestamp"`
				URL              string              `json:"url"`
				LineNumber       int                 `json:"lineNumber"`
				NetworkRequestID string              `json:"networkRequestId"`
				Args             []consoleMessageArg `json:"args"`
				StackTrace       json.RawMessage     `json:"stackTrace"`
			} `json:"entry"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return consoleMessage{}, false
		}
		text := params.Entry.Text
		if text == "" {
			text = consoleArgsText(params.Entry.Args)
		}
		return consoleMessage{
			Source:           params.Entry.Source,
			Level:            params.Entry.Level,
			Text:             text,
			Timestamp:        params.Entry.Timestamp,
			URL:              params.Entry.URL,
			LineNumber:       params.Entry.LineNumber,
			NetworkRequestID: params.Entry.NetworkRequestID,
			Args:             params.Entry.Args,
			StackTrace:       params.Entry.StackTrace,
		}, true
	default:
		return consoleMessage{}, false
	}
}

func runtimeConsoleLevel(consoleType string) string {
	switch consoleType {
	case "error", "assert":
		return "error"
	case "warning":
		return "warning"
	case "debug":
		return "verbose"
	default:
		return "info"
	}
}

func consoleArgsText(args []consoleMessageArg) string {
	texts := make([]string, 0, len(args))
	for _, arg := range args {
		if text := consoleArgText(arg); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, " ")
}

func consoleExceptionText(prefix string, exception consoleMessageArg) string {
	text := strings.TrimSpace(prefix)
	detail := strings.TrimSpace(consoleArgText(exception))
	if detail == "" || detail == exception.Type {
		return text
	}
	if text == "" {
		return detail
	}
	if strings.Contains(text, detail) {
		return text
	}
	return text + ": " + detail
}

func consoleArgText(arg consoleMessageArg) string {
	if len(arg.Value) > 0 {
		var value any
		if err := json.Unmarshal(arg.Value, &value); err == nil {
			return fmt.Sprint(value)
		}
		return string(arg.Value)
	}
	if arg.UnserializableValue != "" {
		return arg.UnserializableValue
	}
	if arg.Description != "" {
		return arg.Description
	}
	if arg.ClassName != "" {
		return arg.ClassName
	}
	return arg.Type
}

func keepConsoleMessage(msg consoleMessage, errorsOnly bool, typeSet map[string]bool) bool {
	if errorsOnly && msg.Level != "error" && msg.Level != "warning" && msg.Type != "exception" && msg.Type != "assert" {
		return false
	}
	if len(typeSet) == 0 {
		return true
	}
	return typeSet[strings.ToLower(msg.Type)] || typeSet[strings.ToLower(msg.Level)] || typeSet[strings.ToLower(msg.Source)]
}

func consoleMessageLines(messages []consoleMessage) []string {
	lines := make([]string, 0, len(messages))
	for _, msg := range messages {
		label := msg.Level
		if label == "" {
			label = msg.Type
		}
		if label == "" {
			label = msg.Source
		}
		lines = append(lines, fmt.Sprintf("%d\t%s\t%s", msg.ID, label, msg.Text))
	}
	return lines
}

func parseCSVSet(value string) map[string]bool {
	set := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			set[part] = true
		}
	}
	return set
}

func setKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
