package cli

import (
	"context"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
)

func TestChatGPTTranscriptionUsesDirectTransportByDefault(t *testing.T) {
	store, err := chatgpt.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &app{build: BuildInfo{Commit: "test"}}
	var captured chatgpt.TranscribeConfig
	provider := &chatGPTTranscriptionProvider{
		app:   app,
		store: store,
		transcribe: func(_ context.Context, config chatgpt.TranscribeConfig, _ string, _ int64) webagent.Result {
			captured = config
			return webagent.Result{
				OK:   true,
				Data: chatgpt.TranscriptionData{Transcript: "direct"},
			}
		},
	}

	result, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
		Task: transcriptionapi.TaskTranscribe,
		Audio: transcriptionapi.AudioAsset{
			FileName:      "recording.webm",
			MIMEType:      "audio/webm;codecs=opus",
			PersistedPath: "synthetic-fixture.wav",
			DurationMS:    100,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Browser != nil {
		t.Fatal("normal ChatGPT transcription must not attach to headed browser")
	}
	if captured.RefreshAuth == nil || captured.BrowserFallback == nil {
		t.Fatal("normal ChatGPT transcription must retain lazy auth repair callbacks")
	}
	if captured.AudioFileName != "recording.webm" || captured.AudioMIMEType != "audio/webm;codecs=opus" {
		t.Fatalf("audio metadata = %q/%q, want recording.webm/audio/webm;codecs=opus", captured.AudioFileName, captured.AudioMIMEType)
	}
	if result.Text != "direct" {
		t.Fatalf("transcript = %q, want direct", result.Text)
	}
}

func TestChatGPTSyntheticProbeDisablesLazyBrowserRepair(t *testing.T) {
	store, err := chatgpt.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &app{build: BuildInfo{Commit: "test"}}
	var captured chatgpt.TranscribeConfig
	provider := &chatGPTTranscriptionProvider{
		app:   app,
		store: store,
		transcribe: func(_ context.Context, config chatgpt.TranscribeConfig, _ string, _ int64) webagent.Result {
			captured = config
			return webagent.Result{OK: true, Data: chatgpt.TranscriptionData{Transcript: "probe"}}
		},
	}

	_, err = provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
		Task:           transcriptionapi.TaskTranscribe,
		SyntheticProbe: true,
		Audio: transcriptionapi.AudioAsset{
			FileName:      "recording.webm",
			MIMEType:      "audio/webm;codecs=opus",
			PersistedPath: "synthetic-fixture.webm",
			DurationMS:    100,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.RefreshAuth != nil || captured.BrowserFallback != nil {
		t.Fatal("synthetic ChatGPT probe must not retain lazy browser repair callbacks")
	}
}
