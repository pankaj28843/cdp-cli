package chatgpt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const browserTranscriptionReadinessTimeout = 30 * time.Second

type browserTranscriptionResponse struct {
	Status       int    `json:"status"`
	ContentType  string `json:"content_type"`
	Body         string `json:"body"`
	BodyTooLarge bool   `json:"body_too_large"`
	NetworkError bool   `json:"network_error"`
}

func transcribeBrowserAttempt(
	ctx context.Context,
	config TranscribeConfig,
	runID string,
	attemptNumber int,
	data TranscriptionData,
	template RequestTemplate,
	audio []byte,
	durationMilliseconds int64,
) (transcriptionAttempt, webagent.Result, error) {
	if config.Browser == nil {
		return transcriptionAttempt{}, webagent.Result{}, &transcribeFailure{
			code:     "chatgpt_browserflow_unavailable",
			errClass: "internal",
			message:  "ChatGPT headed browser transcription is not configured",
		}
	}

	data.Transport = "headed_browser_fetch"
	browserRunID := fmt.Sprintf("%s-browser-%d", runID, attemptNumber)
	var response browserTranscriptionResponse
	result := runOwned(
		ctx,
		*config.Browser,
		browserRunID,
		webagent.OperationTranscribe,
		"transcribe",
		"about:blank",
		"headed_browser_fetch",
		data,
		func(
			lease *browserflow.Lease,
			target *webagent.TargetEvidence,
			pending webagent.CleanupEvidence,
		) webagent.Result {
			session := lease.Session()
			fail := func(
				stage webagent.Stage,
				code string,
				errClass string,
				message string,
			) webagent.Result {
				_ = lease.MarkIncomplete(context.Background())
				return operationFailure(
					browserRunID,
					config.BuildCommit,
					webagent.OperationTranscribe,
					stage,
					"headed_browser_fetch",
					target,
					pending,
					code,
					errClass,
					message,
					data,
					cleanupCommands(browserRunID, pending),
				)
			}
			if err := preparePage(ctx, config.Browser.Client, session, HomeURL); err != nil {
				return fail(
					webagent.StageAttached,
					"chatgpt_transcription_browser_prepare_failed",
					"connection",
					"ChatGPT transcription could not prepare the exact headed target",
				)
			}
			if err := lease.MarkPrepared(ctx); err != nil {
				return fail(
					webagent.StageAttached,
					"chatgpt_transcription_browser_state_failed",
					"internal",
					"ChatGPT transcription browser preparation could not be persisted",
				)
			}

			readiness, readinessErr := authreadiness.WaitForEvidence(
				ctx,
				session,
				authreadiness.MinimumAttempts,
				browserTranscriptionReadinessTimeout,
				250*time.Millisecond,
				func(observationCtx context.Context) (bool, error) {
					cookies, cookieErr := readCookies(observationCtx, session)
					if cookieErr != nil {
						return false, cookieErr
					}
					if !hasSessionCookie(cookies) {
						return false, nil
					}
					return observeSignedInUI(observationCtx, session)
				},
			)
			if readinessErr != nil || readiness.ObservationFailed() {
				return fail(
					webagent.StagePrepared,
					"chatgpt_transcription_browser_observation_failed",
					"connection",
					"ChatGPT signed-in headed browser readiness could not be observed",
				)
			}
			if !readiness.Observed {
				return fail(
					webagent.StageObserveTerminal,
					"chatgpt_auth_not_ready",
					"auth",
					"ChatGPT signed-in headed browser readiness was not observed",
				)
			}

			var evalErr error
			response, evalErr = evaluateBrowserTranscription(
				ctx,
				session,
				template,
				audio,
				durationMilliseconds,
			)
			if evalErr != nil || response.NetworkError {
				return fail(
					webagent.StageActionPending,
					"chatgpt_transcription_browser_request_failed",
					"connection",
					"ChatGPT headed browser transcription request was unavailable",
				)
			}
			data.StatusCode = response.Status
			if response.BodyTooLarge {
				return fail(
					webagent.StageObserveTerminal,
					"chatgpt_transcription_response_too_large",
					"provider",
					"ChatGPT transcription response exceeded its safety bound",
				)
			}
			if response.Status < 200 || response.Status >= 300 {
				failure := transcriptionHTTPFailure(response.Status, []byte(response.Body))
				return fail(
					webagent.StageObserveTerminal,
					failure.code,
					failure.errClass,
					failure.message,
				)
			}
			transcript, parseErr := parseTranscriptionBody([]byte(response.Body))
			if parseErr != nil {
				return fail(
					webagent.StageObserveTerminal,
					"chatgpt_transcription_response_changed",
					"provider",
					"ChatGPT transcription returned an empty or unrecognized response",
				)
			}
			data.Transcript = transcript
			if err := lease.MarkTerminal(ctx); err != nil {
				return fail(
					webagent.StageObserveTerminal,
					"chatgpt_transcription_browser_terminal_state_failed",
					"internal",
					"ChatGPT transcription terminal state could not be persisted",
				)
			}
			return operationSuccess(
				browserRunID,
				config.BuildCommit,
				webagent.OperationTranscribe,
				webagent.StageObserveTerminal,
				"headed_browser_fetch",
				target,
				pending,
				data,
				[]string{
					"cdp workflow agent chatgpt doctor --json",
					"cdp workflow agent chatgpt capabilities --json",
				},
			)
		},
	)
	if result.OK {
		return transcriptionAttempt{transcript: data.Transcript}, result, nil
	}
	return transcriptionAttempt{}, result, browserTranscriptionFailureFromResult(result)
}

