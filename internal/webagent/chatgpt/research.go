package chatgpt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	ResearchExportSchemaVersion = "chatgpt-research-export/v1"
	maxResearchReportBytes      = 2 << 20
	minResearchReportRunes      = 120
	defaultResearchReadTimeout  = 90 * time.Second
)

// ResearchExportConfig controls an owner-only, read-only rendered report
// export. OutputPath is always explicit and is written atomically.
type ResearchExportConfig struct {
	BrowserConfig
	OutputPath string
	Overwrite  bool
	Timeout    time.Duration
}

// ResearchExportData deliberately contains report provenance and a digest, not
// report text. The rendered report is kept only at the explicit output path.
type ResearchExportData struct {
	SchemaVersion  string         `json:"schema_version"`
	ConversationID string         `json:"conversation_id"`
	OutputPath     string         `json:"output_path"`
	ExportedBytes  int            `json:"exported_bytes"`
	SHA256         string         `json:"sha256"`
	ReadMode       string         `json:"read_mode"`
	Metadata       map[string]any `json:"metadata"`
}

type researchSurfaceObservation struct {
	RouteMatches   bool     `json:"route_matches"`
	ConversationID string   `json:"conversation_id"`
	FrameSources   []string `json:"frame_sources"`
}

type researchFrameTreeResponse struct {
	FrameTree *researchFrameTree `json:"frameTree"`
}

type researchFrameTree struct {
	Frame       *researchFrameInfo  `json:"frame"`
	ChildFrames []researchFrameTree `json:"childFrames"`
}

type researchFrameInfo struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type researchFrameCandidate struct {
	ID       string
	URL      string
	ParentID string
}

type researchFrameObservation struct {
	Text      string `json:"text"`
	RootCount int    `json:"root_count"`
	Streaming bool   `json:"streaming"`
	TooLarge  bool   `json:"too_large"`
}

// ExportResearch reads one exact completed Deep Research report from its
// semantically identified embedded frame. It never clicks an export control or
// replays an iframe endpoint: the only side effect is the explicit local file.
func ExportResearch(
	ctx context.Context,
	config ResearchExportConfig,
	conversationID string,
) webagent.Result {
	runID := webagent.NewRunID()
	conversationID = strings.TrimSpace(conversationID)
	data := ResearchExportData{
		SchemaVersion:  ResearchExportSchemaVersion,
		ConversationID: conversationID,
		ReadMode:       "candidate_rendered_embedded_research",
		Metadata:       map[string]any{},
	}
	conversation := conversationRef(conversationID)
	if !conversationIDPattern.MatchString(conversationID) {
		return researchExportFailure(
			runID,
			config,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_invalid_conversation_id",
			"usage",
			"ChatGPT conversation id contains unsupported characters",
			data,
			conversation,
		)
	}
	destination, err := validateArtifactDestination(
		config.OutputPath,
		config.Overwrite,
	)
	if err != nil {
		return researchExportFailure(
			runID,
			config,
			webagent.StagePlanned,
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			"chatgpt_invalid_research_destination",
			"usage",
			err.Error(),
			data,
			conversation,
		)
	}
	data.OutputPath = destination
	if config.Timeout <= 0 {
		config.Timeout = defaultResearchReadTimeout
	}

	return runOwned(
		ctx,
		config.BrowserConfig,
		runID,
		webagent.OperationResearchExport,
		"",
		"about:blank",
		"rendered_embedded_research",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			report, failure := readEmbeddedResearchReport(
				ctx,
				config.BrowserConfig,
				lease,
				conversationID,
				config.Timeout,
			)
			if failure != nil {
				return researchExportFailure(
					runID,
					config,
					webagent.StageObserveTerminal,
					target,
					pending,
					failure.code,
					failure.errClass,
					failure.message,
					data,
					conversation,
				)
			}
			content, failure := normalizedResearchReport(report)
			if failure != nil {
				return researchExportFailure(
					runID,
					config,
					webagent.StageObserveTerminal,
					target,
					pending,
					failure.code,
					failure.errClass,
					failure.message,
					data,
					conversation,
				)
			}
			if err := writeArtifactAtomic(
				destination,
				content,
				config.Overwrite,
			); err != nil {
				return researchExportFailure(
					runID,
					config,
					webagent.StageObserveTerminal,
					target,
					pending,
					"chatgpt_research_output_failed",
					"filesystem",
					"ChatGPT research report could not be written to the explicit destination",
					data,
					conversation,
				)
			}
			digest := sha256.Sum256(content)
			data.ExportedBytes = len(content)
			data.SHA256 = hex.EncodeToString(digest[:])
			data.ReadMode = "rendered_embedded_research_frame"
			data.Metadata["source"] = "rendered_embedded_research_frame"
			if err := lease.MarkTerminal(ctx); err != nil {
				return researchExportFailure(
					runID,
					config,
					webagent.StageObserveTerminal,
					target,
					pending,
					"chatgpt_research_state_persist_failed",
					"internal",
					"ChatGPT research export terminal state could not be persisted",
					data,
					conversation,
				)
			}
			result := operationSuccess(
				runID,
				config.BuildCommit,
				webagent.OperationResearchExport,
				webagent.StageObserveTerminal,
				data.ReadMode,
				target,
				pending,
				data,
				nil,
			)
			result.State = webagent.StateTerminal
			result.Conversation = conversation
			return result
		},
	)
}

