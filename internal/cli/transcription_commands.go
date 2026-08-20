package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
	"github.com/pankaj28843/cdp-cli/internal/webagent/m365"
	"github.com/spf13/cobra"
)

func (a *app) newTranscriptionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transcription",
		Short: "Run the provider-neutral OpenAI-compatible transcription service",
		Long: "Expose one local REST, SSE, and realtime WebSocket boundary for VoxInput. " +
			"Audio is ephemeral transaction media by default and can be explicitly retained with --persist-audio; " +
			"provider-specific auth refresh remains inside the cdp workflow adapter.",
		Example: "  cdp transcription serve --token local-test --default-provider chatgpt-web\n" +
			"  cdp transcription serve --token local-test --local-base-url http://localhost:9000/v1\n" +
			"  cdp transcription service install --address 0.0.0.0:8765 --tls-self-signed --tls-host 192.168.5.249\n" +
			"  cdp transcription spec > openapi.json",
	}
	cmd.AddCommand(a.newTranscriptionServeCommand())
	cmd.AddCommand(a.newTranscriptionSpecCommand())
	cmd.AddCommand(a.newTranscriptionServiceCommand())
	return cmd
}

func (a *app) newTranscriptionSpecCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "spec",
		Short:   "Print the OpenAPI 3.1 transcription contract",
		Example: "  cdp transcription spec > openapi.json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := transcriptionapi.ValidateOpenAPISpec(); err != nil {
				return err
			}
			_, err := a.out.Write(transcriptionapi.OpenAPISpec())
			return err
		},
	}
}

