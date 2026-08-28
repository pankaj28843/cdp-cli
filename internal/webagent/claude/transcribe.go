package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	TranscriptionSchemaVersion = "claude-transcription/v1"
	claudeVoicePath            = "/api/ws/speech_to_text/voice_stream"
	maxTranscriptionAudioBytes = 50 << 20
	maxTranscriptionDurationMS = 10 * 60 * 1000
	maxTranscriptionPCMBytes   = 16_000 * 2 * 10 * 60
	claudeAudioChunkBytes      = 3_200
	claudeFinalTimeout         = 30 * time.Second
)

type SocketDialer func(context.Context, string, http.Header) (augloop.Socket, int, error)

type TranscribeConfig struct {
	Store       *Store
	BuildCommit string
	Dial        SocketDialer
	DecodePCM   func(context.Context, string) ([]byte, error)
	RefreshAuth func(context.Context, string) error
	Language    string
	Sleep       func(context.Context, time.Duration) error
}

type TranscriptionData struct {
	SchemaVersion string `json:"schema_version"`
	Transport     string `json:"transport"`
	EndpointPath  string `json:"endpoint_path"`
	FileName      string `json:"file_name"`
	AudioBytes    int64  `json:"audio_bytes"`
	PCMBytes      int64  `json:"pcm_bytes"`
	DurationMS    int64  `json:"duration_ms"`
	Frames        int    `json:"frames"`
	Attempts      int    `json:"attempts"`
	Transcript    string `json:"transcript,omitempty"`
}

type transcribeFailure struct {
	code      string
	errClass  string
	message   string
	auth      bool
	retrySafe bool
}

type voiceMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type voiceReadResult struct {
	transcript string
	failure    *transcribeFailure
}

func Transcribe(ctx context.Context, config TranscribeConfig, filePath string, durationMS int64) webagent.Result {
	runID := webagent.NewRunID()
	data := TranscriptionData{
		SchemaVersion: TranscriptionSchemaVersion,
		Transport:     "direct_websocket",
		EndpointPath:  claudeVoicePath,
		FileName:      filepath.Base(filePath),
		DurationMS:    durationMS,
		Attempts:      1,
	}
	if durationMS <= 0 || durationMS > maxTranscriptionDurationMS {
		return transcriptionFailureResult(runID, config.BuildCommit, data, transcribeFailure{
			code: "claude_transcription_duration_invalid", errClass: "usage",
			message: "Claude transcription duration must be between 1 ms and 10 minutes",
		})
	}
	info, failure := validateClaudeAudio(filePath)
	if failure != nil {
		return transcriptionFailureResult(runID, config.BuildCommit, data, *failure)
	}
	data.AudioBytes = info.Size()
	decode := config.DecodePCM
	if decode == nil {
		decode = decodeClaudePCM
	}
	pcm, err := decode(ctx, filePath)
	if err != nil || len(pcm) == 0 || len(pcm) > maxTranscriptionPCMBytes || len(pcm)%2 != 0 {
		return transcriptionFailureResult(runID, config.BuildCommit, data, transcribeFailure{
			code: "claude_audio_decode_failed", errClass: "usage",
			message: "Claude transcription could not decode the audio into 16 kHz mono PCM",
		})
	}
	data.PCMBytes = int64(len(pcm))

	transcript, frames, generation, failure := transcribeVoiceAttempt(ctx, config, pcm)
	if failure != nil && failure.auth && config.RefreshAuth != nil {
		if err := config.RefreshAuth(ctx, generation); err != nil {
			return transcriptionFailureResult(runID, config.BuildCommit, data, transcribeFailure{
				code: "claude_auth_refresh_failed", errClass: "auth",
				message: "Claude auth refresh could not complete",
			})
		}
		data.Attempts = 2
		transcript, frames, _, failure = transcribeVoiceAttempt(ctx, config, pcm)
	}
	data.Frames = frames
	if failure != nil {
		failure.retrySafe = false
		return transcriptionFailureResult(runID, config.BuildCommit, data, *failure)
	}
	data.Transcript = transcript
	return webagent.Result{
		OK: true, SchemaVersion: webagent.OperationSchemaVersion,
		Provider: webagent.ProviderClaude, Operation: webagent.OperationTranscribe,
		State: webagent.StateTerminal, Stage: webagent.StageObserveTerminal, Data: data,
		Evidence:     webagent.Evidence{RunID: runID, BuildCommit: normalizedBuildCommit(config.BuildCommit), BrowserMode: "none", ReadMode: "direct_websocket"},
		Cleanup:      webagent.CleanupEvidence{Required: false, State: webagent.CleanupNotRequired},
		NextCommands: []string{},
	}
}