func detailEmbeddedResearchViaBrowser(
	ctx context.Context,
	config BrowserConfig,
	runID string,
	conversationID string,
	data ConversationDetailData,
) webagent.Result {
	conversation := conversationRef(conversationID)
	return runOwned(
		ctx,
		config,
		runID,
		webagent.OperationConversationsDetail,
		"",
		"about:blank",
		"rendered_embedded_research",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			report, failure := readEmbeddedResearchReport(
				ctx,
				config,
				lease,
				conversationID,
				30*time.Second,
			)
			if failure != nil {
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationConversationsDetail,
					webagent.StageObserveTerminal,
					data.ReadMode,
					target,
					pending,
					failure.code,
					failure.errClass,
					failure.message,
					data,
					nil,
				)
			}
			if _, failure := normalizedResearchReport(report); failure != nil {
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationConversationsDetail,
					webagent.StageObserveTerminal,
					data.ReadMode,
					target,
					pending,
					failure.code,
					failure.errClass,
					failure.message,
					data,
					nil,
				)
			}
			data.Text = strings.TrimSpace(report)
			data.CompletionState = conversationCompletionTerminal
			data.CompletionReason = ""
			data.ReadMode = "observed_stable_http_plus_embedded_rendered"
			if data.Metadata == nil {
				data.Metadata = map[string]any{}
			}
			data.Metadata["answer_source"] = "rendered_embedded_research_frame"
			if err := lease.MarkTerminal(ctx); err != nil {
				return operationFailure(
					runID,
					config.BuildCommit,
					webagent.OperationConversationsDetail,
					webagent.StageObserveTerminal,
					data.ReadMode,
					target,
					pending,
					"chatgpt_research_state_persist_failed",
					"internal",
					"ChatGPT embedded research terminal state could not be persisted",
					data,
					nil,
				)
			}
			result := operationSuccess(
				runID,
				config.BuildCommit,
				webagent.OperationConversationsDetail,
				webagent.StageObserveTerminal,
				data.ReadMode,
				target,
				pending,
				data,
				nil,
			)
			result.State = webagent.StateTerminal
			result.Conversation = conversation
			return result
		},
	)
}

