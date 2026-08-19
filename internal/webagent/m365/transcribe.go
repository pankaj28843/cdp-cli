package m365

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
	"github.com/pankaj28843/cdp-cli/internal/resilience"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	maxTranscriptionAudioBytes = 50 << 20
	maxTranscriptionDurationMS = 10 * 60 * 1000
	maxPCMBytes                = pcmSampleRate * 2 * 10 * 60
	defaultSessionTimeout      = 90 * time.Second
)

type TranscribeConfig struct {
	Store       *Store
	BuildCommit string
	RefreshAuth func(context.Context) error
	MaxAttempts int
	Backoff     []time.Duration
	Now         func() time.Time
	DecodePCM   func(context.Context, string) ([]byte, error)
	Dial        socketDialer
}

type TranscriptionData struct {
	SchemaVersion string `json:"schema_version"`
	Transport     string `json:"transport"`
	Endpoint      string `json:"endpoint"`
	FileName      string `json:"file_name"`
	AudioBytes    int64  `json:"audio_bytes"`
	PCMBytes      int64  `json:"pcm_bytes"`
	DurationMS    int64  `json:"duration_ms"`
	Tiles         int    `json:"tiles"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	Transcript    string `json:"transcript,omitempty"`
}

type transcribeFailure struct {
	code      string
	errClass  string
	message   string
	retryable bool
	auth      bool
}

func (f *transcribeFailure) Error() string {
	if f == nil {
		return "Microsoft 365 transcription failed"
	}
	return f.message
}

type transcriptionAttempt struct {
	transcript string
	tiles      int
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
		Endpoint:      "augloop.svc.cloud.microsoft",
		FileName:      filepath.Base(filePath),
		DurationMS:    durationMilliseconds,
		Status:        "not_started",
	}
	if config.Store == nil {
		return transcriptionFailureResult(runID, config, data, transcribeFailure{
			code:     "m365_state_unavailable",
			errClass: "internal",
			message:  "Microsoft 365 owner-only auth state is unavailable",
		})
	}
	if durationMilliseconds <= 0 || durationMilliseconds > maxTranscriptionDurationMS {
		return transcriptionFailureResult(runID, config, data, transcribeFailure{
			code:     "m365_transcription_duration_invalid",
			errClass: "usage",
			message:  "Microsoft 365 transcription duration must be between 1 ms and 10 minutes",
		})
	}
	audioBytes, err := readAudio(filePath)
	if err != nil {
		return transcriptionFailureResult(runID, config, data, *err)
	}
	data.AudioBytes = int64(len(audioBytes))
	decode := config.DecodePCM
	if decode == nil {
		decode = decodeWebMToPCM
	}
	pcm, decodeErr := decode(ctx, filePath)
	if decodeErr != nil {
		return transcriptionFailureResult(runID, config, data, transcribeFailure{
			code:      "m365_audio_decode_failed",
			errClass:  "usage",
			message:   "Microsoft 365 transcription could not decode the saved WebM into 16 kHz PCM",
			retryable: false,
		})
	}
	if len(pcm) == 0 || len(pcm) > maxPCMBytes {
		return transcriptionFailureResult(runID, config, data, transcribeFailure{
			code:     "m365_audio_decode_invalid",
			errClass: "usage",
			message:  "Microsoft 365 transcription decoded audio outside the supported duration bound",
		})
	}
	data.PCMBytes = int64(len(pcm))

	attempt, report, runErr := resilience.Run(
		ctx,
		resilience.Policy{MaxAttempts: config.MaxAttempts, Backoff: config.Backoff},
		resilience.Hooks[transcriptionAttempt]{
			Attempt: func(attemptContext context.Context, _ int) (transcriptionAttempt, error) {
				template, status, templateErr := config.Store.LoadTemplateStatus(
					attemptContext,
					nowForTranscription(config),
					DefaultAuthTTL,
				)
				if templateErr != nil || !status.Ready {
					return transcriptionAttempt{}, &transcribeFailure{
						code:      "m365_auth_not_ready",
						errClass:  "auth",
						message:   "Microsoft 365 auth evidence is not ready for transcription",
						retryable: true,
						auth:      true,
					}
				}
				transcript, tiles, failure := runSession(
					attemptContext,
					template,
					pcm,
					config,
				)
				if failure != nil {
					data.Tiles = tiles
					return transcriptionAttempt{tiles: tiles}, failure
				}
				return transcriptionAttempt{transcript: transcript, tiles: tiles}, nil
			},
			Classify:    classifyTranscriptionFailure,
			RefreshAuth: config.RefreshAuth,
			OnRefreshFailure: func(error) error {
				return &transcribeFailure{
					code:     "m365_auth_refresh_failed",
					errClass: "auth",
					message:  "Microsoft 365 auth refresh could not complete",
				}
			},
		},
	)
	data.Attempts = report.Attempts
	data.Tiles = attempt.tiles
	if runErr == nil {
		data.Status = "completed"
		data.Transcript = attempt.transcript
		return operationSuccess(
			runID,
			config.BuildCommit,
			webagent.OperationTranscribe,
			webagent.StageObserveTerminal,
			"direct_websocket",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
			data,
			[]string{
				"cdp workflow agent m365 doctor --json",
				"cdp workflow agent m365 capabilities --json",
			},
		)
	}
	return transcriptionFailureResult(runID, config, data, transcriptionFailureFromError(runErr))
}

func runSession(
	ctx context.Context,
	template AuthTemplate,
	pcm []byte,
	config TranscribeConfig,
) (string, int, *transcribeFailure) {
	session, failure := openLiveSession(ctx, template, config)
	if failure != nil {
		return "", 0, failure
	}
	defer session.close()
	if failure := session.appendPCM(ctx, pcm); failure != nil {
		return "", session.tiles, failure
	}
	transcript, failure := session.finish(ctx)
	return transcript, session.tiles, failure
}

type liveEvent struct {
	partial string
	final   string
	failure *transcribeFailure
}

type liveSession struct {
	socket            socket
	correlationVector string
	nextSequence      int
	tiles             int
	pendingPCM        []byte
	writeMu           sync.Mutex
	events            chan liveEvent
	readerDone        chan struct{}
	readerContext     context.Context
	readerCancel      context.CancelFunc
	closeOnce         sync.Once
}

func openLiveSession(
	ctx context.Context,
	template AuthTemplate,
	config TranscribeConfig,
) (*liveSession, *transcribeFailure) {
	dial := config.Dial
	if dial == nil {
		return nil, &transcribeFailure{
			code:      "m365_websocket_transport_unavailable",
			errClass:  "internal",
			message:   "Microsoft 365 AugLoop transport was not supplied by the command boundary",
			retryable: false,
		}
	}
	userAgent := template.BrowserUserAgent
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "Mozilla/5.0"
	}
	socket, err := dial(ctx, AugLoopURL, userAgent)
	if err != nil {
		return nil, &transcribeFailure{
			code:      "m365_websocket_unavailable",
			errClass:  "connection",
			message:   "Microsoft 365 AugLoop WebSocket was unavailable",
			retryable: true,
		}
	}
	session := &liveSession{socket: socket}
	writeText := func(payload []byte) error {
		return session.write(ctx, augloop.MessageText, payload)
	}
	if err := writeText([]byte("~")); err != nil {
		session.close()
		return nil, socketFailure(err)
	}
	initMessage, correlationVector, err := sessionInitMessage(template.ClientMetadata)
	if err != nil || writeText(initMessage) != nil {
		session.close()
		return nil, &transcribeFailure{
			code:      "m365_session_init_failed",
			errClass:  "provider",
			message:   "Microsoft 365 AugLoop session initialization changed or failed",
			retryable: true,
		}
	}
	if _, failure := waitForSessionInit(ctx, socket); failure != nil {
		session.close()
		return nil, failure
	}
	for index, annotationType := range activatedAnnotationTypes {
		message, messageErr := annotationActivationMessage(annotationType, correlationVector, index+1)
		if messageErr != nil || writeText(message) != nil {
			session.close()
			return nil, &transcribeFailure{
				code:      "m365_annotation_activation_failed",
				errClass:  "provider",
				message:   "Microsoft 365 voice result subscriptions could not be activated",
				retryable: true,
			}
		}
	}
	sync, syncErr := syncMessage(correlationVector)
	if syncErr != nil || writeText(sync) != nil {
		session.close()
		return nil, &transcribeFailure{
			code:      "m365_sync_failed",
			errClass:  "provider",
			message:   "Microsoft 365 voice session synchronization failed",
			retryable: true,
		}
	}
	token, tokenErr := tokenProvisionMessage(template.AuthToken, correlationVector)
	if tokenErr != nil || writeText(token) != nil {
		session.close()
		return nil, &transcribeFailure{
			code:      "m365_token_provision_failed",
			errClass:  "auth",
			message:   "Microsoft 365 rejected the owner-only auth token provision",
			retryable: true,
			auth:      true,
		}
	}
	warmup, warmupErr := voiceTileMessage(correlationVector, 0, nil, false, true)
	if warmupErr != nil || session.write(ctx, augloop.MessageBinary, warmup) != nil {
		session.close()
		return nil, &transcribeFailure{
			code:      "m365_voice_warmup_failed",
			errClass:  "provider",
			message:   "Microsoft 365 voice transcription warm-up failed",
			retryable: true,
		}
	}
	if failure := waitForSpeechStart(ctx, socket); failure != nil {
		session.close()
		return nil, failure
	}
	session.correlationVector = correlationVector
	session.nextSequence = 1
	session.startReader(ctx)
	return session, nil
}

func (s *liveSession) write(ctx context.Context, messageType augloop.MessageType, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.socket.Write(ctx, messageType, payload)
}

func (s *liveSession) Write(ctx context.Context, messageType augloop.MessageType, payload []byte) error {
	return s.write(ctx, messageType, payload)
}

func (s *liveSession) Read(ctx context.Context) (augloop.MessageType, []byte, error) {
	return s.socket.Read(ctx)
}

func (s *liveSession) Close(code augloop.StatusCode, reason string) error {
	return s.socket.Close(code, reason)
}

func (s *liveSession) startReader(ctx context.Context) {
	s.readerContext, s.readerCancel = context.WithCancel(ctx)
	s.events = make(chan liveEvent, 32)
	s.readerDone = make(chan struct{})
	go func() {
		defer close(s.readerDone)
		defer close(s.events)
		for {
			messageType, payload, err := s.socket.Read(s.readerContext)
			if err != nil {
				if s.readerContext.Err() == nil {
					s.emit(liveEvent{failure: socketFailure(err)})
				}
				return
			}
			if messageType != augloop.MessageText {
				continue
			}
			_, failure, handled := handleAnnotation(payload, s, s.readerContext)
			if failure != nil {
				s.emit(liveEvent{failure: failure})
				return
			}
			if !handled {
				continue
			}
			message, parseErr := parseAnnotationMessage(payload)
			if parseErr != nil {
				continue
			}
			text := annotationText(message)
			switch message.AnnotationType {
			case "AugLoop_Voice_SpeechToTextPartialResult":
				if text != "" {
					s.emit(liveEvent{partial: text})
				}
			case "AugLoop_Voice_SpeechToTextFinalResult":
				if text != "" {
					s.emit(liveEvent{final: text})
				}
			}
		}
	}()
}

func (s *liveSession) emit(event liveEvent) {
	select {
	case s.events <- event:
	case <-s.readerContext.Done():
	}
}

func (s *liveSession) appendPCM(ctx context.Context, pcm []byte) *transcribeFailure {
	if len(pcm) == 0 {
		return nil
	}
	s.pendingPCM = append(s.pendingPCM, pcm...)
	for len(s.pendingPCM) >= pcmBytesPerTile {
		if failure := s.sendTile(ctx, s.pendingPCM[:pcmBytesPerTile], false); failure != nil {
			return failure
		}
		s.pendingPCM = s.pendingPCM[pcmBytesPerTile:]
	}
	return nil
}

func (s *liveSession) sendTile(ctx context.Context, pcm []byte, end bool) *transcribeFailure {
	tile, err := voiceTileMessage(s.correlationVector, s.nextSequence, pcm, end, false)
	if err != nil || s.write(ctx, augloop.MessageBinary, tile) != nil {
		return &transcribeFailure{
			code:      "m365_audio_send_failed",
			errClass:  "connection",
			message:   "Microsoft 365 audio tiles could not be sent",
			retryable: true,
		}
	}
	if !end {
		s.tiles++
	}
	s.nextSequence++
	return nil
}

func (s *liveSession) finish(ctx context.Context) (string, *transcribeFailure) {
	if len(s.pendingPCM) > 0 {
		if failure := s.sendTile(ctx, s.pendingPCM, false); failure != nil {
			return "", failure
		}
		s.pendingPCM = nil
	}
	if failure := s.sendTile(ctx, nil, true); failure != nil {
		failure.code = "m365_audio_end_failed"
		failure.message = "Microsoft 365 voice session could not be ended safely"
		return "", failure
	}
	deadline, cancel := context.WithTimeout(ctx, defaultSessionTimeout)
	defer cancel()
	for {
		select {
		case <-deadline.Done():
			return "", &transcribeFailure{
				code:      "m365_final_result_timeout",
				errClass:  "timeout",
				message:   "Microsoft 365 did not return a final dictation result",
				retryable: true,
			}
		case event, ok := <-s.events:
			if !ok {
				return "", socketFailure(nil)
			}
			if event.failure != nil {
				return "", event.failure
			}
			if event.final != "" {
				return event.final, nil
			}
		}
	}
}

func (s *liveSession) close() {
	s.closeOnce.Do(func() {
		if s.readerCancel != nil {
			s.readerCancel()
		}
		if s.socket != nil {
			_ = s.socket.Close(augloop.StatusNormalClosure, "done")
		}
		if s.readerDone != nil {
			select {
			case <-s.readerDone:
			case <-time.After(time.Second):
			}
		}
	})
}

func annotationText(message annotationMessage) string {
	for _, operation := range message.Ops {
		for _, item := range operation.Items {
			if text := findString(item.Body, "text", "transcript", "transcription"); strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func waitForSessionInit(ctx context.Context, socket socket) (*sessionInitResponse, *transcribeFailure) {
	deadline, cancel := context.WithTimeout(ctx, defaultSessionTimeout)
	defer cancel()
	for {
		messageType, payload, err := socket.Read(deadline)
		if err != nil {
			return nil, socketFailure(err)
		}
		if messageType != augloop.MessageText {
			continue
		}
		var envelope sessionInitResponse
		if json.Unmarshal(payload, &envelope) != nil {
			continue
		}
		if envelope.H.Type != "AugLoop_Session_Protocol_SessionInitResponse" {
			continue
		}
		if envelope.SessionKey == "" || envelope.Origin == "" || envelope.AnonymousToken == "" {
			return nil, &transcribeFailure{
				code:     "m365_session_response_changed",
				errClass: "provider",
				message:  "Microsoft 365 returned an unrecognized AugLoop session response",
			}
		}
		return &envelope, nil
	}
}

func waitForSpeechStart(ctx context.Context, socket socket) *transcribeFailure {
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		messageType, payload, err := socket.Read(deadline)
		if err != nil {
			return socketFailure(err)
		}
		if messageType != augloop.MessageText {
			continue
		}
		started, failure, handled := handleAnnotation(payload, socket, deadline)
		if failure != nil {
			return failure
		}
		if handled && started {
			return nil
		}
	}
}

func waitForFinalResult(ctx context.Context, socket socket) (string, *transcribeFailure) {
	deadline, cancel := context.WithTimeout(ctx, defaultSessionTimeout)
	defer cancel()
	for {
		messageType, payload, err := socket.Read(deadline)
		if err != nil {
			return "", socketFailure(err)
		}
		if messageType != augloop.MessageText {
			continue
		}
		_, failure, handled := handleAnnotation(payload, socket, deadline)
		if failure != nil {
			return "", failure
		}
		if !handled {
			continue
		}
		message, parseErr := parseAnnotationMessage(payload)
		if parseErr != nil || message.AnnotationType != "AugLoop_Voice_SpeechToTextFinalResult" {
			continue
		}
		for _, op := range message.Ops {
			for _, item := range op.Items {
				text := findString(item.Body, "text", "transcript", "transcription")
				if strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text), nil
				}
			}
		}
	}
}

func handleAnnotation(
	payload []byte,
	socket socket,
	ctx context.Context,
) (bool, *transcribeFailure, bool) {
	message, err := parseAnnotationMessage(payload)
	if err != nil || message.H.Type != "AugLoop_Session_Protocol_AnnotationResultsMessage" {
		return false, nil, false
	}
	if message.MessageID != "" {
		ack, ackErr := responseAck(message.MessageID)
		if ackErr == nil {
			_ = socket.Write(ctx, augloop.MessageText, ack)
		}
	}
	if message.AnnotationType == "AugLoop_Voice_SpeechErrorEvent" {
		return false, &transcribeFailure{
			code:      "m365_speech_error",
			errClass:  "provider",
			message:   "Microsoft 365 speech recognition returned an error",
			retryable: true,
		}, true
	}
	started := false
	if message.AnnotationType == "AugLoop_Voice_SpeechSessionEvent" {
		for _, op := range message.Ops {
			for _, item := range op.Items {
				if findString(item.Body, "eventId") == "SpeechRecognitionStarted" {
					started = true
				}
			}
		}
	}
	return started, nil, true
}

func socketFailure(err error) *transcribeFailure {
	if err == nil {
		return &transcribeFailure{
			code:      "m365_websocket_unavailable",
			errClass:  "connection",
			message:   "Microsoft 365 AugLoop WebSocket was unavailable",
			retryable: true,
		}
	}
	return &transcribeFailure{
		code:      "m365_websocket_failed",
		errClass:  "connection",
		message:   "Microsoft 365 AugLoop WebSocket communication failed",
		retryable: true,
	}
}

func readAudio(filePath string) ([]byte, *transcribeFailure) {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &transcribeFailure{
			code:     "m365_audio_file_missing",
			errClass: "usage",
			message:  "Microsoft 365 transcription audio must be an existing regular file",
		}
	}
	if info.Size() <= 0 || info.Size() > maxTranscriptionAudioBytes {
		return nil, &transcribeFailure{
			code:     "m365_audio_file_invalid",
			errClass: "usage",
			message:  "Microsoft 365 transcription audio must be between 1 byte and 50 MiB",
		}
	}
	audio, err := os.ReadFile(filePath)
	if err != nil {
		return nil, &transcribeFailure{
			code:     "m365_audio_file_unreadable",
			errClass: "connection",
			message:  "Microsoft 365 transcription audio could not be read",
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
		"-ac", "1", "-ar", fmt.Sprintf("%d", pcmSampleRate), "pipe:1",
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
			code:     "m365_transcription_canceled",
			errClass: "timeout",
			message:  "Microsoft 365 transcription retry was canceled",
		}
	}
	var failure *transcribeFailure
	if errors.As(err, &failure) && failure != nil {
		return *failure
	}
	return transcribeFailure{
		code:      "m365_transcription_unavailable",
		errClass:  "connection",
		message:   "Microsoft 365 transcription was not completed",
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
	next := []string{
		"cdp workflow agent m365 doctor --json",
		"cdp workflow agent m365 auth refresh --json",
	}
	return operationFailure(
		runID,
		config.BuildCommit,
		webagent.OperationTranscribe,
		webagent.StageObserveTerminal,
		"direct_websocket",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		failure.code,
		failure.errClass,
		failure.message,
		data,
		next,
	)
}
