package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newEventsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "events", Short: "Observe bounded CDP events and DOM interaction causes"}
	cmd.AddCommand(a.newEventsTapCommand())
	cmd.AddCommand(a.newEventsStreamCommand())
	cmd.AddCommand(a.newEventsWaitCommand())
	cmd.AddCommand(a.newEventsInteractionsCommand())
	return cmd
}

func (a *app) newEventsTapCommand() *cobra.Command {
	var targetID, urlContains, titleContains, enable, match string
	var readyFile string
	var duration time.Duration
	var maxEvents, targetIndex int
	cmd := &cobra.Command{Use: "tap", Short: "Collect a bounded stream of CDP events", RunE: func(cmd *cobra.Command, args []string) error {
		if duration < 0 || maxEvents < 0 {
			return commandError("usage", "usage", "--duration and --max-events must be non-negative", ExitUsage, []string{"cdp events tap --duration 10s --max-events 50 --json"})
		}
		if targetIndex < 0 || (cmd.Flags().Changed("target-index") && targetIndex == 0) {
			return commandError("invalid_target_index", "usage", "--target-index must be greater than zero", ExitUsage, []string{"cdp pages --json"})
		}
		if targetIndex > 0 && (strings.TrimSpace(targetID) != "" || strings.TrimSpace(urlContains) != "" || strings.TrimSpace(titleContains) != "") {
			return commandError("invalid_target_selector", "usage", "--target-index cannot be combined with --target, --url-contains, or --title-contains", ExitUsage, []string{"cdp pages --json"})
		}
		enabledDomains, err := parseEventDomains(enable)
		if err != nil {
			return err
		}
		ctx, cancel := a.commandContextWithDefault(cmd, duration+10*time.Second)
		defer cancel()
		client, session, target, err := a.attachPageEventSessionWithIndex(ctx, targetID, urlContains, titleContains, targetIndex)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		for _, domain := range enabledDomains.names() {
			if err := enableEventDomain(ctx, client, session.SessionID, domain); err != nil {
				return commandError("collector_enable_failed", "connection", fmt.Sprintf("enable %s for target %s: %v", domain, target.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
			}
		}
		domainNames := enabledDomains.names()
		removeReady, err := publishCollectorReadiness(readyFile, target.TargetID, session.SessionID, domainNames)
		if err != nil {
			return collectorReadinessError(err)
		}
		defer removeReady()
		matches := parseCSVSet(match)
		var events []cdp.Event
		foreignEventsDropped := 0
		eventCtx := ctx
		cancelEvents := func() {}
		if duration > 0 {
			eventCtx, cancelEvents = context.WithTimeout(ctx, duration)
		}
		defer cancelEvents()
		for duration == 0 || maxEvents == 0 || len(events) < maxEvents {
			event, err := client.ReadEvent(eventCtx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded) {
					break
				}
				break
			}
			if event.SessionID != "" && event.SessionID != session.SessionID {
				foreignEventsDropped++
				continue
			}
			if len(matches) == 0 || matches[strings.ToLower(event.Method)] {
				events = append(events, event)
			}
			if maxEvents > 0 && len(events) >= maxEvents {
				break
			}
		}
		return a.render(ctx, fmt.Sprintf("events\t%d", len(events)), map[string]any{"ok": true, "target": pageRow(target), "events": events, "tap": map[string]any{"duration": durationString(duration), "max_events": maxEvents, "target_index": targetIndex, "truncated": maxEvents > 0 && len(events) >= maxEvents, "session_bound": true, "foreign_events_dropped": foreignEventsDropped, "ready_file": readyFile}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the unique page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the unique page whose title contains this text")
	cmd.Flags().IntVar(&targetIndex, "target-index", 0, "select a 1-based page target index")
	cmd.Flags().StringVar(&enable, "enable", "page,network,runtime,log", "comma-separated CDP target domains to enable (for example page,network,runtime,log,DOM,Performance)")
	cmd.Flags().StringVar(&match, "match", "", "comma-separated event method names to keep")
	cmd.Flags().DurationVar(&duration, "duration", 5*time.Second, "maximum event collection duration")
	cmd.Flags().IntVar(&maxEvents, "max-events", 100, "maximum events to collect; 0 disables the count limit")
	cmd.Flags().StringVar(&readyFile, "ready-file", "", "exclusive owner-only readiness artifact written after exact-target attach and domain enable")
	return cmd
}
