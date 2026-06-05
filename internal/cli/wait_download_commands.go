package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	downloadWaitKind               = "download"
	downloadWaitCDPMethodWillBegin = "Browser.downloadWillBegin"
	downloadWaitCDPMethodProgress  = "Browser.downloadProgress"
)

var errDownloadCanceled = errors.New("download canceled")

type downloadWaitCriteria struct {
	URLContains      string `json:"url_contains,omitempty"`
	FilenameContains string `json:"filename_contains,omitempty"`
	State            string `json:"state"`
}

type downloadWaitOptions struct {
	Criteria    downloadWaitCriteria
	DownloadDir string
	Redact      string
}

type downloadWaitEvent struct {
	Kind              string  `json:"kind"`
	GUID              string  `json:"guid"`
	URL               string  `json:"url,omitempty"`
	SuggestedFilename string  `json:"suggested_filename,omitempty"`
	FrameID           string  `json:"frame_id,omitempty"`
	State             string  `json:"state,omitempty"`
	TotalBytes        float64 `json:"total_bytes,omitempty"`
	ReceivedBytes     float64 `json:"received_bytes,omitempty"`
	FilePath          string  `json:"file_path,omitempty"`
	CDPMethod         string  `json:"cdp_method"`
}

type downloadWaitObservation struct {
	Matched       bool
	EventCount    int
	ObservedCount int
	Begin         *downloadWaitEvent
	Progress      *downloadWaitEvent
	LastEvent     *downloadWaitEvent
}

func (a *app) newWaitDownloadCommand() *cobra.Command {
	var targetID string
	var pageURLContains string
	var titleContains string
	var matchURL string
	var filenameContains string
	var state string
	var downloadDir string
	var redact string
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Wait for a browser download to start or complete",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := downloadWaitOptions{
				Criteria: downloadWaitCriteria{
					URLContains:      strings.TrimSpace(matchURL),
					FilenameContains: strings.TrimSpace(filenameContains),
					State:            strings.TrimSpace(state),
				},
				DownloadDir: strings.TrimSpace(downloadDir),
				Redact:      redact,
			}
			if err := a.normalizeDownloadWaitOptions(&opts); err != nil {
				return err
			}
			redactor, err := networkWaitRedactor(opts.Redact)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(opts.DownloadDir, 0o700); err != nil {
				return commandError("download_dir_unavailable", "usage", fmt.Sprintf("create download dir %s: %v", opts.DownloadDir, err), ExitUsage, []string{"cdp wait download --download-dir tmp/downloads --json"})
			}

			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()

			client, closeClient, err := a.browserEventCDPClient(ctx)
			if err != nil {
				return commandError(
					"connection_not_configured",
					"connection",
					err.Error(),
					ExitConnection,
					a.connectionRemediationCommands(),
				)
			}
			defer closeClient(ctx)

			target, err := a.resolvePageTargetWithClient(ctx, client, targetID, pageURLContains, titleContains)
			if err != nil {
				return err
			}

			start := time.Now()
			observation, teardown, err := waitForDownloadEvent(ctx, client, opts)
			elapsed := time.Since(start)
			if teardown != nil {
				teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer teardownCancel()
				_ = teardown(teardownCtx)
			}
			report := downloadWaitReport(observation, opts, target, elapsed, a.effectiveNetworkWaitTimeout(), redactor)
			if err != nil {
				return downloadWaitError(ctx, target.TargetID, opts, report, err)
			}
			return a.render(ctx, fmt.Sprintf("matched download\t%s", observation.Begin.SuggestedFilename), report)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "page target id or unique prefix used as the triggering page context")
	cmd.Flags().StringVar(&pageURLContains, "url-contains", "", "use the first triggering page whose URL contains this text")
	cmd.Flags().StringVar(&titleContains, "title-contains", "", "use the first triggering page whose title contains this text")
	cmd.Flags().StringVar(&matchURL, "match-url", "", "substring that the download URL must contain")
	cmd.Flags().StringVar(&filenameContains, "filename-contains", "", "substring that the suggested filename must contain")
	cmd.Flags().StringVar(&state, "state", "completed", "download wait state: started or completed")
	cmd.Flags().StringVar(&downloadDir, "download-dir", "", "directory where Chrome should save downloaded files; defaults to the cdp state download directory")
	cmd.Flags().StringVar(&redact, "redact", "safe", "redaction preset for returned download URL: safe or none")
	return cmd
}

