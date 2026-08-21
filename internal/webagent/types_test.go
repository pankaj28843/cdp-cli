package webagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestProviderCatalogIsStableAndHonest(t *testing.T) {
	wantProviders := []Provider{
		ProviderAlex,
		ProviderChatGPT,
		ProviderClaude,
		ProviderGemini,
		ProviderGrok,
		ProviderM365,
		ProviderPerplexity,
		ProviderTripadvisor,
	}
	if got := Providers(); !reflect.DeepEqual(got, wantProviders) {
		t.Fatalf("Providers() = %#v, want %#v", got, wantProviders)
	}

	for _, provider := range wantProviders {
		capabilities, ok := CapabilitiesFor(provider)
		if !ok {
			t.Fatalf("CapabilitiesFor(%q) was not found", provider)
		}
		if capabilities.SchemaVersion != CapabilitySchemaVersion ||
			capabilities.Provider != provider ||
			capabilities.ImplementationStatus != "partial" {
			t.Fatalf("CapabilitiesFor(%q) = %+v", provider, capabilities)
		}
		if len(capabilities.Operations) != len(operationSpecs) {
			t.Fatalf("%q operation count = %d, want %d", provider, len(capabilities.Operations), len(operationSpecs))
		}
		for _, capability := range capabilities.Operations {
			var spec operationSpec
			for _, candidate := range operationSpecs {
				if candidate.operation == capability.Operation {
					spec = candidate
					break
				}
			}
			if !operationAppliesToProvider(spec, provider) {
				if capability.Status != CapabilityUnsupported ||
					capability.Supported ||
					capability.UnavailableBy == "" {
					t.Fatalf("%q unsupported operation = %+v, want explicit unsupported", provider, capability)
				}
				continue
			}
			implemented := capability.Operation == OperationCapabilities ||
				providerOperationImplemented(provider, capability.Operation)
			if implemented {
				if capability.Status != CapabilityImplemented || !capability.Supported {
					t.Fatalf("%q implemented operation = %+v, want implemented", provider, capability)
				}
				continue
			}
			if capability.Status != CapabilityPlanned || capability.Supported || capability.UnavailableBy == "" {
				t.Fatalf("%q planned operation = %+v, want explicit unavailable state", provider, capability)
			}
		}
	}
}

func TestChatGPTAdvertisesOnlyLiveProvenMutationSurface(t *testing.T) {
	capabilities, ok := CapabilitiesFor(ProviderChatGPT)
	if !ok {
		t.Fatal("ChatGPT capabilities were not found")
	}

	wantImplemented := map[Operation]bool{
		OperationCapabilities:          true,
		OperationDoctor:                true,
		OperationAuthRefresh:           true,
		OperationTranscribe:            true,
		OperationAsk:                   true,
		OperationConversationsList:     true,
		OperationConversationsContinue: true,
		OperationConversationsDetail:   true,
		OperationConversationsAwait:    true,
		OperationConversationsDelete:   true,
		OperationArtifactDownload:      true,
		OperationAttachmentsDownload:   true,
	}
	for _, capability := range capabilities.Operations {
		if wantImplemented[capability.Operation] {
			if !capability.Supported || capability.Status != CapabilityImplemented {
				t.Fatalf("ChatGPT operation %q = %+v, want implemented", capability.Operation, capability)
			}
			continue
		}
		var spec operationSpec
		for _, candidate := range operationSpecs {
			if candidate.operation == capability.Operation {
				spec = candidate
				break
			}
		}
		if !operationAppliesToProvider(spec, ProviderChatGPT) {
			if capability.Supported ||
				capability.Status != CapabilityUnsupported {
				t.Fatalf(
					"ChatGPT operation %q = %+v, want explicitly unsupported",
					capability.Operation,
					capability,
				)
			}
			continue
		}
		if capability.Supported ||
			capability.Status != CapabilityPlanned {
			t.Fatalf("ChatGPT operation %q = %+v, want explicitly planned", capability.Operation, capability)
		}
	}
}

