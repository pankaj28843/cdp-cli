package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const (
	downloadWaitKind               = "download"
	downloadWaitCDPMethodWillBegin = "Browser.downloadWillBegin"
	downloadWaitCDPMethodProgress  = "Browser.downloadProgress"
	downloadFinalizeVisibilityWait = 500 * time.Millisecond
	downloadFinalizeVisibilityPoll = 10 * time.Millisecond
	downloadFilenameMaxBytes       = 255
	downloadExtensionMaxBytes      = 32
	downloadFilenameMaxCollisions  = 10_000
)

var (
	errDownloadCanceled     = errors.New("download canceled")
	errDownloadFinalization = errors.New("download finalization failed")
)

type downloadWaitCriteria struct {
	URLContains      string `json:"url_contains,omitempty"`
	FilenameContains string `json:"filename_contains,omitempty"`
	State            string `json:"state"`
}

type downloadWaitOptions struct {
	Criteria                  downloadWaitCriteria
	DownloadDir               string
	Redact                    string
	FinalizeSuggestedFilename bool
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
			explicitDownloadDir := strings.TrimSpace(downloadDir)
			opts := downloadWaitOptions{
				Criteria: downloadWaitCriteria{
					URLContains:      strings.TrimSpace(matchURL),
					FilenameContains: strings.TrimSpace(filenameContains),
					State:            strings.TrimSpace(state),
				},
				DownloadDir:               explicitDownloadDir,
				Redact:                    redact,
				FinalizeSuggestedFilename: explicitDownloadDir != "",
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
	teardown, err := setupDownloadWait(ctx, client, opts)
	if err != nil {
		return downloadWaitObservation{}, nil, err
	}
	observation, err := collectDownloadEvent(ctx, client, opts)
	return observation, teardown, err
}

func setupDownloadWait(ctx context.Context, client browserEventClient, opts downloadWaitOptions) (func(context.Context) error, error) {
	if _, err := client.DrainEvents(ctx); err != nil {
		return nil, err
	}
	params := map[string]any{
		"behavior":      "allowAndName",
		"downloadPath":  opts.DownloadDir,
		"eventsEnabled": true,
	}
	if err := client.Call(ctx, "Browser.setDownloadBehavior", params, nil); err != nil {
		return nil, err
	}
	teardown := func(teardownCtx context.Context) error {
		return client.Call(teardownCtx, "Browser.setDownloadBehavior", map[string]any{"behavior": "default", "eventsEnabled": false}, nil)
	}
	return teardown, nil
}

func collectDownloadEvent(ctx context.Context, client browserEventClient, opts downloadWaitOptions) (downloadWaitObservation, error) {
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
			finalPath, err := finalizeCompletedDownload(ctx, opts, *observation.Begin, downloadEvent)
			if err != nil {
				observation.LastEvent = &downloadEvent
				observation.Progress = &downloadEvent
				return false, err
			}
			if finalPath != "" {
				downloadEvent.FilePath = finalPath
				observation.LastEvent = &downloadEvent
				observation.Progress = &downloadEvent
			}
			observation.Matched = true
			return true, nil
		}
		return false, nil
	}
	for {
		event, err := client.ReadEvent(ctx)
		if err != nil {
			return observation, err
		}
		matched, err := observe(event)
		if matched || err != nil {
			return observation, err
		}
	}
}

func finalizeCompletedDownload(ctx context.Context, opts downloadWaitOptions, begin, progress downloadWaitEvent) (string, error) {
	if !opts.FinalizeSuggestedFilename || progress.State != "completed" {
		return progress.FilePath, nil
	}
	guid := begin.GUID
	if strings.TrimSpace(guid) != guid || !plainDownloadFilename(guid) {
		return "", fmt.Errorf("%w: browser supplied an unsafe download guid", errDownloadFinalization)
	}

	downloadDir := filepath.Clean(opts.DownloadDir)
	sourcePath := filepath.Join(downloadDir, guid)
	if filepath.Dir(sourcePath) != downloadDir {
		return "", fmt.Errorf("%w: guid path escaped the download directory", errDownloadFinalization)
	}
	sourceInfo, err := waitForCompletedDownloadFile(ctx, sourcePath)
	if err != nil {
		return "", err
	}

	filename := sanitizeSuggestedDownloadFilename(begin.SuggestedFilename)
	if filename == "" {
		filename = sanitizeSuggestedDownloadFilename("download-" + guid)
	}
	if filename == "" {
		filename = "download"
	}
	finalPath, err := retainDownloadWithoutOverwriteFrom(sourcePath, downloadDir, filename, sourceInfo, localDownloadFileOperations())
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDownloadFinalization, err)
	}
	if filepath.Dir(filepath.Clean(finalPath)) != downloadDir {
		return "", fmt.Errorf("%w: retained path escaped the download directory", errDownloadFinalization)
	}
	retainedInfo, err := os.Lstat(finalPath)
	if err != nil {
		return "", fmt.Errorf("%w: inspect retained download: %v", errDownloadFinalization, err)
	}
	if !retainedInfo.Mode().IsRegular() || !os.SameFile(sourceInfo, retainedInfo) {
		return "", fmt.Errorf("%w: retained download changed before completion was reported", errDownloadFinalization)
	}
	return finalPath, nil
}