func (a *app) newTranscriptionServeCommand() *cobra.Command {
	var address string
	var httpAddress string
	var token string
	var defaultProvider string
	var allowedProviders []string
	var localBaseURL string
	var localRealtimeBaseURL string
	var localAPIKey string
	var maxAudioBytes int64
	var authRefreshInterval time.Duration
	var persistAudio bool
	var tlsCertFile string
	var tlsKeyFile string
	var tlsSelfSigned bool
	var tlsHosts []string
	var tlsRegenerate bool
	var printReady bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve REST, SSE, and realtime transcription locally",
		Long: "Start the deployable provider-neutral transcription API. The server binds to loopback by " +
			"default, requires a bearer token when --token is set, and retains result records under " +
			"<state-dir>/transcription; uploaded media is ephemeral unless --persist-audio is set. " +
			"Configure a local OpenAI-compatible backend with " +
			"--local-base-url or select an authenticated cdp-cli provider as the default.",
		Example: "  cdp transcription serve --token local-test --default-provider chatgpt-web\n" +
			"  cdp transcription serve --token local-test --local-base-url http://localhost:9000/v1 --print-ready\n" +
			"  cdp transcription serve --address 0.0.0.0:8765 --tls-self-signed --tls-host 192.168.5.249 --print-ready",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxAudioBytes <= 0 {
				return commandError("transcription_cache_limit_invalid", "usage", "--max-audio-bytes must be positive", ExitUsage, nil)
			}
			if authRefreshInterval < 0 {
				return commandError("transcription_auth_refresh_interval_invalid", "usage", "--auth-refresh-interval must be zero or positive", ExitUsage, nil)
			}
			stateStore, err := a.stateStore()
			if err != nil {
				return err
			}
			tlsFiles, err := configureTranscriptionTLS(stateStore.Dir, tlsCertFile, tlsKeyFile, tlsSelfSigned, tlsHosts, tlsRegenerate)
			if err != nil {
				return err
			}
			storeRoot := filepath.Join(stateStore.Dir, "transcription")
			var store *transcriptionapi.Store
			if persistAudio {
				store, err = transcriptionapi.NewStore(storeRoot, maxAudioBytes)
			} else {
				store, err = transcriptionapi.NewEphemeralStore(storeRoot, maxAudioBytes)
			}
			if err != nil {
				return err
			}
			registry, err := a.transcriptionRegistry(cmd.Context(), localBaseURL, localRealtimeBaseURL, localAPIKey, allowedProviders)
			if err != nil {
				return err
			}
			authCoordinator := transcriptionapi.NewAuthRefreshCoordinator(registry, authRefreshInterval)
			server, err := transcriptionapi.NewServer(transcriptionapi.ServerConfig{
				Registry:        registry,
				Store:           store,
				DefaultProvider: transcriptionapi.ProviderID(strings.TrimSpace(defaultProvider)),
				BearerToken:     strings.TrimSpace(token),
				Address:         strings.TrimSpace(address),
				HTTPAddress:     strings.TrimSpace(httpAddress),
				TLSCertFile:     tlsFiles.CertFile,
				TLSKeyFile:      tlsFiles.KeyFile,
				AuthCoordinator: authCoordinator,
			})
			if err != nil {
				return err
			}
			if printReady {
				tlsEnabled := strings.TrimSpace(tlsFiles.CertFile) != ""
				ready := map[string]any{
					"ok":                    true,
					"address":               address,
					"http_address":          httpAddress,
					"contract_version":      transcriptionapi.ContractVersion,
					"state_dir":             store.Root(),
					"auth_refresh_interval": authRefreshInterval.String(),
					"audio_persisted":       persistAudio,
					"tls_enabled":           tlsEnabled,
					"tls_cert_file":         tlsFiles.CertFile,
					"tls_hosts":             tlsFiles.Hosts,
					"tls_reused":            tlsFiles.Reused,
					"demo_url":              preferredDemoURL(address, tlsEnabled, tlsFiles.Hosts),
					"demo_urls":             demoURLs(address, tlsEnabled, tlsFiles.Hosts),
					"providers":             registry.Capabilities(cmd.Context()),
				}
				if err := a.render(cmd.Context(), "transcription API ready", ready); err != nil {
					return err
				}
			}
			err = server.ListenAndServe(cmd.Context())
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringVar(&address, "address", envDefault("CDP_TRANSCRIPTION_ADDRESS", transcriptionapi.DefaultListenAddress), "listen address; loopback is the safe default")
	cmd.Flags().StringVar(&httpAddress, "http-address", os.Getenv("CDP_TRANSCRIPTION_HTTP_ADDRESS"), "optional plain-HTTP listener; use a distinct port for explicit private-LAN/Tailscale access")
	cmd.Flags().StringVar(&token, "token", os.Getenv("CDP_TRANSCRIPTION_API_TOKEN"), "local bearer token; set this before exposing the service beyond a trusted loopback")
	cmd.Flags().StringVar(&defaultProvider, "default-provider", envDefault("CDP_TRANSCRIPTION_PROVIDER", string(transcriptionapi.ProviderLocal)), "default provider: local, chatgpt-web, or microsoft-365-web")
	cmd.Flags().StringSliceVar(&allowedProviders, "providers", nil, "provider allowlist; repeat or comma-separate (default: all configured providers)")
	cmd.Flags().StringVar(&localBaseURL, "local-base-url", os.Getenv("CDP_TRANSCRIPTION_LOCAL_BASE_URL"), "local OpenAI-compatible provider base URL, usually ending in /v1")
	cmd.Flags().StringVar(&localRealtimeBaseURL, "local-realtime-base-url", os.Getenv("CDP_TRANSCRIPTION_LOCAL_REALTIME_BASE_URL"), "optional separate local realtime provider base URL, usually ending in /v1")
	cmd.Flags().StringVar(&localAPIKey, "local-api-key", os.Getenv("CDP_TRANSCRIPTION_LOCAL_API_KEY"), "API key for the configured local provider")
	cmd.Flags().Int64Var(&maxAudioBytes, "max-audio-bytes", envInt64("CDP_TRANSCRIPTION_MAX_AUDIO_BYTES", transcriptionapi.DefaultMaxAudioBytes), "maximum retained audio-cache bytes; transcript records are retained independently")
	cmd.Flags().DurationVar(&authRefreshInterval, "auth-refresh-interval", envDuration("CDP_TRANSCRIPTION_AUTH_REFRESH_INTERVAL", transcriptionapi.DefaultAuthRefreshInterval), "shared recurring freshness check for all online providers")
	cmd.Flags().BoolVar(&persistAudio, "persist-audio", envBool("CDP_TRANSCRIPTION_PERSIST_AUDIO"), "retain uploaded audio under the state directory; default is ephemeral transaction media")
	cmd.Flags().StringVar(&tlsCertFile, "tls-cert", os.Getenv("CDP_TRANSCRIPTION_TLS_CERT"), "TLS certificate file; provide with --tls-key for HTTPS microphone access over LAN")
	cmd.Flags().StringVar(&tlsKeyFile, "tls-key", os.Getenv("CDP_TRANSCRIPTION_TLS_KEY"), "TLS private key file; provide with --tls-cert")
	cmd.Flags().BoolVar(&tlsSelfSigned, "tls-self-signed", false, "generate or reuse a private-LAN self-signed certificate under the cdp state directory")
	cmd.Flags().StringSliceVar(&tlsHosts, "tls-host", nil, "DNS name or IP to include in a self-signed certificate; repeat for multiple names")
	cmd.Flags().BoolVar(&tlsRegenerate, "tls-regenerate", false, "replace the generated self-signed certificate and key")
	cmd.Flags().BoolVar(&printReady, "print-ready", false, "print one readiness JSON object before serving")
	return cmd
}

