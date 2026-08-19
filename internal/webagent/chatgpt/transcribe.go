package chatgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/resilience"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	TranscriptionSchemaVersion = "chatgpt-transcription/v1"
	maxTranscriptionAudioBytes = 50 << 20
	maxTranscriptionBodyBytes  = 2 << 20
	maxTranscriptionDurationMS = 10 * 60 * 1000
)

type TranscribeConfig struct {
	Store       *Store
	BuildCommit string
	HTTPClient  *http.Client
	RefreshAuth func(context.Context) error
	MaxAttempts int
	Backoff     []time.Duration
	Now         func() time.Time
}

type TranscriptionData struct {
	SchemaVersion        string `json:"schema_version"`
	Transport            string `json:"transport"`
	EndpointPath         string `json:"endpoint_path"`
	FileName             string `json:"file_name"`
	AudioBytes           int64  `json:"audio_bytes"`
	DurationMilliseconds int64  `json:"duration_ms"`
	StatusCode           int    `json:"status_code"`
	Attempts             int    `json:"attempts"`
	Transcript           string `json:"transcript,omitempty"`
}

type transcribeFailure struct {
	code      string
	errClass  string
	message   string
	status    int
	retryable bool
	auth      bool
}

func (f *transcribeFailure) Error() string {
	if f == nil {
		return "ChatGPT transcription failed"
	}
	return f.message
}

type transcriptionAttempt struct {
	transcript string
}

func Transcribe(
	ctx context.Context,
	config TranscribeConfig,
	filePath string,
	durationMilliseconds int64,
) webagent.Result {
	runID := webagent.NewRunID()
	fileName := filepath.Base(filePath)
	data := TranscriptionData{
		SchemaVersion:        TranscriptionSchemaVersion,
		Transport:            "direct_http",
		EndpointPath:         "/backend-api/transcribe",
		FileName:             fileName,
		DurationMilliseconds: durationMilliseconds,
	}

	if config.Store == nil {
		return transcriptionFailureResult(
			runID, config, data,
			transcribeFailure{
				code:     "chatgpt_state_unavailable",
				errClass: "internal",
				message:  "ChatGPT owner-only auth state is unavailable",
			},
		)
	}
	if durationMilliseconds <= 0 || durationMilliseconds > maxTranscriptionDurationMS {
		return transcriptionFailureResult(
			runID, config, data,
			transcribeFailure{
				code:     "chatgpt_transcription_duration_invalid",
				errClass: "usage",
				message:  "ChatGPT transcription duration must be between 1 ms and 10 minutes",
			},
		)
	}

	audio, audioFailure := readTranscriptionAudio(filePath)
	if audioFailure != nil {
		return transcriptionFailureResult(runID, config, data, *audioFailure)
	}
	data.AudioBytes = int64(len(audio))

	attempt, report, runErr := resilience.Run(
		ctx,
		resilience.Policy{MaxAttempts: config.MaxAttempts, Backoff: config.Backoff},
		resilience.Hooks[transcriptionAttempt]{
			Attempt: func(attemptContext context.Context, _ int) (transcriptionAttempt, error) {
				template, templateFailure := loadTranscriptionTemplate(attemptContext, config)
				if templateFailure != nil {
					return transcriptionAttempt{}, templateFailure
				}
				request, requestErr := newTranscriptionRequest(
					attemptContext,
					template,
					audio,
					durationMilliseconds,
				)
				if requestErr != nil {
					return transcriptionAttempt{}, &transcribeFailure{
						code:     "chatgpt_transcription_request_invalid",
						errClass: "internal",
						message:  "ChatGPT transcription request could not be prepared",
					}
				}
				response, requestFailure := doTranscriptionRequest(config.HTTPClient, request)
				if requestFailure != nil {
					return transcriptionAttempt{}, requestFailure
				}
				data.StatusCode = response.statusCode
				if response.statusCode < 200 || response.statusCode >= 300 {
					failure := transcriptionHTTPFailure(response.statusCode)
					return transcriptionAttempt{}, &failure
				}
				transcript, parseErr := parseTranscriptionBody(response.body)
				if parseErr != nil {
					return transcriptionAttempt{}, &transcribeFailure{
						code:     "chatgpt_transcription_response_changed",
						errClass: "provider",
						message:  "ChatGPT transcription returned an empty or unrecognized response",
					}
				}
				return transcriptionAttempt{transcript: transcript}, nil
			},
			Classify:    classifyTranscriptionFailure,
			RefreshAuth: config.RefreshAuth,
			OnRefreshFailure: func(error) error {
				return &transcribeFailure{
					code:     "chatgpt_auth_refresh_failed",
					errClass: "auth",
					message:  "ChatGPT auth refresh could not complete",
				}
			},
		},
	)
	data.Attempts = report.Attempts
	if runErr == nil {
		data.Transcript = attempt.transcript
		return operationSuccess(
			runID,
			config.BuildCommit,
			webagent.OperationTranscribe,
			webagent.StageObserveTerminal,
			"direct_http",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			data,
			[]string{
				"cdp workflow agent chatgpt doctor --json",
				"cdp workflow agent chatgpt capabilities --json",
			},
		)
	}
	return transcriptionFailureResult(runID, config, data, transcriptionFailureFromError(runErr))
}