func TestTripadvisorAdvertisesOnlyRenderedProvenSurface(t *testing.T) {
	capabilities, ok := CapabilitiesFor(ProviderTripadvisor)
	if !ok {
		t.Fatal("Tripadvisor capabilities were not found")
	}
	wantImplemented := map[Operation]bool{
		OperationCapabilities:        true,
		OperationDoctor:              true,
		OperationAuthRefresh:         true,
		OperationAsk:                 true,
		OperationConversationsList:   true,
		OperationConversationsDetail: true,
		OperationConversationsAwait:  true,
	}
	for _, capability := range capabilities.Operations {
		if wantImplemented[capability.Operation] {
			if !capability.Supported ||
				capability.Status != CapabilityImplemented {
				t.Fatalf(
					"Tripadvisor operation %q = %+v, want implemented",
					capability.Operation,
					capability,
				)
			}
			continue
		}
		if capability.Supported ||
			capability.Status != CapabilityUnsupported {
			t.Fatalf(
				"Tripadvisor operation %q = %+v, want unsupported",
				capability.Operation,
				capability,
			)
		}
	}
}

func TestParseProviderRejectsCatalogAndUnknownValues(t *testing.T) {
	if got, ok := ParseProvider(" CLAUDE "); !ok || got != ProviderClaude {
		t.Fatalf("ParseProvider(CLAUDE) = %q, %v", got, ok)
	}
	for _, value := range []string{"", "catalog", "unknown"} {
		if got, ok := ParseProvider(value); ok {
			t.Fatalf("ParseProvider(%q) = %q, true; want false", value, got)
		}
	}
}

