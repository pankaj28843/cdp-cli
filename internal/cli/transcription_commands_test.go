package cli

import (
	"context"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
)

func TestProviderAudioDurationUsesClientMeasurement(t *testing.T) {
	got, err := providerAudioDuration(context.Background(), transcriptionapi.FileRequest{Audio: transcriptionapi.AudioAsset{DurationMS: 1250}})
	if err != nil {
		t.Fatal(err)
	}
	if got != 1250 {
		t.Fatalf("duration = %d, want 1250", got)
	}
}

func TestProbePacketDurationMillisecondsHandlesDurationlessWebM(t *testing.T) {
	got, ok := probePacketDurationMilliseconds("0.000000,0.060000\n1.920000,0.060000\n")
	if !ok || got != 1980 {
		t.Fatalf("duration = %d, ok=%v, want 1980,true", got, ok)
	}
}
