package chatgpt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	maxResearchOOPIFCandidates  = 8
	researchSandboxHost         = "connector-openai-deep-research.web-sandbox.oaiusercontent.com"
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

type researchFrameTargetRoot struct {
	ID  string
	URL string
}

type semanticResearchFrame struct {
	BackendNodeID int64
	FrameID       string
}

type researchFrameBinding struct {
	ReportSession       *cdp.PageSession
	ParentSession       *cdp.PageSession
	FrameID             string
	FrameURL            string
	OwnerFrameID        string
	OwnerFrameURL       string
	OwnerBackendNodeID  int64
	TargetRootFrameRead bool
	ParentSemanticFrame bool
}

type researchFrameObservation struct {
	Text            string `json:"text"`
	RootCount       int    `json:"root_count"`
	Streaming       bool   `json:"streaming"`
	TooLarge        bool   `json:"too_large"`
	DocumentTrusted bool   `json:"document_trusted"`
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
			nil,
		)
	}
	conversation := conversationRef(conversationID)
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
	semanticFrame, failure := resolveSemanticResearchFrame(
		ctx,
		lease.Session(),
		surface,
	)
	if failure != nil {
		return "", failure
	}
	directBinding, failure := bindDirectResearchFrame(
		ctx,
		lease.Session(),
		surface,
		semanticFrame.BackendNodeID,
	)
	if failure != nil {
		return "", failure
	}
	if directBinding != nil {
		return waitForBoundResearchFrameReport(
			ctx,
			directBinding,
			surface,
			timeout,
		)
	}
	semanticBinding, failure := bindParentSemanticResearchFrame(
		ctx,
		lease.Session(),
		surface,
		semanticFrame,
	)
	if failure != nil {
		return "", failure
	}
	if semanticBinding != nil {
		report, semanticFailure := waitForBoundResearchFrameReport(
			ctx,
			semanticBinding,
			surface,
			timeout,
		)
		if semanticFailure == nil {
			return report, nil
		}
		// A frame ID exposed through the parent DOM can still be an OOPIF that
		// rejects isolated-world creation in the parent session. Its backend-node
		// ownership proof remains useful, but the report must be retried through
		// a separately attached renderer target.
		if !retryResearchOOPIFFallback(semanticFailure) {
			return "", semanticFailure
		}
	}
	return readEmbeddedResearchOOPIFReport(
		ctx,
		config.Client,
		lease.Session(),
		surface,
		semanticFrame.BackendNodeID,
		timeout,
	)
}

func retryResearchOOPIFFallback(failure *readFailure) bool {
	return failure != nil && failure.code == "chatgpt_research_frame_unreadable"
}

// readEmbeddedResearchOOPIFReport handles an out-of-process report iframe
// which Chrome does not include in the parent target's Page.getFrameTree.
// It binds a candidate target to the exact semantic parent DOM node with
// DOM.getFrameOwner before reading any report content. Candidate targets are
// never closed or marked owned: each temporary session is only detached.
func readEmbeddedResearchOOPIFReport(
	ctx context.Context,
	client cdp.CommandClient,
	parentSession *cdp.PageSession,
	surface researchSurfaceObservation,
	expectedOwnerID int64,
	timeout time.Duration,
) (string, *readFailure) {
	deadline := time.Now().Add(timeout)
	for {
		binding, failure := bindResearchOOPIFCandidate(
			ctx,
			client,
			parentSession,
			surface,
			expectedOwnerID,
		)
		if failure != nil {
			return "", failure
		}
		if binding != nil {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				if closeFailure := closeResearchOOPIFSession(binding.ReportSession); closeFailure != nil {
					return "", closeFailure
				}
				return "", &readFailure{
					code:     "chatgpt_research_report_unavailable",
					errClass: "provider",
					message:  "ChatGPT embedded research frame did not expose a completed report",
				}
			}
			report, reportFailure := waitForBoundResearchFrameReport(
				ctx,
				binding,
				surface,
				remaining,
			)
			if closeFailure := closeResearchOOPIFSession(binding.ReportSession); closeFailure != nil {
				return "", closeFailure
			}
			return report, reportFailure
		}
		if remaining := time.Until(deadline); remaining <= 0 {
			return "", &readFailure{
				code:     "chatgpt_research_frame_unavailable",
				errClass: "provider",
				message:  "ChatGPT research report frame could not be bound to the owned page",
			}
		} else if !waitForObservation(ctx, 250*time.Millisecond, remaining) {
			return "", &readFailure{
				code:     "chatgpt_research_read_canceled",
				errClass: "timeout",
				message:  "ChatGPT embedded research read was canceled before a report frame was observed",
			}
		}
	}
}

