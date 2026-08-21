package webagent

import (
	"fmt"

	"github.com/pankaj28843/cdp-cli/internal/providerpolicy"
)

type CapabilityStatus string

const (
	CapabilityImplemented CapabilityStatus = "implemented"
	CapabilityPlanned     CapabilityStatus = "planned"
	CapabilityUnsupported CapabilityStatus = "unsupported"
)

type OperationCapability struct {
	Operation     Operation        `json:"operation"`
	Command       string           `json:"command"`
	Status        CapabilityStatus `json:"status"`
	Supported     bool             `json:"supported"`
	SideEffect    string           `json:"side_effect"`
	Browser       string           `json:"browser"`
	Summary       string           `json:"summary"`
	UnavailableBy string           `json:"unavailable_by,omitempty"`
}

type Capabilities struct {
	SchemaVersion        string                `json:"schema_version"`
	Provider             Provider              `json:"provider"`
	DisplayName          string                `json:"display_name"`
	ImplementationStatus string                `json:"implementation_status"`
	Availability         string                `json:"availability,omitempty"`
	Reason               string                `json:"reason,omitempty"`
	Operations           []OperationCapability `json:"operations"`
}

type CatalogData struct {
	SchemaVersion string         `json:"schema_version"`
	Providers     []Capabilities `json:"providers"`
}

type operationSpec struct {
	operation  Operation
	path       string
	sideEffect string
	browser    string
	summary    string
	providers  []Provider
}

var readableConversationProviders = []Provider{
	ProviderChatGPT,
	ProviderClaude,
	ProviderGemini,
	ProviderGrok,
	ProviderPerplexity,
	ProviderTripadvisor,
}

var mutableConversationProviders = []Provider{
	ProviderChatGPT,
	ProviderClaude,
	ProviderGemini,
	ProviderGrok,
	ProviderPerplexity,
}

var operationSpecs = []operationSpec{
	{
		operation:  OperationCapabilities,
		path:       "capabilities",
		sideEffect: "none",
		browser:    "none",
		summary:    "Report the installed provider operation contract without probing the browser.",
	},
	{
		operation:  OperationDoctor,
		path:       "doctor",
		sideEffect: "none",
		browser:    "provider_defined",
		summary:    "Report provider readiness and safe remediation.",
	},
	{
		operation:  OperationAuthRefresh,
		path:       "auth refresh",
		sideEffect: "auth_observation",
		browser:    "headed",
		summary:    "Observe and refresh local auth evidence without creating a conversation.",
	},
	{
		operation:  OperationTranscribe,
		path:       "transcribe",
		sideEffect: "provider_request",
		browser:    "none",
		summary:    "Send one persisted WebM audio file through an observed provider transcription transport; headed auth repair is bounded and lazy.",
		providers:  []Provider{ProviderChatGPT, ProviderM365},
	},
	{
		operation:  OperationAsk,
		path:       "ask",
		sideEffect: "conversation",
		browser:    "headed",
		summary:    "Submit one visible provider request and return its observed result.",
	},
	{
		operation:  OperationCatalogStatus,
		path:       "catalog status",
		sideEffect: "none",
		browser:    "none",
		summary:    "Report owner-only dynamic course and chapter catalog freshness without probing Chrome.",
		providers:  []Provider{ProviderAlex},
	},
	{
		operation:  OperationCatalogRefresh,
		path:       "catalog refresh",
		sideEffect: "catalog_write",
		browser:    "headed",
		summary:    "Discover current courses and chapter TOCs from one exact headed ByteByteGo target.",
		providers:  []Provider{ProviderAlex},
	},
	{
		operation:  OperationCoursesList,
		path:       "courses list",
		sideEffect: "none",
		browser:    "none",
		summary:    "List dynamically discovered ByteByteGo courses from owner-only catalog state.",
		providers:  []Provider{ProviderAlex},
	},
	{
		operation:  OperationChaptersList,
		path:       "chapters list",
		sideEffect: "none",
		browser:    "none",
		summary:    "List dynamically discovered chapters for one exact ByteByteGo course.",
		providers:  []Provider{ProviderAlex},
	},
	{
		operation:  OperationContentFetch,
		path:       "content fetch",
		sideEffect: "content_cache_write",
		browser:    "headed",
		summary:    "Read rendered exact-chapter content through one owned headed ByteByteGo target.",
		providers:  []Provider{ProviderAlex},
	},
	{
		operation:  OperationConversationsList,
		path:       "conversations list",
		sideEffect: "none",
		browser:    "provider_defined",
		summary:    "List stored provider conversations through a proven read path.",
		providers:  readableConversationProviders,
	},
	{
		operation:  OperationConversationsContinue,
		path:       "conversations continue",
		sideEffect: "conversation",
		browser:    "headed",
		summary:    "Continue one exact stored conversation through a provider-specific proven path.",
		providers:  []Provider{ProviderChatGPT},
	},
	{
		operation:  OperationConversationsDetail,
		path:       "conversations detail",
		sideEffect: "none",
		browser:    "provider_defined",
		summary:    "Read one exact stored provider conversation.",
		providers:  readableConversationProviders,
	},
	{
		operation:  OperationConversationsAwait,
		path:       "conversations await",
		sideEffect: "none",
		browser:    "provider_defined",
		summary:    "Wait for one exact conversation to reach a provider-defined terminal or incomplete state without resubmitting.",
		providers:  readableConversationProviders,
	},
	{
		operation:  OperationConversationsDelete,
		path:       "conversations delete",
		sideEffect: "destructive",
		browser:    "headed",
		summary:    "Delete one exact owned test conversation with a same-target postcondition.",
		providers:  mutableConversationProviders,
	},
	{
		operation:  OperationArtifactDownload,
		path:       "conversations download-artifact",
		sideEffect: "local_file_write",
		browser:    "headed",
		summary:    "Download one exact generated artifact from a finished assistant turn to an explicit local destination.",
		providers:  []Provider{ProviderChatGPT},
	},
	{
		operation:  OperationAttachmentsDownload,
		path:       "conversations download-attachments",
		sideEffect: "local_file_write",
		browser:    "provider_defined",
		summary:    "Export every attachment from one exact terminal ChatGPT answer as bounded original bytes and a deterministic owner-only manifest.",
		providers:  []Provider{ProviderChatGPT},
	},
	{
		operation:  OperationResearch,
		path:       "research",
		sideEffect: "conversation",
		browser:    "headed",
		summary:    "Submit one visible Deep Research request only when the exact runtime product control is proven.",
		providers:  []Provider{ProviderChatGPT},
	},
	{
		operation:  OperationResearchExport,
		path:       "conversations export-research",
		sideEffect: "none",
		browser:    "headed",
		summary:    "Export one exact completed Deep Research report only through a proven rendered target.",
		providers:  []Provider{ProviderChatGPT},
	},
}