func readEmbeddedResearchReport(
	ctx context.Context,
	config BrowserConfig,
	lease *browserflow.Lease,
	conversationID string,
	timeout time.Duration,
) (string, *readFailure) {
	if timeout <= 0 {
		timeout = defaultResearchReadTimeout
	}
	if err := preparePage(
		ctx,
		config.Client,
		lease.Session(),
		Origin+"/c/"+url.PathEscape(conversationID),
	); err != nil {
		return "", &readFailure{
			code:     "chatgpt_research_route_unavailable",
			errClass: "connection",
			message:  "ChatGPT exact research conversation route could not be opened",
		}
	}
	if failure := commitResearchReadPreparation(ctx, lease); failure != nil {
		return "", failure
	}
	surface, failure := waitForResearchSurface(
		ctx,
		lease.Session(),
		conversationID,
		timeout,
	)
	if failure != nil {
		return "", failure
	}
	frameID, failure := researchFrameID(ctx, lease.Session(), surface)
	if failure == nil {
		return waitForResearchFrameReport(
			ctx,
			lease.Session(),
			frameID,
			timeout,
		)
	}
	if failure.code != "chatgpt_research_frame_unavailable" {
		return "", failure
	}
	return readEmbeddedResearchOOPIFReport(ctx, config.Client, surface, timeout)
}

// readEmbeddedResearchOOPIFReport handles an out-of-process report iframe
// which Chrome does not include in the parent target's Page.getFrameTree.
// The semantic parent reader has already identified exactly one HTTPS source;
// the daemon target registry must identify exactly one matching iframe target
// before this function attaches to it. The target is never closed or marked
// owned: closing the attached session only detaches from it.
func readEmbeddedResearchOOPIFReport(
	ctx context.Context,
	client cdp.CommandClient,
	surface researchSurfaceObservation,
	timeout time.Duration,
) (report string, failure *readFailure) {
	target, failure := waitForResearchOOPIFTarget(ctx, client, surface, timeout)
	if failure != nil {
		return "", failure
	}
	session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, nil)
	if err != nil {
		return "", &readFailure{
			code:     "chatgpt_research_oopif_unreadable",
			errClass: "connection",
			message:  "ChatGPT embedded research report target could not be attached",
		}
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := session.Close(closeCtx); err != nil && failure == nil {
			report = ""
			failure = &readFailure{
				code:     "chatgpt_research_oopif_unreadable",
				errClass: "connection",
				message:  "ChatGPT embedded research report target could not be detached",
			}
		}
	}()

	frameID, failure := researchFrameRootID(ctx, session)
	if failure != nil {
		return "", failure
	}
	return waitForResearchFrameReport(ctx, session, frameID, timeout)
}

func waitForResearchOOPIFTarget(
	ctx context.Context,
	client cdp.CommandClient,
	surface researchSurfaceObservation,
	timeout time.Duration,
) (cdp.TargetInfo, *readFailure) {
	if len(surface.FrameSources) != 1 ||
		!safeResearchFrameURL(surface.FrameSources[0]) {
		return cdp.TargetInfo{}, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame could not be identified safely",
		}
	}
	deadline := time.Now().Add(timeout)
	for {
		targets, err := cdp.ListTargetsWithClient(ctx, client)
		if err != nil {
			return cdp.TargetInfo{}, &readFailure{
				code:     "chatgpt_research_oopif_targets_unavailable",
				errClass: "connection",
				message:  "ChatGPT embedded research report targets could not be listed",
			}
		}
		matching := make([]cdp.TargetInfo, 0, 1)
		for _, target := range targets {
			if target.Type != "iframe" ||
				strings.TrimSpace(target.TargetID) == "" ||
				!safeResearchFrameURL(target.URL) ||
				!researchFrameURLsMatch(surface.FrameSources[0], target.URL) {
				continue
			}
			matching = append(matching, target)
		}
		switch len(matching) {
		case 1:
			return matching[0], nil
		case 0:
			// A freshly navigated OOPIF can reach the target registry slightly
			// after its semantic iframe appears in the parent document.
		default:
			return cdp.TargetInfo{}, &readFailure{
				code:     "chatgpt_research_frame_ambiguous",
				errClass: "provider",
				message:  "ChatGPT research conversation exposed multiple matching report frame targets",
			}
		}
		if remaining := time.Until(deadline); remaining <= 0 {
			return cdp.TargetInfo{}, &readFailure{
				code:     "chatgpt_research_frame_unavailable",
				errClass: "provider",
				message:  "ChatGPT research report frame was not available in the owned page target",
			}
		} else if !waitForObservation(ctx, 250*time.Millisecond, remaining) {
			return cdp.TargetInfo{}, &readFailure{
				code:     "chatgpt_research_read_canceled",
				errClass: "timeout",
				message:  "ChatGPT embedded research read was canceled before a report frame was observed",
			}
		}
	}
}

