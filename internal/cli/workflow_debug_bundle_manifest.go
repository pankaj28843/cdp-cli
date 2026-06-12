package cli

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const (
	debugBundleSchemaVersion        = "cdp-evidence-bundle/v1"
	debugBundleRawArtifactWarning   = "debug bundle artifacts may include page text, console messages, screenshot pixels, network URLs, and browser metadata; keep local unless reviewed"
	debugBundleScreenshotWarning    = "screenshots are raw browser pixels; keep local unless reviewed and redacted"
	debugBundleConsoleWarning       = "console artifacts may include page content, errors, tokens, or user data; keep local unless reviewed"
	debugBundleSnapshotWarning      = "snapshot artifacts may include visible page text and links; keep local unless reviewed"
	debugBundlePageMetadataWarning  = "page metadata may include private titles, target ids, or local URLs; keep local unless reviewed"
	debugBundleCommandRecordWarning = "command records redact sensitive URL values by default and reference raw artifacts by path"
)

type debugBundleArtifactRecord struct {
	Type    string                   `json:"type"`
	Path    string                   `json:"path"`
	Bytes   int                      `json:"bytes,omitempty"`
	Content string                   `json:"content"`
	Safety  artifacts.SafetyMetadata `json:"safety"`
}

type debugBundleLayout struct {
	Root       string `json:"root,omitempty"`
	Manifest   string `json:"manifest,omitempty"`
	CommandLog string `json:"command_log,omitempty"`
	StageLog   string `json:"stage_log,omitempty"`
}

type debugBundleCommandRecord struct {
	Name         string   `json:"name"`
	BrowserMode  string   `json:"browser_mode"`
	Timeout      string   `json:"timeout"`
	ExitCode     int      `json:"exit_code"`
	Status       string   `json:"status"`
	TaskID       string   `json:"task_id"`
	RunID        string   `json:"run_id"`
	Stage        string   `json:"stage"`
	Attempt      int      `json:"attempt"`
	ArtifactPath string   `json:"artifact_path"`
	Argv         []string `json:"argv,omitempty"`
	ArgvRedacted bool     `json:"argv_redacted,omitempty"`
}

type debugBundleCommandRecordOptions struct {
	Name         string
	BrowserMode  string
	Timeout      string
	ExitCode     int
	Status       string
	TaskID       string
	RunID        string
	Stage        string
	Attempt      int
	ArtifactPath string
	Argv         []string
	ArgvRedacted bool
}

type debugBundleStageRecord struct {
	Name         string                      `json:"name"`
	Status       string                      `json:"status"`
	TaskID       string                      `json:"task_id"`
	RunID        string                      `json:"run_id"`
	AttemptCount int                         `json:"attempt_count"`
	ElapsedMS    int64                       `json:"elapsed_ms"`
	Commands     []debugBundleCommandRecord  `json:"commands"`
	Artifacts    []debugBundleArtifactRecord `json:"artifacts"`
}

type debugBundleSummary struct {
	SchemaVersion        string                      `json:"schema_version"`
	Layout               debugBundleLayout           `json:"layout"`
	RedactionMode        string                      `json:"redaction_mode"`
	DefaultJSON          string                      `json:"default_json"`
	InlinePayloads       bool                        `json:"inline_payloads"`
	ArtifactCount        int                         `json:"artifact_count"`
	PublicSafeArtifacts  int                         `json:"public_safe_artifacts"`
	LocalOnlyArtifacts   int                         `json:"local_only_artifacts"`
	UnsafeOptInArtifacts int                         `json:"unsafe_opt_in_artifacts"`
	Artifacts            []debugBundleArtifactRecord `json:"artifacts"`
	Commands             []debugBundleCommandRecord  `json:"commands"`
	Stages               []debugBundleStageRecord    `json:"stages"`
	Warnings             []string                    `json:"warnings,omitempty"`
}

func newDebugBundleCommandRecord(opts debugBundleCommandRecordOptions) debugBundleCommandRecord {
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = "ok"
	}
	attempt := opts.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	return debugBundleCommandRecord{
		Name:         strings.TrimSpace(opts.Name),
		BrowserMode:  strings.TrimSpace(opts.BrowserMode),
		Timeout:      strings.TrimSpace(opts.Timeout),
		ExitCode:     opts.ExitCode,
		Status:       status,
		TaskID:       strings.TrimSpace(opts.TaskID),
		RunID:        strings.TrimSpace(opts.RunID),
		Stage:        strings.TrimSpace(opts.Stage),
		Attempt:      attempt,
		ArtifactPath: strings.TrimSpace(opts.ArtifactPath),
		Argv:         append([]string(nil), opts.Argv...),
		ArgvRedacted: opts.ArgvRedacted,
	}
}