func bindResearchOOPIFCandidate(
	ctx context.Context,
	client cdp.CommandClient,
	parentSession *cdp.PageSession,
	surface researchSurfaceObservation,
	expectedOwnerID int64,
) (*researchFrameBinding, *readFailure) {
	if len(surface.FrameSources) != 1 ||
		!trustedResearchFrameURL(surface.FrameSources[0]) {
		return nil, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame could not be identified safely",
		}
	}
	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return nil, &readFailure{
			code:     "chatgpt_research_oopif_targets_unavailable",
			errClass: "connection",
			message:  "ChatGPT embedded research report targets could not be listed",
		}
	}
	candidates := make([]cdp.TargetInfo, 0, 1)
	for _, target := range targets {
		if target.Type != "iframe" ||
			strings.TrimSpace(target.TargetID) == "" ||
			!trustedResearchFrameURL(target.URL) {
			continue
		}
		candidates = append(candidates, target)
	}
	if len(candidates) > maxResearchOOPIFCandidates {
		return nil, &readFailure{
			code:     "chatgpt_research_frame_ambiguous",
			errClass: "provider",
			message:  "ChatGPT research conversation exposed too many candidate report frame targets",
		}
	}
	var bound *researchFrameBinding
	releaseBound := func() *readFailure {
		if bound == nil {
			return nil
		}
		return closeResearchOOPIFSession(bound.ReportSession)
	}
	for _, candidate := range candidates {
		session, err := cdp.AttachToTargetWithClient(ctx, client, candidate.TargetID, nil)
		if err != nil {
			continue
		}
		root, reportFrame, rootFailure := researchTargetReportFrame(ctx, session)
		if rootFailure != nil {
			if closeFailure := closeResearchOOPIFSession(session); closeFailure != nil {
				_ = releaseBound()
				return nil, closeFailure
			}
			if rootFailure.errClass == "connection" {
				if closeFailure := releaseBound(); closeFailure != nil {
					return nil, closeFailure
				}
				return nil, rootFailure
			}
			continue
		}
		matches, matchFailure := researchFrameOwnerMatches(
			ctx,
			parentSession,
			root.ID,
			expectedOwnerID,
		)
		if matchFailure != nil {
			if closeFailure := closeResearchOOPIFSession(session); closeFailure != nil {
				_ = releaseBound()
				return nil, closeFailure
			}
			if closeFailure := releaseBound(); closeFailure != nil {
				return nil, closeFailure
			}
			return nil, matchFailure
		}
		if !matches {
			if closeFailure := closeResearchOOPIFSession(session); closeFailure != nil {
				_ = releaseBound()
				return nil, closeFailure
			}
			continue
		}
		candidateBinding := &researchFrameBinding{
			ReportSession:       session,
			ParentSession:       parentSession,
			FrameID:             reportFrame.ID,
			FrameURL:            reportFrame.URL,
			OwnerFrameID:        root.ID,
			OwnerFrameURL:       root.URL,
			OwnerBackendNodeID:  expectedOwnerID,
			TargetRootFrameRead: true,
		}
		if verificationFailure := candidateBinding.verify(ctx, surface); verificationFailure != nil {
			if closeFailure := closeResearchOOPIFSession(session); closeFailure != nil {
				_ = releaseBound()
				return nil, closeFailure
			}
			if closeFailure := releaseBound(); closeFailure != nil {
				return nil, closeFailure
			}
			return nil, verificationFailure
		}
		if bound != nil {
			if closeFailure := closeResearchOOPIFSession(session); closeFailure != nil {
				_ = closeResearchOOPIFSession(bound.ReportSession)
				return nil, closeFailure
			}
			if closeFailure := closeResearchOOPIFSession(bound.ReportSession); closeFailure != nil {
				return nil, closeFailure
			}
			return nil, &readFailure{
				code:     "chatgpt_research_frame_ambiguous",
				errClass: "provider",
				message:  "ChatGPT research conversation exposed multiple frame targets for one semantic report",
			}
		}
		bound = candidateBinding
	}
	return bound, nil
}

