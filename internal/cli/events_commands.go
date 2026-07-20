package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func (a *app) newEventsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "events", Short: "Observe bounded raw CDP event streams"}
	cmd.AddCommand(a.newEventsTapCommand())
	return cmd
}

func (a *app) newEventsTapCommand() *cobra.Command {
	var targetID, urlContains, titleContains, enable, match string
	var readyFile string
	var duration time.Duration
	var maxEvents int
	cmd := &cobra.Command{Use: "tap", Short: "Collect a bounded stream of CDP events", RunE: func(cmd *cobra.Command, args []string) error {
		if duration < 0 || maxEvents < 0 {
			return commandError("usage", "usage", "--duration and --max-events must be non-negative", ExitUsage, []string{"cdp events tap --duration 10s --max-events 50 --json"})
		}
		ctx, cancel := a.commandContextWithDefault(cmd, duration+10*time.Second)
		defer cancel()
		client, session, target, err := a.attachPageEventSession(ctx, targetID, urlContains, titleContains)
		if err != nil {
			return err
		}
		defer session.Close(ctx)
		enabledDomains := parseCSVSet(enable)
		for domain := range enabledDomains {
			var enableErr error
			switch domain {
			case "page":
				enableErr = client.CallSession(ctx, session.SessionID, "Page.enable", map[string]any{}, nil)
			case "network":
				enableErr = client.CallSession(ctx, session.SessionID, "Network.enable", map[string]any{}, nil)
			case "runtime":
				enableErr = client.CallSession(ctx, session.SessionID, "Runtime.enable", map[string]any{}, nil)
			case "log":
				enableErr = client.CallSession(ctx, session.SessionID, "Log.enable", map[string]any{}, nil)
			default:
				return commandError("usage", "usage", fmt.Sprintf("unsupported --enable domain %q", domain), ExitUsage, []string{"cdp events tap --enable page,network,runtime,log --json"})
			}
			if enableErr != nil {
				return commandError("collector_enable_failed", "connection", fmt.Sprintf("enable %s for target %s: %v", domain, target.TargetID, enableErr), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
			}
		}
		domainNames := setKeys(enabledDomains)
		sort.Strings(domainNames)
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
		return a.render(ctx, fmt.Sprintf("events\t%d", len(events)), map[string]any{"ok": true, "target": pageRow(target), "events": events, "tap": map[string]any{"duration": durationString(duration), "max_events": maxEvents, "truncated": maxEvents > 0 && len(events) >= maxEvents, "session_bound": true, "foreign_events_dropped": foreignEventsDropped, "ready_file": readyFile}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&enable, "enable", "page,network,runtime,log", "comma-separated domains to enable: page,network,runtime,log")
	cmd.Flags().StringVar(&match, "match", "", "comma-separated event method names to keep")
	cmd.Flags().DurationVar(&duration, "duration", 5*time.Second, "maximum event collection duration")
	cmd.Flags().IntVar(&maxEvents, "max-events", 100, "maximum events to collect; 0 disables the count limit")
	cmd.Flags().StringVar(&readyFile, "ready-file", "", "exclusive owner-only readiness artifact written after exact-target attach and domain enable")
	return cmd
}