func newDebugBundleStageRecord(name, status, taskID, runID string, elapsed time.Duration, commands []debugBundleCommandRecord, artifactRefs []debugBundleArtifactRecord) debugBundleStageRecord {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "ok"
	}
	return debugBundleStageRecord{
		Name:         strings.TrimSpace(name),
		Status:       status,
		TaskID:       strings.TrimSpace(taskID),
		RunID:        strings.TrimSpace(runID),
		AttemptCount: len(commands),
		ElapsedMS:    elapsed.Milliseconds(),
		Commands:     append([]debugBundleCommandRecord(nil), commands...),
		Artifacts:    append([]debugBundleArtifactRecord(nil), artifactRefs...),
	}
}

func newDebugBundleSummary(layout debugBundleLayout, redactionMode string, inlinePayloads bool, artifactRefs []debugBundleArtifactRecord, commands []debugBundleCommandRecord, stages []debugBundleStageRecord) debugBundleSummary {
	summary := debugBundleSummary{
		SchemaVersion:  debugBundleSchemaVersion,
		Layout:         layout,
		RedactionMode:  artifacts.NormalizeMode(redactionMode),
		DefaultJSON:    "artifact_references",
		InlinePayloads: inlinePayloads,
		ArtifactCount:  len(artifactRefs),
		Artifacts:      append([]debugBundleArtifactRecord(nil), artifactRefs...),
		Commands:       append([]debugBundleCommandRecord(nil), commands...),
		Stages:         append([]debugBundleStageRecord(nil), stages...),
	}
	if inlinePayloads {
		summary.DefaultJSON = "inline_payloads"
	}
	localOnlySeen := false
	for _, artifactRef := range artifactRefs {
		switch artifactRef.Safety.Classification {
		case "public_safe":
			summary.PublicSafeArtifacts++
		case "unsafe_opt_in":
			summary.UnsafeOptInArtifacts++
			localOnlySeen = true
		default:
			summary.LocalOnlyArtifacts++
			localOnlySeen = true
		}
	}
	if localOnlySeen {
		summary.Warnings = append(summary.Warnings, debugBundleRawArtifactWarning)
	}
	if inlinePayloads {
		summary.Warnings = append(summary.Warnings, "inline payloads may include browser content; keep command output local unless reviewed")
	}
	return summary
}

func debugBundleArtifactSafety(redactor *artifacts.Redactor, localOnly bool, warning string) artifacts.SafetyMetadata {
	meta := redactor.Metadata(true, warning)
	if !localOnly {
		return meta
	}
	meta.Shareable = false
	meta.LocalOnlyWarning = strings.TrimSpace(warning)
	if redactor == nil || redactor.Mode() == artifacts.ModeNone {
		meta.Classification = "unsafe_opt_in"
		meta.UnsafeOptIn = true
		return meta
	}
	meta.Classification = "local_only"
	meta.UnsafeOptIn = false
	return meta
}

func debugBundleArtifactList(records []debugBundleArtifactRecord) []map[string]any {
	if len(records) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		out = append(out, map[string]any{
			"type":           record.Type,
			"path":           record.Path,
			"classification": record.Safety.Classification,
			"shareable":      record.Safety.Shareable,
			"redaction_mode": record.Safety.RedactionMode,
		})
	}
	return out
}

func debugBundleRedactedPageRow(target cdp.TargetInfo, redactor *artifacts.Redactor) map[string]any {
	row := pageRow(target)
	if rawURL, ok := row["url"].(string); ok {
		row["url"] = redactor.URL(rawURL, "target.url")
	}
	return row
}

func debugBundleRedactedRequests(requests []networkRequest, redactor *artifacts.Redactor) []networkRequest {
	if len(requests) == 0 {
		return nil
	}
	out := make([]networkRequest, len(requests))
	copy(out, requests)
	for i := range out {
		out[i].URL = redactor.URL(out[i].URL, "requests.url")
	}
	return out
}

func debugBundleRedactedMessages(messages []consoleMessage, redactor *artifacts.Redactor) []consoleMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]consoleMessage, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].URL = redactor.URL(out[i].URL, "messages.url")
		out[i].Text = redactor.BodyText(out[i].Text, "messages.text")
	}
	return out
}

func debugBundleRedactedSnapshot(snapshot pageSnapshot, redactor *artifacts.Redactor) pageSnapshot {
	out := snapshot
	out.URL = redactor.URL(out.URL, "snapshot.url")
	out.Items = append([]snapshotItem(nil), snapshot.Items...)
	for i := range out.Items {
		out.Items[i].Href = redactor.URL(out.Items[i].Href, "snapshot.items.href")
		out.Items[i].AriaLabel = redactor.BodyText(out.Items[i].AriaLabel, "snapshot.items.aria_label")
		out.Items[i].Text = redactor.BodyText(out.Items[i].Text, "snapshot.items.text")
	}
	return out
}

func debugBundleCommandLogJSONL(records []debugBundleCommandRecord) ([]byte, error) {
	var out []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out, nil
}