// resolveSemanticResearchFrame resolves the exact visible iframe element once
// and retains both identifiers emitted for that same DOM node. FrameID is
// optional because Chrome does not expose it for every renderer arrangement.
func resolveSemanticResearchFrame(
	ctx context.Context,
	parentSession *cdp.PageSession,
	surface researchSurfaceObservation,
) (semanticResearchFrame, *readFailure) {
	if parentSession == nil || len(surface.FrameSources) != 1 ||
		!trustedResearchFrameURL(surface.FrameSources[0]) {
		return semanticResearchFrame{}, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame could not be identified safely",
		}
	}
	source, err := json.Marshal(surface.FrameSources[0])
	if err != nil {
		return semanticResearchFrame{}, internalReadFailure(
			"ChatGPT research frame source could not be encoded",
		)
	}
	expression := fmt.Sprintf(`(() => {
  const source = %s;
  const visible = element => Boolean(
    element && element.getAttribute('aria-hidden') !== 'true' &&
    (element.offsetWidth || element.offsetHeight || element.getClientRects().length)
  );
  const frames = Array.from(document.querySelectorAll(
    'iframe[src*="deep-research"],iframe[src*="connector_openai_deep_research"],' +
    '[data-testid*="deep-research"] iframe,[data-testid*="research-report"] iframe'
  )).filter(visible).filter(frame => String(frame.src || '') === source);
  return frames.length === 1 ? frames[0] : null;
})()`, source)
	params, err := json.Marshal(map[string]any{
		"expression":    expression,
		"returnByValue": false,
		"awaitPromise":  true,
	})
	if err != nil {
		return semanticResearchFrame{}, internalReadFailure(
			"ChatGPT research frame owner request could not be encoded",
		)
	}
	raw, err := parentSession.Exec(ctx, "Runtime.evaluate", params)
	if err != nil {
		return semanticResearchFrame{}, &readFailure{
			code:     "chatgpt_research_oopif_unreadable",
			errClass: "connection",
			message:  "ChatGPT research frame owner could not be observed",
		}
	}
	var evaluation struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
		ExceptionDetails any `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &evaluation); err != nil ||
		evaluation.ExceptionDetails != nil ||
		strings.TrimSpace(evaluation.Result.ObjectID) == "" {
		return semanticResearchFrame{}, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame could not be bound to one semantic iframe",
		}
	}
	defer releaseResearchFrameObject(parentSession, evaluation.Result.ObjectID)

	describeParams, err := json.Marshal(map[string]any{
		"objectId": evaluation.Result.ObjectID,
	})
	if err != nil {
		return semanticResearchFrame{}, internalReadFailure(
			"ChatGPT research frame owner description could not be encoded",
		)
	}
	raw, err = parentSession.Exec(ctx, "DOM.describeNode", describeParams)
	if err != nil {
		return semanticResearchFrame{}, &readFailure{
			code:     "chatgpt_research_oopif_unreadable",
			errClass: "connection",
			message:  "ChatGPT research frame owner could not be described",
		}
	}
	var description struct {
		Node struct {
			BackendNodeID int64  `json:"backendNodeId"`
			NodeName      string `json:"nodeName"`
			FrameID       string `json:"frameId"`
		} `json:"node"`
	}
	if err := json.Unmarshal(raw, &description); err != nil ||
		description.Node.BackendNodeID <= 0 ||
		!strings.EqualFold(description.Node.NodeName, "IFRAME") {
		return semanticResearchFrame{}, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame could not be bound to one semantic iframe",
		}
	}
	return semanticResearchFrame{
		BackendNodeID: description.Node.BackendNodeID,
		FrameID:       strings.TrimSpace(description.Node.FrameID),
	}, nil
}

func releaseResearchFrameObject(session *cdp.PageSession, objectID string) {
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	params, err := json.Marshal(map[string]any{"objectId": objectID})
	if err != nil {
		return
	}
	_, _ = session.Exec(closeCtx, "Runtime.releaseObject", params)
}

func researchFrameOwnerMatches(
	ctx context.Context,
	parentSession *cdp.PageSession,
	frameID string,
	expectedBackendNodeID int64,
) (bool, *readFailure) {
	params, err := json.Marshal(map[string]any{"frameId": frameID})
	if err != nil {
		return false, internalReadFailure(
			"ChatGPT research frame ownership request could not be encoded",
		)
	}
	raw, err := parentSession.Exec(ctx, "DOM.getFrameOwner", params)
	if err != nil {
		var protocolErr *cdp.ProtocolError
		if errors.As(err, &protocolErr) {
			return false, nil
		}
		return false, &readFailure{
			code:     "chatgpt_research_oopif_unreadable",
			errClass: "connection",
			message:  "ChatGPT research frame ownership could not be verified",
		}
	}
	var owner struct {
		BackendNodeID int64 `json:"backendNodeId"`
	}
	if err := json.Unmarshal(raw, &owner); err != nil {
		return false, &readFailure{
			code:     "chatgpt_research_oopif_unreadable",
			errClass: "connection",
			message:  "ChatGPT research frame ownership response could not be decoded",
		}
	}
	return owner.BackendNodeID > 0 &&
		owner.BackendNodeID == expectedBackendNodeID, nil
}

func closeResearchOOPIFSession(session *cdp.PageSession) *readFailure {
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.Close(closeCtx); err != nil {
		return &readFailure{
			code:     "chatgpt_research_oopif_unreadable",
			errClass: "connection",
			message:  "ChatGPT embedded research report target could not be detached",
		}
	}
	return nil
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
  const frames = Array.from(document.querySelectorAll(
    'iframe[src*="deep-research"],iframe[src*="connector_openai_deep_research"],' +
    '[data-testid*="deep-research"] iframe,[data-testid*="research-report"] iframe'
  )).filter(visible);
  const sources = frames.map(frame => String(frame.src || ''))
    .filter(source => source.startsWith('https://'));
  const match = location.pathname.match(/^\/c\/([A-Za-z0-9_-]+)$/);
  return {
    route_matches: Boolean(match),
    conversation_id: match ? match[1] : '',
    frame_sources: sources
  };
})()`, observation)
}

