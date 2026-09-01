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
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
	"github.com/pankaj28843/cdp-cli/internal/transcriptionapi"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
	"github.com/pankaj28843/cdp-cli/internal/webagent/bing"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
	"github.com/pankaj28843/cdp-cli/internal/webagent/claude"
	"github.com/pankaj28843/cdp-cli/internal/webagent/gemini"
	"github.com/pankaj28843/cdp-cli/internal/webagent/m365"
	"github.com/spf13/cobra"
)

func (a *app) newTranscriptionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transcription",
		Short: "Run the provider-neutral OpenAI-compatible transcription service",
		Long: "Expose one local REST, SSE, and realtime WebSocket boundary for VoxInput. " +
			"Audio is ephemeral transaction media by default and can be explicitly retained with --persist-audio; " +
			"online provider auth and capability refresh is shared by the service and remains inside the cdp workflow adapter. " +
			"Recurring provider health probes cap decoded PCM and ffprobe diagnostics and terminate only their owned subprocess group on cancellation. " +
			"Browser-backed provider converters use the same owned process-group boundary.",
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
	var authRefreshMode string
	var externalAuthRefreshCommand string
	var authRefreshInterval time.Duration
	var authRefreshOffset time.Duration
	var authRefreshAPIEnabled bool
	var authRefreshRequestMinAge time.Duration
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
			validatedAuthRefreshMode, err := validateTranscriptionAuthRefreshMode(authRefreshMode, authRefreshInterval)
			if err != nil {
				return err
			}
			if strings.TrimSpace(externalAuthRefreshCommand) != "" && !filepath.IsAbs(strings.TrimSpace(externalAuthRefreshCommand)) {
				return commandError("transcription_external_auth_refresh_command_invalid", "usage", "--external-auth-refresh-command must be an absolute executable path", ExitUsage, nil)
			}
			if authRefreshAPIEnabled && validatedAuthRefreshMode != transcriptionapi.AuthRefreshModeLocal {
				return commandError("transcription_auth_refresh_api_owner_invalid", "usage", "--auth-refresh-api requires --auth-refresh-mode local", ExitUsage, nil)
			}
			if authRefreshAPIEnabled && authRefreshRequestMinAge <= 0 {
				return commandError("transcription_auth_refresh_request_min_age_invalid", "usage", "--auth-refresh-request-min-age must be positive when the refresh API is enabled", ExitUsage, nil)
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
			registry, err := a.transcriptionRegistry(cmd.Context(), localBaseURL, localRealtimeBaseURL, localAPIKey, allowedProviders, validatedAuthRefreshMode, externalAuthRefreshCommand)
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
				Registry:                 registry,
				Store:                    store,
				DefaultProvider:          transcriptionapi.ProviderID(strings.TrimSpace(defaultProvider)),
				Address:                  strings.TrimSpace(address),
				HTTPAddress:              strings.TrimSpace(httpAddress),
				TLSCertFile:              tlsFiles.CertFile,
				TLSKeyFile:               tlsFiles.KeyFile,
				AuthCoordinator:          authCoordinator,
				ProbeHealth:              probeHealth,
				ProbeCoordinator:         probeCoordinator,
				AuthRefreshAPIEnabled:    authRefreshAPIEnabled,
				AuthRefreshRequestMinAge: authRefreshRequestMinAge,
				Logger:                   slog.New(slog.NewJSONHandler(a.err, nil)),
			})
			if err != nil {
				_ = store.Close()
				return err
			}
			if printReady {
				tlsEnabled := strings.TrimSpace(tlsFiles.CertFile) != ""
				ready := map[string]any{
					"ok":                           true,
					"address":                      address,
					"http_address":                 httpAddress,
					"contract_version":             transcriptionapi.ContractVersion,
					"state_dir":                    store.Root(),
					"auth_refresh_mode":            validatedAuthRefreshMode,
					"auth_refresh_interval":        authRefreshInterval.String(),
					"auth_refresh_offset":          authRefreshOffset.String(),
					"auth_refresh_enabled":         authCoordinator != nil,
					"auth_refresh_api_enabled":     authRefreshAPIEnabled,
					"auth_refresh_request_min_age": authRefreshRequestMinAge.String(),
					"probe_interval":               probeInterval.String(),
					"probe_enabled":                probeCoordinator != nil,
					"audio_persisted":              persistAudio,
					"tls_enabled":                  tlsEnabled,
					"tls_cert_file":                tlsFiles.CertFile,
					"tls_hosts":                    tlsFiles.Hosts,
					"tls_reused":                   tlsFiles.Reused,
					"demo_url":                     preferredDemoURL(address, tlsEnabled, tlsFiles.Hosts),
					"demo_urls":                    demoURLs(address, tlsEnabled, tlsFiles.Hosts),
					"providers":                    registry.Capabilities(cmd.Context()),
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
	cmd.Flags().StringVar(&defaultProvider, "default-provider", envDefault("CDP_TRANSCRIPTION_PROVIDER", string(transcriptionapi.ProviderLocal)), "default provider: local, bing-web, chatgpt-web, claude-web, gemini-web, or microsoft-365-web")
	cmd.Flags().StringSliceVar(&allowedProviders, "providers", envStringSlice("CDP_TRANSCRIPTION_PROVIDERS"), "provider allowlist; repeat or comma-separate (default: all configured providers)")
	cmd.Flags().StringVar(&localBaseURL, "local-base-url", os.Getenv("CDP_TRANSCRIPTION_LOCAL_BASE_URL"), "local OpenAI-compatible provider base URL, usually ending in /v1")
	cmd.Flags().StringVar(&localRealtimeBaseURL, "local-realtime-base-url", os.Getenv("CDP_TRANSCRIPTION_LOCAL_REALTIME_BASE_URL"), "optional separate local realtime provider base URL, usually ending in /v1")
	cmd.Flags().StringVar(&localAPIKey, "local-api-key", os.Getenv("CDP_TRANSCRIPTION_LOCAL_API_KEY"), "API key for the configured local provider")
	cmd.Flags().Int64Var(&maxAudioBytes, "max-audio-bytes", envInt64("CDP_TRANSCRIPTION_MAX_AUDIO_BYTES", transcriptionapi.DefaultMaxAudioBytes), "maximum retained audio-cache bytes; transcript records are retained independently")
	cmd.Flags().StringVar(&authRefreshMode, "auth-refresh-mode", envDefault("CDP_TRANSCRIPTION_AUTH_REFRESH_MODE", string(transcriptionapi.AuthRefreshModeLocal)), "auth repair owner: local or external; external requires a zero local refresh interval")
	cmd.Flags().StringVar(&externalAuthRefreshCommand, "external-auth-refresh-command", os.Getenv("CDP_TRANSCRIPTION_EXTERNAL_AUTH_REFRESH_COMMAND"), "absolute helper invoked as COMMAND refresh PROVIDER when externally managed state needs repair")
	cmd.Flags().DurationVar(&authRefreshInterval, "auth-refresh-interval", envDuration("CDP_TRANSCRIPTION_AUTH_REFRESH_INTERVAL", transcriptionapi.DefaultAuthRefreshInterval), "shared recurring freshness check for all online providers; use 0s to disable")
	cmd.Flags().DurationVar(&authRefreshOffset, "auth-refresh-offset", envDuration("CDP_TRANSCRIPTION_AUTH_REFRESH_OFFSET", 0), "wall-clock phase offset for recurring auth refreshes; must be shorter than the interval")
	cmd.Flags().BoolVar(&authRefreshAPIEnabled, "auth-refresh-api", envBool("CDP_TRANSCRIPTION_AUTH_REFRESH_API_ENABLED"), "allow cooldown-enforced provider auth refresh requests on this authority")
	cmd.Flags().DurationVar(&authRefreshRequestMinAge, "auth-refresh-request-min-age", envDuration("CDP_TRANSCRIPTION_AUTH_REFRESH_REQUEST_MIN_AGE", 45*time.Minute), "minimum authority-state age before the refresh API may open a provider tab")
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
// remain distinct from refresh and are ordered behind the first lifecycle pass.
func authRefreshScheduleEnabled(interval time.Duration) bool {
	return interval > 0
}

func validateTranscriptionAuthRefreshMode(raw string, interval time.Duration) (transcriptionapi.AuthRefreshMode, error) {
	mode, err := transcriptionapi.ParseAuthRefreshMode(raw)
	if err != nil {
		return "", commandError("transcription_auth_refresh_mode_invalid", "usage", "--auth-refresh-mode must be local or external", ExitUsage, nil)
	}
	if err := mode.Validate(interval); err != nil {
		return "", commandError("transcription_external_auth_refresh_schedule_invalid", "usage", "--auth-refresh-mode external requires --auth-refresh-interval 0s", ExitUsage, nil)
	}
	return mode, nil
}

func externalAuthRefreshRunner(command string) func(context.Context, transcriptionapi.ProviderID) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	return func(ctx context.Context, provider transcriptionapi.ProviderID) error {
		return exec.CommandContext(ctx, command, "refresh", string(provider)).Run()
	}
}

func (a *app) transcriptionRegistry(ctx context.Context, localBaseURL, localRealtimeBaseURL, localAPIKey string, allowedProviders []string, authRefreshMode transcriptionapi.AuthRefreshMode, externalAuthRefreshCommand string) (*transcriptionapi.Registry, error) {
	policy, err := a.providerPolicy()
	if err != nil {
		return nil, err
	}
	providers := make([]transcriptionapi.Provider, 0, 6)
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
	externalRefresh := externalAuthRefreshRunner(externalAuthRefreshCommand)
	if chatErr == nil {
		providers = append(providers, &chatGPTTranscriptionProvider{app: a, store: chatStore, externalAuth: authRefreshMode == transcriptionapi.AuthRefreshModeExternal, externalRefresh: externalRefresh})
	}
	claudeStore, claudeErr := claude.NewStore(stateStore.Dir)
	if claudeErr == nil {
		providers = append(providers, &claudeTranscriptionProvider{app: a, store: claudeStore, externalAuth: authRefreshMode == transcriptionapi.AuthRefreshModeExternal, externalRefresh: externalRefresh})
	}
	geminiStore, geminiErr := gemini.NewStore(stateStore.Dir)
	if geminiErr == nil {
		providers = append(providers, &geminiTranscriptionProvider{app: a, store: geminiStore, externalAuth: authRefreshMode == transcriptionapi.AuthRefreshModeExternal, externalRefresh: externalRefresh})
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

type authRepairFlight struct {
	generation string
	done       chan struct{}
	err        error
}

type authRepairGroup struct {
	mu     sync.Mutex
	flight *authRepairFlight
}

func (g *authRepairGroup) Do(ctx context.Context, generation string, refresh func(context.Context) error) error {
	g.mu.Lock()
	if flight := g.flight; flight != nil && flight.generation == generation {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-flight.done:
			return flight.err
		}
	}
	flight := &authRepairFlight{generation: generation, done: make(chan struct{})}
	g.flight = flight
	g.mu.Unlock()

	err := refresh(ctx)
	g.mu.Lock()
	flight.err = err
	if g.flight == flight {
		g.flight = nil
	}
	close(flight.done)
	g.mu.Unlock()
	return err
}

type chatGPTTranscriptionProvider struct {
	app             *app
	store           *chatgpt.Store
	externalAuth    bool
	externalRefresh func(context.Context, transcriptionapi.ProviderID) error
	authMu          contextMutex
	authRepair      authRepairGroup
	refresh         func(context.Context) error
	transcribe      func(context.Context, chatgpt.TranscribeConfig, string, int64) webagent.Result
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

func (p *chatGPTTranscriptionProvider) Transcribe(ctx context.Context, request transcriptionapi.FileRequest) (transcriptionapi.Result, error) {
	if request.Task == transcriptionapi.TaskTranslate {
		return transcriptionapi.Result{}, transcriptionProviderError(501, "unsupported", "translation_unsupported", "ChatGPT web transcription adapter does not expose Whisper translation", false)
	}
	duration, err := providerAudioDuration(ctx, request)
	if err != nil {
		return transcriptionapi.Result{}, err
	}
	// File transcription deliberately uses the persisted request template and
	// direct HTTP replay. Headed cdp is reserved for bounded auth refresh, so a
	// normal request never opens or attaches to a browser target. Runtime
	// capability discovery remains an explicit diagnostic workflow because the
	// transcription contract does not consume it.
	transcribe := p.transcribe
	if transcribe == nil {
		transcribe = chatgpt.Transcribe
	}
	authGeneration := p.store.AuthStatus(ctx, time.Now(), chatgpt.DefaultAuthTTL).CapturedAt
	result := transcribe(ctx, chatgpt.TranscribeConfig{
		Store:       p.store,
		BuildCommit: p.app.build.Commit,
		MaxAttempts: 2,
		RefreshAuth: func(refreshContext context.Context) error {
			return p.repairAuth(refreshContext, authGeneration)
		},
		AudioFileName: request.Audio.FileName,
		AudioMIMEType: request.Audio.MIMEType,
	}, request.Audio.PersistedPath, duration)
	if !result.OK {
		return transcriptionapi.Result{}, withoutOuterProviderRetry(webAgentProviderError(result))
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

func (p *chatGPTTranscriptionProvider) repairAuth(ctx context.Context, observedGeneration string) error {
	return p.authRepair.Do(ctx, observedGeneration, func(refreshContext context.Context) error {
		if err := p.authMu.Lock(refreshContext); err != nil {
			return err
		}
		defer p.authMu.Unlock()
		currentGeneration := p.store.AuthStatus(refreshContext, time.Now(), chatgpt.DefaultAuthTTL).CapturedAt
		if currentGeneration != observedGeneration {
			return nil
		}
		return p.refreshAuthLocked(refreshContext)
	})
}

func (p *chatGPTTranscriptionProvider) refreshAuthLocked(ctx context.Context) error {
	if p.externalAuth {
		return requestExternalAuthRefresh(ctx, p.ID(), p.externalRefresh)
	}
	if p.refresh != nil {
		return p.refresh(ctx)
	}
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

func (p *chatGPTTranscriptionProvider) AuthCapturedAt(ctx context.Context) (time.Time, bool) {
	return parseAuthCapturedAt(p.store.AuthStatus(ctx, time.Now(), chatgpt.DefaultAuthTTL).CapturedAt)
}

func (p *chatGPTTranscriptionProvider) RefreshAuthNow(ctx context.Context, observed time.Time) (bool, error) {
	before := p.store.AuthStatus(ctx, time.Now(), chatgpt.DefaultAuthTTL).CapturedAt
	if err := p.repairAuth(ctx, formatAuthGeneration(observed)); err != nil {
		return false, err
	}
	after := p.store.AuthStatus(ctx, time.Now(), chatgpt.DefaultAuthTTL).CapturedAt
	return after != "" && after != before, nil
}

type claudeTranscriptionProvider struct {
	app             *app
	store           *claude.Store
	externalAuth    bool
	externalRefresh func(context.Context, transcriptionapi.ProviderID) error
	authMu          contextMutex
	authRepair      authRepairGroup
	refresh         func(context.Context) error
	transcribe      func(context.Context, claude.TranscribeConfig, string, int64) webagent.Result
}

func (p *claudeTranscriptionProvider) ID() transcriptionapi.ProviderID {
	return transcriptionapi.ProviderClaude
}

func (p *claudeTranscriptionProvider) Capabilities(ctx context.Context) transcriptionapi.ProviderCapability {
	auth := p.store.Status(ctx, time.Now(), claude.DefaultAuthTTL)
	return transcriptionapi.ProviderCapability{
		Provider:    p.ID(),
		Models:      []string{transcriptionapi.DefaultModel},
		File:        true,
		Translation: false,
		Streaming:   false,
		Realtime:    false,
		Ready:       auth.Ready,
		Reason:      auth.Reason,
	}
}

func (p *claudeTranscriptionProvider) EnsureAuthFresh(ctx context.Context) error {
	now := time.Now()
	status := p.store.Status(ctx, now, claude.DefaultAuthTTL)
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

func (p *claudeTranscriptionProvider) ensureAuthFreshLocked(ctx context.Context) error {
	status := p.store.Status(ctx, time.Now(), claude.DefaultAuthTTL)
	if status.Ready && !authEvidenceExpiringSoon(status.ExpiresAt, time.Now()) {
		return nil
	}
	err := p.refreshAuthLocked(ctx)
	return err
}

func (p *claudeTranscriptionProvider) EnsureCapabilitiesFresh(ctx context.Context) error {
	return p.EnsureAuthFresh(ctx)
}

func (p *claudeTranscriptionProvider) Transcribe(ctx context.Context, request transcriptionapi.FileRequest) (transcriptionapi.Result, error) {
	if request.Task == transcriptionapi.TaskTranslate {
		return transcriptionapi.Result{}, transcriptionProviderError(501, "unsupported", "translation_unsupported", "Claude web transcription adapter does not expose Whisper translation", false)
	}
	duration, err := providerAudioDuration(ctx, request)
	if err != nil {
		return transcriptionapi.Result{}, err
	}
	transcribe := p.transcribe
	if transcribe == nil {
		transcribe = claude.Transcribe
	}
	result := transcribe(ctx, claude.TranscribeConfig{
		Store:       p.store,
		BuildCommit: p.app.build.Commit,
		Language:    request.Language,
		RefreshAuth: func(refreshContext context.Context, rejectedGeneration string) error {
			return p.repairAuth(refreshContext, rejectedGeneration)
		},
	}, request.Audio.PersistedPath, duration)
	if !result.OK {
		return transcriptionapi.Result{}, withoutOuterProviderRetry(webAgentProviderError(result))
	}
	data, ok := result.Data.(claude.TranscriptionData)
	if !ok {
		return transcriptionapi.Result{}, transcriptionProviderError(502, "provider", "response_changed", "Claude transcription result shape changed", true)
	}
	return transcriptionapi.Result{Task: request.Task, Text: data.Transcript}, nil
}

func (p *claudeTranscriptionProvider) NewRealtime(context.Context, transcriptionapi.RealtimeSessionConfig) (transcriptionapi.RealtimeSession, error) {
	return nil, transcriptionProviderError(501, "unsupported", "realtime_unsupported", "Claude web transcription currently accepts completed audio files", false)
}

func (p *claudeTranscriptionProvider) repairAuth(ctx context.Context, observedGeneration string) error {
	return p.authRepair.Do(ctx, observedGeneration, func(refreshContext context.Context) error {
		if err := p.authMu.Lock(refreshContext); err != nil {
			return err
		}
		defer p.authMu.Unlock()
		currentStatus := p.store.Status(refreshContext, time.Now(), claude.DefaultAuthTTL)
		currentGeneration := currentStatus.CapturedAt
		if currentStatus.Ready && currentGeneration != "" && currentGeneration != observedGeneration {
			return nil
		}
		return p.refreshAuthLocked(refreshContext)
	})
}

func (p *claudeTranscriptionProvider) refreshAuthLocked(ctx context.Context) error {
	if p.externalAuth {
		return requestExternalAuthRefresh(ctx, p.ID(), p.externalRefresh)
	}
	if p.refresh != nil {
		return p.refresh(ctx)
	}
	result := p.app.refreshClaudeAuth(ctx)
	if !result.OK {
		return webAgentProviderError(result)
	}
	return nil
}

func (p *claudeTranscriptionProvider) AuthCapturedAt(ctx context.Context) (time.Time, bool) {
	return parseAuthCapturedAt(p.store.Status(ctx, time.Now(), claude.DefaultAuthTTL).CapturedAt)
}

func (p *claudeTranscriptionProvider) RefreshAuthNow(ctx context.Context, observed time.Time) (bool, error) {
	before := p.store.Status(ctx, time.Now(), claude.DefaultAuthTTL).CapturedAt
	if err := p.repairAuth(ctx, formatAuthGeneration(observed)); err != nil {
		return false, err
	}
	after := p.store.Status(ctx, time.Now(), claude.DefaultAuthTTL).CapturedAt
	return after != "" && after != before, nil
}

type geminiTranscriptionProvider struct {
	app             *app
	store           *gemini.Store
	externalAuth    bool
	externalRefresh func(context.Context, transcriptionapi.ProviderID) error
	authMu          contextMutex
	authRepair      authRepairGroup
	refresh         func(context.Context) error
	transcribe      func(context.Context, gemini.TranscribeConfig, string, int64) webagent.Result
}

func (p *geminiTranscriptionProvider) ID() transcriptionapi.ProviderID {
	return transcriptionapi.ProviderGemini
}

func (p *geminiTranscriptionProvider) Capabilities(ctx context.Context) transcriptionapi.ProviderCapability {
	status := p.store.TemplateStatus(ctx, time.Now(), gemini.DefaultAuthTTL)
	return transcriptionapi.ProviderCapability{
		Provider: p.ID(), Models: []string{transcriptionapi.DefaultModel},
		File: true, Translation: false, Streaming: false, Realtime: false,
		Ready: status.Ready, Reason: status.Reason,
	}
}

func (p *geminiTranscriptionProvider) EnsureAuthFresh(ctx context.Context) error {
	now := time.Now()
	status := p.store.TemplateStatus(ctx, now, gemini.DefaultAuthTTL)
	locked, err := lockProviderAuthRefresh(ctx, &p.authMu, status.Ready, status.ExpiresAt, now)
	if err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer p.authMu.Unlock()
	status = p.store.TemplateStatus(ctx, time.Now(), gemini.DefaultAuthTTL)
	if status.Ready && !authEvidenceExpiringSoon(status.ExpiresAt, time.Now()) {
		return nil
	}
	if err := p.refreshAuthLocked(ctx); err != nil {
		return err
	}
	return nil
}

func (p *geminiTranscriptionProvider) EnsureCapabilitiesFresh(ctx context.Context) error {
	return p.EnsureAuthFresh(ctx)
}

func (p *geminiTranscriptionProvider) Transcribe(ctx context.Context, request transcriptionapi.FileRequest) (transcriptionapi.Result, error) {
	if request.Task == transcriptionapi.TaskTranslate {
		return transcriptionapi.Result{}, transcriptionProviderError(501, "unsupported", "translation_unsupported", "Gemini dictation does not expose translation", false)
	}
	duration, err := providerAudioDuration(ctx, request)
	if err != nil {
		return transcriptionapi.Result{}, err
	}
	transcribe := p.transcribe
	if transcribe == nil {
		transcribe = gemini.Transcribe
	}
	authGeneration := p.store.TemplateStatus(ctx, time.Now(), gemini.DefaultAuthTTL).CapturedAt
	result := transcribe(ctx, gemini.TranscribeConfig{
		Store: p.store, BuildCommit: p.app.build.Commit,
		Language: request.Language,
		RefreshAuth: func(refreshContext context.Context, rejectedGeneration string) error {
			if rejectedGeneration == "" {
				rejectedGeneration = authGeneration
			}
			return p.repairAuth(refreshContext, rejectedGeneration)
		},
	}, request.Audio.PersistedPath, duration)
	if !result.OK {
		return transcriptionapi.Result{}, withoutOuterProviderRetry(webAgentProviderError(result))
	}
	data, ok := result.Data.(gemini.TranscriptionData)
	if !ok {
		return transcriptionapi.Result{}, transcriptionProviderError(502, "provider", "response_changed", "Gemini transcription result shape changed", true)
	}
	return transcriptionapi.Result{Task: request.Task, Text: data.Transcript}, nil
}

func (p *geminiTranscriptionProvider) NewRealtime(context.Context, transcriptionapi.RealtimeSessionConfig) (transcriptionapi.RealtimeSession, error) {
	return nil, transcriptionProviderError(501, "unsupported", "realtime_unsupported", "Gemini dictation currently accepts completed WebM files", false)
}

func (p *geminiTranscriptionProvider) repairAuth(ctx context.Context, observedGeneration string) error {
	return p.authRepair.Do(ctx, observedGeneration, func(refreshContext context.Context) error {
		if err := p.authMu.Lock(refreshContext); err != nil {
			return err
		}
		defer p.authMu.Unlock()
		currentGeneration := p.store.TemplateStatus(refreshContext, time.Now(), gemini.DefaultAuthTTL).CapturedAt
		if currentGeneration != "" && currentGeneration != observedGeneration {
			return nil
		}
		return p.refreshAuthLocked(refreshContext)
	})
}

func (p *geminiTranscriptionProvider) refreshAuthLocked(ctx context.Context) error {
	if p.externalAuth {
		return requestExternalAuthRefresh(ctx, p.ID(), p.externalRefresh)
	}
	if p.refresh != nil {
		return p.refresh(ctx)
	}
	if !p.app.selectHeadedProviderRuntime() {
		return fmt.Errorf("Gemini headed browser runtime is unavailable for auth repair")
	}
	browserConfig, refreshedStore, unavailable := p.app.geminiBrowserOperationConfig(ctx, webagent.OperationAuthRefresh)
	if unavailable != nil {
		if unavailable.Error != nil {
			return fmt.Errorf("%s", unavailable.Error.Message)
		}
		return fmt.Errorf("Gemini headed browser auth repair is unavailable")
	}
	result := gemini.RefreshAuth(ctx, gemini.AuthRefreshConfig{BrowserConfig: browserConfig, Store: refreshedStore})
	if !result.OK {
		return webAgentProviderError(result)
	}
	return nil
}

func (p *geminiTranscriptionProvider) AuthCapturedAt(ctx context.Context) (time.Time, bool) {
	return parseAuthCapturedAt(p.store.TemplateStatus(ctx, time.Now(), gemini.DefaultAuthTTL).CapturedAt)
}

func (p *geminiTranscriptionProvider) RefreshAuthNow(ctx context.Context, observed time.Time) (bool, error) {
	before := p.store.TemplateStatus(ctx, time.Now(), gemini.DefaultAuthTTL).CapturedAt
	if err := p.repairAuth(ctx, formatAuthGeneration(observed)); err != nil {
		return false, err
	}
	after := p.store.TemplateStatus(ctx, time.Now(), gemini.DefaultAuthTTL).CapturedAt
	return after != "" && after != before, nil
}

func externalAuthRefreshRequired(provider transcriptionapi.ProviderID) error {
	return transcriptionProviderError(
		503,
		"provider_unavailable",
		"external_auth_refresh_required",
		fmt.Sprintf("%s auth state is managed externally; retry after provider state synchronization", provider),
		false,
	)
}

func requestExternalAuthRefresh(ctx context.Context, provider transcriptionapi.ProviderID, refresh func(context.Context, transcriptionapi.ProviderID) error) error {
	if refresh == nil {
		return externalAuthRefreshRequired(provider)
	}
	if err := refresh(ctx, provider); err != nil {
		return transcriptionProviderError(503, "provider_unavailable", "external_auth_refresh_failed", fmt.Sprintf("%s external auth refresh failed", provider), false)
	}
	return nil
}

func parseAuthCapturedAt(value string) (time.Time, bool) {
	capturedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return capturedAt.UTC(), true
}

func formatAuthGeneration(capturedAt time.Time) string {
	if capturedAt.IsZero() {
		return ""
	}
	return capturedAt.UTC().Format(time.RFC3339Nano)
}

type m365TranscriptionProvider struct {
	app        *app
	store      *m365.Store
	authMu     contextMutex
	transcribe func(context.Context, m365.TranscribeConfig, string, int64) webagent.Result
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
	transcribe := p.transcribe
	if transcribe == nil {
		transcribe = m365.Transcribe
	}
	result := transcribe(ctx, m365.TranscribeConfig{
		Store:       p.store,
		BuildCommit: p.app.build.Commit,
		MaxAttempts: 2,
		RefreshAuth: refreshAuth,
		Dial:        augloop.Dial,
	}, request.Audio.PersistedPath, duration)
	if !result.OK {
		return transcriptionapi.Result{}, withoutOuterProviderRetry(webAgentProviderError(result))
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
	output, truncated, err := runBoundedTranscriptionProbeCommand(ctx, ffprobe, []string{"-v", "error", "-show_entries", "format=duration:stream=duration", "-of", "default=noprint_wrappers=1:nokey=1", request.Audio.PersistedPath}, maxExternalProcessOutputBytes)
	if truncated {
		return 0, transcriptionProviderError(422, "usage", "duration_probe_output_too_large", "audio duration probe output exceeded its safety limit", false)
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if err == nil {
		if duration, ok := probeDurationMilliseconds(string(output)); ok {
			return duration, nil
		}
	}
	// Browser MediaRecorder WebM often has no container duration. Its packet
	// timestamps are sufficient and keep normal OpenAI-compatible clients from
	// having to know the VoxInput duration_ms extension.
	packets, truncated, packetErr := runBoundedTranscriptionProbeCommand(ctx, ffprobe, []string{"-v", "error", "-select_streams", "a:0", "-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", request.Audio.PersistedPath}, maxExternalProcessOutputBytes)
	if truncated {
		return 0, transcriptionProviderError(422, "usage", "duration_probe_output_too_large", "audio duration probe output exceeded its safety limit", false)
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
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

func withoutOuterProviderRetry(err error) error {
	var providerErr *transcriptionapi.ProviderError
	if !errors.As(err, &providerErr) || providerErr == nil || !providerErr.Retryable {
		return err
	}
	bounded := *providerErr
	bounded.Retryable = false
	return &bounded
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