type transcriptionHTTPResponse struct {
	statusCode int
	body       []byte
}

func readTranscriptionAudio(filePath string) ([]byte, *transcribeFailure) {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &transcribeFailure{
			code:     "chatgpt_audio_file_missing",
			errClass: "usage",
			message:  "ChatGPT transcription audio must be an existing regular file",
		}
	}
	if info.Size() <= 0 || info.Size() > maxTranscriptionAudioBytes {
		return nil, &transcribeFailure{
			code:     "chatgpt_audio_file_invalid",
			errClass: "usage",
			message:  "ChatGPT transcription audio must be between 1 byte and 50 MiB",
		}
	}
	audio, err := os.ReadFile(filePath)
	if err != nil {
		return nil, &transcribeFailure{
			code:     "chatgpt_audio_file_unreadable",
			errClass: "connection",
			message:  "ChatGPT transcription audio could not be read",
		}
	}
	return audio, nil
}

func loadTranscriptionTemplate(
	ctx context.Context,
	config TranscribeConfig,
) (RequestTemplate, *transcribeFailure) {
	template, status, err := config.Store.LoadTemplateStatus(
		ctx,
		nowForTranscription(config),
		DefaultAuthTTL,
	)
	if !status.Ready {
		return RequestTemplate{}, &transcribeFailure{
			code:      "chatgpt_auth_not_ready",
			errClass:  "auth",
			message:   "ChatGPT auth evidence is not ready for transcription",
			retryable: true,
			auth:      true,
		}
	}
	if err != nil {
		return RequestTemplate{}, &transcribeFailure{
			code:     "chatgpt_auth_state_invalid",
			errClass: "auth",
			message:  "ChatGPT auth state could not be loaded",
			auth:     true,
		}
	}
	return template, nil
}