func (a *app) transcriptionRegistry(ctx context.Context, localBaseURL, localRealtimeBaseURL, localAPIKey string, allowedProviders []string) (*transcriptionapi.Registry, error) {
	providers := make([]transcriptionapi.Provider, 0, 3)
	if strings.TrimSpace(localBaseURL) != "" {
		localProvider, err := transcriptionapi.NewOpenAIHTTPProvider(
			transcriptionapi.ProviderLocal,
			localBaseURL,
			localAPIKey,
		)
		if err != nil {
			return nil, err
		}
		localProvider.RealtimeBaseURL = strings.TrimRight(strings.TrimSpace(localRealtimeBaseURL), "/")
		providers = append(providers, localProvider)
	}
	stateStore, err := a.stateStore()
	if err != nil {
		providers, filterErr := filterTranscriptionProviders(providers, allowedProviders)
		if filterErr != nil {
			return nil, filterErr
		}
		return transcriptionapi.NewRegistry(providers...), nil
	}
	chatStore, chatErr := chatgpt.NewStore(stateStore.Dir)
	if chatErr == nil {
		providers = append(providers, &chatGPTTranscriptionProvider{app: a, store: chatStore})
	}
	m365Store, m365Err := m365.NewStore(stateStore.Dir)
	if m365Err == nil {
		providers = append(providers, &m365TranscriptionProvider{app: a, store: m365Store})
	}
	providers, err = filterTranscriptionProviders(providers, allowedProviders)
	if err != nil {
		return nil, err
	}
	return transcriptionapi.NewRegistry(providers...), nil
}

