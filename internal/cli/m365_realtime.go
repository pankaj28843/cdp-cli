package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
	"github.com/pankaj28843/cdp-cli/internal/webagent/m365"
)

const (
	m365RealtimeSampleRate          = 16_000
	m365RealtimeWireChunk           = 64 * 1024
	m365RealtimeReadyWait           = 2 * time.Minute
	m365RealtimeHypothesisQuietTime = 25 * time.Millisecond
)

// m365RealtimeSession keeps the public WebSocket contract at OpenAI's 24 kHz
// PCM boundary while adapting the existing Microsoft 365 JSON-lines stream,
// which accepts 16 kHz PCM. The provider-specific pipe and resampler stay in
// this adapter; the transcription API only sees normalized events.
type m365RealtimeSession struct {
	input  *io.PipeWriter
	events <-chan m365.StreamEvent

	streamContext context.Context
	cancel        context.CancelFunc
	streamDone    chan struct{}
	streamErrMu   sync.Mutex
	streamErr     error

	writeMu   sync.Mutex
	stateMu   sync.Mutex
	latest    string
	sequence  int64
	resampler pcm24To16Resampler
	closeOnce sync.Once
}

func newM365RealtimeSession(ctx context.Context, provider *m365TranscriptionProvider, _ transcriptionapi.RealtimeSessionConfig) (transcriptionapi.RealtimeSession, error) {
	if provider == nil || provider.store == nil {
		return nil, transcriptionProviderError(503, "provider_unavailable", "m365_state_unavailable", "Microsoft 365 owner-only auth state is unavailable", false)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	streamContext, cancel := context.WithCancel(ctx)
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	events := make(chan m365.StreamEvent, 32)
	session := &m365RealtimeSession{
		input:         inputWriter,
		events:        events,
		streamContext: streamContext,
		cancel:        cancel,
		streamDone:    make(chan struct{}),
	}
	streamConfig := m365.TranscribeConfig{
		Store:       provider.store,
		BuildCommit: provider.app.build.Commit,
		RefreshAuth: provider.refreshAuth,
		Dial:        augloop.Dial,
		PaceTiles:   false,
	}
	go func() {
		err := m365.StreamTranscribe(streamContext, streamConfig, inputReader, outputWriter)
		session.streamErrMu.Lock()
		session.streamErr = err
		session.streamErrMu.Unlock()
		_ = inputReader.Close()
		_ = outputWriter.Close()
		close(session.streamDone)
	}()
	go session.readOutput(outputReader, events)

	if err := session.writeLine(map[string]any{
		"type":        "start",
		"sample_rate": m365RealtimeSampleRate,
	}); err != nil {
		_ = session.Close()
		return nil, transcriptionProviderError(503, "connection", "m365_stream_start_failed", "Microsoft 365 streaming transcription could not be started", true)
	}
	readyContext, readyCancel := context.WithTimeout(ctx, m365RealtimeReadyWait)
	defer readyCancel()
	for {
		event, err := session.nextEvent(readyContext)
		if err != nil {
			_ = session.Close()
			return nil, m365RealtimeStartError(err)
		}
		if event.Type == "ready" {
			return session, nil
		}
		if event.Type == "error" {
			_ = session.Close()
			return nil, m365StreamError(event)
		}
	}
}

func m365RealtimeStartError(err error) error {
	if err == nil {
		return nil
	}
	var providerErr *transcriptionapi.ProviderError
	if errors.As(err, &providerErr) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return transcriptionProviderError(504, "provider", "m365_stream_ready_timeout", "Microsoft 365 streaming transcription did not become ready", true)
	}
	return transcriptionProviderError(503, "connection", "m365_stream_ready_failed", "Microsoft 365 streaming transcription could not become ready", true)
}

func (s *m365RealtimeSession) readOutput(reader *io.PipeReader, events chan<- m365.StreamEvent) {
	defer close(events)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4*1024), 1<<20)
	for scanner.Scan() {
		var event m365.StreamEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		select {
		case events <- event:
		case <-s.cancelled():
			return
		}
	}
}

func (s *m365RealtimeSession) cancelled() <-chan struct{} {
	return s.streamContext.Done()
}

func (s *m365RealtimeSession) writeLine(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.input.Write(append(payload, '\n'))
	return err
}

func (s *m365RealtimeSession) nextEvent(ctx context.Context) (m365.StreamEvent, error) {
	select {
	case <-ctx.Done():
		return m365.StreamEvent{}, ctx.Err()
	case event, ok := <-s.events:
		if !ok {
			if err := s.getStreamError(); err != nil {
				return m365.StreamEvent{}, err
			}
			return m365.StreamEvent{}, errors.New("Microsoft 365 streaming transcription ended unexpectedly")
		}
		return event, nil
	}
}

func (s *m365RealtimeSession) getStreamError() error {
	s.streamErrMu.Lock()
	defer s.streamErrMu.Unlock()
	return s.streamErr
}

func (s *m365RealtimeSession) Append(ctx context.Context, audio []byte) ([]transcriptionapi.ProviderEvent, error) {
	if len(audio) == 0 || len(audio)%2 != 0 {
		return nil, transcriptionProviderError(422, "usage", "pcm_invalid", "realtime PCM must be non-empty 16-bit audio", false)
	}
	pcm, err := s.resampler.convert(audio, false)
	if err != nil {
		return nil, err
	}
	if err := s.writeAudio(ctx, pcm); err != nil {
		return nil, err
	}
	// Keep append handling below the iOS capture cadence. The final event on
	// Commit remains authoritative; this quiet window only bounds partial-event
	// draining so small chunks cannot queue behind a 100 ms sleep each.
	return s.collectHypotheses(ctx, m365RealtimeHypothesisQuietTime)
}