func evaluateBrowserTranscription(
	ctx context.Context,
	session *cdp.PageSession,
	template RequestTemplate,
	audio []byte,
	durationMilliseconds int64,
) (browserTranscriptionResponse, error) {
	expression, err := browserTranscriptionExpression(
		template,
		audio,
		durationMilliseconds,
	)
	if err != nil {
		return browserTranscriptionResponse{}, err
	}
	var response browserTranscriptionResponse
	if err := evaluateInto(ctx, session, expression, &response); err != nil {
		return browserTranscriptionResponse{}, err
	}
	return response, nil
}

func browserTranscriptionExpression(
	template RequestTemplate,
	audio []byte,
	durationMilliseconds int64,
) (string, error) {
	audioJSON, err := json.Marshal(base64.StdEncoding.EncodeToString(audio))
	if err != nil {
		return "", err
	}
	durationJSON, err := json.Marshal(strconv.FormatInt(durationMilliseconds, 10))
	if err != nil {
		return "", err
	}
	authorization := transcriptionTemplateHeader(template, "authorization")
	accountID := transcriptionTemplateHeader(template, "chatgpt-account-id")
	authorizationJSON, err := json.Marshal(authorization)
	if err != nil {
		return "", err
	}
	accountIDJSON, err := json.Marshal(accountID)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`(async()=>{
		const encoded=%s;
		const binary=atob(encoded);
		const bytes=new Uint8Array(binary.length);
		for(let i=0;i<binary.length;i++) bytes[i]=binary.charCodeAt(i);
		const form=new FormData();
		form.append("file",new Blob([bytes],{type:"audio/webm;codecs=opus"}),"whisper.webm");
		form.append("duration_ms",%s);
		const headers={Accept:"*/*"};
		if(%s) headers.Authorization=%s;
		if(%s) headers["ChatGPT-Account-Id"]=%s;
		try {
			const response=await fetch("/backend-api/transcribe",{method:"POST",headers,body:form,credentials:"include"});
			const body=await response.text();
			const tooLarge=body.length>%d;
			return {status:response.status,content_type:response.headers.get("content-type")||"",body:tooLarge?"":body,body_too_large:tooLarge,network_error:false};
		} catch (_) {
			return {status:0,content_type:"",body:"",body_too_large:false,network_error:true};
		}
	})()`,
		string(audioJSON),
		string(durationJSON),
		strconv.FormatBool(authorization != ""),
		string(authorizationJSON),
		strconv.FormatBool(accountID != ""),
		string(accountIDJSON),
		maxTranscriptionBodyBytes,
	), nil
}

func transcriptionTemplateHeader(template RequestTemplate, wanted string) string {
	for name, value := range template.Headers {
		if strings.EqualFold(strings.TrimSpace(name), wanted) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func browserTranscriptionFailureFromResult(result webagent.Result) error {
	if result.Error == nil {
		return &transcribeFailure{
			code:      "chatgpt_transcription_browser_failed",
			errClass:  "connection",
			message:   "ChatGPT headed browser transcription was not completed",
			retryable: true,
		}
	}
	failure := &transcribeFailure{
		code:      result.Error.Code,
		errClass:  result.Error.ErrClass,
		message:   result.Error.Message,
		retryable: result.Error.RetrySafe,
		auth:      strings.Contains(result.Error.Code, "auth"),
	}
	if data, ok := result.Data.(TranscriptionData); ok {
		failure.status = data.StatusCode
	}
	return failure
}