func commitResearchReadPreparation(
	ctx context.Context,
	lease *browserflow.Lease,
) *readFailure {
	if ctx.Err() != nil {
		return &readFailure{
			code:     "chatgpt_research_read_canceled",
			errClass: "timeout",
			message:  "ChatGPT embedded research read was canceled before observation",
		}
	}
	if err := lease.MarkPrepared(ctx); err != nil {
		return internalReadFailure(
			"ChatGPT embedded research read preparation could not be persisted",
		)
	}
	if err := lease.ReleaseInput(); err != nil {
		return internalReadFailure(
			"ChatGPT embedded research read could not release the headed input lease",
		)
	}
	return nil
}

func waitForResearchSurface(
	ctx context.Context,
	session *cdp.PageSession,
	conversationID string,
	timeout time.Duration,
) (researchSurfaceObservation, *readFailure) {
	deadline := time.Now().Add(timeout)
	for {
		var observation researchSurfaceObservation
		if err := observeResearchSurface(ctx, session, &observation); err == nil &&
			observation.RouteMatches &&
			observation.ConversationID == conversationID {
			if len(observation.FrameSources) == 1 {
				return observation, nil
			}
			if len(observation.FrameSources) > 1 {
				return researchSurfaceObservation{}, &readFailure{
					code:     "chatgpt_research_frame_ambiguous",
					errClass: "provider",
					message:  "ChatGPT research conversation exposed multiple candidate report frames",
				}
			}
		}
		if remaining := time.Until(deadline); remaining <= 0 {
			return researchSurfaceObservation{}, &readFailure{
				code:     "chatgpt_research_report_unavailable",
				errClass: "provider",
				message:  "ChatGPT research conversation did not expose one readable embedded report frame",
			}
		} else if !waitForObservation(ctx, 250*time.Millisecond, remaining) {
			return researchSurfaceObservation{}, &readFailure{
				code:     "chatgpt_research_read_canceled",
				errClass: "timeout",
				message:  "ChatGPT embedded research read was canceled before a report frame was observed",
			}
		}
	}
}

func observeResearchSurface(
	ctx context.Context,
	session *cdp.PageSession,
	observation *researchSurfaceObservation,
) error {
	return evaluateInto(ctx, session, `(() => {
  const visible = element => Boolean(
    element && element.getAttribute('aria-hidden') !== 'true' &&
    (element.offsetWidth || element.offsetHeight || element.getClientRects().length)
  );
  const unique = values => Array.from(new Set(values));
  const frames = Array.from(document.querySelectorAll(
    'iframe[src*="deep-research"],iframe[src*="connector_openai_deep_research"],' +
    '[data-testid*="deep-research"] iframe,[data-testid*="research-report"] iframe'
  )).filter(visible);
  const sources = unique(frames.map(frame => String(frame.src || ''))
    .filter(source => source.startsWith('https://')));
  const match = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
  return {
    route_matches: Boolean(match),
    conversation_id: match ? match[1] : '',
    frame_sources: sources
  };
})()`, observation)
}