func bindDirectResearchFrame(
	ctx context.Context,
	session *cdp.PageSession,
	surface researchSurfaceObservation,
	expectedOwnerID int64,
) (*researchFrameBinding, *readFailure) {
	if len(surface.FrameSources) != 1 ||
		!trustedResearchFrameURL(surface.FrameSources[0]) {
		return nil, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame could not be identified safely",
		}
	}
	tree, failure := researchOwnedFrameTree(ctx, session)
	if failure != nil {
		return nil, failure
	}
	candidates := collectResearchFrames(tree.FrameTree, "")
	var bound *researchFrameBinding
	candidateCount := 0
	for _, candidate := range candidates {
		if candidate.ParentID == "" ||
			strings.TrimSpace(candidate.ID) == "" ||
			!trustedResearchFrameURL(candidate.URL) {
			continue
		}
		candidateCount++
		if candidateCount > maxResearchOOPIFCandidates {
			return nil, &readFailure{
				code:     "chatgpt_research_frame_ambiguous",
				errClass: "provider",
				message:  "ChatGPT research conversation exposed too many candidate report frames",
			}
		}
		matches, matchFailure := researchFrameOwnerMatches(
			ctx,
			session,
			candidate.ID,
			expectedOwnerID,
		)
		if matchFailure != nil {
			return nil, matchFailure
		}
		if !matches {
			continue
		}
		candidateBinding := &researchFrameBinding{
			ReportSession:      session,
			ParentSession:      session,
			FrameID:            candidate.ID,
			FrameURL:           candidate.URL,
			OwnerBackendNodeID: expectedOwnerID,
		}
		if verificationFailure := candidateBinding.verify(ctx, surface); verificationFailure != nil {
			return nil, verificationFailure
		}
		if bound != nil {
			return nil, &readFailure{
				code:     "chatgpt_research_frame_ambiguous",
				errClass: "provider",
				message:  "ChatGPT research conversation exposed multiple frames for one semantic report",
			}
		}
		bound = candidateBinding
	}
	return bound, nil
}