func Providers() []Provider {
	return ProvidersFor(providerpolicy.Default())
}

func ParseProvider(value string) (Provider, bool) {
	canonical, ok := providerpolicy.Canonicalize(value)
	return Provider(canonical), ok
}

func Catalog() CatalogData {
	return CatalogFor(providerpolicy.Default(), false)
}

func ProvidersFor(policy providerpolicy.Policy) []Provider {
	descriptors := providerpolicy.Descriptors()
	providers := make([]Provider, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if policy.IsEnabled(string(descriptor.ID)) {
			providers = append(providers, Provider(descriptor.ID))
		}
	}
	return providers
}

func CatalogFor(policy providerpolicy.Policy, includeDisabled bool) CatalogData {
	providers := make([]Capabilities, 0, len(providerpolicy.Descriptors()))
	for _, descriptor := range providerpolicy.Descriptors() {
		decision := policy.Decision(string(descriptor.ID))
		if !decision.Enabled && !includeDisabled {
			continue
		}
		capabilities := capabilitiesForSpec(descriptor)
		if !decision.Enabled {
			capabilities.Availability = "disabled"
			capabilities.Reason = string(decision.Reason)
		}
		providers = append(providers, capabilities)
	}
	return CatalogData{
		SchemaVersion: CapabilitySchemaVersion,
		Providers:     providers,
	}
}

func CapabilitiesFor(provider Provider) (Capabilities, bool) {
	descriptor, ok := providerpolicy.DescriptorFor(string(provider))
	if !ok {
		return Capabilities{}, false
	}
	return capabilitiesForSpec(descriptor), true
}

func capabilitiesForSpec(spec providerpolicy.Descriptor) Capabilities {
	provider := Provider(spec.ID)
	operations := make([]OperationCapability, 0, len(operationSpecs))
	for _, operation := range operationSpecs {
		status := CapabilityPlanned
		supported := false
		unavailableBy := "provider migration is not implemented yet"
		if !operationAppliesToProvider(operation, provider) {
			status = CapabilityUnsupported
			unavailableBy = "operation does not apply to this provider"
		} else if operation.operation == OperationCapabilities ||
			providerOperationImplemented(provider, operation.operation) {
			status = CapabilityImplemented
			supported = true
			unavailableBy = ""
		}
		operations = append(operations, OperationCapability{
			Operation:     operation.operation,
			Command:       fmt.Sprintf("cdp workflow agent %s %s", provider, operation.path),
			Status:        status,
			Supported:     supported,
			SideEffect:    operation.sideEffect,
			Browser:       operation.browser,
			Summary:       operation.summary,
			UnavailableBy: unavailableBy,
		})
	}
	return Capabilities{
		SchemaVersion:        CapabilitySchemaVersion,
		Provider:             provider,
		DisplayName:          spec.DisplayName,
		ImplementationStatus: "partial",
		Operations:           operations,
	}
}

func operationAppliesToProvider(spec operationSpec, provider Provider) bool {
	if len(spec.providers) == 0 {
		return true
	}
	for _, candidate := range spec.providers {
		if candidate == provider {
			return true
		}
	}
	return false
}

func providerOperationImplemented(
	provider Provider,
	operation Operation,
) bool {
	if provider != ProviderAlex &&
		provider != ProviderChatGPT &&
		provider != ProviderM365 &&
		provider != ProviderClaude &&
		provider != ProviderGemini &&
		provider != ProviderGrok &&
		provider != ProviderPerplexity &&
		provider != ProviderTripadvisor {
		return false
	}
	switch operation {
	case OperationDoctor, OperationAuthRefresh:
		return provider == ProviderAlex ||
			provider == ProviderChatGPT ||
			provider == ProviderM365 ||
			provider == ProviderClaude ||
			provider == ProviderGemini ||
			provider == ProviderGrok ||
			provider == ProviderPerplexity ||
			provider == ProviderTripadvisor
	case OperationTranscribe:
		return provider == ProviderChatGPT || provider == ProviderM365
	case OperationConversationsList,
		OperationConversationsDetail,
		OperationConversationsAwait:
		return provider != ProviderAlex
	case OperationConversationsContinue:
		return provider == ProviderChatGPT
	case OperationArtifactDownload, OperationAttachmentsDownload:
		return provider == ProviderChatGPT
	case OperationAsk:
		return true
	case OperationConversationsDelete:
		return provider != ProviderAlex &&
			provider != ProviderTripadvisor
	case OperationCatalogStatus,
		OperationCatalogRefresh,
		OperationCoursesList,
		OperationChaptersList,
		OperationContentFetch:
		return provider == ProviderAlex
	default:
		return false
	}
}