func researchFrameID(
	ctx context.Context,
	session *cdp.PageSession,
	surface researchSurfaceObservation,
) (string, *readFailure) {
	if len(surface.FrameSources) != 1 || !safeResearchFrameURL(surface.FrameSources[0]) {
		return "", &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame could not be identified safely",
		}
	}
	raw, err := session.Exec(ctx, "Page.getFrameTree", nil)
	if err != nil {
		return "", &readFailure{
			code:     "chatgpt_research_frame_tree_unavailable",
			errClass: "connection",
			message:  "ChatGPT research frame tree could not be read",
		}
	}
	var tree researchFrameTreeResponse
	if err := json.Unmarshal(raw, &tree); err != nil {
		return "", &readFailure{
			code:     "chatgpt_research_frame_tree_unavailable",
			errClass: "connection",
			message:  "ChatGPT research frame tree could not be decoded",
		}
	}
	if tree.FrameTree == nil || tree.FrameTree.Frame == nil {
		return "", &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame was not available in the owned page target",
		}
	}
	candidates := collectResearchFrames(tree.FrameTree, "")
	matching := make([]researchFrameCandidate, 0, 1)
	for _, candidate := range candidates {
		if candidate.ParentID == "" ||
			!researchFrameURLsMatch(surface.FrameSources[0], candidate.URL) {
			continue
		}
		matching = append(matching, candidate)
	}
	if len(matching) == 1 && strings.TrimSpace(matching[0].ID) != "" {
		return matching[0].ID, nil
	}
	// A provider redirect can replace the iframe URL before the frame tree is
	// observed. The semantic main-frame reader already proved exactly one Deep
	// Research iframe, so one direct child frame is an unambiguous binding even
	// when its loaded URL differs from the element's original source.
	directChildren := make([]researchFrameCandidate, 0, 1)
	rootID := tree.FrameTree.Frame.ID
	for _, candidate := range candidates {
		if candidate.ParentID != rootID || !safeResearchFrameURL(candidate.URL) {
			continue
		}
		directChildren = append(directChildren, candidate)
	}
	if len(matching) == 0 && len(directChildren) == 1 &&
		strings.TrimSpace(directChildren[0].ID) != "" {
		return directChildren[0].ID, nil
	}
	return "", &readFailure{
		code:     "chatgpt_research_frame_unavailable",
		errClass: "provider",
		message:  "ChatGPT research report frame was not available in the owned page target",
	}
}

// researchFrameRootID returns the top-level frame ID for an attached OOPIF
// target. Its parent page owns the semantic iframe binding; this target's root
// is therefore the only frame that may be read through this session.
func researchFrameRootID(
	ctx context.Context,
	session *cdp.PageSession,
) (string, *readFailure) {
	raw, err := session.Exec(ctx, "Page.getFrameTree", nil)
	if err != nil {
		return "", &readFailure{
			code:     "chatgpt_research_oopif_unreadable",
			errClass: "connection",
			message:  "ChatGPT embedded research report target frame tree could not be read",
		}
	}
	var tree researchFrameTreeResponse
	if err := json.Unmarshal(raw, &tree); err != nil ||
		tree.FrameTree == nil || tree.FrameTree.Frame == nil ||
		strings.TrimSpace(tree.FrameTree.Frame.ID) == "" {
		return "", &readFailure{
			code:     "chatgpt_research_oopif_unreadable",
			errClass: "connection",
			message:  "ChatGPT embedded research report target did not expose a readable root frame",
		}
	}
	return tree.FrameTree.Frame.ID, nil
}

func collectResearchFrames(
	node *researchFrameTree,
	parentID string,
) []researchFrameCandidate {
	if node == nil || node.Frame == nil {
		return nil
	}
	current := researchFrameCandidate{
		ID:       node.Frame.ID,
		URL:      node.Frame.URL,
		ParentID: parentID,
	}
	frames := []researchFrameCandidate{current}
	for index := range node.ChildFrames {
		frames = append(
			frames,
			collectResearchFrames(&node.ChildFrames[index], current.ID)...,
		)
	}
	return frames
}