// bindParentSemanticResearchFrame covers renderers whose iframe frame ID is
// available from DOM.describeNode but absent from both Page.getFrameTree and
// Target.getTargets. It never discovers a frame by ID alone: DOM.getFrameOwner
// must bind that exact frame ID to the exact semantic iframe backend node.
func bindParentSemanticResearchFrame(
	ctx context.Context,
	parentSession *cdp.PageSession,
	surface researchSurfaceObservation,
	semanticFrame semanticResearchFrame,
) (*researchFrameBinding, *readFailure) {
	if strings.TrimSpace(semanticFrame.FrameID) == "" {
		return nil, nil
	}
	if semanticFrame.BackendNodeID <= 0 || len(surface.FrameSources) != 1 ||
		!trustedResearchFrameURL(surface.FrameSources[0]) {
		return nil, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame could not be identified safely",
		}
	}
	matches, failure := researchFrameOwnerMatches(
		ctx,
		parentSession,
		semanticFrame.FrameID,
		semanticFrame.BackendNodeID,
	)
	if failure != nil {
		return nil, failure
	}
	if !matches {
		return nil, nil
	}
	binding := &researchFrameBinding{
		ReportSession:       parentSession,
		ParentSession:       parentSession,
		FrameID:             semanticFrame.FrameID,
		FrameURL:            surface.FrameSources[0],
		OwnerBackendNodeID:  semanticFrame.BackendNodeID,
		ParentSemanticFrame: true,
	}
	if failure := binding.verify(ctx, surface); failure != nil {
		return nil, failure
	}
	return binding, nil
}

func researchOwnedFrameTree(
	ctx context.Context,
	session *cdp.PageSession,
) (*researchFrameTreeResponse, *readFailure) {
	raw, err := session.Exec(ctx, "Page.getFrameTree", nil)
	if err != nil {
		return nil, &readFailure{
			code:     "chatgpt_research_frame_tree_unavailable",
			errClass: "connection",
			message:  "ChatGPT research frame tree could not be read",
		}
	}
	var tree researchFrameTreeResponse
	if err := json.Unmarshal(raw, &tree); err != nil ||
		tree.FrameTree == nil || tree.FrameTree.Frame == nil {
		return nil, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame was not available in the owned page target",
		}
	}
	return &tree, nil
}

