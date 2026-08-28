package cli

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/bing"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
	"github.com/pankaj28843/cdp-cli/internal/webagent/claude"
	"github.com/pankaj28843/cdp-cli/internal/webagent/gemini"
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
	if captured.RefreshAuth == nil {
		t.Fatal("normal ChatGPT transcription must retain bounded lazy auth repair")
	}
	if captured.MaxAttempts != 2 {
		t.Fatalf("ChatGPT direct attempts = %d, want one initial request plus one retry", captured.MaxAttempts)
	}
	if captured.AudioFileName != "recording.webm" || captured.AudioMIMEType != "audio/webm;codecs=opus" {
		t.Fatalf("audio metadata = %q/%q, want recording.webm/audio/webm;codecs=opus", captured.AudioFileName, captured.AudioMIMEType)
	}
	if result.Text != "direct" {
		t.Fatalf("transcript = %q, want direct", result.Text)
	}
}

func TestLazyAuthRepairRetriesAfterTransientFailure(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		repair func(context.Context) error
		calls  *atomic.Int32
	}{
		{
			name:  "ChatGPT",
			calls: new(atomic.Int32),
		},
		{
			name:  "Claude",
			calls: new(atomic.Int32),
		},
		{
			name:  "Gemini",
			calls: new(atomic.Int32),
		},
	}
	chatGPTProvider := &chatGPTTranscriptionProvider{store: testChatGPTTranscriptionStore(t, now)}
	chatGPTProvider.refresh = failOnceRefresh(tests[0].calls)
	tests[0].repair = func(ctx context.Context) error {
		return chatGPTProvider.repairAuth(ctx, now.Format(time.RFC3339Nano))
	}
	claudeProvider := &claudeTranscriptionProvider{store: testClaudeTranscriptionStore(t, now)}
	claudeProvider.refresh = failOnceRefresh(tests[1].calls)
	tests[1].repair = func(ctx context.Context) error {
		return claudeProvider.repairAuth(ctx, now.Format(time.RFC3339Nano))
	}
	geminiProvider := &geminiTranscriptionProvider{store: testGeminiTranscriptionStore(t, now)}
	geminiProvider.refresh = failOnceRefresh(tests[2].calls)
	tests[2].repair = func(ctx context.Context) error {
		return geminiProvider.repairAuth(ctx, now.Format(time.RFC3339Nano))
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.repair(context.Background()); err == nil {
				t.Fatal("first transient refresh unexpectedly succeeded")
			}
			if err := test.repair(context.Background()); err != nil {
				t.Fatalf("second refresh did not retry: %v", err)
			}
			if got := test.calls.Load(); got != 2 {
				t.Fatalf("refresh calls = %d, want 2", got)
			}
		})
	}
}

func failOnceRefresh(calls *atomic.Int32) func(context.Context) error {
	return func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("transient refresh failure")
		}
		return nil
	}
}

func TestChatGPTSyntheticProbeRetainsLazyAuthRepair(t *testing.T) {
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
}

func TestChatGPTTranscriptionCoalescesConcurrentLazyAuthRepair(t *testing.T) {
	now := time.Now().UTC()
	store := testChatGPTTranscriptionStore(t, now)
	var refreshes atomic.Int32
	const requests = 8
	ready := sync.WaitGroup{}
	ready.Add(requests)
	release := make(chan struct{})
	provider := &chatGPTTranscriptionProvider{
		app:   &app{build: BuildInfo{Commit: "test"}},
		store: store,
		refresh: func(ctx context.Context) error {
			refreshes.Add(1)
			return store.SaveTemplate(ctx, chatGPTTranscriptionTemplate(now.Add(time.Second)))
		},
		transcribe: func(ctx context.Context, config chatgpt.TranscribeConfig, _ string, _ int64) webagent.Result {
			ready.Done()
			<-release
			if err := config.RefreshAuth(ctx); err != nil {
				return webagent.Result{OK: false, Error: &webagent.OperationError{Code: "refresh_failed", ErrClass: "auth", Message: err.Error()}}
			}
			return webagent.Result{OK: true, Data: chatgpt.TranscriptionData{Transcript: "direct"}}
		},
	}

	errorsByRequest := make(chan error, requests)
	for range requests {
		go func() {
			_, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
				Task: transcriptionapi.TaskTranscribe,
				Audio: transcriptionapi.AudioAsset{
					FileName: "recording.webm", PersistedPath: "synthetic-fixture.webm", DurationMS: 100,
				},
			})
			errorsByRequest <- err
		}()
	}
	ready.Wait()
	close(release)
	for range requests {
		if err := <-errorsByRequest; err != nil {
			t.Fatal(err)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("ChatGPT lazy auth refreshes = %d, want one single flight", got)
	}
}

