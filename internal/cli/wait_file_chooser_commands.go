package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	fileChooserWaitKind    = "file-chooser"
	fileChooserWaitCDPName = "Page.fileChooserOpened"
)

type fileChooserWaitCriteria struct {
	Mode string `json:"mode,omitempty"`
}

type fileChooserWaitEvent struct {
	FrameID       string `json:"frame_id,omitempty"`
	Mode          string `json:"mode"`
	Multiple      bool   `json:"multiple"`
	BackendNodeID int    `json:"backend_node_id,omitempty"`
	CDPMethod     string `json:"cdp_method"`
}

type fileChooserWaitObservation struct {
	Matched       bool
	EventCount    int
	ObservedCount int
	Event         *fileChooserWaitEvent
	LastEvent     *fileChooserWaitEvent
}

func (a *app) newWaitFileChooserCommand() *cobra.Command {
	var targetID string
	var pageURLContains string
	var titleContains string
	var mode string
	cmd := &cobra.Command{
		Use:   "file-chooser",
		Short: "Wait for a file chooser to open without showing a native dialog",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			criteria := fileChooserWaitCriteria{Mode: mode}
			if err := normalizeFileChooserWaitCriteria(&criteria); err != nil {
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
			observation, teardown, err := waitForFileChooserEvent(ctx, client, session.SessionID, criteria)
			elapsed := time.Since(start)
			if teardown != nil {
				teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer teardownCancel()
				_ = teardown(teardownCtx)
			}
			report := fileChooserWaitReport(observation, criteria, elapsed, a.effectiveNetworkWaitTimeout())
			report["target"] = pageRow(target)
			if err != nil {
				return fileChooserWaitError(ctx, session.TargetID, criteria, report, err)
			}
			return a.render(ctx, fmt.Sprintf("matched file-chooser\t%s", observation.Event.Mode), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix")
	cmd.Flags().StringVar(&pageURLContains, "url-contains", "", "use the first page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first page whose title contains this text")
	cmd.Flags().StringVar(&mode, "mode", "", "input mode to match: selectSingle/single or selectMultiple/multiple")
	return cmd
}

func normalizeFileChooserWaitCriteria(criteria *fileChooserWaitCriteria) error {
	switch strings.TrimSpace(criteria.Mode) {
	case "":
		criteria.Mode = ""
	case "selectSingle", "single":
		criteria.Mode = "selectSingle"
	case "selectMultiple", "multiple":
		criteria.Mode = "selectMultiple"
	default:
		return commandError("usage", "usage", "--mode must be selectSingle/single or selectMultiple/multiple", ExitUsage, []string{"cdp wait file-chooser --mode single --json"})
	}
	return nil
}

func waitForFileChooserEvent(ctx context.Context, client browserEventClient, sessionID string, criteria fileChooserWaitCriteria) (fileChooserWaitObservation, func(context.Context) error, error) {
	teardown, err := setupFileChooserWait(ctx, client, sessionID)
	if err != nil {
		return fileChooserWaitObservation{}, nil, err
	}
	observation, err := collectFileChooserEvent(ctx, client, sessionID, criteria)
	return observation, teardown, err
}

func setupFileChooserWait(ctx context.Context, client browserEventClient, sessionID string) (func(context.Context) error, error) {
	if err := client.CallSession(ctx, sessionID, "Page.enable", map[string]any{"enableFileChooserOpenedEvent": true}, nil); err != nil {
		return nil, err
	}
	if err := client.CallSession(ctx, sessionID, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}, nil); err != nil {
		return nil, err
	}
	teardown := func(teardownCtx context.Context) error {
		return client.CallSession(teardownCtx, sessionID, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": false}, nil)
	}
	return teardown, nil
}

func collectFileChooserEvent(ctx context.Context, client browserEventClient, sessionID string, criteria fileChooserWaitCriteria) (fileChooserWaitObservation, error) {
	observation := fileChooserWaitObservation{}
	observe := func(event cdp.Event) {
		if event.SessionID != "" && event.SessionID != sessionID {
			return
		}
		chooserEvent, ok := fileChooserWaitEventFromCDP(event)
		if !ok {
			return
		}
		observation.EventCount++
		observation.LastEvent = &chooserEvent
		if !fileChooserWaitMatches(chooserEvent, criteria) {
			return
		}
		observation.ObservedCount++
		observation.Event = &chooserEvent
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

func fileChooserWaitEventFromCDP(event cdp.Event) (fileChooserWaitEvent, bool) {
	if event.Method != fileChooserWaitCDPName {
		return fileChooserWaitEvent{}, false
	}
	var params struct {
		FrameID       string `json:"frameId"`
		Mode          string `json:"mode"`
		BackendNodeID int    `json:"backendNodeId"`
	}
	if err := json.Unmarshal(event.Params, &params); err != nil {
		return fileChooserWaitEvent{}, false
	}
	return fileChooserWaitEvent{
		FrameID:       params.FrameID,
		Mode:          params.Mode,
		Multiple:      params.Mode == "selectMultiple",
		BackendNodeID: params.BackendNodeID,
		CDPMethod:     event.Method,
	}, true
}

func fileChooserWaitMatches(event fileChooserWaitEvent, criteria fileChooserWaitCriteria) bool {
	return criteria.Mode == "" || event.Mode == criteria.Mode
}

func fileChooserWaitReport(observation fileChooserWaitObservation, criteria fileChooserWaitCriteria, elapsed time.Duration, timeout time.Duration) map[string]any {
	wait := map[string]any{
		"kind":           fileChooserWaitKind,
		"matched":        observation.Matched,
		"criteria":       criteria,
		"cdp_method":     fileChooserWaitCDPName,
		"elapsed_ms":     elapsed.Milliseconds(),
		"timeout":        durationString(timeout),
		"source":         "cdp-page-events",
		"scope":          "Page.enable(enableFileChooserOpenedEvent) plus Page.setInterceptFileChooserDialog",
		"event_count":    observation.EventCount,
		"observed_count": observation.ObservedCount,
		"intercepted":    true,
		"evidence": map[string]any{
			"headers": false,
			"bodies":  false,
			"bounded": true,
		},
		"warnings": []string{"file chooser interception prevents the native dialog from opening; use cdp file on the associated input to assign a local file"},
	}
	report := map[string]any{
		"ok":   observation.Matched,
		"wait": wait,
	}
	if observation.Event != nil {
		report["file_chooser"] = observation.Event
		wait["event"] = observation.Event
	}
	if observation.LastEvent != nil {
		report["last_event"] = observation.LastEvent
		wait["last_event"] = observation.LastEvent
	}
	report["next_commands"] = fileChooserWaitNextCommands()
	return report
}

func fileChooserWaitError(ctx context.Context, targetID string, criteria fileChooserWaitCriteria, report map[string]any, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return commandErrorWithData(
			"timeout",
			"timeout",
			fmt.Sprintf("wait file-chooser did not observe a matching file chooser for target %s: %v", targetID, context.DeadlineExceeded),
			ExitTimeout,
			fileChooserWaitRemediations(criteria),
			report,
		)
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return err
	}
	return commandError("connection_failed", "connection", fmt.Sprintf("wait file-chooser target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
}

func fileChooserWaitRemediations(criteria fileChooserWaitCriteria) []string {
	waitCommand := "cdp wait file-chooser"
	if criteria.Mode != "" {
		waitCommand += " --mode " + shellQuote(criteria.Mode)
	}
	return []string{
		waitCommand + " --timeout 15s --json",
		"cdp events tap --enable page --match Page.fileChooserOpened --duration 5s --json",
		"cdp locator find 'Upload file' --by label --json",
		"cdp file input[type=file] tmp/upload.txt --json",
	}
}

func fileChooserWaitNextCommands() []string {
	return []string{
		"cdp file input[type=file] tmp/upload.txt --json",
		"cdp locator find 'Upload file' --by label --json",
		"cdp events tap --enable page --match Page.fileChooserOpened --duration 5s --json",
	}
}