func safeResearchFrameURL(raw string) bool {
	if len(raw) == 0 || len(raw) > 8192 || strings.ContainsRune(raw, '\x00') {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil &&
		parsed.User == nil &&
		strings.EqualFold(parsed.Scheme, "https") &&
		parsed.Host != ""
}

func researchFrameURLsMatch(source string, loaded string) bool {
	if source == loaded {
		return true
	}
	sourceIdentity, sourceOK := normalizedResearchFrameURLIdentity(source)
	loadedIdentity, loadedOK := normalizedResearchFrameURLIdentity(loaded)
	return sourceOK && loadedOK && sourceIdentity == loadedIdentity
}

type researchFrameURLIdentity struct {
	Scheme string
	Host   string
	Path   string
}

func normalizedResearchFrameURLIdentity(
	raw string,
) (researchFrameURLIdentity, bool) {
	if !safeResearchFrameURL(raw) {
		return researchFrameURLIdentity{}, false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return researchFrameURLIdentity{}, false
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	return researchFrameURLIdentity{
		Scheme: strings.ToLower(parsed.Scheme),
		Host:   strings.ToLower(parsed.Host),
		Path:   path,
	}, true
}

func waitForResearchFrameReport(
	ctx context.Context,
	session *cdp.PageSession,
	frameID string,
	timeout time.Duration,
) (string, *readFailure) {
	deadline := time.Now().Add(timeout)
	contextID, failure := researchFrameExecutionContext(ctx, session, frameID)
	if failure != nil {
		return "", failure
	}
	for {
		observation, failure := observeResearchFrame(ctx, session, contextID)
		if failure != nil {
			return "", failure
		}
		if observation.TooLarge {
			return "", &readFailure{
				code:     "chatgpt_research_report_too_large",
				errClass: "provider",
				message:  "ChatGPT embedded research report exceeded the bounded export size",
			}
		}
		if observation.RootCount > 1 {
			return "", &readFailure{
				code:     "chatgpt_research_report_ambiguous",
				errClass: "provider",
				message:  "ChatGPT embedded research frame exposed multiple report roots",
			}
		}
		if !observation.Streaming &&
			len([]rune(strings.TrimSpace(observation.Text))) >= minResearchReportRunes {
			return observation.Text, nil
		}
		if remaining := time.Until(deadline); remaining <= 0 {
			return "", &readFailure{
				code:     "chatgpt_research_report_unavailable",
				errClass: "provider",
				message:  "ChatGPT embedded research frame did not expose a completed report",
			}
		} else if !waitForObservation(ctx, 250*time.Millisecond, remaining) {
			return "", &readFailure{
				code:     "chatgpt_research_read_canceled",
				errClass: "timeout",
				message:  "ChatGPT embedded research read was canceled before a completed report was observed",
			}
		}
	}
}

func researchFrameExecutionContext(
	ctx context.Context,
	session *cdp.PageSession,
	frameID string,
) (int, *readFailure) {
	params, err := json.Marshal(map[string]any{
		"frameId":             frameID,
		"worldName":           "cdp-cli-research-export",
		"grantUniveralAccess": false,
	})
	if err != nil {
		return 0, internalReadFailure(
			"ChatGPT research frame request could not be encoded",
		)
	}
	raw, err := session.Exec(ctx, "Page.createIsolatedWorld", params)
	if err != nil {
		return 0, &readFailure{
			code:     "chatgpt_research_frame_unreadable",
			errClass: "connection",
			message:  "ChatGPT research report frame could not be read in the owned page target",
		}
	}
	var result struct {
		ExecutionContextID int `json:"executionContextId"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.ExecutionContextID <= 0 {
		return 0, &readFailure{
			code:     "chatgpt_research_frame_unreadable",
			errClass: "connection",
			message:  "ChatGPT research report frame did not expose an isolated read context",
		}
	}
	return result.ExecutionContextID, nil
}

func observeResearchFrame(
	ctx context.Context,
	session *cdp.PageSession,
	contextID int,
) (researchFrameObservation, *readFailure) {
	params, err := json.Marshal(map[string]any{
		"expression":    researchFrameObservationExpression(),
		"contextId":     contextID,
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if err != nil {
		return researchFrameObservation{}, internalReadFailure(
			"ChatGPT research frame observation could not be encoded",
		)
	}
	raw, err := session.Exec(ctx, "Runtime.evaluate", params)
	if err != nil {
		return researchFrameObservation{}, &readFailure{
			code:     "chatgpt_research_frame_unreadable",
			errClass: "connection",
			message:  "ChatGPT research report frame could not be observed",
		}
	}
	var evaluation struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails any `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &evaluation); err != nil ||
		evaluation.ExceptionDetails != nil || len(evaluation.Result.Value) == 0 {
		return researchFrameObservation{}, &readFailure{
			code:     "chatgpt_research_frame_unreadable",
			errClass: "connection",
			message:  "ChatGPT research report frame observation could not be decoded",
		}
	}
	var observation researchFrameObservation
	if err := json.Unmarshal(evaluation.Result.Value, &observation); err != nil {
		return researchFrameObservation{}, &readFailure{
			code:     "chatgpt_research_frame_unreadable",
			errClass: "connection",
			message:  "ChatGPT research report frame observation had an invalid shape",
		}
	}
	return observation, nil
}

func researchFrameObservationExpression() string {
	return fmt.Sprintf(`(() => {
  const maxCharacters = %d;
  const visible = element => Boolean(
    element && element.getAttribute('aria-hidden') !== 'true' &&
    (element.offsetWidth || element.offsetHeight || element.getClientRects().length)
  );
  const unique = values => Array.from(new Set(values));
  const allRoots = unique(Array.from(document.querySelectorAll(
    'article,main,[role="main"],[data-testid*="report"]'
  )).filter(visible));
  const roots = allRoots.filter(root => !allRoots.some(other =>
    other !== root && root.contains(other)
  ));
  const root = roots.length === 1 ? roots[0] : null;
  const text = root ? String(root.innerText || root.textContent || '').trim() : '';
  const streaming = Array.from(document.querySelectorAll(
    '[aria-busy="true"],[data-is-streaming="true"]'
  )).some(visible);
  return {
    text: text.length > maxCharacters ? '' : text,
    root_count: roots.length,
    streaming,
    too_large: text.length > maxCharacters
  };
})()`, maxResearchReportBytes)
}

func normalizedResearchReport(report string) ([]byte, *readFailure) {
	report = strings.ReplaceAll(report, "\r\n", "\n")
	report = strings.ReplaceAll(report, "\r", "\n")
	report = strings.TrimSpace(report)
	if !utf8.ValidString(report) || strings.ContainsRune(report, '\x00') {
		return nil, &readFailure{
			code:     "chatgpt_research_report_unreadable",
			errClass: "provider",
			message:  "ChatGPT embedded research report was not valid readable text",
		}
	}
	if len([]rune(report)) < minResearchReportRunes {
		return nil, &readFailure{
			code:     "chatgpt_research_report_unavailable",
			errClass: "provider",
			message:  "ChatGPT embedded research frame did not expose a completed report",
		}
	}
	content := []byte(report + "\n")
	if len(content) > maxResearchReportBytes {
		return nil, &readFailure{
			code:     "chatgpt_research_report_too_large",
			errClass: "provider",
			message:  "ChatGPT embedded research report exceeded the bounded export size",
		}
	}
	return content, nil
}

func researchExportFailure(
	runID string,
	config ResearchExportConfig,
	stage webagent.Stage,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	code string,
	errClass string,
	message string,
	data ResearchExportData,
	conversation *webagent.ConversationRef,
) webagent.Result {
	result := operationFailure(
		runID,
		config.BuildCommit,
		webagent.OperationResearchExport,
		stage,
		data.ReadMode,
		target,
		cleanup,
		code,
		errClass,
		message,
		data,
		nil,
	)
	result.Conversation = conversation
	return result
}
