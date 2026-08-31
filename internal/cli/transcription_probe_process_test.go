package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
)

func TestDecodeTranscriptionProbeBoundsPCMOutput(t *testing.T) {
	binDir := t.TempDir()
	writeFakeTranscriptionExecutable(t, binDir, "ffmpeg", "#!/bin/sh\nhead -c 16777218 /dev/zero\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := decodeTranscriptionProbePCM(context.Background(), filepath.Join(t.TempDir(), "fixture.webm"))
	if err == nil {
		t.Fatal("decodeTranscriptionProbePCM accepted output beyond the probe bound")
	}
	var providerErr *transcriptionapi.ProviderError
	if !errors.As(err, &providerErr) || providerErr.APIError.Code != "realtime_probe_output_too_large" {
		t.Fatalf("decodeTranscriptionProbePCM error = %T %v, want realtime_probe_output_too_large", err, err)
	}
}

func TestProviderAudioDurationBoundsFFProbeOutput(t *testing.T) {
	binDir := t.TempDir()
	writeFakeTranscriptionExecutable(t, binDir, "ffprobe", "#!/bin/sh\nhead -c 131072 /dev/zero\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := providerAudioDuration(context.Background(), transcriptionapi.FileRequest{Audio: transcriptionapi.AudioAsset{
		PersistedPath: filepath.Join(t.TempDir(), "fixture.webm"),
	}})
	if err == nil {
		t.Fatal("providerAudioDuration accepted ffprobe output beyond the probe bound")
	}
	var providerErr *transcriptionapi.ProviderError
	if !errors.As(err, &providerErr) || providerErr.APIError.Code != "duration_probe_output_too_large" {
		t.Fatalf("providerAudioDuration error = %T %v, want duration_probe_output_too_large", err, err)
	}
}

func TestProviderAudioDurationBoundsFFProbeDiagnostics(t *testing.T) {
	binDir := t.TempDir()
	writeFakeTranscriptionExecutable(t, binDir, "ffprobe", "#!/bin/sh\nhead -c 131072 /dev/zero >&2\nprintf '1.250\\n'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := providerAudioDuration(context.Background(), transcriptionapi.FileRequest{Audio: transcriptionapi.AudioAsset{
		PersistedPath: filepath.Join(t.TempDir(), "fixture.webm"),
	}})
	if err == nil {
		t.Fatal("providerAudioDuration accepted ffprobe diagnostics beyond the probe bound")
	}
	var providerErr *transcriptionapi.ProviderError
	if !errors.As(err, &providerErr) || providerErr.APIError.Code != "duration_probe_output_too_large" {
		t.Fatalf("providerAudioDuration error = %T %v, want duration_probe_output_too_large", err, err)
	}
}

func TestDecodeTranscriptionProbeRetainsValidPCM(t *testing.T) {
	binDir := t.TempDir()
	writeFakeTranscriptionExecutable(t, binDir, "ffmpeg", "#!/bin/sh\nprintf '\\001\\000\\002\\000'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pcm, err := decodeTranscriptionProbePCM(context.Background(), "synthetic-fixture.webm")
	if err != nil {
		t.Fatalf("decodeTranscriptionProbePCM() error = %v", err)
	}
	if got, want := string(pcm), string([]byte{1, 0, 2, 0}); got != want {
		t.Fatalf("decoded PCM = %v, want %v", []byte(got), []byte(want))
	}
}

func TestProviderAudioDurationRetainsValidFFProbeOutput(t *testing.T) {
	binDir := t.TempDir()
	writeFakeTranscriptionExecutable(t, binDir, "ffprobe", "#!/bin/sh\nprintf '1.250\\n'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	duration, err := providerAudioDuration(context.Background(), transcriptionapi.FileRequest{Audio: transcriptionapi.AudioAsset{
		PersistedPath: "synthetic-fixture.webm",
	}})
	if err != nil {
		t.Fatalf("providerAudioDuration() error = %v", err)
	}
	if duration != 1250 {
		t.Fatalf("duration = %d, want 1250", duration)
	}
}

func TestProviderAudioDurationPreservesPacketFallback(t *testing.T) {
	binDir := t.TempDir()
	writeFakeTranscriptionExecutable(t, binDir, "ffprobe", `#!/bin/sh
case "$*" in
  *format=duration:stream=duration*) printf 'not-a-duration\n' ;;
  *) printf '0.000000,0.060000\n1.920000,0.060000\n' ;;
esac
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	duration, err := providerAudioDuration(context.Background(), transcriptionapi.FileRequest{Audio: transcriptionapi.AudioAsset{
		PersistedPath: "synthetic-fixture.webm",
	}})
	if err != nil {
		t.Fatalf("providerAudioDuration() error = %v", err)
	}
	if duration != 1980 {
		t.Fatalf("duration = %d, want packet fallback 1980", duration)
	}
}

func writeFakeTranscriptionExecutable(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return path
}
