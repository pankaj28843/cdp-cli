package cli

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
)

const (
	transcriptionProbeRealtimeChunkBytes = 8192
	transcriptionProbeRealtimeSampleRate = 24_000
	transcriptionProbeMaxPCMBytes        = 16 * 1024 * 1024
)

// ProbeRealtime exercises the same paced PCM/realtime boundary used by the
// service, while keeping fixture decoding and provider wire details in the
// Microsoft 365 adapter. The coordinator owns cadence and redacted state.
func (p *m365TranscriptionProvider) ProbeRealtime(ctx context.Context, fixture transcriptionapi.ProbeFixture) error {
	pcm, err := decodeTranscriptionProbePCM(ctx, fixture.Path)
	if err != nil {
		return err
	}
	session, err := p.NewRealtime(ctx, transcriptionapi.RealtimeSessionConfig{
		Type:           "transcription",
		Model:          transcriptionapi.DefaultModel,
		SyntheticProbe: true,
		InputFormat:    transcriptionapi.RealtimeAudioFormat{Type: "audio/pcm", Rate: transcriptionProbeRealtimeSampleRate},
	})
	if err != nil {
		return err
	}
	defer session.Close()

	for start := 0; start < len(pcm); start += transcriptionProbeRealtimeChunkBytes {
		end := start + transcriptionProbeRealtimeChunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		if _, err := session.Append(ctx, pcm[start:end]); err != nil {
			return err
		}
		if end < len(pcm) {
			samples := int64((end - start) / 2)
			if err := waitForTranscriptionProbeAudio(ctx, time.Duration(samples)*time.Second/transcriptionProbeRealtimeSampleRate); err != nil {
				return err
			}
		}
	}
	events, err := session.Commit(ctx)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Kind == transcriptionapi.EventFinal && strings.TrimSpace(event.Text) != "" {
			return nil
		}
	}
	return transcriptionProviderError(502, "provider", "realtime_probe_empty_transcript", "Microsoft 365 realtime probe returned no final transcript", true)
}

func decodeTranscriptionProbePCM(ctx context.Context, path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, transcriptionProviderError(422, "usage", "realtime_probe_fixture_missing", "transcription realtime probe fixture path is empty", false)
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, transcriptionProviderError(503, "probe", "realtime_probe_decoder_unavailable", "ffmpeg is required for the Microsoft 365 realtime probe", false)
	}
	output, truncated, err := runBoundedTranscriptionProbeCommand(ctx, ffmpeg, []string{
		"-hide_banner",
		"-loglevel", "error",
		"-i", path,
		"-vn",
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ac", "1",
		"-ar", "24000",
		"pipe:1",
	}, transcriptionProbeMaxPCMBytes)
	if truncated {
		return nil, transcriptionProviderError(502, "probe", "realtime_probe_output_too_large", "the realtime probe decoder output exceeded its safety limit", false)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, transcriptionProviderError(502, "probe", "realtime_probe_decode_failed", "the realtime probe fixture could not be decoded", true)
	}
	if len(output) == 0 || len(output)%2 != 0 {
		return nil, transcriptionProviderError(502, "probe", "realtime_probe_decode_empty", "the realtime probe fixture decoded to invalid PCM", true)
	}
	return output, nil
}

func runBoundedTranscriptionProbeCommand(ctx context.Context, bin string, args []string, maxStdoutBytes int) ([]byte, bool, error) {
	processCtx, cancelProcess := context.WithCancel(ctx)
	defer cancelProcess()
	stdout := &boundedProcessOutput{maxBytes: maxStdoutBytes, onTruncate: cancelProcess}
	stderr := &boundedProcessOutput{maxBytes: maxExternalProcessOutputBytes, onTruncate: cancelProcess}
	err := runOwnedCommand(processCtx, bin, args, stdout, stderr)
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if stdout.truncated || stderr.truncated {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]byte(nil), stdout.buffer.Bytes()...), false, nil
}

func waitForTranscriptionProbeAudio(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
