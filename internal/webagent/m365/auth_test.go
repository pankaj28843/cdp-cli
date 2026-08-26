package m365

import (
	"fmt"
	"strings"
	"testing"
)

func TestProbeErrorClassifiesUnavailableVoiceInput(t *testing.T) {
	err := fmt.Errorf("direct M365 auth provider unavailable: Microsoft 365 direct auth token provider was not found: context deadline exceeded; legacy dictation probe failed: Microsoft 365 voice input was unavailable on the exact headed target")
	if got := probeErrorCode(err); got != "m365_voice_input_unavailable" {
		t.Fatalf("probeErrorCode = %q, want m365_voice_input_unavailable", got)
	}
	message := probeErrorMessage(err)
	if !strings.Contains(message, "voice-input control") || !strings.Contains(message, "may not be eligible") {
		t.Fatalf("probeErrorMessage = %q, want eligibility guidance", message)
	}
}

func TestProbeErrorClassifiesDirectWebSocketFailure(t *testing.T) {
	err := fmt.Errorf("Microsoft 365 direct AugLoop WebSocket probe failed: speech recognition did not start")
	if got := probeErrorCode(err); got != "m365_direct_websocket_probe_failed" {
		t.Fatalf("probeErrorCode = %q, want m365_direct_websocket_probe_failed", got)
	}
	message := probeErrorMessage(err)
	if !strings.Contains(message, "live auth provider") || !strings.Contains(message, "WebSocket probe failed") {
		t.Fatalf("probeErrorMessage = %q, want direct transport guidance", message)
	}
}

func TestM365DictationControlExpressionKeepsCurrentAndLegacySignals(t *testing.T) {
	for _, signal := range []string{
		"m365-chat-input-shared-container",
		"aria-label",
		"[role=\"button\"]",
		"find(visible)",
		"start dictation",
		"stop dictation",
		"M10 13a3 3 0 0 0 3-3V5",
	} {
		if !strings.Contains(m365DictationControlExpression, signal) {
			t.Fatalf("dictation control expression is missing %q", signal)
		}
	}
}

func TestM365DirectAuthExpressionUsesRuntimeTokenProvider(t *testing.T) {
	for _, signal := range []string{
		"tokenProviders",
		"augloop",
		"auth_token",
		"client_metadata",
		"tokenProvider()",
	} {
		if !strings.Contains(m365DirectAuthExpression, signal) {
			t.Fatalf("direct auth expression is missing %q", signal)
		}
	}
	if strings.Contains(m365DirectAuthExpression, "button_found") {
		t.Fatal("direct auth expression must not require a visible dictation button")
	}
}

func TestM365StateAcceptsDirectRuntimeSources(t *testing.T) {
	template := AuthTemplate{
		SchemaVersion: AuthTemplateSchemaVersion,
		AuthToken:     "owner-token",
		ClientMetadata: ClientMetadata{
			AppName:              "BizChat",
			AppPlatform:          "Web",
			AppVersion:           "Client",
			ReleaseAudienceGroup: "Production",
			Flights:              "voice",
			RuntimeVersion:       "2.37.2567",
		},
		BrowserUserAgent: "Mozilla/5.0",
		CapturedAt:       "2026-08-26T00:00:00Z",
		Source:           "headed-cdp-runtime-token-provider",
	}
	if err := template.Validate(); err != nil {
		t.Fatalf("direct auth template rejected: %v", err)
	}
	runtime := RuntimeCapabilities{
		SchemaVersion:     RuntimeCapabilitiesSchemaVersion,
		State:             "ready",
		CapturedAt:        "2026-08-26T00:00:00Z",
		DictationObserved: true,
		WebSocketObserved: true,
		TokenProvisioned:  true,
		AudioProtocol:     "AugLoop_Voice_VoiceTile/v2",
		Source:            "headed-cdp-direct-websocket-probe",
	}
	if err := runtime.Validate(); err != nil {
		t.Fatalf("direct runtime capabilities rejected: %v", err)
	}
}
