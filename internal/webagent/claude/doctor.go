package claude

import (
	"context"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const DoctorSchemaVersion = "claude-doctor/v1"

type BrowserRequirement struct {
	Mode   string `json:"mode"`
	State  string `json:"state"`
	Probed bool   `json:"probed"`
}

type DoctorData struct {
	SchemaVersion    string             `json:"schema_version"`
	Auth             AuthStatus         `json:"auth"`
	ConversationRead string             `json:"conversation_read"`
	BrowserSubmit    string             `json:"browser_submit"`
	Browser          BrowserRequirement `json:"browser"`
}

func UnavailableDoctor(buildCommit string) webagent.Result {
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationDoctor,
		State:         webagent.StateFailed,
		Stage:         webagent.StageMetadata,
		Error: &webagent.OperationError{
			Code:      "claude_state_unavailable",
			ErrClass:  "internal",
			Message:   "Claude owner-only state is unavailable",
			RetrySafe: true,
		},
		Data: map[string]any{
			"schema_version": DoctorSchemaVersion,
			"auth_state":     "unavailable",
		},
		Evidence: webagent.Evidence{
			RunID:       webagent.NewRunID(),
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "none",
			ReadMode:    "owner_only_local_state",
		},
		Cleanup: webagent.CleanupEvidence{
			Required: false,
			State:    webagent.CleanupNotRequired,
		},
		NextCommands: []string{"cdp doctor --json"},
	}
}

func Doctor(ctx context.Context, store *Store, now time.Time, ttl time.Duration, buildCommit string) webagent.Result {
	auth := store.Status(ctx, now, ttl)
	data := DoctorData{
		SchemaVersion:    DoctorSchemaVersion,
		Auth:             auth,
		ConversationRead: auth.State,
		BrowserSubmit:    "requires_headed_runtime",
		Browser: BrowserRequirement{
			Mode:   "headed",
			State:  "not_probed",
			Probed: false,
		},
	}
	if auth.Ready {
		result := webagent.NewMetadataResult(
			webagent.ProviderClaude,
			webagent.OperationDoctor,
			data,
			buildCommit,
			[]string{
				"cdp --browser-mode headed daemon status --json",
				"cdp workflow agent claude capabilities --json",
			},
		)
		result.Evidence.ReadMode = "owner_only_local_state"
		return result
	}

	code := "claude_auth_" + auth.State
	message := "Claude auth evidence is not ready"
	switch auth.State {
	case "missing":
		message = "Claude auth evidence is missing"
	case "expired":
		message = "Claude auth evidence is expired"
	case "invalid":
		message = "Claude auth evidence failed owner-only validation"
	}
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationDoctor,
		State:         webagent.StateFailed,
		Stage:         webagent.StageMetadata,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  "auth",
			Message:   message,
			RetrySafe: true,
		},
		Data: data,
		Evidence: webagent.Evidence{
			RunID:       webagent.NewRunID(),
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "none",
			ReadMode:    "owner_only_local_state",
		},
		Cleanup: webagent.CleanupEvidence{
			Required: false,
			State:    webagent.CleanupNotRequired,
		},
		NextCommands: []string{
			"cdp workflow agent claude auth refresh --json",
			"cdp workflow agent claude capabilities --json",
		},
	}
}

func normalizedBuildCommit(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "unknown"
}