func TestChatGPTProviderDoesNotExposeASecondOuterRetryLoop(t *testing.T) {
	provider := &chatGPTTranscriptionProvider{
		app:   &app{build: BuildInfo{Commit: "test"}},
		store: testChatGPTTranscriptionStore(t, time.Now().UTC()),
		transcribe: func(context.Context, chatgpt.TranscribeConfig, string, int64) webagent.Result {
			return webagent.Result{OK: false, Error: &webagent.OperationError{
				Code: "chatgpt_auth_rejected", ErrClass: "auth",
				Message: "bounded direct retry exhausted", RetrySafe: true,
			}}
		},
	}
	_, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
		Task: transcriptionapi.TaskTranscribe,
		Audio: transcriptionapi.AudioAsset{
			FileName: "recording.webm", PersistedPath: "synthetic-fixture.webm", DurationMS: 100,
		},
	})
	var providerErr *transcriptionapi.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %v, want ProviderError", err)
	}
	if providerErr.Retryable {
		t.Fatal("ChatGPT provider exposed its exhausted internal retry to the outer API retry loop")
	}
}

func TestClaudeTranscriptionUsesDirectTransportWithLazyAuthRepair(t *testing.T) {
	store, err := claude.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &app{build: BuildInfo{Commit: "test"}}
	var captured claude.TranscribeConfig
	var capturedPath string
	provider := &claudeTranscriptionProvider{
		app:   app,
		store: store,
		transcribe: func(_ context.Context, config claude.TranscribeConfig, path string, _ int64) webagent.Result {
			captured = config
			capturedPath = path
			return webagent.Result{
				OK:   true,
				Data: claude.TranscriptionData{Transcript: "direct"},
			}
		},
	}

	result, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
		Task:     transcriptionapi.TaskTranscribe,
		Language: "da-DK",
		Audio: transcriptionapi.AudioAsset{
			FileName:      "recording.webm",
			MIMEType:      "audio/webm",
			PersistedPath: "synthetic-fixture.webm",
			DurationMS:    100,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.RefreshAuth == nil {
		t.Fatal("Claude transcription must retain bounded lazy auth repair")
	}
	if capturedPath != "synthetic-fixture.webm" {
		t.Fatalf("audio path = %q", capturedPath)
	}
	if captured.Language != "da-DK" {
		t.Fatalf("language = %q", captured.Language)
	}
	if result.Text != "direct" {
		t.Fatalf("transcript = %q, want direct", result.Text)
	}
}

func TestClaudeAuthUsesValidTemplateWhileBackgroundRefreshIsBusy(t *testing.T) {
	store := testClaudeTranscriptionStore(t, time.Now().UTC())
	provider := &claudeTranscriptionProvider{store: store}
	if err := provider.authMu.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer provider.authMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := provider.EnsureAuthFresh(ctx); err != nil {
		t.Fatalf("still-valid Claude auth waited behind proactive refresh: %v", err)
	}
}

func TestClaudeTranscriptionCoalescesConcurrentLazyAuthRepair(t *testing.T) {
	now := time.Now().UTC()
	store := testClaudeTranscriptionStore(t, now)
	var refreshes atomic.Int32
	const requests = 8
	ready := sync.WaitGroup{}
	ready.Add(requests)
	release := make(chan struct{})
	provider := &claudeTranscriptionProvider{
		app:   &app{build: BuildInfo{Commit: "test"}},
		store: store,
		refresh: func(ctx context.Context) error {
			refreshes.Add(1)
			return store.Save(ctx, claudeTranscriptionTemplate(now.Add(time.Second)))
		},
		transcribe: func(ctx context.Context, config claude.TranscribeConfig, _ string, _ int64) webagent.Result {
			ready.Done()
			<-release
			if err := config.RefreshAuth(ctx, now.Format(time.RFC3339Nano)); err != nil {
				return webagent.Result{OK: false, Error: &webagent.OperationError{Code: "refresh_failed", ErrClass: "auth", Message: err.Error()}}
			}
			return webagent.Result{OK: true, Data: claude.TranscriptionData{Transcript: "direct"}}
		},
	}

	errorsByRequest := make(chan error, requests)
	for range requests {
		go func() {
			_, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
				Task: transcriptionapi.TaskTranscribe,
				Audio: transcriptionapi.AudioAsset{
					FileName:      "recording.webm",
					MIMEType:      "audio/webm",
					PersistedPath: "synthetic-fixture.webm",
					DurationMS:    100,
				},
			})
			errorsByRequest <- err
		}()
	}
	ready.Wait()
	close(release)
	for range requests {
		if err := <-errorsByRequest; err != nil {
			t.Fatal(err)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("Claude lazy auth refreshes = %d, want one single flight", got)
	}
}