func (a *app) normalizeDownloadWaitOptions(opts *downloadWaitOptions) error {
	opts.Criteria.State = strings.ToLower(strings.TrimSpace(opts.Criteria.State))
	switch opts.Criteria.State {
	case "", "completed":
		opts.Criteria.State = "completed"
	case "started", "start":
		opts.Criteria.State = "started"
	default:
		return commandError("usage", "usage", "--state must be started or completed", ExitUsage, []string{"cdp wait download --state completed --json"})
	}
	opts.Redact = artifacts.NormalizeMode(opts.Redact)
	if opts.Redact != artifacts.ModeSafe && opts.Redact != artifacts.ModeNone {
		return commandError("usage", "usage", "--redact must be safe or none", ExitUsage, []string{"cdp wait download --redact safe --json"})
	}
	if strings.TrimSpace(opts.DownloadDir) == "" {
		store, err := a.stateStore()
		if err != nil {
			return commandError("state_unavailable", "connection", fmt.Sprintf("resolve state dir: %v", err), ExitConnection, a.connectionRemediationCommands())
		}
		opts.DownloadDir = filepath.Join(store.Dir, "downloads")
	}
	abs, err := filepath.Abs(opts.DownloadDir)
	if err != nil {
		return commandError("download_dir_unavailable", "usage", fmt.Sprintf("resolve download dir %s: %v", opts.DownloadDir, err), ExitUsage, []string{"cdp wait download --download-dir tmp/downloads --json"})
	}
	opts.DownloadDir = abs
	return nil
}

func waitForDownloadEvent(ctx context.Context, client browserEventClient, opts downloadWaitOptions) (downloadWaitObservation, func(context.Context) error, error) {
	if _, err := client.DrainEvents(ctx); err != nil {
		return downloadWaitObservation{}, nil, err
	}
	params := map[string]any{
		"behavior":      "allowAndName",
		"downloadPath":  opts.DownloadDir,
		"eventsEnabled": true,
	}
	if err := client.Call(ctx, "Browser.setDownloadBehavior", params, nil); err != nil {
		return downloadWaitObservation{}, nil, err
	}
	teardown := func(teardownCtx context.Context) error {
		return client.Call(teardownCtx, "Browser.setDownloadBehavior", map[string]any{"behavior": "default", "eventsEnabled": false}, nil)
	}

	observation := downloadWaitObservation{}
	observe := func(event cdp.Event) (bool, error) {
		downloadEvent, ok := downloadWaitEventFromCDP(event)
		if !ok {
			return false, nil
		}
		observation.EventCount++
		observation.LastEvent = &downloadEvent
		if downloadEvent.Kind == "will-begin" {
			if !downloadWaitMatches(downloadEvent, opts.Criteria) {
				return false, nil
			}
			observation.ObservedCount++
			observation.Begin = &downloadEvent
			if opts.Criteria.State == "started" {
				observation.Matched = true
				return true, nil
			}
			return false, nil
		}
		if observation.Begin == nil || downloadEvent.GUID != observation.Begin.GUID {
			return false, nil
		}
		observation.Progress = &downloadEvent
		if downloadEvent.State == "canceled" {
			return false, errDownloadCanceled
		}
		if opts.Criteria.State == "completed" && downloadEvent.State == "completed" {
			observation.Matched = true
			return true, nil
		}
		return false, nil
	}
	for {
		event, err := client.ReadEvent(ctx)
		if err != nil {
			return observation, teardown, err
		}
		matched, err := observe(event)
		if matched || err != nil {
			return observation, teardown, err
		}
	}
}