func newTranscriptionRequest(
	ctx context.Context,
	template RequestTemplate,
	audio []byte,
	durationMilliseconds int64,
) (*http.Request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="file"; filename="whisper.webm"`},
		"Content-Type":        []string{"audio/webm;codecs=opus"},
	})
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	if err := writer.WriteField("duration_ms", strconv.FormatInt(durationMilliseconds, 10)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		Origin+"/backend-api/transcribe",
		bytes.NewReader(body.Bytes()),
	)
	if err != nil {
		return nil, err
	}
	for name, value := range template.Headers {
		lower := strings.ToLower(name)
		if lower == "content-type" ||
			lower == "content-length" ||
			lower == "oai-echo-logs" ||
			lower == "oai-telemetry" ||
			lower == "origin" ||
			lower == "priority" ||
			strings.HasPrefix(lower, "sec-fetch-") ||
			lower == "x-conduit-token" ||
			lower == "x-oai-turn-trace-id" ||
			strings.HasPrefix(lower, "openai-sentinel-") {
			continue
		}
		request.Header.Set(name, value)
	}
	request.Header.Del("Accept-Encoding")
	request.Header.Set("Accept", "*/*")
	request.Header.Set("User-Agent", template.BrowserUserAgent)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-OpenAI-Target-Path", "/backend-api/transcribe")
	request.Header.Set("X-OpenAI-Target-Route", "/backend-api/transcribe")
	request.Header.Set("Cookie", template.CookieHeader)
	return request, nil
}

func doTranscriptionRequest(
	configured *http.Client,
	request *http.Request,
) (transcriptionHTTPResponse, *transcribeFailure) {
	client := configured
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(request)
	if err != nil {
		return transcriptionHTTPResponse{}, &transcribeFailure{
			code:      "chatgpt_transcription_http_unavailable",
			errClass:  "connection",
			message:   "ChatGPT transcription HTTP request was unavailable",
			retryable: true,
		}
	}
	body, bodyErr := readBoundedTranscriptionBody(response.Body)
	_ = response.Body.Close()
	if bodyErr != nil {
		return transcriptionHTTPResponse{statusCode: response.StatusCode}, &transcribeFailure{
			code:      "chatgpt_transcription_response_too_large",
			errClass:  "provider",
			message:   "ChatGPT transcription response exceeded its safety bound",
			retryable: false,
		}
	}
	return transcriptionHTTPResponse{
		statusCode: response.StatusCode,
		body:       body,
	}, nil
}

func readBoundedTranscriptionBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxTranscriptionBodyBytes+1))
	if len(body) > maxTranscriptionBodyBytes {
		return body, errors.New("response body exceeds bound")
	}
	return body, err
}

func parseTranscriptionBody(body []byte) (string, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err == nil {
		if text := findTranscriptionText(payload); strings.TrimSpace(text) != "" {
			return text, nil
		}
		return "", errors.New("transcription text not found")
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "", errors.New("transcription body is empty")
	}
	return text, nil
}

func findTranscriptionText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"text", "transcript", "transcription", "result", "data", "output"} {
			if found := findTranscriptionText(object[key]); found != "" {
				return found
			}
		}
	}
	if values, ok := value.([]any); ok {
		for _, item := range values {
			if found := findTranscriptionText(item); found != "" {
				return found
			}
		}
	}
	return ""
}

func transcriptionHTTPFailure(status int) transcribeFailure {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return transcribeFailure{
			code:      "chatgpt_transcription_auth_failed",
			errClass:  "auth",
			message:   "ChatGPT rejected the transcription auth evidence",
			status:    status,
			retryable: true,
			auth:      true,
		}
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return transcribeFailure{
			code:      "chatgpt_transcription_timeout",
			errClass:  "timeout",
			message:   "ChatGPT transcription timed out",
			status:    status,
			retryable: true,
		}
	case status == http.StatusTooManyRequests:
		return transcribeFailure{
			code:      "chatgpt_transcription_rate_limited",
			errClass:  "rate_limit",
			message:   "ChatGPT transcription was rate limited",
			status:    status,
			retryable: true,
		}
	case status >= 500 && status <= 599:
		return transcribeFailure{
			code:      "chatgpt_transcription_server",
			errClass:  "provider",
			message:   "ChatGPT transcription returned a server failure",
			status:    status,
			retryable: true,
		}
	default:
		return transcribeFailure{
			code:     "chatgpt_transcription_invalid_request",
			errClass: "provider",
			message:  "ChatGPT rejected the transcription request",
			status:   status,
		}
	}
}

func classifyTranscriptionFailure(err error) resilience.Decision {
	var failure *transcribeFailure
	if !errors.As(err, &failure) || failure == nil {
		return resilience.Decision{}
	}
	return resilience.Decision{
		Retry:       failure.retryable,
		RefreshAuth: failure.auth,
	}
}

func transcriptionFailureFromError(err error) transcribeFailure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return transcribeFailure{
			code:     "chatgpt_transcription_canceled",
			errClass: "timeout",
			message:  "ChatGPT transcription retry was canceled",
		}
	}
	var failure *transcribeFailure
	if errors.As(err, &failure) && failure != nil {
		return *failure
	}
	return transcribeFailure{
		code:      "chatgpt_transcription_http_unavailable",
		errClass:  "connection",
		message:   "ChatGPT transcription request was not completed",
		retryable: true,
	}
}

func nowForTranscription(config TranscribeConfig) time.Time {
	if config.Now != nil {
		return config.Now()
	}
	return time.Now()
}

func transcriptionFailureResult(
	runID string,
	config TranscribeConfig,
	data TranscriptionData,
	failure transcribeFailure,
) webagent.Result {
	if failure.status != 0 {
		data.StatusCode = failure.status
	}
	next := []string{
		"cdp workflow agent chatgpt doctor --json",
		"cdp workflow agent chatgpt auth refresh --json",
	}
	return operationFailure(
		runID,
		config.BuildCommit,
		webagent.OperationTranscribe,
		webagent.StageObserveTerminal,
		"direct_http",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		failure.code,
		failure.errClass,
		failure.message,
		data,
		next,
	)
}
