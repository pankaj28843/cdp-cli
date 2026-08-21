package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
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

func TestWebAgentProviderErrorHidesProviderResponseShapeDetails(t *testing.T) {
	err := webAgentProviderError(webagent.Result{
		Error: &webagent.OperationError{
			Code:     "chatgpt_transcription_response_changed",
			ErrClass: "provider",
			Message:  "internal provider response details",
		},
	})
	var providerErr *transcriptionapi.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %T %v, want ProviderError", err, err)
	}
	if providerErr.APIError.Code != "provider_transcript_unavailable" {
		t.Fatalf("code = %q", providerErr.APIError.Code)
	}
	if providerErr.APIError.Message != "The transcription provider did not return a usable result; retry the saved audio" {
		t.Fatalf("message = %q", providerErr.APIError.Message)
	}
}
