package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	eventWaitPollInterval = 20 * time.Millisecond
	eventWaitMaxLineBytes = 16 << 20
)

type eventWaitOptions struct {
	file        string
	methods     []string
	contains    []string
	fromOffset  int64
	printOffset bool
}

type eventWaitMatch struct {
	record       json.RawMessage
	event        json.RawMessage
	method       string
	offset       int64
	linesScanned int
}

func (a *app) newEventsWaitCommand() *cobra.Command {
	var options eventWaitOptions
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for a matching record in a JSONL event stream file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(options.file) == "" {
				return commandError("usage", "usage", "--file must not be empty", ExitUsage, []string{"cdp events wait --file tmp/events.jsonl --method Page.loadEventFired --json"})
			}
			if options.fromOffset < 0 {
				return commandError("usage", "usage", "--from-offset must be non-negative", ExitUsage, []string{"cdp events wait --file tmp/events.jsonl --from-offset 0 --method Page.loadEventFired --json"})
			}

			methods, err := normalizeEventWaitMethods(options.methods)
			if err != nil {
				return err
			}
			contains, err := normalizeEventWaitContains(options.contains)
			if err != nil {
				return err
			}
			if len(methods) == 0 && len(contains) == 0 {
				return commandError("usage", "usage", "provide at least one --method or --contains predicate", ExitUsage, []string{"cdp events wait --file tmp/events.jsonl --method Page.loadEventFired --timeout 20s --json"})
			}

			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()
			started := time.Now()
			match, waitErr := waitForEventFile(ctx, options.file, methods, contains, options.fromOffset)
			metadata := eventWaitMetadata(options.file, methods, contains, options.fromOffset, match, time.Since(started))
			if options.printOffset {
				if _, err := fmt.Fprintf(a.err, "offset=%d\n", match.offset); err != nil {
					return fmt.Errorf("write event wait offset: %w", err)
				}
			}
			if waitErr != nil {
				if errors.Is(waitErr, context.DeadlineExceeded) {
					return commandErrorWithData("event_wait_timeout", "timeout", fmt.Sprintf("no event record matched before the wait deadline at byte offset %d", match.offset), ExitTimeout, eventWaitRemediations(options.file), map[string]any{
						"file": options.file,
						"wait": metadata,
					})
				}
				if errors.Is(waitErr, context.Canceled) {
					return commandErrorWithData("event_wait_canceled", "canceled", fmt.Sprintf("event wait canceled at byte offset %d", match.offset), ExitCheckFailed, eventWaitRemediations(options.file), map[string]any{
						"file": options.file,
						"wait": metadata,
					})
				}
				return commandErrorWithData("event_wait_file_failed", "filesystem", fmt.Sprintf("read event stream file %q: %v", options.file, waitErr), ExitInternal, eventWaitRemediations(options.file), map[string]any{
					"file": options.file,
					"wait": metadata,
				})
			}

			data := map[string]any{
				"ok":     true,
				"file":   options.file,
				"record": match.record,
				"offset": match.offset,
				"wait":   metadata,
			}
			if len(match.event) > 0 {
				data["event"] = match.event
			}
			matched := match.method
			if matched == "" {
				matched = "record"
			}
			return a.render(ctx, fmt.Sprintf("matched %s\toffset=%d", matched, match.offset), data)
		},
	}
	cmd.Flags().StringVar(&options.file, "file", "", "JSONL event stream file to read; waits for the file if it does not exist yet")
	cmd.Flags().StringArrayVar(&options.methods, "method", nil, "CDP event method to match; repeat for any-of matching")
	cmd.Flags().StringArrayVar(&options.contains, "contains", nil, "substring that must appear in the complete JSON record; repeat for all-of matching")
	cmd.Flags().Int64Var(&options.fromOffset, "from-offset", 0, "byte offset to resume from; complete matched lines return the next offset")
	cmd.Flags().BoolVar(&options.printOffset, "print-offset", false, "also print offset=N to stderr for shell chaining")
	return cmd
}

func normalizeEventWaitMethods(raw []string) ([]string, error) {
	methods := make([]string, 0, len(raw))
	for _, value := range raw {
		method := strings.TrimSpace(value)
		if !validEventMethod(method) {
			return nil, commandError("invalid_event_method", "usage", fmt.Sprintf("invalid CDP event method %q", value), ExitUsage, []string{"cdp events wait --file tmp/events.jsonl --method Page.loadEventFired --json"})
		}
		methods = append(methods, method)
	}
	return methods, nil
}