func researchFrameCandidateByID(
	ctx context.Context,
	session *cdp.PageSession,
	frameID string,
) (researchFrameCandidate, *readFailure) {
	tree, failure := researchOwnedFrameTree(ctx, session)
	if failure != nil {
		return researchFrameCandidate{}, failure
	}
	matching := make([]researchFrameCandidate, 0, 1)
	for _, candidate := range collectResearchFrames(tree.FrameTree, "") {
		if candidate.ParentID != "" && candidate.ID == frameID {
			matching = append(matching, candidate)
		}
	}
	if len(matching) != 1 || !trustedResearchFrameURL(matching[0].URL) {
		return researchFrameCandidate{}, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame changed before it could be read",
		}
	}
	return matching[0], nil
}

// researchFrameRoot returns the current root frame for an attached OOPIF
// target. Its parent page owns the semantic iframe binding; this target's root
// is therefore the only frame that may be read through this session.
func researchFrameRoot(
	ctx context.Context,
	session *cdp.PageSession,
) (researchFrameTargetRoot, *readFailure) {
	root, _, failure := researchTargetReportFrame(ctx, session)
	return root, failure
}

// researchTargetReportFrame retains the target root for the parent DOM owner
// proof, then resolves the sole sandbox descendant when the renderer nests the
// report in its own iframe. The descendant is confined to the already-owned
// renderer target and is rechecked before every report observation.
func researchTargetReportFrame(
	ctx context.Context,
	session *cdp.PageSession,
) (researchFrameTargetRoot, researchFrameTargetRoot, *readFailure) {
	raw, err := session.Exec(ctx, "Page.getFrameTree", nil)
	if err != nil {
		return researchFrameTargetRoot{}, researchFrameTargetRoot{}, &readFailure{
			code:     "chatgpt_research_oopif_unreadable",
			errClass: "connection",
			message:  "ChatGPT embedded research report target frame tree could not be read",
		}
	}
	var tree researchFrameTreeResponse
	if err := json.Unmarshal(raw, &tree); err != nil ||
		tree.FrameTree == nil || tree.FrameTree.Frame == nil ||
		strings.TrimSpace(tree.FrameTree.Frame.ID) == "" ||
		!trustedResearchFrameURL(tree.FrameTree.Frame.URL) {
		return researchFrameTargetRoot{}, researchFrameTargetRoot{}, &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT embedded research report target did not expose a trusted root frame",
		}
	}
	root := researchFrameTargetRoot{
		ID:  tree.FrameTree.Frame.ID,
		URL: tree.FrameTree.Frame.URL,
	}
	directChildren := make([]researchFrameCandidate, 0, len(tree.FrameTree.ChildFrames))
	children := make([]researchFrameCandidate, 0, 1)
	for _, candidate := range collectResearchFrames(tree.FrameTree, "") {
		if candidate.ParentID != root.ID {
			continue
		}
		directChildren = append(directChildren, candidate)
		if researchSandboxFrameURL(candidate.URL) {
			children = append(children, candidate)
		}
	}
	switch len(children) {
	case 0:
		if len(directChildren) > 0 {
			return researchFrameTargetRoot{}, researchFrameTargetRoot{}, &readFailure{
				code:     "chatgpt_research_frame_unavailable",
				errClass: "provider",
				message:  "ChatGPT embedded research target did not expose one trusted sandbox report frame",
			}
		}
		return root, root, nil
	case 1:
		return root, researchFrameTargetRoot{
			ID:  children[0].ID,
			URL: children[0].URL,
		}, nil
	default:
		return researchFrameTargetRoot{}, researchFrameTargetRoot{}, &readFailure{
			code:     "chatgpt_research_frame_ambiguous",
			errClass: "provider",
			message:  "ChatGPT embedded research target exposed multiple sandbox report frames",
		}
	}
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