func waitForCompletedDownloadFile(ctx context.Context, sourcePath string) (os.FileInfo, error) {
	deadline := time.NewTimer(downloadFinalizeVisibilityWait)
	defer deadline.Stop()
	poll := time.NewTicker(downloadFinalizeVisibilityPoll)
	defer poll.Stop()

	for {
		info, visible, err := completedDownloadFileInfo(sourcePath)
		if err != nil {
			return nil, err
		}
		if visible {
			return info, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			info, visible, err := completedDownloadFileInfo(sourcePath)
			if err != nil {
				return nil, err
			}
			if visible {
				return info, nil
			}
			return nil, fmt.Errorf(
				"%w: completed guid file did not become visible within %s",
				errDownloadFinalization,
				downloadFinalizeVisibilityWait,
			)
		case <-poll.C:
		}
	}
}

func completedDownloadFileInfo(sourcePath string) (os.FileInfo, bool, error) {
	info, err := os.Lstat(sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: inspect guid file: %v", errDownloadFinalization, err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%w: guid path is not a regular file", errDownloadFinalization)
	}
	return info, true, nil
}

func plainDownloadFilename(name string) bool {
	return name != "" &&
		name != "." &&
		name != ".." &&
		!strings.ContainsRune(name, '\x00') &&
		!strings.ContainsAny(name, `/\`) &&
		filepath.Base(name) == name
}

func sanitizeSuggestedDownloadFilename(suggested string) string {
	filename := path.Base(strings.ReplaceAll(strings.TrimSpace(suggested), `\`, "/"))
	if filename == "." || filename == ".." {
		return ""
	}
	var sanitized strings.Builder
	for _, r := range filename {
		switch {
		case unicode.IsControl(r), strings.ContainsRune(`<>:"|?*`, r):
			sanitized.WriteRune('_')
		default:
			sanitized.WriteRune(r)
		}
	}
	filename = strings.TrimRight(strings.TrimSpace(sanitized.String()), ". ")
	if !plainDownloadFilename(filename) {
		return ""
	}
	if windowsReservedDownloadFilename(filename) {
		filename = "_" + filename
	}
	filename = boundedDownloadFilename(filename, "")
	if !plainDownloadFilename(filename) {
		return ""
	}
	return filename
}

func boundedDownloadFilename(filename, suffix string) string {
	stem, extension := downloadFilenameParts(filename)
	stemBudget := downloadFilenameMaxBytes - len(suffix) - len(extension)
	if stemBudget < 1 {
		stem = filename
		extension = ""
		stemBudget = downloadFilenameMaxBytes - len(suffix)
	}
	stem = strings.TrimRight(truncateUTF8Bytes(stem, stemBudget), ". ")
	if stem == "" {
		stem = truncateUTF8Bytes("download", stemBudget)
	}
	return stem + suffix + extension
}