func (s *m365RealtimeSession) writeAudio(ctx context.Context, pcm []byte) error {
	for start := 0; start < len(pcm); {
		end := start + m365RealtimeWireChunk
		if end > len(pcm) {
			end = len(pcm)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := s.writeLine(map[string]any{
			"type":        "audio",
			"sample_rate": m365RealtimeSampleRate,
			"pcm_base64":  base64.StdEncoding.EncodeToString(pcm[start:end]),
		}); err != nil {
			return transcriptionProviderError(503, "connection", "m365_stream_audio_failed", "Microsoft 365 streaming audio could not be sent", true)
		}
		start = end
	}
	return nil
}

func (s *m365RealtimeSession) collectHypotheses(ctx context.Context, quiet time.Duration) ([]transcriptionapi.ProviderEvent, error) {
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	result := make([]transcriptionapi.ProviderEvent, 0, 2)
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-timer.C:
			return result, nil
		case event, ok := <-s.events:
			if !ok {
				if err := s.getStreamError(); err != nil {
					return result, err
				}
				return result, errors.New("Microsoft 365 streaming transcription ended unexpectedly")
			}
			switch event.Type {
			case "partial", "final":
				if event.Text == "" {
					continue
				}
				s.stateMu.Lock()
				s.latest = event.Text
				s.sequence++
				sequence := s.sequence
				s.stateMu.Unlock()
				result = append(result, transcriptionapi.ProviderEvent{Kind: transcriptionapi.EventHypothesis, Text: event.Text, Replace: true, Sequence: sequence})
				timer.Reset(quiet)
			case "error":
				return result, m365StreamError(event)
			}
		}
	}
}

func (s *m365RealtimeSession) Commit(ctx context.Context) ([]transcriptionapi.ProviderEvent, error) {
	pcm, err := s.resampler.convert(nil, true)
	if err != nil {
		return nil, err
	}
	if err := s.writeAudio(ctx, pcm); err != nil {
		return nil, err
	}
	if err := s.writeLine(map[string]any{"type": "end"}); err != nil {
		return nil, transcriptionProviderError(503, "connection", "m365_stream_end_failed", "Microsoft 365 streaming transcription could not be ended", true)
	}
	for {
		event, err := s.nextEvent(ctx)
		if err != nil {
			return nil, err
		}
		switch event.Type {
		case "partial", "final":
			if event.Text != "" {
				s.stateMu.Lock()
				s.latest = event.Text
				s.stateMu.Unlock()
			}
			if event.Type == "final" && event.Terminal {
				s.stateMu.Lock()
				latest := s.latest
				s.sequence++
				sequence := s.sequence
				s.stateMu.Unlock()
				return []transcriptionapi.ProviderEvent{{Kind: transcriptionapi.EventFinal, Text: latest, Sequence: sequence}}, nil
			}
		case "error":
			return nil, m365StreamError(event)
		}
		select {
		case <-s.streamDone:
			s.stateMu.Lock()
			latest := s.latest
			s.sequence++
			sequence := s.sequence
			s.stateMu.Unlock()
			return []transcriptionapi.ProviderEvent{{Kind: transcriptionapi.EventFinal, Text: latest, Sequence: sequence}}, nil
		default:
		}
	}
}

func (s *m365RealtimeSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.input.Close()
		select {
		case <-s.streamDone:
		case <-time.After(time.Second):
		}
	})
	return nil
}

func m365StreamError(event m365.StreamEvent) error {
	status := 502
	retryable := true
	switch event.ErrorClass {
	case "auth":
		status = 401
	case "usage":
		status = 422
		retryable = false
	case "timeout":
		status = 504
	case "connection":
		status = 503
	}
	message := event.Message
	if message == "" {
		message = "Microsoft 365 streaming transcription failed"
	}
	return transcriptionProviderError(status, "provider", event.Code, message, retryable)
}

type pcm24To16Resampler struct {
	pending []int16
}

func (r *pcm24To16Resampler) convert(pcm []byte, flush bool) ([]byte, error) {
	if len(pcm)%2 != 0 {
		return nil, transcriptionProviderError(422, "usage", "pcm_invalid", "realtime PCM must contain complete 16-bit samples", false)
	}
	for index := 0; index < len(pcm); index += 2 {
		r.pending = append(r.pending, int16(binary.LittleEndian.Uint16(pcm[index:index+2])))
	}
	output := make([]int16, 0, len(r.pending)*2/3+1)
	for len(r.pending) >= 3 {
		output = append(output, r.pending[0], int16((int32(r.pending[1])+int32(r.pending[2]))/2))
		r.pending = r.pending[3:]
	}
	if flush {
		switch len(r.pending) {
		case 1:
			output = append(output, r.pending[0])
		case 2:
			output = append(output, int16((int32(r.pending[0])+int32(r.pending[1]))/2))
		}
		r.pending = nil
	}
	result := make([]byte, len(output)*2)
	for index, sample := range output {
		binary.LittleEndian.PutUint16(result[index*2:index*2+2], uint16(sample))
	}
	return result, nil
}