// trustedResearchFrameURL pins rendered research frames to ChatGPT's page or
// its known Deep Research sandbox renderer. A semantic selector alone must
// not make an arbitrary HTTPS iframe readable.
func trustedResearchFrameURL(raw string) bool {
	if !safeResearchFrameURL(raw) {
		return false
	}
	frameURL, err := url.Parse(raw)
	if err != nil {
		return false
	}
	providerURL, err := url.Parse(Origin)
	if err != nil || frameURL.Port() != "" || providerURL.Port() != "" ||
		!strings.EqualFold(frameURL.Scheme, providerURL.Scheme) {
		return false
	}
	host := frameURL.Hostname()
	return strings.EqualFold(host, providerURL.Hostname()) ||
		strings.EqualFold(host, researchSandboxHost)
}

func researchSandboxFrameURL(raw string) bool {
	if !trustedResearchFrameURL(raw) {
		return false
	}
	frameURL, err := url.Parse(raw)
	return err == nil && strings.EqualFold(frameURL.Hostname(), researchSandboxHost)
}

func (binding *researchFrameBinding) verify(
	ctx context.Context,
	surface researchSurfaceObservation,
) *readFailure {
	ownerFrameID, ownerFrameURL := binding.ownerFrame()
	if binding == nil || binding.ReportSession == nil || binding.ParentSession == nil ||
		strings.TrimSpace(binding.FrameID) == "" ||
		!trustedResearchFrameURL(binding.FrameURL) ||
		strings.TrimSpace(ownerFrameID) == "" ||
		!trustedResearchFrameURL(ownerFrameURL) ||
		binding.OwnerBackendNodeID <= 0 {
		return &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame could not be verified safely",
		}
	}
	semanticFrame, failure := resolveSemanticResearchFrame(
		ctx,
		binding.ParentSession,
		surface,
	)
	if failure != nil {
		return failure
	}
	if semanticFrame.BackendNodeID != binding.OwnerBackendNodeID ||
		(binding.ParentSemanticFrame && semanticFrame.FrameID != binding.FrameID) {
		return &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame changed before it could be read",
		}
	}
	if binding.TargetRootFrameRead {
		root, reportFrame, rootFailure := researchTargetReportFrame(ctx, binding.ReportSession)
		if rootFailure != nil {
			return rootFailure
		}
		if root.ID != ownerFrameID || root.URL != ownerFrameURL ||
			reportFrame.ID != binding.FrameID || reportFrame.URL != binding.FrameURL {
			return &readFailure{
				code:     "chatgpt_research_frame_unavailable",
				errClass: "provider",
				message:  "ChatGPT research report frame changed before it could be read",
			}
		}
	} else if !binding.ParentSemanticFrame {
		candidate, candidateFailure := researchFrameCandidateByID(
			ctx,
			binding.ParentSession,
			binding.FrameID,
		)
		if candidateFailure != nil {
			return candidateFailure
		}
		if candidate.URL != binding.FrameURL {
			return &readFailure{
				code:     "chatgpt_research_frame_unavailable",
				errClass: "provider",
				message:  "ChatGPT research report frame changed before it could be read",
			}
		}
	}
	matches, failure := researchFrameOwnerMatches(
		ctx,
		binding.ParentSession,
		ownerFrameID,
		binding.OwnerBackendNodeID,
	)
	if failure != nil {
		return failure
	}
	if !matches {
		return &readFailure{
			code:     "chatgpt_research_frame_unavailable",
			errClass: "provider",
			message:  "ChatGPT research report frame changed before it could be read",
		}
	}
	return nil
}

func (binding *researchFrameBinding) ownerFrame() (string, string) {
	if binding == nil {
		return "", ""
	}
	frameID := binding.OwnerFrameID
	if strings.TrimSpace(frameID) == "" {
		frameID = binding.FrameID
	}
	frameURL := binding.OwnerFrameURL
	if strings.TrimSpace(frameURL) == "" {
		frameURL = binding.FrameURL
	}
	return frameID, frameURL
}

