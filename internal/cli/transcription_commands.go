package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/bing"
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
			"online provider auth and capability refresh is shared by the service and remains inside the cdp workflow adapter.",
		Example: "  cdp transcription serve --default-provider chatgpt-web\n" +
			"  cdp transcription serve --local-base-url http://localhost:9000/v1\n" +
			"  cdp transcription service install --address '[::]:28765' --http-address '[::]:28766' --tls-self-signed --tls-host 192.168.5.249\n" +
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
	var defaultProvider string
	var allowedProviders []string
	var localBaseURL string
	var localRealtimeBaseURL string
	var localAPIKey string
	var maxAudioBytes int64
	var authRefreshInterval time.Duration
	var authRefreshOffset time.Duration
	var fixtureDir string
	var probeInterval time.Duration
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
		Long: "Start the deployable provider-neutral transcription API. The server defaults to the " +
			"LAN-capable primary and cleartext companion listeners without bearer-token authentication, and retains result records under " +
			"<state-dir>/transcription; uploaded media is ephemeral unless --persist-audio is set. " +
			"When --fixture-dir is configured, the service runs a bounded live transcription probe for every configured provider on a recurring cadence; " +
			"each probe performs a cheap auth/capability preflight and health is green only after a recent probe succeeds. The shared headed auth/capability schedule " +
			"runs by default and can be disabled with --auth-refresh-interval 0s. Configure a local OpenAI-compatible backend with " +
			"--local-base-url or select an authenticated cdp-cli provider as the default.",
		Example: "  cdp transcription serve --default-provider chatgpt-web\n" +
			"  cdp transcription serve --local-base-url http://localhost:9000/v1 --print-ready\n" +
			"  cdp transcription serve --address '[::]:28765' --http-address '[::]:28766' --tls-self-signed --tls-host 192.168.5.249 --print-ready",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxAudioBytes <= 0 {
				return commandError("transcription_cache_limit_invalid", "usage", "--max-audio-bytes must be positive", ExitUsage, nil)
			}
			if authRefreshInterval < 0 {
				return commandError("transcription_auth_refresh_interval_invalid", "usage", "--auth-refresh-interval must be zero or positive", ExitUsage, nil)
			}
			if authRefreshOffset < 0 || (authRefreshInterval == 0 && authRefreshOffset != 0) || (authRefreshInterval > 0 && authRefreshOffset >= authRefreshInterval) {
				return commandError("transcription_auth_refresh_offset_invalid", "usage", "--auth-refresh-offset must be non-negative and shorter than its interval", ExitUsage, nil)
			}
			if probeInterval < 0 {
				return commandError("transcription_probe_interval_invalid", "usage", "--probe-interval must be zero or positive", ExitUsage, nil)
			}
			if probeInterval == 0 {
				probeInterval = transcriptionapi.DefaultProbeInterval
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
				_ = store.Close()
				return err
			}
			probeEnabled := strings.TrimSpace(fixtureDir) != ""
			var authCoordinator *transcriptionapi.AuthRefreshCoordinator
			if authRefreshScheduleEnabled(authRefreshInterval) {
				authCoordinator = transcriptionapi.NewAuthRefreshCoordinator(registry, authRefreshInterval)
				authCoordinator.SetScheduleOffset(authRefreshOffset)
			}
			var probeCoordinator *transcriptionapi.SyntheticProbeCoordinator
			var probeHealth *transcriptionapi.ProbeHealth
			if probeEnabled {
				fixtures, fixtureErr := transcriptionapi.LoadFixtureCatalog(fixtureDir)
				if fixtureErr != nil {
					_ = store.Close()
					return fmt.Errorf("validate transcription fixture corpus: %w", fixtureErr)
				}
				probeCoordinator, err = transcriptionapi.NewSyntheticProbeCoordinator(
					registry,
					fixtures,
					stateStore.Dir,
					probeInterval,
					transcriptionapi.DefaultProbeTimeout,
					transcriptionapi.DefaultProbeMaxAge,
				)
				if err != nil {
					_ = store.Close()
					return err
				}
				probeHealth = probeCoordinator.Health()
			}
			server, err := transcriptionapi.NewServer(transcriptionapi.ServerConfig{
				Registry:         registry,
				Store:            store,
				DefaultProvider:  transcriptionapi.ProviderID(strings.TrimSpace(defaultProvider)),
				Address:          strings.TrimSpace(address),
				HTTPAddress:      strings.TrimSpace(httpAddress),
				TLSCertFile:      tlsFiles.CertFile,
				TLSKeyFile:       tlsFiles.KeyFile,
				AuthCoordinator:  authCoordinator,
				ProbeHealth:      probeHealth,
				ProbeCoordinator: probeCoordinator,
				Logger:           slog.New(slog.NewJSONHandler(a.err, nil)),
			})
			if err != nil {
				_ = store.Close()
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
					"auth_refresh_offset":   authRefreshOffset.String(),
					"auth_refresh_enabled":  authCoordinator != nil,
					"probe_interval":        probeInterval.String(),
					"probe_enabled":         probeCoordinator != nil,
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
	cmd.Flags().StringVar(&address, "address", envDefault("CDP_TRANSCRIPTION_ADDRESS", transcriptionapi.DefaultListenAddress), "primary listen address; defaults to [::]:28765 (dual-stack wildcard where IPv6 is enabled)")
	cmd.Flags().StringVar(&httpAddress, "http-address", envDefault("CDP_TRANSCRIPTION_HTTP_ADDRESS", transcriptionapi.DefaultHTTPListenAddress), "cleartext companion listen address; defaults to [::]:28766 (dual-stack wildcard where IPv6 is enabled)")
	cmd.Flags().StringVar(&defaultProvider, "default-provider", envDefault("CDP_TRANSCRIPTION_PROVIDER", string(transcriptionapi.ProviderLocal)), "default provider: local, chatgpt-web, microsoft-365-web, or bing-web")
	cmd.Flags().StringSliceVar(&allowedProviders, "providers", envStringSlice("CDP_TRANSCRIPTION_PROVIDERS"), "provider allowlist; repeat or comma-separate (default: all configured providers)")
	cmd.Flags().StringVar(&localBaseURL, "local-base-url", os.Getenv("CDP_TRANSCRIPTION_LOCAL_BASE_URL"), "local OpenAI-compatible provider base URL, usually ending in /v1")
	cmd.Flags().StringVar(&localRealtimeBaseURL, "local-realtime-base-url", os.Getenv("CDP_TRANSCRIPTION_LOCAL_REALTIME_BASE_URL"), "optional separate local realtime provider base URL, usually ending in /v1")
	cmd.Flags().StringVar(&localAPIKey, "local-api-key", os.Getenv("CDP_TRANSCRIPTION_LOCAL_API_KEY"), "API key for the configured local provider")
	cmd.Flags().Int64Var(&maxAudioBytes, "max-audio-bytes", envInt64("CDP_TRANSCRIPTION_MAX_AUDIO_BYTES", transcriptionapi.DefaultMaxAudioBytes), "maximum retained audio-cache bytes; transcript records are retained independently")
	cmd.Flags().DurationVar(&authRefreshInterval, "auth-refresh-interval", envDuration("CDP_TRANSCRIPTION_AUTH_REFRESH_INTERVAL", transcriptionapi.DefaultAuthRefreshInterval), "shared recurring freshness check for all online providers; use 0s to disable")
	cmd.Flags().DurationVar(&authRefreshOffset, "auth-refresh-offset", envDuration("CDP_TRANSCRIPTION_AUTH_REFRESH_OFFSET", 0), "wall-clock phase offset for recurring auth refreshes; must be shorter than the interval")
	cmd.Flags().StringVar(&fixtureDir, "fixture-dir", os.Getenv("CDP_TRANSCRIPTION_FIXTURE_DIR"), "checked-in WebM corpus used by the bounded provider health probe; empty disables probe scheduling for transient runs")
	cmd.Flags().DurationVar(&probeInterval, "probe-interval", envDuration("CDP_TRANSCRIPTION_PROBE_INTERVAL", transcriptionapi.DefaultProbeInterval), "interval between bounded synthetic provider health probes")
	cmd.Flags().BoolVar(&persistAudio, "persist-audio", envBool("CDP_TRANSCRIPTION_PERSIST_AUDIO"), "retain uploaded audio under the state directory; default is ephemeral transaction media")
	cmd.Flags().StringVar(&tlsCertFile, "tls-cert", os.Getenv("CDP_TRANSCRIPTION_TLS_CERT"), "TLS certificate file; provide with --tls-key for HTTPS microphone access over LAN")
	cmd.Flags().StringVar(&tlsKeyFile, "tls-key", os.Getenv("CDP_TRANSCRIPTION_TLS_KEY"), "TLS private key file; provide with --tls-cert")
	cmd.Flags().BoolVar(&tlsSelfSigned, "tls-self-signed", false, "generate or reuse a private-LAN self-signed certificate under the cdp state directory")
	cmd.Flags().StringSliceVar(&tlsHosts, "tls-host", nil, "DNS name or IP to include in a self-signed certificate; repeat for multiple names")
	cmd.Flags().BoolVar(&tlsRegenerate, "tls-regenerate", false, "replace the generated self-signed certificate and key")
	cmd.Flags().BoolVar(&printReady, "print-ready", false, "print one readiness JSON object before serving")
	return cmd
}

