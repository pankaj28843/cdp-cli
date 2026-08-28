package cli

import (
	"context"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/bing"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
)

func TestChatGPTAuthUsesValidTemplateWhileBackgroundRefreshIsBusy(t *testing.T) {
	store, err := chatgpt.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTemplate(context.Background(), chatgpt.RequestTemplate{
		SchemaVersion:    chatgpt.AuthTemplateSchemaVersion,
		Method:           "GET",
		URL:              "https://chatgpt.com/backend-api/conversations",
		Headers:          map[string]string{"user-agent": "test-agent", "authorization": "Bearer test"},
		Cookies:          map[string]string{"__Secure-next-auth.session-token": "test-session"},
		CookieHeader:     "__Secure-next-auth.session-token=test-session",
		BrowserUserAgent: "test-agent",
		CapturedAt:       time.Now().UTC().Add(-50 * time.Minute).Format(time.RFC3339Nano),
		Source:           "headed-cdp-observed-read-request",
	}); err != nil {
		t.Fatal(err)
	}

	provider := &chatGPTTranscriptionProvider{store: store}
	if err := provider.authMu.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer provider.authMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := provider.EnsureAuthFresh(ctx); err != nil {
		t.Fatalf("still-valid auth waited behind proactive refresh: %v", err)
	}
}

func TestBingTranscriptionUsesDirectWebSocketAdapter(t *testing.T) {
	app := &app{build: BuildInfo{Commit: "test"}}
	var captured bing.TranscribeConfig
	provider := &bingTranscriptionProvider{
		app: app,
		transcribe: func(_ context.Context, config bing.TranscribeConfig, path string, duration int64) webagent.Result {
			captured = config
			if path != "synthetic-fixture.webm" || duration != 1_200 {
				t.Fatalf("Bing audio arguments = %q/%d", path, duration)
			}
			return webagent.Result{OK: true, Data: bing.TranscriptionData{Transcript: "direct Bing"}}
		},
	}

	result, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
		Task:     transcriptionapi.TaskTranscribe,
		Language: "en-US",
		Audio: transcriptionapi.AudioAsset{
			FileName:      "recording.webm",
			MIMEType:      "audio/webm;codecs=opus",
			PersistedPath: "synthetic-fixture.webm",
			DurationMS:    1_200,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "direct Bing" {
		t.Fatalf("transcript = %q, want direct Bing", result.Text)
	}
	if captured.Dial == nil || captured.Language != "en-US" {
		t.Fatalf("Bing transport config = %+v, want direct dial and language", captured)
	}
}

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

func TestChatGPTSyntheticProbeRetainsLazyAuthRepairWithoutBrowserFallback(t *testing.T) {
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
	if captured.RefreshAuth == nil {
		t.Fatal("synthetic ChatGPT probe must retain bounded lazy auth repair")
	}
	if captured.BrowserFallback != nil {
		t.Fatal("synthetic ChatGPT probe must not retain the browser transcription fallback")
	}
}