func waitForBoundResearchFrameReport(
	ctx context.Context,
	binding *researchFrameBinding,
	surface researchSurfaceObservation,
	timeout time.Duration,
) (string, *readFailure) {
	deadline := time.Now().Add(timeout)
	if failure := binding.verify(ctx, surface); failure != nil {
		return "", failure
	}
	contextID, failure := researchFrameExecutionContext(
		ctx,
		binding.ReportSession,
		binding.FrameID,
	)
	if failure != nil {
		return "", failure
	}
	for {
		if failure := binding.verify(ctx, surface); failure != nil {
			return "", failure
		}
		if binding.ParentSemanticFrame {
			trusted, trustFailure := researchFrameDocumentTrusted(
				ctx,
				binding.ReportSession,
				contextID,
			)
			if trustFailure != nil {
				return "", trustFailure
			}
			if !trusted {
				return "", &readFailure{
					code:     "chatgpt_research_frame_unavailable",
					errClass: "provider",
					message:  "ChatGPT research report frame changed before it could be read",
				}
			}
		}
		observation, failure := observeResearchFrame(
			ctx,
			binding.ReportSession,
			contextID,
		)
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
		if !observation.DocumentTrusted {
			return "", &readFailure{
				code:     "chatgpt_research_frame_unavailable",
				errClass: "provider",
				message:  "ChatGPT research report frame changed before it could be read",
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

// researchFrameDocumentTrusted checks only a boolean computed inside the
// isolated world. It never returns or records the renderer URL.
func researchFrameDocumentTrusted(
	ctx context.Context,
	session *cdp.PageSession,
	contextID int,
) (bool, *readFailure) {
	providerURL, err := url.Parse(Origin)
	if err != nil {
		return false, internalReadFailure(
			"ChatGPT research renderer origin could not be prepared",
		)
	}
	allowedHosts, err := json.Marshal([]string{
		strings.ToLower(providerURL.Hostname()),
		strings.ToLower(researchSandboxHost),
	})
	if err != nil {
		return false, internalReadFailure(
			"ChatGPT research renderer allowlist could not be encoded",
		)
	}
	params, err := json.Marshal(map[string]any{
		"expression": fmt.Sprintf(`(() => {
  try {
    const current = new URL(location.href);
    const allowedHosts = %s;
    return current.protocol === 'https:' && current.port === '' &&
      allowedHosts.includes(String(current.hostname || '').toLowerCase());
  } catch (_) {
    return false;
  }
})()`, allowedHosts),
		"contextId":     contextID,
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if err != nil {
		return false, internalReadFailure(
			"ChatGPT research renderer trust request could not be encoded",
		)
	}
	raw, err := session.Exec(ctx, "Runtime.evaluate", params)
	if err != nil {
		return false, &readFailure{
			code:     "chatgpt_research_frame_unreadable",
			errClass: "connection",
			message:  "ChatGPT research renderer trust could not be observed",
		}
	}
	var evaluation struct {
		Result struct {
			Value *bool `json:"value"`
		} `json:"result"`
		ExceptionDetails any `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &evaluation); err != nil ||
		evaluation.ExceptionDetails != nil || evaluation.Result.Value == nil {
		return false, &readFailure{
			code:     "chatgpt_research_frame_unreadable",
			errClass: "connection",
			message:  "ChatGPT research renderer trust could not be decoded",
		}
	}
	return *evaluation.Result.Value, nil
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
  const allowedHosts = ["chatgpt.com", %q];
  let documentTrusted = false;
  try {
    const current = new URL(location.href);
    documentTrusted = current.protocol === 'https:' && current.port === '' &&
      allowedHosts.includes(String(current.hostname || '').toLowerCase());
  } catch (_) {}
  if (!documentTrusted) {
    return {
      text: '',
      root_count: 0,
      streaming: false,
      too_large: false,
      document_trusted: false
    };
  }
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
    too_large: text.length > maxCharacters,
    document_trusted: true
  };
})()`, maxResearchReportBytes, researchSandboxHost)
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
