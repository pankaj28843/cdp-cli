package m365

import (
	"fmt"
	"strings"
	"testing"
)

func TestProbeErrorClassifiesUnavailableVoiceInput(t *testing.T) {
	err := fmt.Errorf("Microsoft 365 dictation control was not observed: Microsoft 365 voice input was unavailable on the exact headed target")
	if got := probeErrorCode(err); got != "m365_voice_input_unavailable" {
		t.Fatalf("probeErrorCode = %q, want m365_voice_input_unavailable", got)
	}
	message := probeErrorMessage(err)
	if !strings.Contains(message, "voice-input control") || !strings.Contains(message, "may not be eligible") {
		t.Fatalf("probeErrorMessage = %q, want eligibility guidance", message)
	}
}

func TestM365DictationControlExpressionKeepsCurrentAndLegacySignals(t *testing.T) {
	for _, signal := range []string{
		"m365-chat-input-shared-container",
		"aria-label",
		"start dictation",
		"stop dictation",
		"M10 13a3 3 0 0 0 3-3V5",
	} {
		if !strings.Contains(m365DictationControlExpression, signal) {
			t.Fatalf("dictation control expression is missing %q", signal)
		}
	}
}