func transcribeVoiceAttempt(ctx context.Context, config TranscribeConfig, pcm []byte) (string, int, string, *transcribeFailure) {
	status := config.Store.Status(ctx, time.Now(), DefaultAuthTTL)
	template, readErr := loadFreshReadTemplate(ctx, ReadConfig{Store: config.Store})
	if readErr != nil {
		return "", 0, status.CapturedAt, readFailureAsTranscription(*readErr, true)
	}
	transcript, frames, failure := transcribeVoice(ctx, config, template, pcm)
	return transcript, frames, template.CapturedAt, failure
}

func transcribeVoice(ctx context.Context, config TranscribeConfig, template AuthTemplate, pcm []byte) (string, int, *transcribeFailure) {
	dial := config.Dial
	if dial == nil {
		dial = augloop.DialWithHeaders
	}
	header := http.Header{
		"Origin":     []string{Origin},
		"User-Agent": []string{template.BrowserUserAgent},
		"Cookie":     []string{claudeCookieHeader(template.Cookies)},
	}
	socket, status, err := dial(ctx, claudeVoiceURL(config.Language, template.OrganizationID), header)
	if err != nil {
		auth := status == http.StatusUnauthorized || status == http.StatusForbidden
		failure := &transcribeFailure{
			code: "claude_voice_unavailable", errClass: "connection",
			message: "Claude dictation WebSocket was unavailable", auth: auth,
		}
		if auth {
			failure.code = "claude_auth_rejected"
			failure.errClass = "auth"
			failure.message = "Claude dictation requires refreshed browser auth state"
		}
		return "", 0, failure
	}
	defer socket.Close(augloop.StatusNormalClosure, "completed")
	finishedWriting := make(chan struct{})
	readResult := make(chan voiceReadResult, 1)
	go func() { readResult <- readClaudeVoice(ctx, socket, finishedWriting) }()

	sleep := config.Sleep
	if sleep == nil {
		sleep = sleepClaude
	}
	frames := 0
	for offset := 0; offset < len(pcm); {
		select {
		case result := <-readResult:
			return result.transcript, frames, result.failure
		default:
		}
		end := offset + claudeAudioChunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		chunkBytes := end - offset
		if err := socket.Write(ctx, augloop.MessageBinary, pcm[offset:end]); err != nil {
			return "", frames, voiceSocketFailure()
		}
		frames++
		offset = end
		if offset < len(pcm) {
			delay := time.Duration(float64(chunkBytes) / (16_000 * 2) * float64(time.Second))
			if err := sleep(ctx, delay); err != nil {
				return "", frames, voiceSocketFailure()
			}
		}
	}
	if err := socket.Write(ctx, augloop.MessageText, []byte(`{"type":"CloseStream"}`)); err != nil {
		return "", frames, voiceSocketFailure()
	}
	close(finishedWriting)

	timer := time.NewTimer(claudeFinalTimeout)
	defer timer.Stop()
	select {
	case result := <-readResult:
		return result.transcript, frames, result.failure
	case <-timer.C:
		return "", frames, &transcribeFailure{code: "claude_voice_timeout", errClass: "timeout", message: "Claude dictation did not return a final transcript"}
	case <-ctx.Done():
		return "", frames, voiceSocketFailure()
	}
}

