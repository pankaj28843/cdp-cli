package bing

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
	"github.com/pankaj28843/cdp-cli/internal/resilience"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	TranscriptionSchemaVersion = "bing-transcription/v1"
	maxAudioBytes              = 50 << 20
	maxPCMBytes                = 16_000 * 2 * 10 * 60
	audioChunkBytes            = 3_200
	defaultFinalTimeout        = 30 * time.Second
)

type TranscribeConfig struct {
	BuildCommit string
	Dial        SocketDialer
	DecodePCM   func(context.Context, string) ([]byte, error)
	UserAgent   string
	Language    string
	MaxAttempts int
	Backoff     []time.Duration
	Sleep       func(context.Context, time.Duration) error
}

type TranscriptionData struct {
	SchemaVersion string `json:"schema_version"`
	Transport     string `json:"transport"`
	Endpoint      string `json:"endpoint"`
	FileName      string `json:"file_name"`
	AudioBytes    int64  `json:"audio_bytes"`
	PCMBytes      int64  `json:"pcm_bytes"`
	DurationMS    int64  `json:"duration_ms"`
	Frames        int    `json:"frames"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	Transcript    string `json:"transcript,omitempty"`
}

type transcribeFailure struct {
	code      string
	errClass  string
	message   string
	retryable bool
}

func (f *transcribeFailure) Error() string {
	if f == nil {
		return "Bing transcription failed"
	}
	return f.message
}

type transcriptionAttempt struct {
	transcript string
	frames     int
}

// Dial opens the public Bing Speech SDK socket. The endpoint is a public
// voice-search transport: the adapter sends no account cookies or bearer
// credentials. The origin still identifies the first-party Bing surface.
func Dial(ctx context.Context, rawURL, userAgent string) (Socket, error) {
	return augloop.DialWithOrigin(ctx, rawURL, userAgent, "https://www.bing.com")
}

func Transcribe(
	ctx context.Context,
	config TranscribeConfig,
	filePath string,
	durationMilliseconds int64,
) webagent.Result {
	runID := webagent.NewRunID()
	data := TranscriptionData{
		SchemaVersion: TranscriptionSchemaVersion,
		Transport:     "direct_websocket",
		Endpoint:      "sr.bing.com",
		FileName:      filepath.Base(filePath),
		DurationMS:    durationMilliseconds,
		Status:        "not_started",
	}
	if durationMilliseconds <= 0 || durationMilliseconds > 10*60*1000 {
		return transcriptionFailureResult(runID, config, data, transcribeFailure{
			code:     "bing_transcription_duration_invalid",
			errClass: "usage",
			message:  "Bing transcription duration must be between 1 ms and 10 minutes",
		})
	}
	audio, failure := readAudio(filePath)
	if failure != nil {
		return transcriptionFailureResult(runID, config, data, *failure)
	}
	data.AudioBytes = int64(len(audio))
	decode := config.DecodePCM
	if decode == nil {
		decode = decodeWebMToPCM
	}
	pcm, err := decode(ctx, filePath)
	if err != nil {
		return transcriptionFailureResult(runID, config, data, transcribeFailure{
			code:      "bing_audio_decode_failed",
			errClass:  "usage",
			message:   "Bing transcription could not decode the saved audio into 16 kHz PCM",
			retryable: false,
		})
	}
	if len(pcm) == 0 || len(pcm) > maxPCMBytes || len(pcm)%2 != 0 {
		return transcriptionFailureResult(runID, config, data, transcribeFailure{
			code:     "bing_audio_decode_invalid",
			errClass: "usage",
			message:  "Bing transcription decoded audio outside the supported duration bound",
		})
	}

	attempt, report, runErr := resilience.Run(
		ctx,
		resilience.Policy{MaxAttempts: config.MaxAttempts, Backoff: config.Backoff},
		resilience.Hooks[transcriptionAttempt]{
			Attempt: func(attemptContext context.Context, _ int) (transcriptionAttempt, error) {
				return runSession(attemptContext, pcm, config)
			},
			Classify: classifyTranscriptionFailure,
			Sleep:    config.Sleep,
		},
	)
	data.Attempts = report.Attempts
	data.Frames = attempt.frames
	if runErr == nil {
		data.Status = "completed"
		data.Transcript = attempt.transcript
		return operationSuccess(runID, config.BuildCommit, data)
	}
	return transcriptionFailureResult(runID, config, data, transcriptionFailureFromError(runErr))
}

func runSession(ctx context.Context, pcm []byte, config TranscribeConfig) (transcriptionAttempt, error) {
	requestID := newID()
	connectionID := newID()
	rawURL, err := speechURL(config.Language, requestID, connectionID)
	if err != nil {
		return transcriptionAttempt{}, &transcribeFailure{
			code:     "bing_speech_url_invalid",
			errClass: "internal",
			message:  "Bing speech recognition URL could not be constructed",
		}
	}
	dial := config.Dial
	if dial == nil {
		dial = Dial
	}
	socket, err := dial(ctx, rawURL, config.UserAgent)
	if err != nil {
		return transcriptionAttempt{}, &transcribeFailure{
			code:      "bing_speech_websocket_unavailable",
			errClass:  "connection",
			message:   "Bing speech recognition WebSocket was unavailable",
			retryable: true,
		}
	}
	defer socket.Close(augloop.StatusNormalClosure, "completed")

	messageTime := time.Now()
	configMessage, err := speechConfigMessage(requestID, messageTime, config.UserAgent)
	if err != nil {
		return transcriptionAttempt{}, &transcribeFailure{
			code:     "bing_speech_config_failed",
			errClass: "internal",
			message:  "Bing speech recognition configuration could not be encoded",
		}
	}
	if err := socket.Write(ctx, augloop.MessageText, configMessage); err != nil {
		return transcriptionAttempt{}, socketFailure()
	}
	if err := socket.Write(ctx, augloop.MessageText, speechContextMessage(requestID, time.Now())); err != nil {
		return transcriptionAttempt{}, socketFailure()
	}
	frames := 0
	if err := socket.Write(ctx, augloop.MessageBinary, audioFrame(requestID, time.Now(), nil, true)); err != nil {
		return transcriptionAttempt{frames: frames}, socketFailure()
	}
	frames++
	sleep := config.Sleep
	if sleep == nil {
		sleep = sleepWithContext
	}
	for offset := 0; offset < len(pcm); {
		end := offset + audioChunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		chunkBytes := end - offset
		if err := socket.Write(ctx, augloop.MessageBinary, audioFrame(requestID, time.Now(), pcm[offset:end], false)); err != nil {
			return transcriptionAttempt{frames: frames}, socketFailure()
		}
		frames++
		offset = end
		if offset < len(pcm) {
			chunkDuration := time.Duration(float64(chunkBytes) / (16_000.0 * 2.0) * float64(time.Second))
			if err := sleep(ctx, chunkDuration); err != nil {
				return transcriptionAttempt{frames: frames}, err
			}
		}
	}
	if err := socket.Write(ctx, augloop.MessageBinary, audioFrame(requestID, time.Now(), nil, false)); err != nil {
		return transcriptionAttempt{frames: frames}, socketFailure()
	}
	frames++

	readContext, cancel := context.WithTimeout(ctx, defaultFinalTimeout)
	defer cancel()
	for {
		messageType, payload, readErr := socket.Read(readContext)
		if readErr != nil {
			if errors.Is(readErr, context.DeadlineExceeded) {
				return transcriptionAttempt{frames: frames}, &transcribeFailure{
					code:      "bing_speech_final_timeout",
					errClass:  "timeout",
					message:   "Bing speech recognition did not return a final phrase",
					retryable: true,
				}
			}
			if errors.Is(readErr, context.Canceled) && ctx.Err() != nil {
				return transcriptionAttempt{frames: frames}, readErr
			}
			return transcriptionAttempt{frames: frames}, socketFailure()
		}
		if messageType != augloop.MessageText {
			continue
		}
		message, parseErr := parseSpeechMessage(payload)
		if parseErr != nil {
			return transcriptionAttempt{frames: frames}, &transcribeFailure{
				code:      "bing_speech_response_changed",
				errClass:  "provider",
				message:   "Bing speech recognition response shape changed",
				retryable: true,
			}
		}
		switch message.Path {
		case "speech.phrase":
			if message.RecognitionStatus == "Success" && strings.TrimSpace(message.DisplayText) != "" {
				return transcriptionAttempt{transcript: strings.TrimSpace(message.DisplayText), frames: frames}, nil
			}
			if message.RecognitionStatus != "" && message.RecognitionStatus != "Success" {
				return transcriptionAttempt{frames: frames}, &transcribeFailure{
					code:     "bing_speech_no_match",
					errClass: "provider",
					message:  "Bing speech recognition returned no usable phrase",
				}
			}
		case "speech.error":
			return transcriptionAttempt{frames: frames}, &transcribeFailure{
				code:      "bing_speech_provider_error",
				errClass:  "provider",
				message:   "Bing speech recognition returned an error",
				retryable: true,
			}
		case "turn.end":
			return transcriptionAttempt{frames: frames}, &transcribeFailure{
				code:     "bing_speech_no_match",
				errClass: "provider",
				message:  "Bing speech recognition ended without a usable phrase",
			}
		}
	}
}

func readAudio(filePath string) ([]byte, *transcribeFailure) {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &transcribeFailure{
			code:     "bing_audio_file_missing",
			errClass: "usage",
			message:  "Bing transcription audio must be an existing regular file",
		}
	}
	if info.Size() <= 0 || info.Size() > maxAudioBytes {
		return nil, &transcribeFailure{
			code:     "bing_audio_file_invalid",
			errClass: "usage",
			message:  "Bing transcription audio must be between 1 byte and 50 MiB",
		}
	}
	audio, err := os.ReadFile(filePath)
	if err != nil {
		return nil, &transcribeFailure{
			code:     "bing_audio_file_unreadable",
			errClass: "connection",
			message:  "Bing transcription audio could not be read",
		}
	}
	return audio, nil
}

func decodeWebMToPCM(ctx context.Context, filePath string) ([]byte, error) {
	command := exec.CommandContext(
		ctx,
		"ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", filePath,
		"-vn", "-f", "s16le", "-acodec", "pcm_s16le",
		"-ac", "1", "-ar", "16000", "pipe:1",
	)
	output := &boundedBuffer{limit: maxPCMBytes}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type boundedBuffer struct {
	bytes []byte
	limit int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if len(b.bytes)+len(data) > b.limit {
		return 0, errors.New("decoded PCM exceeds bound")
	}
	b.bytes = append(b.bytes, data...)
	return len(data), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.bytes }

func socketFailure() *transcribeFailure {
	return &transcribeFailure{
		code:      "bing_speech_websocket_failed",
		errClass:  "connection",
		message:   "Bing speech recognition WebSocket communication failed",
		retryable: true,
	}
}

func classifyTranscriptionFailure(err error) resilience.Decision {
	var failure *transcribeFailure
	if !errors.As(err, &failure) || failure == nil {
		return resilience.Decision{}
	}
	return resilience.Decision{Retry: failure.retryable}
}

func transcriptionFailureFromError(err error) transcribeFailure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return transcribeFailure{
			code:     "bing_transcription_canceled",
			errClass: "timeout",
			message:  "Bing transcription retry was canceled",
		}
	}
	var failure *transcribeFailure
	if errors.As(err, &failure) && failure != nil {
		return *failure
	}
	return transcribeFailure{
		code:      "bing_transcription_unavailable",
		errClass:  "connection",
		message:   "Bing transcription was not completed",
		retryable: true,
	}
}

func operationSuccess(runID, buildCommit string, data TranscriptionData) webagent.Result {
	return webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderBing,
		Operation:     webagent.OperationTranscribe,
		State:         webagent.StateReady,
		Stage:         webagent.StageObserveTerminal,
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "none",
			ReadMode:    "direct_websocket",
		},
		Cleanup: webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{
			"cdp transcription service status --json",
			"cdp transcription --help",
		},
	}
}

func transcriptionFailureResult(runID string, config TranscribeConfig, data TranscriptionData, failure transcribeFailure) webagent.Result {
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderBing,
		Operation:     webagent.OperationTranscribe,
		State:         webagent.StateFailed,
		Stage:         webagent.StageObserveTerminal,
		Error: &webagent.OperationError{
			Code:      failure.code,
			ErrClass:  failure.errClass,
			Message:   failure.message,
			RetrySafe: failure.retryable,
		},
		Data: data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(config.BuildCommit),
			BrowserMode: "none",
			ReadMode:    "direct_websocket",
		},
		Cleanup: webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{
			"cdp transcription service status --json",
			"cdp transcription --help",
		},
	}
}

func normalizedBuildCommit(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