// authRefreshScheduleEnabled makes lifecycle repair default-on for online
// providers. The explicit zero interval is the only opt-out; synthetic probes
// remain browser-free and are ordered behind the first lifecycle pass.
func authRefreshScheduleEnabled(interval time.Duration) bool {
	return interval > 0
}

func (a *app) transcriptionRegistry(ctx context.Context, localBaseURL, localRealtimeBaseURL, localAPIKey string, allowedProviders []string) (*transcriptionapi.Registry, error) {
	policy, err := a.providerPolicy()
	if err != nil {
		return nil, err
	}
	providers := make([]transcriptionapi.Provider, 0, 4)
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
	providers = append(providers, &bingTranscriptionProvider{app: a})
	stateStore, err := a.stateStore()
	if err != nil {
		providers, filterErr := filterTranscriptionProviders(providers, allowedProviders)
		if filterErr != nil {
			return nil, filterErr
		}
		return transcriptionapi.NewRegistryWithPolicy(policy, providers...), nil
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
	return transcriptionapi.NewRegistryWithPolicy(policy, providers...), nil
}

// headedProviderRepairMayUseOwnedTarget keeps transcription independent of
// unrelated Chrome work. The browserflow engine still creates one exact,
// disposable target for the operation and closes it; this only prevents a
// foreign tab or renderer from making the request wait behind a global budget.
func (a *app) headedProviderRepairMayUseOwnedTarget(operation webagent.Operation) bool {
	return a.opts.allowOverBudget ||
		operation == webagent.OperationTranscribe ||
		operation == webagent.OperationAuthRefresh ||
		operation == webagent.OperationCapabilities
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

type bingTranscriptionProvider struct {
	app        *app
	transcribe func(context.Context, bing.TranscribeConfig, string, int64) webagent.Result
}

func (p *bingTranscriptionProvider) ID() transcriptionapi.ProviderID {
	return transcriptionapi.ProviderBing
}

func (p *bingTranscriptionProvider) Capabilities(context.Context) transcriptionapi.ProviderCapability {
	return transcriptionapi.ProviderCapability{
		Provider:    p.ID(),
		Models:      []string{transcriptionapi.DefaultModel},
		File:        true,
		Translation: false,
		Streaming:   false,
		Realtime:    false,
		Ready:       true,
	}
}

func (p *bingTranscriptionProvider) Transcribe(ctx context.Context, request transcriptionapi.FileRequest) (transcriptionapi.Result, error) {
	if request.Task == transcriptionapi.TaskTranslate {
		return transcriptionapi.Result{}, transcriptionProviderError(501, "unsupported", "translation_unsupported", "Bing voice transcription does not expose translation", false)
	}
	duration, err := providerAudioDuration(ctx, request)
	if err != nil {
		return transcriptionapi.Result{}, err
	}
	transcribe := p.transcribe
	if transcribe == nil {
		transcribe = bing.Transcribe
	}
	result := transcribe(ctx, bing.TranscribeConfig{
		BuildCommit: p.app.build.Commit,
		Dial:        bing.Dial,
		Language:    request.Language,
	}, request.Audio.PersistedPath, duration)
	if !result.OK {
		return transcriptionapi.Result{}, webAgentProviderError(result)
	}
	data, ok := result.Data.(bing.TranscriptionData)
	if !ok {
		return transcriptionapi.Result{}, transcriptionProviderError(502, "provider", "response_changed", "Bing transcription result shape changed", true)
	}
	return transcriptionapi.Result{Task: request.Task, Text: data.Transcript}, nil
}

func (p *bingTranscriptionProvider) NewRealtime(context.Context, transcriptionapi.RealtimeSessionConfig) (transcriptionapi.RealtimeSession, error) {
	return nil, transcriptionProviderError(501, "unsupported", "realtime_unsupported", "Bing voice transcription currently accepts completed audio files", false)
}

type chatGPTTranscriptionProvider struct {
	app        *app
	store      *chatgpt.Store
	authMu     contextMutex
	transcribe func(context.Context, chatgpt.TranscribeConfig, string, int64) webagent.Result
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
	status := p.store.AuthStatus(ctx, now, chatgpt.DefaultAuthTTL)
	locked, err := lockProviderAuthRefresh(ctx, &p.authMu, status.Ready, status.ExpiresAt, now)
	if err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer p.authMu.Unlock()
	return p.ensureAuthFreshLocked(ctx)
}

func (p *chatGPTTranscriptionProvider) ensureAuthFreshLocked(ctx context.Context) error {
	now := time.Now()
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

func (p *chatGPTTranscriptionProvider) EnsureCapabilitiesFresh(ctx context.Context) error {
	if err := p.authMu.Lock(ctx); err != nil {
		return err
	}
	defer p.authMu.Unlock()
	if err := p.ensureAuthFreshLocked(ctx); err != nil {
		return err
	}
	now := time.Now()
	status := p.store.RuntimeStatus(ctx, now, chatgpt.DefaultCapabilitiesTTL)
	if status.Ready && !authEvidenceExpiringSoon(status.ExpiresAt, now) {
		return nil
	}
	return p.refreshCapabilitiesLocked(ctx)
}

func (p *chatGPTTranscriptionProvider) Transcribe(ctx context.Context, request transcriptionapi.FileRequest) (transcriptionapi.Result, error) {
	if request.Task == transcriptionapi.TaskTranslate {
		return transcriptionapi.Result{}, transcriptionProviderError(501, "unsupported", "translation_unsupported", "ChatGPT web transcription adapter does not expose Whisper translation", false)
	}
	duration, err := providerAudioDuration(ctx, request)
	if err != nil {
		return transcriptionapi.Result{}, err
	}
	// File transcription deliberately uses the persisted request template and
	// direct HTTP replay. Headed cdp is reserved for bounded auth/capability
	// refresh, so a normal request never opens or attaches to a browser target.
	transcribe := p.transcribe
	if transcribe == nil {
		transcribe = chatgpt.Transcribe
	}
	refreshAuth := p.refreshAuth
	var browserFallback func(context.Context, chatgpt.TranscribeConfig, string, int64) webagent.Result
	if !request.SyntheticProbe {
		browserFallback = func(
			fallbackContext context.Context,
			fallbackConfig chatgpt.TranscribeConfig,
			filePath string,
			durationMilliseconds int64,
		) webagent.Result {
			if !p.app.selectHeadedProviderRuntime() {
				return chatgpt.UnavailableOperation(
					p.app.build.Commit,
					webagent.OperationTranscribe,
					"chatgpt_headed_browser_required",
					"connection",
					"ChatGPT headed browser fallback is unavailable",
				)
			}
			browserConfig, refreshedStore, unavailable := p.app.chatgptBrowserOperationConfig(
				fallbackContext,
				webagent.OperationTranscribe,
			)
			if unavailable != nil {
				return *unavailable
			}
			fallbackConfig.Browser = &browserConfig
			fallbackConfig.Store = refreshedStore
			fallbackConfig.BrowserFallback = nil
			return transcribe(fallbackContext, fallbackConfig, filePath, durationMilliseconds)
		}
	}
	result := transcribe(ctx, chatgpt.TranscribeConfig{
		Store:           p.store,
		BuildCommit:     p.app.build.Commit,
		RefreshAuth:     refreshAuth,
		AudioFileName:   request.Audio.FileName,
		AudioMIMEType:   request.Audio.MIMEType,
		BrowserFallback: browserFallback,
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
	if err := p.authMu.Lock(ctx); err != nil {
		return err
	}
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

func (p *chatGPTTranscriptionProvider) refreshCapabilitiesLocked(ctx context.Context) error {
	if !p.app.selectHeadedProviderRuntime() {
		return fmt.Errorf("ChatGPT headed browser runtime is unavailable for capability repair")
	}
	browserConfig, refreshedStore, unavailable := p.app.chatgptBrowserOperationConfig(ctx, webagent.OperationCapabilities)
	if unavailable != nil {
		if unavailable.Error != nil {
			return fmt.Errorf("%s", unavailable.Error.Message)
		}
		return fmt.Errorf("ChatGPT headed browser capability repair is unavailable")
	}
	result := chatgpt.RefreshCapabilities(ctx, chatgpt.CapabilityRefreshConfig{BrowserConfig: browserConfig, Store: refreshedStore})
	if !result.OK {
		return webAgentProviderError(result)
	}
	return nil
}

type m365TranscriptionProvider struct {
	app    *app
	store  *m365.Store
	authMu contextMutex
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
	status := p.store.AuthStatus(ctx, now, m365.DefaultAuthTTL)
	locked, err := lockProviderAuthRefresh(ctx, &p.authMu, status.Ready, status.ExpiresAt, now)
	if err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer p.authMu.Unlock()
	return p.ensureAuthFreshLocked(ctx)
}

func (p *m365TranscriptionProvider) ensureAuthFreshLocked(ctx context.Context) error {
	now := time.Now()
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

func (p *m365TranscriptionProvider) EnsureCapabilitiesFresh(ctx context.Context) error {
	if err := p.authMu.Lock(ctx); err != nil {
		return err
	}
	defer p.authMu.Unlock()
	if err := p.ensureAuthFreshLocked(ctx); err != nil {
		return err
	}
	now := time.Now()
	status := p.store.RuntimeStatus(ctx, now, m365.DefaultCapabilitiesTTL)
	if status.Ready && !authEvidenceExpiringSoon(status.ExpiresAt, now) {
		return nil
	}
	return p.refreshCapabilitiesLocked(ctx)
}

func (p *m365TranscriptionProvider) Transcribe(ctx context.Context, request transcriptionapi.FileRequest) (transcriptionapi.Result, error) {
	if request.Task == transcriptionapi.TaskTranslate {
		return transcriptionapi.Result{}, transcriptionProviderError(501, "unsupported", "translation_unsupported", "Microsoft 365 dictation adapter does not expose translation", false)
	}
	duration, err := providerAudioDuration(ctx, request)
	if err != nil {
		return transcriptionapi.Result{}, err
	}
	refreshAuth := p.refreshAuth
	result := m365.Transcribe(ctx, m365.TranscribeConfig{
		Store:       p.store,
		BuildCommit: p.app.build.Commit,
		RefreshAuth: refreshAuth,
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
	if err := p.authMu.Lock(ctx); err != nil {
		return err
	}
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

func (p *m365TranscriptionProvider) refreshCapabilitiesLocked(ctx context.Context) error {
	if !p.app.selectHeadedProviderRuntime() {
		return fmt.Errorf("Microsoft 365 headed browser runtime is unavailable for capability repair")
	}
	browserConfig, refreshedStore, unavailable := p.app.m365BrowserOperationConfig(ctx, webagent.OperationCapabilities)
	if unavailable != nil {
		if unavailable.Error != nil {
			return fmt.Errorf("%s", unavailable.Error.Message)
		}
		return fmt.Errorf("Microsoft 365 headed browser capability repair is unavailable")
	}
	result := m365.RefreshCapabilities(ctx, m365.AuthRefreshConfig{BrowserConfig: browserConfig, Store: refreshedStore})
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
		return 0, transcriptionProviderError(422, "usage", "duration_required", "browser-backed transcription adapters need audio duration_ms or ffprobe on PATH", false)
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
	code := result.Error.Code
	message := result.Error.Message
	if isUnusableTranscriptionResult(code) {
		code = "provider_transcript_unavailable"
		message = "The transcription provider did not return a usable result; retry the saved audio"
	}
	return transcriptionProviderError(status, "provider", code, message, result.Error.RetrySafe)
}

func isUnusableTranscriptionResult(code string) bool {
	normalized := strings.ToLower(strings.TrimSpace(code))
	return strings.Contains(normalized, "response_changed") || strings.Contains(normalized, "shape")
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

func envStringSlice(name string) []string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func authEvidenceExpiringSoon(expiresAt string, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(expiresAt))
	if err != nil {
		return true
	}
	return !now.UTC().Add(15 * time.Minute).Before(expires.UTC())
}

func lockProviderAuthRefresh(
	ctx context.Context,
	mutex *contextMutex,
	ready bool,
	expiresAt string,
	now time.Time,
) (bool, error) {
	if ready {
		if !authEvidenceExpiringSoon(expiresAt, now) {
			return false, nil
		}
		// A still-valid template remains usable while one proactive refresh owns
		// the headed browser. The 15-minute margin leaves that refresh bounded
		// repair time without placing user requests behind its mutex.
		if !mutex.TryLock() {
			return false, nil
		}
		return true, nil
	}
	if err := mutex.Lock(ctx); err != nil {
		return false, err
	}
	return true, nil
}
