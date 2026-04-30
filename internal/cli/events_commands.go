package cli

import (
	"fmt"
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
		for domain := range parseCSVSet(enable) {
			switch domain {
			case "page":
				_ = client.CallSession(ctx, session.SessionID, "Page.enable", map[string]any{}, nil)
			case "network":
				_ = client.CallSession(ctx, session.SessionID, "Network.enable", map[string]any{}, nil)
			case "runtime":
				_ = client.CallSession(ctx, session.SessionID, "Runtime.enable", map[string]any{}, nil)
			case "log":
				_ = client.CallSession(ctx, session.SessionID, "Log.enable", map[string]any{}, nil)
			}
		}
		matches := parseCSVSet(match)
		var events []cdp.Event
		deadline := time.NewTimer(duration)
		defer deadline.Stop()
		for duration == 0 || maxEvents == 0 || len(events) < maxEvents {
			event, err := client.ReadEvent(ctx)
			if err != nil {
				break
			}
			if len(matches) == 0 || matches[event.Method] {
				events = append(events, event)
			}
			if maxEvents > 0 && len(events) >= maxEvents {
				break
			}
			select {
			case <-deadline.C:
				goto done
			default:
			}
		}
	done:
		return a.render(ctx, fmt.Sprintf("events\t%d", len(events)), map[string]any{"ok": true, "target": pageRow(target), "events": events, "tap": map[string]any{"duration": durationString(duration), "max_events": maxEvents, "truncated": maxEvents > 0 && len(events) >= maxEvents}})
	}}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&urlContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&enable, "enable", "page,network,runtime,log", "comma-separated domains to enable: page,network,runtime,log")
	cmd.Flags().StringVar(&match, "match", "", "comma-separated event method names to keep")
	cmd.Flags().DurationVar(&duration, "duration", 5*time.Second, "maximum event collection duration")
	cmd.Flags().IntVar(&maxEvents, "max-events", 100, "maximum events to collect; 0 disables the count limit")
	return cmd
}