func downloadWaitEventFromCDP(event cdp.Event) (downloadWaitEvent, bool) {
	switch event.Method {
	case downloadWaitCDPMethodWillBegin:
		var params struct {
			FrameID           string `json:"frameId"`
			GUID              string `json:"guid"`
			URL               string `json:"url"`
			SuggestedFilename string `json:"suggestedFilename"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil || params.GUID == "" {
			return downloadWaitEvent{}, false
		}
		return downloadWaitEvent{
			Kind:              "will-begin",
			GUID:              params.GUID,
			URL:               params.URL,
			SuggestedFilename: params.SuggestedFilename,
			FrameID:           params.FrameID,
			CDPMethod:         event.Method,
		}, true
	case downloadWaitCDPMethodProgress:
		var params struct {
			GUID          string  `json:"guid"`
			TotalBytes    float64 `json:"totalBytes"`
			ReceivedBytes float64 `json:"receivedBytes"`
			State         string  `json:"state"`
			FilePath      string  `json:"filePath"`
		}
		if err := json.Unmarshal(event.Params, &params); err != nil || params.GUID == "" {
			return downloadWaitEvent{}, false
		}
		return downloadWaitEvent{
			Kind:          "progress",
			GUID:          params.GUID,
			State:         params.State,
			TotalBytes:    params.TotalBytes,
			ReceivedBytes: params.ReceivedBytes,
			FilePath:      params.FilePath,
			CDPMethod:     event.Method,
		}, true
	default:
		return downloadWaitEvent{}, false
	}
}

func downloadWaitMatches(event downloadWaitEvent, criteria downloadWaitCriteria) bool {
	if criteria.URLContains != "" && !strings.Contains(strings.ToLower(event.URL), strings.ToLower(criteria.URLContains)) {
		return false
	}
	if criteria.FilenameContains != "" && !strings.Contains(strings.ToLower(event.SuggestedFilename), strings.ToLower(criteria.FilenameContains)) {
		return false
	}
	return true
}

func downloadWaitReport(observation downloadWaitObservation, opts downloadWaitOptions, target cdp.TargetInfo, elapsed time.Duration, timeout time.Duration, redactor *artifacts.Redactor) map[string]any {
	wait := map[string]any{
		"kind":           downloadWaitKind,
		"matched":        observation.Matched,
		"criteria":       opts.Criteria,
		"cdp_methods":    []string{downloadWaitCDPMethodWillBegin, downloadWaitCDPMethodProgress},
		"elapsed_ms":     elapsed.Milliseconds(),
		"timeout":        durationString(timeout),
		"source":         "cdp-browser-events",
		"scope":          "Browser.setDownloadBehavior(eventsEnabled) with browser-scoped download events",
		"download_dir":   opts.DownloadDir,
		"event_count":    observation.EventCount,
		"observed_count": observation.ObservedCount,
		"redact":         opts.Redact,
		"evidence": map[string]any{
			"headers": false,
			"bodies":  false,
			"bounded": true,
		},
		"warnings": []string{"Browser download events are browser-scoped; use --match-url or --filename-contains when multiple pages may download concurrently", "downloaded files can contain page-provided content; do not commit them unless they are synthetic fixtures"},
	}
	report := map[string]any{
		"ok":            observation.Matched,
		"target":        pageRow(target),
		"wait":          wait,
		"next_commands": downloadWaitNextCommands(observation.Begin, opts.DownloadDir),
	}
	if observation.Begin != nil {
		begin := redactDownloadWaitEvent(*observation.Begin, redactor, "download.url")
		report["event"] = &begin
		wait["event"] = &begin
	}
	if observation.Progress != nil {
		progress := *observation.Progress
		report["progress"] = &progress
		wait["progress"] = &progress
	}
	if observation.Begin != nil {
		download := downloadWaitSummary(*observation.Begin, observation.Progress, redactor)
		report["download"] = download
	}
	if observation.LastEvent != nil {
		last := redactDownloadWaitEvent(*observation.LastEvent, redactor, "last_event.url")
		report["last_event"] = &last
		wait["last_event"] = &last
	}
	return report
}

func downloadWaitSummary(begin downloadWaitEvent, progress *downloadWaitEvent, redactor *artifacts.Redactor) map[string]any {
	begin = redactDownloadWaitEvent(begin, redactor, "download.url")
	state := "started"
	completed := false
	canceled := false
	var totalBytes float64
	var receivedBytes float64
	filePath := ""
	if progress != nil {
		state = progress.State
		completed = progress.State == "completed"
		canceled = progress.State == "canceled"
		totalBytes = progress.TotalBytes
		receivedBytes = progress.ReceivedBytes
		filePath = progress.FilePath
	}
	return map[string]any{
		"guid":               begin.GUID,
		"url":                begin.URL,
		"suggested_filename": begin.SuggestedFilename,
		"frame_id":           begin.FrameID,
		"state":              state,
		"completed":          completed,
		"canceled":           canceled,
		"total_bytes":        totalBytes,
		"received_bytes":     receivedBytes,
		"file_path":          filePath,
	}
}

func redactDownloadWaitEvent(event downloadWaitEvent, redactor *artifacts.Redactor, field string) downloadWaitEvent {
	if event.URL != "" {
		event.URL = redactor.URL(event.URL, field)
	}
	return event
}

func downloadWaitError(ctx context.Context, targetID string, opts downloadWaitOptions, report map[string]any, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return commandErrorWithData(
			"timeout",
			"timeout",
			fmt.Sprintf("wait download did not observe a matching %s download for target %s: %v", opts.Criteria.State, targetID, context.DeadlineExceeded),
			ExitTimeout,
			downloadWaitRemediations(opts),
			report,
		)
	}
	if errors.Is(err, errDownloadCanceled) {
		return commandErrorWithData(
			"download_canceled",
			"check_failed",
			fmt.Sprintf("wait download observed a matching download for target %s, but it was canceled", targetID),
			ExitCheckFailed,
			downloadWaitRemediations(opts),
			report,
		)
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return err
	}
	return commandError("connection_failed", "connection", fmt.Sprintf("wait download target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
}

func downloadWaitRemediations(opts downloadWaitOptions) []string {
	waitCommand := "cdp wait download --state " + opts.Criteria.State
	if opts.Criteria.URLContains != "" {
		waitCommand += " --match-url " + shellQuote(opts.Criteria.URLContains)
	}
	if opts.Criteria.FilenameContains != "" {
		waitCommand += " --filename-contains " + shellQuote(opts.Criteria.FilenameContains)
	}
	if opts.DownloadDir != "" {
		waitCommand += " --download-dir " + shellQuote(opts.DownloadDir)
	}
	return []string{
		waitCommand + " --timeout 15s --json",
		"cdp protocol exec Browser.setDownloadBehavior --params '{\"behavior\":\"allowAndName\",\"downloadPath\":\"tmp/downloads\",\"eventsEnabled\":true}' --json",
		"cdp pages --json",
	}
}

func downloadWaitNextCommands(begin *downloadWaitEvent, downloadDir string) []string {
	if begin != nil {
		return []string{
			"ls -lah " + shellQuote(downloadDir),
			"cdp wait download --filename-contains " + shellQuote(begin.SuggestedFilename) + " --state completed --download-dir " + shellQuote(downloadDir) + " --json",
			"cdp pages --json",
		}
	}
	return []string{
		"cdp wait download --state completed --download-dir tmp/downloads --json",
		"cdp pages --json",
	}
}