func normalizeEventWaitContains(raw []string) ([]string, error) {
	contains := make([]string, 0, len(raw))
	for _, value := range raw {
		if strings.TrimSpace(value) == "" {
			return nil, commandError("invalid_event_contains", "usage", "--contains values must not be empty", ExitUsage, []string{"cdp events wait --file tmp/events.jsonl --contains target --json"})
		}
		contains = append(contains, value)
	}
	return contains, nil
}

func waitForEventFile(ctx context.Context, path string, methods, contains []string, fromOffset int64) (eventWaitMatch, error) {
	match := eventWaitMatch{offset: fromOffset}
	cursor := fromOffset
	ticker := time.NewTicker(eventWaitPollInterval)
	defer ticker.Stop()

	for {
		found, nextOffset, scanned, err := scanEventWaitFile(path, cursor, methods, contains)
		match.linesScanned += scanned
		if err != nil {
			match.offset = cursor
			return match, err
		}
		cursor = nextOffset
		match.offset = cursor
		if found.record != nil {
			found.linesScanned = match.linesScanned
			return found, nil
		}

		select {
		case <-ctx.Done():
			match.offset = cursor
			return match, ctx.Err()
		case <-ticker.C:
		}
	}
}

func scanEventWaitFile(path string, fromOffset int64, methods, contains []string) (eventWaitMatch, int64, int, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return eventWaitMatch{}, fromOffset, 0, nil
		}
		return eventWaitMatch{}, fromOffset, 0, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return eventWaitMatch{}, fromOffset, 0, err
	}
	if !info.Mode().IsRegular() {
		return eventWaitMatch{}, fromOffset, 0, fmt.Errorf("event stream path is not a regular file")
	}
	if info.Size() < fromOffset {
		return eventWaitMatch{}, fromOffset, 0, fmt.Errorf("file size %d is smaller than --from-offset %d; refusing to reread a truncated stream", info.Size(), fromOffset)
	}
	if _, err := file.Seek(fromOffset, io.SeekStart); err != nil {
		return eventWaitMatch{}, fromOffset, 0, err
	}

	reader := bufio.NewReader(file)
	cursor := fromOffset
	scanned := 0
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > eventWaitMaxLineBytes {
			return eventWaitMatch{}, cursor, scanned, fmt.Errorf("event stream record exceeds %d bytes", eventWaitMaxLineBytes)
		}
		if readErr == nil {
			cursor += int64(len(line))
			scanned++
			found, ok := matchEventWaitLine([]byte(line), methods, contains)
			if ok {
				found.offset = cursor
				return found, cursor, scanned, nil
			}
			continue
		}
		if errors.Is(readErr, io.EOF) {
			// A producer may have written a partial final record. Do not advance
			// past it; reopening at cursor will reread the whole record once the
			// newline arrives.
			return eventWaitMatch{}, cursor, scanned, nil
		}
		return eventWaitMatch{}, cursor, scanned, readErr
	}
}

func matchEventWaitLine(line []byte, methods, contains []string) (eventWaitMatch, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(line, &fields); err != nil {
		return eventWaitMatch{}, false
	}

	method := ""
	var event json.RawMessage
	if rawMethod, ok := fields["method"]; ok {
		_ = json.Unmarshal(rawMethod, &method)
	}
	if method == "" {
		if rawEvent, ok := fields["event"]; ok {
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(rawEvent, &nested); err == nil {
				_ = json.Unmarshal(nested["method"], &method)
				event = append(json.RawMessage(nil), rawEvent...)
			}
		}
	}
	if len(methods) > 0 && !eventWaitMethodIn(methods, method) {
		return eventWaitMatch{}, false
	}
	for _, needle := range contains {
		if !bytes.Contains(line, []byte(needle)) {
			return eventWaitMatch{}, false
		}
	}

	record := bytes.TrimRight(line, "\r\n")
	return eventWaitMatch{
		record: append(json.RawMessage(nil), record...),
		event:  event,
		method: method,
	}, true
}

func eventWaitMethodIn(methods []string, method string) bool {
	for _, candidate := range methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func eventWaitMetadata(path string, methods, contains []string, fromOffset int64, match eventWaitMatch, elapsed time.Duration) map[string]any {
	metadata := map[string]any{
		"file":             path,
		"methods":          methods,
		"contains":         contains,
		"from_offset":      fromOffset,
		"offset":           match.offset,
		"lines_scanned":    match.linesScanned,
		"elapsed_ms":       elapsed.Milliseconds(),
		"poll_interval":    eventWaitPollInterval.String(),
		"complete_records": true,
	}
	if match.method != "" {
		metadata["matched_method"] = match.method
	}
	return metadata
}

func eventWaitRemediations(path string) []string {
	return []string{
		"inspect the event stream producer and file path: " + path,
		"cdp events stream --json",
		"cdp pages --json",
	}
}