func TestClaudeProviderDoesNotExposeASecondOuterRetryLoop(t *testing.T) {
	provider := &claudeTranscriptionProvider{
		app:   &app{build: BuildInfo{Commit: "test"}},
		store: testClaudeTranscriptionStore(t, time.Now().UTC()),
		transcribe: func(context.Context, claude.TranscribeConfig, string, int64) webagent.Result {
			return webagent.Result{OK: false, Error: &webagent.OperationError{
				Code: "claude_auth_rejected", ErrClass: "auth",
				Message: "bounded direct retry exhausted", RetrySafe: true,
			}}
		},
	}
	_, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
		Task: transcriptionapi.TaskTranscribe,
		Audio: transcriptionapi.AudioAsset{
			FileName: "recording.webm", PersistedPath: "synthetic-fixture.webm", DurationMS: 100,
		},
	})
	var providerErr *transcriptionapi.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %v, want ProviderError", err)
	}
	if providerErr.Retryable {
		t.Fatal("Claude provider exposed its exhausted internal retry to the outer API retry loop")
	}
}

func TestGeminiTranscriptionUsesDirectWebChannelWithLazyAuthRepair(t *testing.T) {
	store, err := gemini.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var captured gemini.TranscribeConfig
	provider := &geminiTranscriptionProvider{
		app:   &app{build: BuildInfo{Commit: "test"}},
		store: store,
		transcribe: func(_ context.Context, config gemini.TranscribeConfig, path string, duration int64) webagent.Result {
			captured = config
			if path != "synthetic-fixture.webm" || duration != 100 {
				t.Fatalf("Gemini audio arguments = %q/%d", path, duration)
			}
			return webagent.Result{OK: true, Data: gemini.TranscriptionData{Transcript: "direct Gemini"}}
		},
	}
	result, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
		Task: transcriptionapi.TaskTranscribe,
		Audio: transcriptionapi.AudioAsset{
			FileName: "recording.webm", MIMEType: "audio/webm;codecs=opus",
			PersistedPath: "synthetic-fixture.webm", DurationMS: 100,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.RefreshAuth == nil || result.Text != "direct Gemini" {
		t.Fatalf("captured=%+v result=%+v", captured, result)
	}
}

func TestTranscriptionRegistryIncludesGeminiDirectProvider(t *testing.T) {
	a := &app{opts: options{stateDir: t.TempDir()}}
	registry, err := a.transcriptionRegistry(context.Background(), "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := registry.Provider(transcriptionapi.ProviderGemini)
	if !ok {
		t.Fatal("Gemini was not registered as a transcription provider")
	}
	if _, ok := provider.(*geminiTranscriptionProvider); !ok {
		t.Fatalf("Gemini provider = %T", provider)
	}
}

func TestGeminiTranscriptionCoalescesConcurrentLazyAuthRepair(t *testing.T) {
	now := time.Now().UTC()
	store := testGeminiTranscriptionStore(t, now)
	var refreshes atomic.Int32
	const requests = 8
	ready := sync.WaitGroup{}
	ready.Add(requests)
	release := make(chan struct{})
	provider := &geminiTranscriptionProvider{
		app:   &app{build: BuildInfo{Commit: "test"}},
		store: store,
		refresh: func(ctx context.Context) error {
			refreshes.Add(1)
			template := geminiTranscriptionTemplate(now.Add(time.Second))
			return store.SaveTemplate(ctx, template)
		},
		transcribe: func(ctx context.Context, config gemini.TranscribeConfig, _ string, _ int64) webagent.Result {
			ready.Done()
			<-release
			if err := config.RefreshAuth(ctx, now.Format(time.RFC3339Nano)); err != nil {
				return webagent.Result{OK: false, Error: &webagent.OperationError{Code: "refresh_failed", ErrClass: "auth", Message: err.Error()}}
			}
			return webagent.Result{OK: true, Data: gemini.TranscriptionData{Transcript: "direct"}}
		},
	}
	errorsByRequest := make(chan error, requests)
	for range requests {
		go func() {
			_, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
				Task:  transcriptionapi.TaskTranscribe,
				Audio: transcriptionapi.AudioAsset{FileName: "recording.webm", PersistedPath: "synthetic-fixture.webm", DurationMS: 100},
			})
			errorsByRequest <- err
		}()
	}
	ready.Wait()
	close(release)
	for range requests {
		if err := <-errorsByRequest; err != nil {
			t.Fatal(err)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("Gemini lazy auth refreshes = %d, want one single flight", got)
	}
}

func TestGeminiProviderDoesNotExposeASecondOuterRetryLoop(t *testing.T) {
	provider := &geminiTranscriptionProvider{
		app:   &app{build: BuildInfo{Commit: "test"}},
		store: testGeminiTranscriptionStore(t, time.Now().UTC()),
		transcribe: func(context.Context, gemini.TranscribeConfig, string, int64) webagent.Result {
			return webagent.Result{OK: false, Error: &webagent.OperationError{
				Code: "gemini_auth_rejected", ErrClass: "auth", Message: "bounded direct retry exhausted", RetrySafe: true,
			}}
		},
	}
	_, err := provider.Transcribe(context.Background(), transcriptionapi.FileRequest{
		Task:  transcriptionapi.TaskTranscribe,
		Audio: transcriptionapi.AudioAsset{FileName: "recording.webm", PersistedPath: "synthetic-fixture.webm", DurationMS: 100},
	})
	var providerErr *transcriptionapi.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Retryable {
		t.Fatalf("error = %#v, want non-retryable ProviderError", err)
	}
}

func testClaudeTranscriptionStore(t *testing.T, capturedAt time.Time) *claude.Store {
	t.Helper()
	store, err := claude.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), claudeTranscriptionTemplate(capturedAt)); err != nil {
		t.Fatal(err)
	}
	return store
}