func TestMetadataResultRoundTripsStableEnvelope(t *testing.T) {
	result := NewMetadataResult(
		ProviderClaude,
		OperationCapabilities,
		map[string]any{"schema_version": CapabilitySchemaVersion},
		"abc123",
		[]string{"cdp schema webagent-operation --json"},
	)
	if !result.OK ||
		result.SchemaVersion != OperationSchemaVersion ||
		result.State != StateReady ||
		result.Stage != StageMetadata ||
		result.Action != nil ||
		result.Conversation != nil ||
		result.Cleanup.Required ||
		result.Cleanup.State != CleanupNotRequired ||
		result.Evidence.BrowserMode != "none" ||
		result.Evidence.ReadMode != "local_metadata" {
		t.Fatalf("NewMetadataResult() = %+v", result)
	}
	if !regexp.MustCompile(`^wa-[0-9a-f]{32}$`).MatchString(result.Evidence.RunID) {
		t.Fatalf("run id %q does not use the stable opaque format", result.Evidence.RunID)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var decoded Result
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if decoded.SchemaVersion != OperationSchemaVersion ||
		decoded.Provider != ProviderClaude ||
		decoded.Operation != OperationCapabilities ||
		decoded.Data == nil ||
		len(decoded.NextCommands) != 1 {
		t.Fatalf("round-tripped result = %+v", decoded)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("round-tripped result validation: %v", err)
	}
}

func TestMetadataResultKeepsEmptyNextCommandsAsJSONArray(t *testing.T) {
	result := NewMetadataResult(
		ProviderClaude,
		OperationCapabilities,
		map[string]any{},
		"abc123",
		[]string{},
	)
	if result.NextCommands == nil {
		t.Fatal("NextCommands is nil, want a non-nil empty slice")
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("metadata result validation: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(encoded), `"next_commands":[]`) {
		t.Fatalf("metadata result encoded without JSON array: %s", encoded)
	}
}

func TestOperationResultGoldens(t *testing.T) {
	capabilityData := Capabilities{
		SchemaVersion:        CapabilitySchemaVersion,
		Provider:             ProviderClaude,
		DisplayName:          "Claude",
		ImplementationStatus: "partial",
		Operations: []OperationCapability{
			{
				Operation:  OperationCapabilities,
				Command:    "cdp workflow agent claude capabilities",
				Status:     CapabilityImplemented,
				Supported:  true,
				SideEffect: "none",
				Browser:    "none",
				Summary:    "Report the installed provider operation contract without probing the browser.",
			},
		},
	}
	tests := []struct {
		name   string
		result Result
	}{
		{
			name: "capability",
			result: Result{
				OK:            true,
				SchemaVersion: OperationSchemaVersion,
				Provider:      ProviderClaude,
				Operation:     OperationCapabilities,
				State:         StateReady,
				Stage:         StageMetadata,
				Data:          capabilityData,
				Evidence: Evidence{
					RunID:       "wa-example-capability",
					BuildCommit: "abc123",
					BrowserMode: "none",
					ReadMode:    "local_metadata",
				},
				Cleanup: CleanupEvidence{State: CleanupNotRequired},
				NextCommands: []string{
					"cdp schema webagent-operation --json",
				},
			},
		},
		{
			name: "success",
			result: Result{
				OK:            true,
				SchemaVersion: OperationSchemaVersion,
				Provider:      ProviderClaude,
				Operation:     OperationAsk,
				State:         StateTerminal,
				Stage:         StageObserveTerminal,
				Action: &ActionEvidence{
					Dispatch:         DispatchPerformed,
					AttemptCount:     1,
					RawInputCount:    1,
					RetrySafe:        false,
					PendingPersisted: true,
				},
				Conversation: &ConversationRef{
					ID:  "conversation-example",
					URL: "https://claude.example/chat/conversation-example",
				},
				Data: map[string]any{
					"answer":           "useful provider result",
					"completion_state": "terminal",
				},
				Evidence: Evidence{
					RunID:       "wa-example-success",
					BuildCommit: "abc123",
					BrowserMode: "headed",
					ReadMode:    "rendered_same_target",
					Target: &TargetEvidence{
						TargetID:  "target-example",
						SessionID: "session-example",
						Owned:     true,
						Created:   true,
						Closed:    true,
					},
				},
				Cleanup: CleanupEvidence{
					Required:     true,
					State:        CleanupClosed,
					TargetID:     "target-example",
					TargetClosed: true,
					CloseProof:   "exact_target_absent",
				},
				NextCommands: []string{},
			},
		},
		{
			name: "failure",
			result: Result{
				OK:            false,
				SchemaVersion: OperationSchemaVersion,
				Provider:      ProviderClaude,
				Operation:     OperationAsk,
				State:         StateFailed,
				Stage:         StageActionDispatched,
				Error: &OperationError{
					Code:      "provider_observation_timeout",
					ErrClass:  "completion",
					Message:   "the provider action may have been sent, but terminal completion was not proven",
					RetrySafe: false,
					RetryAt:   "2026-07-25T18:15:00Z",
				},
				Action: &ActionEvidence{
					Dispatch:         DispatchUnknown,
					AttemptCount:     1,
					RawInputCount:    1,
					RetrySafe:        false,
					PendingPersisted: true,
				},
				Data: map[string]any{
					"completion_state": "unknown",
				},
				Evidence: Evidence{
					RunID:       "wa-example-failure",
					BuildCommit: "abc123",
					BrowserMode: "headed",
					ReadMode:    "rendered_same_target",
					Target: &TargetEvidence{
						TargetID:  "target-example",
						SessionID: "session-example",
						Owned:     true,
						Created:   true,
						Closed:    true,
					},
				},
				Cleanup: CleanupEvidence{
					Required:     true,
					State:        CleanupClosed,
					TargetID:     "target-example",
					TargetClosed: true,
					CloseProof:   "exact_target_absent",
				},
				NextCommands: []string{
					"cdp workflow agent claude capabilities --json",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.result.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got, err := json.MarshalIndent(test.result, "", "  ")
			if err != nil {
				t.Fatalf("MarshalIndent: %v", err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", "webagent-operation-"+test.name+".golden.json")
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if string(got) != string(want) {
				t.Fatalf("%s mismatch\n--- got ---\n%s--- want ---\n%s", path, got, want)
			}
		})
	}
}

func TestResultValidationRejectsUnsafeOrInconsistentEvidence(t *testing.T) {
	base := Result{
		OK:            false,
		SchemaVersion: OperationSchemaVersion,
		Provider:      ProviderClaude,
		Operation:     OperationAsk,
		State:         StateFailed,
		Stage:         StageActionDispatched,
		Error: &OperationError{
			Code: "dispatch_unknown", ErrClass: "provider",
			Message: "dispatch outcome is unknown", RetrySafe: false,
		},
		Action: &ActionEvidence{
			Dispatch: DispatchUnknown, AttemptCount: 1, RawInputCount: 1,
			PendingPersisted: true,
		},
		Data: map[string]any{},
		Evidence: Evidence{
			RunID: "wa-validation", BuildCommit: "abc123",
			BrowserMode: "headed", ReadMode: "rendered_same_target",
		},
		Cleanup:      CleanupEvidence{State: CleanupNotRequired},
		NextCommands: []string{},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid base result: %v", err)
	}

	unsafe := base
	unsafe.Error = &OperationError{
		Code: "dispatch_unknown", ErrClass: "provider",
		Message: "private\npayload", RetrySafe: false,
	}
	if err := unsafe.Validate(); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("unsafe message validation = %v", err)
	}
	retryable := base
	retryable.Action = &ActionEvidence{
		Dispatch: DispatchUnknown, AttemptCount: 1, RawInputCount: 1,
		RetrySafe: true, PendingPersisted: true,
	}
	if err := retryable.Validate(); err == nil || !strings.Contains(err.Error(), "retry_safe=false") {
		t.Fatalf("unsafe retry validation = %v", err)
	}
}

func TestCloneCommandsAlwaysReturnsIndependentNonNilSlice(t *testing.T) {
	for _, input := range [][]string{nil, {}, {"one"}} {
		cloned := CloneCommands(input)
		if cloned == nil {
			t.Fatalf("CloneCommands(%v) returned nil", input)
		}
		if !slices.Equal(cloned, input) {
			t.Fatalf("CloneCommands(%v) = %v", input, cloned)
		}
		if len(input) > 0 {
			cloned[0] = "changed"
			if input[0] == "changed" {
				t.Fatal("CloneCommands aliased its input")
			}
		}
	}
}

func TestResultValidationRejectsIncompleteMutationWithoutAction(t *testing.T) {
	result := Result{
		OK:            true,
		SchemaVersion: OperationSchemaVersion,
		Provider:      ProviderClaude,
		Operation:     OperationAsk,
		State:         StateIncomplete,
		Stage:         StageObserveTerminal,
		Data:          map[string]any{},
		Evidence: Evidence{
			RunID:       "wa-incomplete-action",
			BuildCommit: "abc123",
			BrowserMode: "none",
			ReadMode:    "direct_http_replay",
		},
		Cleanup:      CleanupEvidence{State: CleanupNotRequired},
		NextCommands: []string{},
	}
	if err := result.Validate(); err == nil ||
		!strings.Contains(err.Error(), "requires action evidence") {
		t.Fatalf("incomplete mutation validation = %v", err)
	}

	result.Operation = OperationConversationsList
	if err := result.Validate(); err != nil {
		t.Fatalf("incomplete read-only operation: %v", err)
	}

	result.Operation = OperationAsk
	result.Action = &ActionEvidence{
		Dispatch:     DispatchNotPerformed,
		AttemptCount: 1,
		RetrySafe:    true,
	}
	if err := result.Validate(); err == nil ||
		!strings.Contains(err.Error(), "performed or unknown dispatch") {
		t.Fatalf("incomplete not-performed mutation validation = %v", err)
	}
}

func TestResultValidationLinksOwnedTargetAndCleanup(t *testing.T) {
	result := Result{
		OK:            true,
		SchemaVersion: OperationSchemaVersion,
		Provider:      ProviderClaude,
		Operation:     OperationDoctor,
		State:         StateTerminal,
		Stage:         StageClosed,
		Data:          map[string]any{},
		Evidence: Evidence{
			RunID:       "wa-owned-cleanup",
			BuildCommit: "abc123",
			BrowserMode: "headed",
			ReadMode:    "rendered_same_target",
			Target: &TargetEvidence{
				TargetID: "target-owned",
				Owned:    true,
				Created:  true,
			},
		},
		Cleanup:      CleanupEvidence{State: CleanupNotRequired},
		NextCommands: []string{},
	}
	if err := result.Validate(); err == nil {
		t.Fatalf("unclosed owned target validation = %v", err)
	}

	result.Cleanup = CleanupEvidence{
		State:    CleanupPending,
		TargetID: "target-owned",
	}
	if err := result.Validate(); err == nil ||
		!strings.Contains(err.Error(), "requires required=true") {
		t.Fatalf("optional pending cleanup validation = %v", err)
	}

	result.Evidence.Target.Closed = true
	result.Cleanup = CleanupEvidence{
		Required:     true,
		State:        CleanupClosed,
		TargetID:     "different-target",
		TargetClosed: true,
	}
	if err := result.Validate(); err == nil ||
		!strings.Contains(err.Error(), "must match") {
		t.Fatalf("mismatched cleanup target validation = %v", err)
	}

	result.Cleanup.TargetID = "target-owned"
	if err := result.Validate(); err != nil {
		t.Fatalf("closed owned target validation: %v", err)
	}

	result.Evidence.Target = nil
	result.Cleanup.TargetID = ""
	result.Cleanup.IdentityOmitted = true
	if err := result.Validate(); err != nil {
		t.Fatalf("privacy-omitted cleanup identity validation: %v", err)
	}
	result.Cleanup.TargetID = "target-owned"
	if err := result.Validate(); err == nil ||
		!strings.Contains(err.Error(), "cannot accompany target_id") {
		t.Fatalf("mixed cleanup identity validation = %v", err)
	}
}