func filterTranscriptionProviders(providers []transcriptionapi.Provider, requested []string) ([]transcriptionapi.Provider, error) {
	if len(requested) == 0 {
		return providers, nil
	}

	available := make(map[transcriptionapi.ProviderID]transcriptionapi.Provider, len(providers))
	for _, provider := range providers {
		if provider != nil {
			available[provider.ID()] = provider
		}
	}
	selected := make([]transcriptionapi.Provider, 0, len(requested))
	seen := make(map[transcriptionapi.ProviderID]struct{}, len(requested))
	for _, raw := range requested {
		id := transcriptionapi.ProviderID(strings.TrimSpace(raw))
		if id == "" {
			continue
		}
		provider, ok := available[id]
		if !ok {
			return nil, commandError(
				"transcription_provider_not_configured",
				"usage",
				fmt.Sprintf("transcription provider %q is not configured", id),
				ExitUsage,
				[]string{"cdp transcription serve --help"},
			)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		selected = append(selected, provider)
	}
	if len(selected) == 0 {
		return nil, commandError(
			"transcription_provider_allowlist_empty",
			"usage",
			"--providers must contain at least one configured provider",
			ExitUsage,
			nil,
		)
	}
	return selected, nil
}

type chatGPTTranscriptionProvider struct {
	app    *app
	store  *chatgpt.Store
	authMu sync.Mutex
}

func (p *chatGPTTranscriptionProvider) ID() transcriptionapi.ProviderID {
	return transcriptionapi.ProviderChatGPT
}

func (p *chatGPTTranscriptionProvider) Capabilities(ctx context.Context) transcriptionapi.ProviderCapability {
	status := p.store.AuthStatus(ctx, time.Now(), chatgpt.DefaultAuthTTL)
	return transcriptionapi.ProviderCapability{
		Provider:    p.ID(),
		Models:      []string{transcriptionapi.DefaultModel},
		File:        true,
		Translation: false,
		Streaming:   true,
		Realtime:    false,
		Ready:       status.Ready,
		Reason:      status.Reason,
	}
}

func (p *chatGPTTranscriptionProvider) EnsureAuthFresh(ctx context.Context) error {
	now := time.Now()
	p.authMu.Lock()
	defer p.authMu.Unlock()
	status := p.store.AuthStatus(ctx, now, chatgpt.DefaultAuthTTL)
	if status.Ready && !authEvidenceExpiringSoon(status.ExpiresAt, now) {
		return nil
	}
	if err := p.refreshAuthLocked(ctx); err != nil {
		var providerErr *transcriptionapi.ProviderError
		if errors.As(err, &providerErr) {
			return err
		}
		return transcriptionProviderError(401, "authentication_error", "auth_refresh_failed", err.Error(), false)
	}
	return nil
}

func (p *chatGPTTranscriptionProvider) Transcribe(ctx context.Context, request transcriptionapi.FileRequest) (transcriptionapi.Result, error) {
	if request.Task == transcriptionapi.TaskTranslate {
		return transcriptionapi.Result{}, transcriptionProviderError(501, "unsupported", "translation_unsupported", "ChatGPT web transcription adapter does not expose Whisper translation", false)
	}
	duration, err := providerAudioDuration(ctx, request)
	if err != nil {
		return transcriptionapi.Result{}, err
	}
	var browserConfig *chatgpt.BrowserConfig
	if p.app.selectHeadedProviderRuntime() {
		candidate, _, unavailable := p.app.chatgptBrowserOperationConfig(
			ctx,
			webagent.OperationTranscribe,
		)
		if unavailable == nil {
			browserConfig = &candidate
		}
	}
	result := chatgpt.Transcribe(ctx, chatgpt.TranscribeConfig{
		Store:       p.store,
		Browser:     browserConfig,
		BuildCommit: p.app.build.Commit,
		RefreshAuth: p.refreshAuth,
	}, request.Audio.PersistedPath, duration)
	if !result.OK {
		return transcriptionapi.Result{}, webAgentProviderError(result)
	}
	data, ok := result.Data.(chatgpt.TranscriptionData)
	if !ok {
		return transcriptionapi.Result{}, transcriptionProviderError(502, "provider", "response_changed", "ChatGPT transcription result shape changed", true)
	}
	return transcriptionapi.Result{Task: request.Task, Text: data.Transcript}, nil
}

func (p *chatGPTTranscriptionProvider) NewRealtime(context.Context, transcriptionapi.RealtimeSessionConfig) (transcriptionapi.RealtimeSession, error) {
	return nil, transcriptionProviderError(501, "unsupported", "realtime_unsupported", "ChatGPT web transcription currently accepts completed WebM files", false)
}

func (p *chatGPTTranscriptionProvider) refreshAuth(ctx context.Context) error {
	p.authMu.Lock()
	defer p.authMu.Unlock()
	return p.refreshAuthLocked(ctx)
}

func (p *chatGPTTranscriptionProvider) refreshAuthLocked(ctx context.Context) error {
	if !p.app.selectHeadedProviderRuntime() {
		return fmt.Errorf("ChatGPT headed browser runtime is unavailable for auth repair")
	}
	browserConfig, refreshedStore, unavailable := p.app.chatgptBrowserOperationConfig(ctx, webagent.OperationTranscribe)
	if unavailable != nil {
		if unavailable.Error != nil {
			return fmt.Errorf("%s", unavailable.Error.Message)
		}
		return fmt.Errorf("ChatGPT headed browser auth repair is unavailable")
	}
	result := chatgpt.RefreshAuth(ctx, chatgpt.AuthRefreshConfig{BrowserConfig: browserConfig, Store: refreshedStore})
	if !result.OK {
		return webAgentProviderError(result)
	}
	return nil
}

type m365TranscriptionProvider struct {
	app    *app
	store  *m365.Store
	authMu sync.Mutex
}

func (p *m365TranscriptionProvider) ID() transcriptionapi.ProviderID {
	return transcriptionapi.ProviderM365
}

func (p *m365TranscriptionProvider) Capabilities(ctx context.Context) transcriptionapi.ProviderCapability {
	auth := p.store.AuthStatus(ctx, time.Now(), m365.DefaultAuthTTL)
	runtime := p.store.RuntimeStatus(ctx, time.Now(), m365.DefaultCapabilitiesTTL)
	ready := auth.Ready && runtime.Ready
	reason := auth.Reason
	if reason == "" {
		reason = runtime.Reason
	}
	return transcriptionapi.ProviderCapability{
		Provider:    p.ID(),
		Models:      []string{transcriptionapi.DefaultModel},
		File:        true,
		Translation: false,
		Streaming:   true,
		Realtime:    true,
		Ready:       ready,
		Reason:      reason,
	}
}

func (p *m365TranscriptionProvider) EnsureAuthFresh(ctx context.Context) error {
	now := time.Now()
	p.authMu.Lock()
	defer p.authMu.Unlock()
	status := p.store.AuthStatus(ctx, now, m365.DefaultAuthTTL)
	if status.Ready && !authEvidenceExpiringSoon(status.ExpiresAt, now) {
		return nil
	}
	if err := p.refreshAuthLocked(ctx); err != nil {
		var providerErr *transcriptionapi.ProviderError
		if errors.As(err, &providerErr) {
			return err
		}
		return transcriptionProviderError(401, "authentication_error", "auth_refresh_failed", err.Error(), false)
	}
	return nil
}

func (p *m365TranscriptionProvider) Transcribe(ctx context.Context, request transcriptionapi.FileRequest) (transcriptionapi.Result, error) {
	if request.Task == transcriptionapi.TaskTranslate {
		return transcriptionapi.Result{}, transcriptionProviderError(501, "unsupported", "translation_unsupported", "Microsoft 365 dictation adapter does not expose translation", false)
	}
	duration, err := providerAudioDuration(ctx, request)
	if err != nil {
		return transcriptionapi.Result{}, err
	}
	result := m365.Transcribe(ctx, m365.TranscribeConfig{
		Store:       p.store,
		BuildCommit: p.app.build.Commit,
		RefreshAuth: p.refreshAuth,
		Dial:        augloop.Dial,
	}, request.Audio.PersistedPath, duration)
	if !result.OK {
		return transcriptionapi.Result{}, webAgentProviderError(result)
	}
	data, ok := result.Data.(m365.TranscriptionData)
	if !ok {
		return transcriptionapi.Result{}, transcriptionProviderError(502, "provider", "response_changed", "Microsoft 365 transcription result shape changed", true)
	}
	return transcriptionapi.Result{Task: request.Task, Text: data.Transcript}, nil
}

func (p *m365TranscriptionProvider) NewRealtime(ctx context.Context, config transcriptionapi.RealtimeSessionConfig) (transcriptionapi.RealtimeSession, error) {
	if config.Model == "" {
		config.Model = transcriptionapi.DefaultModel
	}
	if config.InputFormat.Type == "" {
		config.InputFormat = transcriptionapi.RealtimeAudioFormat{Type: "audio/pcm", Rate: 24_000}
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return newM365RealtimeSession(ctx, p, config)
}

func (p *m365TranscriptionProvider) refreshAuth(ctx context.Context) error {
	p.authMu.Lock()
	defer p.authMu.Unlock()
	return p.refreshAuthLocked(ctx)
}

func (p *m365TranscriptionProvider) refreshAuthLocked(ctx context.Context) error {
	if !p.app.selectHeadedProviderRuntime() {
		return fmt.Errorf("Microsoft 365 headed browser runtime is unavailable for auth repair")
	}
	browserConfig, refreshedStore, unavailable := p.app.m365BrowserOperationConfig(ctx, webagent.OperationTranscribe)
	if unavailable != nil {
		if unavailable.Error != nil {
			return fmt.Errorf("%s", unavailable.Error.Message)
		}
		return fmt.Errorf("Microsoft 365 headed browser auth repair is unavailable")
	}
	result := m365.RefreshAuth(ctx, m365.AuthRefreshConfig{BrowserConfig: browserConfig, Store: refreshedStore})
	if !result.OK {
		return webAgentProviderError(result)
	}
	return nil
}

func providerAudioDuration(ctx context.Context, request transcriptionapi.FileRequest) (int64, error) {
	if request.Audio.DurationMS > 0 {
		return request.Audio.DurationMS, nil
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0, transcriptionProviderError(422, "usage", "duration_required", "ChatGPT and Microsoft 365 adapters need audio duration_ms or ffprobe on PATH", false)
	}
	output, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration:stream=duration", "-of", "default=noprint_wrappers=1:nokey=1", request.Audio.PersistedPath).Output()
	if err == nil {
		if duration, ok := probeDurationMilliseconds(string(output)); ok {
			return duration, nil
		}
	}
	// Browser MediaRecorder WebM often has no container duration. Its packet
	// timestamps are sufficient and keep normal OpenAI-compatible clients from
	// having to know the VoxInput duration_ms extension.
	packets, packetErr := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "a:0", "-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", request.Audio.PersistedPath).Output()
	if packetErr == nil {
		if duration, ok := probePacketDurationMilliseconds(string(packets)); ok {
			return duration, nil
		}
	}
	return 0, transcriptionProviderError(422, "usage", "duration_probe_failed", "audio duration could not be determined", false)
}

func probeDurationMilliseconds(output string) (int64, bool) {
	for _, line := range strings.Split(output, "\n") {
		seconds, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
		if err == nil && seconds > 0 {
			return maxDurationMilliseconds(seconds), true
		}
	}
	return 0, false
}

func probePacketDurationMilliseconds(output string) (int64, bool) {
	var endSeconds float64
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ",")
		if len(fields) == 0 {
			continue
		}
		start, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
		if err != nil || start < 0 {
			continue
		}
		packetDuration := 0.0
		if len(fields) > 1 {
			packetDuration, _ = strconv.ParseFloat(strings.TrimSpace(fields[len(fields)-1]), 64)
			if packetDuration < 0 {
				packetDuration = 0
			}
		}
		if start+packetDuration > endSeconds {
			endSeconds = start + packetDuration
		}
	}
	if endSeconds <= 0 {
		return 0, false
	}
	return maxDurationMilliseconds(endSeconds), true
}

func maxDurationMilliseconds(seconds float64) int64 {
	duration := int64(seconds * 1000)
	if duration <= 0 {
		return 1
	}
	return duration
}

func webAgentProviderError(result webagent.Result) error {
	if result.Error == nil {
		return transcriptionProviderError(502, "provider", "provider_failed", "provider transcription failed", false)
	}
	status := 502
	switch result.Error.ErrClass {
	case "auth":
		status = 401
	case "usage":
		status = 422
	case "timeout":
		status = 504
	case "connection":
		status = 503
	}
	return transcriptionProviderError(status, "provider", result.Error.Code, result.Error.Message, false)
}

func transcriptionProviderError(status int, kind, code, message string, retryable bool) error {
	return &transcriptionapi.ProviderError{
		Status:    status,
		Retryable: retryable,
		APIError: transcriptionapi.APIError{
			Type:    kind,
			Code:    code,
			Message: message,
		},
	}
}

func envInt64(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func authEvidenceExpiringSoon(expiresAt string, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(expiresAt))
	if err != nil {
		return true
	}
	return !now.UTC().Add(15 * time.Minute).Before(expires.UTC())
}