func claudeTranscriptionTemplate(capturedAt time.Time) claude.AuthTemplate {
	return claude.AuthTemplate{
		SchemaVersion:    claude.AuthTemplateSchemaVersion,
		Method:           "GET",
		Origin:           claude.Origin,
		OrganizationID:   "org-1",
		ListURL:          claude.Origin + "/api/organizations/org-1/chat_conversations_v2?limit=30&starred=false&consistency=eventual",
		Headers:          map[string]string{"accept": "application/json"},
		Cookies:          map[string]string{"sessionKey": "private-test-session"},
		BrowserUserAgent: "Browser/Test",
		CapturedAt:       capturedAt.Format(time.RFC3339Nano),
		Source:           "headed-cdp-observed-list-request",
	}
}

func testChatGPTTranscriptionStore(t *testing.T, capturedAt time.Time) *chatgpt.Store {
	t.Helper()
	store, err := chatgpt.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTemplate(context.Background(), chatGPTTranscriptionTemplate(capturedAt)); err != nil {
		t.Fatal(err)
	}
	return store
}

func chatGPTTranscriptionTemplate(capturedAt time.Time) chatgpt.RequestTemplate {
	return chatgpt.RequestTemplate{
		SchemaVersion:    chatgpt.AuthTemplateSchemaVersion,
		Method:           "GET",
		URL:              "https://chatgpt.com/backend-api/conversations",
		Headers:          map[string]string{"user-agent": "test-agent", "authorization": "Bearer test"},
		Cookies:          map[string]string{"__Secure-next-auth.session-token": "test-session"},
		CookieHeader:     "__Secure-next-auth.session-token=test-session",
		BrowserUserAgent: "test-agent",
		CapturedAt:       capturedAt.Format(time.RFC3339Nano),
		Source:           "headed-cdp-observed-read-request",
	}
}

func testGeminiTranscriptionStore(t *testing.T, capturedAt time.Time) *gemini.Store {
	t.Helper()
	store, err := gemini.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTemplate(context.Background(), geminiTranscriptionTemplate(capturedAt)); err != nil {
		t.Fatal(err)
	}
	return store
}

func geminiTranscriptionTemplate(capturedAt time.Time) gemini.RequestTemplate {
	return gemini.RequestTemplate{
		SchemaVersion: gemini.RequestTemplateSchemaVersion,
		APIKey:        "test-api-key", AuthUser: "0",
		Cookies: map[string]string{
			"SAPISID": "test-sapisid", "__Secure-1PAPISID": "test-1p", "__Secure-3PAPISID": "test-3p",
		},
		BrowserUserAgent: "Browser/Test",
		CapturedAt:       capturedAt.Format(time.RFC3339Nano), Source: "headed-cdp-observed-dictation-template",
	}
}
