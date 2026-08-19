package transcriptionapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrProviderUnavailable = errors.New("transcription provider is unavailable")
	ErrProviderUnsupported = errors.New("transcription provider operation is unsupported")
)

// Provider is the only effect boundary used by the HTTP server. Provider
// adapters may use a local model, a browser-observed web API, or another
// OpenAI-compatible service, but the server sees one stable result shape.
type Provider interface {
	ID() ProviderID
	Capabilities(context.Context) ProviderCapability
	Transcribe(context.Context, FileRequest) (Result, error)
	NewRealtime(context.Context, RealtimeSessionConfig) (RealtimeSession, error)
}

// RealtimeSession receives normalized PCM chunks and returns provider events.
// Adapters own wire details; the server owns protocol framing, persistence,
// ordering, and reduction.
type RealtimeSession interface {
	Append(context.Context, []byte) ([]ProviderEvent, error)
	Commit(context.Context) ([]ProviderEvent, error)
	Close() error
}

type ProviderError struct {
	APIError  APIError
	Status    int
	Retryable bool
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ErrProviderUnavailable.Error()
	}
	if strings.TrimSpace(e.APIError.Message) != "" {
		return e.APIError.Message
	}
	return ErrProviderUnavailable.Error()
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return ErrProviderUnavailable
	}
	if e.APIError.Type == "unsupported" {
		return ErrProviderUnsupported
	}
	return ErrProviderUnavailable
}

func providerError(status int, kind, code, message string, retryable bool) error {
	return &ProviderError{
		Status:    status,
		Retryable: retryable,
		APIError: APIError{
			Type:    kind,
			Code:    code,
			Message: message,
		},
	}
}

// Registry is a deterministic provider catalog. It deliberately does not
// perform auth refresh or capability refresh itself; those effects belong to
// the adapter and are bounded by the shared provider implementation.
type Registry struct {
	providers map[ProviderID]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	registry := &Registry{providers: make(map[ProviderID]Provider, len(providers))}
	for _, provider := range providers {
		if provider == nil || strings.TrimSpace(string(provider.ID())) == "" {
			continue
		}
		registry.providers[provider.ID()] = provider
	}
	return registry
}

func (r *Registry) Provider(id ProviderID) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	provider, ok := r.providers[id]
	return provider, ok
}

func (r *Registry) Capabilities(ctx context.Context) []ProviderCapability {
	if r == nil {
		return []ProviderCapability{}
	}
	capabilities := make([]ProviderCapability, 0, len(r.providers))
	for _, provider := range r.providers {
		capabilities = append(capabilities, provider.Capabilities(ctx))
	}
	// Provider IDs are small and stable; sorting prevents map iteration from
	// making health/model responses nondeterministic.
	for i := 1; i < len(capabilities); i++ {
		for j := i; j > 0 && capabilities[j].Provider < capabilities[j-1].Provider; j-- {
			capabilities[j], capabilities[j-1] = capabilities[j-1], capabilities[j]
		}
	}
	return capabilities
}

func (r *Registry) Select(id ProviderID, fallback ProviderID) (Provider, error) {
	if strings.TrimSpace(string(id)) != "" {
		if provider, ok := r.Provider(id); ok {
			return provider, nil
		}
		return nil, providerError(503, "provider_unavailable", "provider_not_configured", fmt.Sprintf("provider %q is not configured", id), false)
	}
	if provider, ok := r.Provider(fallback); ok {
		return provider, nil
	}
	return nil, providerError(503, "provider_unavailable", "default_provider_not_configured", "no default transcription provider is configured", false)
}

// AuthRefreshers returns the optional online-provider lifecycle hooks in
// deterministic provider order.
func (r *Registry) AuthRefreshers() []AuthRefresher {
	if r == nil {
		return []AuthRefresher{}
	}
	ids := make([]ProviderID, 0, len(r.providers))
	for id, provider := range r.providers {
		if _, ok := provider.(AuthRefresher); ok {
			ids = append(ids, id)
		}
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	result := make([]AuthRefresher, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.providers[id].(AuthRefresher))
	}
	return result
}