func readClaudeVoice(ctx context.Context, socket augloop.Socket, finishedWriting <-chan struct{}) voiceReadResult {
	transcript := ""
	segments := []string{}
	endpointObserved := false
	for {
		messageType, payload, err := socket.Read(ctx)
		if err != nil {
			if ctx.Err() == nil && endpointObserved {
				joined := strings.TrimSpace(strings.Join(segments, " "))
				if joined != "" {
					return voiceReadResult{transcript: joined}
				}
			}
			return voiceReadResult{failure: voiceSocketFailure()}
		}
		if messageType != augloop.MessageText {
			continue
		}
		var message voiceMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			return voiceReadResult{failure: &transcribeFailure{code: "claude_voice_response_changed", errClass: "provider", message: "Claude dictation response shape changed"}}
		}
		switch message.Type {
		case "TranscriptText":
			if strings.TrimSpace(message.Data) != "" {
				transcript = strings.TrimSpace(message.Data)
			}
		case "TranscriptEndpoint":
			endpointObserved = true
			if transcript != "" {
				segments = append(segments, transcript)
				transcript = ""
			}
			select {
			case <-finishedWriting:
				joined := strings.TrimSpace(strings.Join(segments, " "))
				if joined == "" {
					return voiceReadResult{failure: &transcribeFailure{code: "claude_voice_no_match", errClass: "provider", message: "Claude dictation returned no usable transcript"}}
				}
				return voiceReadResult{transcript: joined}
			default:
			}
		case "TranscriptError":
			return voiceReadResult{failure: &transcribeFailure{code: "claude_voice_provider_error", errClass: "provider", message: "Claude dictation returned an error"}}
		}
	}
}

func claudeVoiceURL(language, organizationID string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		language = "en-US"
	}
	return "wss://claude.ai" + claudeVoicePath +
		"?encoding=linear16&sample_rate=16000&channels=1&endpointing_ms=300&utterance_end_ms=1000" +
		"&language=" + url.QueryEscape(language) +
		"&use_conversation_engine=true&stt_provider=deepgram-nova3&client_platform=web_claude_ai" +
		"&organization_uuid=" + url.QueryEscape(organizationID)
}

func validateClaudeAudio(filePath string) (os.FileInfo, *transcribeFailure) {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &transcribeFailure{code: "claude_audio_file_missing", errClass: "usage", message: "Claude transcription audio must be an existing regular file"}
	}
	if info.Size() <= 0 || info.Size() > maxTranscriptionAudioBytes {
		return nil, &transcribeFailure{code: "claude_audio_file_invalid", errClass: "usage", message: "Claude transcription audio must be between 1 byte and 50 MiB"}
	}
	return info, nil
}

func decodeClaudePCM(ctx context.Context, filePath string) ([]byte, error) {
	command := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-i", filePath, "-vn", "-f", "s16le", "-acodec", "pcm_s16le", "-ac", "1", "-ar", "16000", "pipe:1")
	output := &claudeBoundedBuffer{limit: maxTranscriptionPCMBytes}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("decode Claude audio")
	}
	return output.Bytes(), nil
}

type claudeBoundedBuffer struct {
	data  []byte
	limit int
}

func (b *claudeBoundedBuffer) Write(data []byte) (int, error) {
	if len(b.data)+len(data) > b.limit {
		return 0, fmt.Errorf("decoded Claude audio exceeds limit")
	}
	b.data = append(b.data, data...)
	return len(data), nil
}

func (b *claudeBoundedBuffer) Bytes() []byte { return b.data }

func claudeCookieHeader(cookies map[string]string) string {
	request, _ := http.NewRequest(http.MethodGet, Origin, nil)
	setClaudeCookieHeader(request, cookies)
	return request.Header.Get("Cookie")
}

func voiceSocketFailure() *transcribeFailure {
	return &transcribeFailure{code: "claude_voice_websocket_failed", errClass: "connection", message: "Claude dictation WebSocket communication failed"}
}

func sleepClaude(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readFailureAsTranscription(failure readFailure, allowRefresh bool) *transcribeFailure {
	return &transcribeFailure{
		code: failure.code, errClass: failure.errClass, message: failure.message,
		auth: allowRefresh && failure.errClass == "auth",
	}
}

func transcriptionFailureResult(runID, buildCommit string, data TranscriptionData, failure transcribeFailure) webagent.Result {
	return webagent.Result{
		OK: false, SchemaVersion: webagent.OperationSchemaVersion,
		Provider: webagent.ProviderClaude, Operation: webagent.OperationTranscribe,
		State: webagent.StateFailed, Stage: webagent.StageObserveTerminal,
		Error:        &webagent.OperationError{Code: failure.code, ErrClass: failure.errClass, Message: failure.message, RetrySafe: failure.retrySafe},
		Data:         data,
		Evidence:     webagent.Evidence{RunID: runID, BuildCommit: normalizedBuildCommit(buildCommit), BrowserMode: "none", ReadMode: "direct_websocket"},
		Cleanup:      webagent.CleanupEvidence{Required: false, State: webagent.CleanupNotRequired},
		NextCommands: []string{"cdp workflow agent claude auth refresh --json"},
	}
}