func downloadFilenameParts(filename string) (string, string) {
	extension := path.Ext(filename)
	stem := strings.TrimSuffix(filename, extension)
	if stem == "" || extension == "." || len(extension) > downloadExtensionMaxBytes {
		return filename, ""
	}
	return stem, extension
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func windowsReservedDownloadFilename(filename string) bool {
	stem := filename
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

type downloadFileOperations struct {
	lstat  func(string) (os.FileInfo, error)
	link   func(string, string) error
	remove func(string) error
}

func localDownloadFileOperations() downloadFileOperations {
	return downloadFileOperations{
		lstat:  os.Lstat,
		link:   os.Link,
		remove: os.Remove,
	}
}

func retainDownloadWithoutOverwrite(sourcePath, downloadDir, filename string) (string, error) {
	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return "", fmt.Errorf("inspect guid file before retaining: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return "", fmt.Errorf("guid path is not a regular file")
	}
	return retainDownloadWithoutOverwriteFrom(sourcePath, downloadDir, filename, sourceInfo, localDownloadFileOperations())
}

func retainDownloadWithoutOverwriteFrom(sourcePath, downloadDir, filename string, expectedSource os.FileInfo, fileOps downloadFileOperations) (string, error) {
	if expectedSource == nil || !expectedSource.Mode().IsRegular() {
		return "", fmt.Errorf("guid path is not a regular file")
	}
	currentSource, err := fileOps.lstat(sourcePath)
	if err != nil {
		return "", fmt.Errorf("revalidate guid file before retaining: %w", err)
	}
	if !currentSource.Mode().IsRegular() || !os.SameFile(expectedSource, currentSource) {
		return "", fmt.Errorf("guid file changed before retaining")
	}

	filename = boundedDownloadFilename(filename, "")
	if !plainDownloadFilename(filename) {
		return "", fmt.Errorf("suggested filename is not a plain filename")
	}
	for collision := 0; collision < downloadFilenameMaxCollisions; collision++ {
		suffix := ""
		if collision > 0 {
			suffix = fmt.Sprintf(" (%d)", collision)
		}
		candidateName := boundedDownloadFilename(filename, suffix)
		candidatePath := filepath.Join(downloadDir, candidateName)
		if filepath.Dir(candidatePath) != downloadDir {
			return "", fmt.Errorf("suggested filename escaped the download directory")
		}
		if candidatePath == sourcePath {
			currentSource, err := fileOps.lstat(sourcePath)
			if err != nil {
				return "", fmt.Errorf("revalidate retained guid file: %w", err)
			}
			if !currentSource.Mode().IsRegular() || !os.SameFile(expectedSource, currentSource) {
				return "", fmt.Errorf("retained guid file changed")
			}
			return candidatePath, nil
		}
		if err := fileOps.link(sourcePath, candidatePath); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", fmt.Errorf("retain completed download as %s: %w", candidateName, err)
		}
		if err := revalidateLinkedDownload(sourcePath, candidatePath, expectedSource, fileOps); err != nil {
			rollbackErr := removeRetainedDownloadCandidate(candidatePath, expectedSource, fileOps)
			if rollbackErr != nil {
				return "", fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
			return "", err
		}
		if err := fileOps.remove(sourcePath); err != nil {
			rollbackErr := removeRetainedDownloadCandidate(candidatePath, expectedSource, fileOps)
			if rollbackErr != nil {
				return "", fmt.Errorf("remove guid file after retaining %s: %v (rollback failed: %v)", candidateName, err, rollbackErr)
			}
			return "", fmt.Errorf("remove guid file after retaining %s: %w", candidateName, err)
		}
		retainedInfo, err := fileOps.lstat(candidatePath)
		if err != nil {
			return "", fmt.Errorf("revalidate retained download %s: %w", candidateName, err)
		}
		if !retainedInfo.Mode().IsRegular() || !os.SameFile(expectedSource, retainedInfo) {
			return "", fmt.Errorf("retained download %s changed after removing guid file", candidateName)
		}
		return candidatePath, nil
	}
	return "", fmt.Errorf("retain completed download: too many filename collisions for %s", filename)
}

func revalidateLinkedDownload(sourcePath, candidatePath string, expectedSource os.FileInfo, fileOps downloadFileOperations) error {
	currentSource, err := fileOps.lstat(sourcePath)
	if err != nil {
		return fmt.Errorf("revalidate guid file after linking: %w", err)
	}
	retainedInfo, err := fileOps.lstat(candidatePath)
	if err != nil {
		return fmt.Errorf("inspect retained download after linking: %w", err)
	}
	if !currentSource.Mode().IsRegular() || !retainedInfo.Mode().IsRegular() ||
		!os.SameFile(expectedSource, currentSource) ||
		!os.SameFile(expectedSource, retainedInfo) ||
		!os.SameFile(currentSource, retainedInfo) {
		return fmt.Errorf("guid file changed while retaining completed download")
	}
	return nil
}

func removeRetainedDownloadCandidate(candidatePath string, expectedSource os.FileInfo, fileOps downloadFileOperations) error {
	current, err := fileOps.lstat(candidatePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect retained candidate before rollback: %w", err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(expectedSource, current) {
		return fmt.Errorf("refuse to remove changed retained candidate")
	}
	if err := fileOps.remove(candidatePath); err != nil {
		return fmt.Errorf("remove retained candidate: %w", err)
	}
	return nil
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
	if errors.Is(err, errDownloadFinalization) {
		return commandErrorWithData(
			"download_finalize_failed",
			"check_failed",
			fmt.Sprintf("wait download completed for target %s, but retaining the suggested filename failed: %v", targetID, err),
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
