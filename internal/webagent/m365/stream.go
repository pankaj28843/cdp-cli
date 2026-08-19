package m365

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// StreamEvent is the line-oriented boundary used by VoxInput for a provider
// that exposes hypotheses while audio is still arriving. It intentionally
// carries only provider-neutral text and failure metadata; AugLoop wire data
// remains private to this package.
type StreamEvent struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	Code       string `json:"code,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
	Message    string `json:"message,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

type streamInput struct {
	Type       string `json:"type"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	PCMBase64  string `json:"pcm_base64,omitempty"`
}

// StreamTranscribe serves a bounded JSON-lines protocol over stdin/stdout.
// The first line must be {"type":"start"}; following audio lines contain
// base64 little-endian signed 16-bit mono PCM at 16 kHz; the final line is
// {"type":"end"}. Output is ready, partial, final, or error events.
func StreamTranscribe(
	ctx context.Context,
	config TranscribeConfig,
	input io.Reader,
	output io.Writer,
) error {
	inputs := make(chan streamInput, 16)
	readErrors := make(chan error, 1)
	go readStreamInputs(input, inputs, readErrors)

	first, ok, err := nextStreamInput(ctx, inputs, readErrors)
	if err != nil {
		return writeStreamFailure(output, &transcribeFailure{
			code:     "m365_stream_input_invalid",
			errClass: "usage",
			message:  "Microsoft 365 streaming input could not be read",
		})
	}
	if !ok || first.Type != "start" {
		return writeStreamFailure(output, &transcribeFailure{
			code:     "m365_stream_start_required",
			errClass: "usage",
			message:  "Microsoft 365 streaming input must begin with a start event",
		})
	}
	if first.SampleRate != 0 && first.SampleRate != pcmSampleRate {
		return writeStreamFailure(output, &transcribeFailure{
			code:     "m365_stream_sample_rate_invalid",
			errClass: "usage",
			message:  "Microsoft 365 streaming audio must use a 16 kHz sample rate",
		})
	}
	if first.DurationMS < 0 || first.DurationMS > maxTranscriptionDurationMS {
		return writeStreamFailure(output, &transcribeFailure{
			code:     "m365_stream_duration_invalid",
			errClass: "usage",
			message:  "Microsoft 365 streaming duration must be at most 10 minutes",
		})
	}
	if config.Store == nil {
		return writeStreamFailure(output, &transcribeFailure{
			code:     "m365_state_unavailable",
			errClass: "internal",
			message:  "Microsoft 365 owner-only auth state is unavailable",
		})
	}

	now := time.Now()
	if config.Now != nil {
		now = config.Now()
	}
	template, status, err := config.Store.LoadTemplateStatus(ctx, now, DefaultAuthTTL)
	if err != nil || !status.Ready {
		if config.RefreshAuth == nil {
			return writeStreamFailure(output, &transcribeFailure{
				code:      "m365_auth_not_ready",
				errClass:  "auth",
				message:   "Microsoft 365 auth evidence is not ready for streaming transcription",
				retryable: true,
				auth:      true,
			})
		}
		if refreshErr := config.RefreshAuth(ctx); refreshErr != nil {
			return writeStreamFailure(output, &transcribeFailure{
				code:      "m365_auth_refresh_failed",
				errClass:  "auth",
				message:   "Microsoft 365 auth refresh could not complete before streaming transcription",
				retryable: true,
				auth:      true,
			})
		}
		template, status, err = config.Store.LoadTemplateStatus(ctx, now, DefaultAuthTTL)
		if err != nil || !status.Ready {
			return writeStreamFailure(output, &transcribeFailure{
				code:      "m365_auth_refresh_missing",
				errClass:  "auth",
				message:   "Microsoft 365 auth refresh completed without usable streaming evidence",
				retryable: true,
				auth:      true,
			})
		}
	}

	session, failure := openLiveSession(ctx, template, config)
	if failure != nil {
		return writeStreamFailure(output, failure)
	}
	defer session.close()
	if err := writeStreamEvent(output, StreamEvent{
		Type:       "ready",
		SampleRate: pcmSampleRate,
		Protocol:   "AugLoop_Voice_VoiceTile/v2",
	}); err != nil {
		return err
	}

	events := session.events
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return writeStreamFailure(output, socketFailure(nil))
			}
			if event.failure != nil {
				return writeStreamFailure(output, event.failure)
			}
			if event.partial != "" {
				if err := writeStreamEvent(output, StreamEvent{Type: "partial", Text: event.partial}); err != nil {
					return err
				}
			}
			if event.final != "" {
				if err := writeStreamEvent(output, StreamEvent{Type: "final", Text: event.final}); err != nil {
					return err
				}
			}
		case item, ok := <-inputs:
			if !ok {
				return writeStreamFailure(output, &transcribeFailure{
					code:     "m365_stream_end_required",
					errClass: "usage",
					message:  "Microsoft 365 streaming input ended before an end event",
				})
			}
			switch item.Type {
			case "audio":
				if item.SampleRate != 0 && item.SampleRate != pcmSampleRate {
					return writeStreamFailure(output, &transcribeFailure{
						code:     "m365_stream_sample_rate_invalid",
						errClass: "usage",
						message:  "Microsoft 365 streaming audio must use a 16 kHz sample rate",
					})
				}
				pcm, decodeErr := base64.StdEncoding.DecodeString(item.PCMBase64)
				if decodeErr != nil || len(pcm) == 0 || len(pcm)%2 != 0 {
					return writeStreamFailure(output, &transcribeFailure{
						code:     "m365_stream_audio_invalid",
						errClass: "usage",
						message:  "Microsoft 365 streaming audio must be non-empty 16-bit PCM",
					})
				}
				if failure := session.appendPCM(ctx, pcm); failure != nil {
					return writeStreamFailure(output, failure)
				}
			case "end":
				transcript, finishFailure := session.finish(ctx)
				if finishFailure != nil {
					return writeStreamFailure(output, finishFailure)
				}
				if err := writeStreamEvent(output, StreamEvent{Type: "final", Text: transcript}); err != nil {
					return err
				}
				return nil
			default:
				return writeStreamFailure(output, &transcribeFailure{
					code:     "m365_stream_event_invalid",
					errClass: "usage",
					message:  "Microsoft 365 streaming input event type is not recognized",
				})
			}
		}
	}
}

func readStreamInputs(input io.Reader, inputs chan<- streamInput, readErrors chan<- error) {
	defer close(inputs)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4*1024), 1<<20)
	for scanner.Scan() {
		var item streamInput
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			readErrors <- err
			return
		}
		inputs <- item
	}
	if err := scanner.Err(); err != nil {
		readErrors <- err
	}
}

func nextStreamInput(
	ctx context.Context,
	inputs <-chan streamInput,
	readErrors <-chan error,
) (streamInput, bool, error) {
	select {
	case <-ctx.Done():
		return streamInput{}, false, ctx.Err()
	case err := <-readErrors:
		if err != nil {
			return streamInput{}, false, err
		}
		return streamInput{}, false, nil
	case item, ok := <-inputs:
		return item, ok, nil
	}
}

func writeStreamEvent(output io.Writer, event StreamEvent) error {
	return json.NewEncoder(output).Encode(event)
}

func writeStreamFailure(output io.Writer, failure *transcribeFailure) error {
	if failure == nil {
		failure = socketFailure(nil)
	}
	if err := writeStreamEvent(output, StreamEvent{
		Type:       "error",
		Code:       failure.code,
		ErrorClass: failure.errClass,
		Message:    failure.message,
	}); err != nil {
		return err
	}
	return fmt.Errorf("%s", strings.TrimSpace(failure.message))
}
