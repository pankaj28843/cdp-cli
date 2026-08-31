package cli

import (
	"context"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

const (
	eventStreamRuntimeRegistrationPresent = "present"
	eventStreamRuntimeRegistrationRetired = "retired"
	eventStreamRuntimeUnknown             = "unknown"
)

type eventStreamRuntimeRegistrationCheck func(context.Context) (string, error)

// eventStreamRuntimeRegistrationStatus applies the source registry's
// present/retired/unknown distinction to cdp-cli's mode-scoped daemon runtime
// record. Runtime state is a private ownership signal; callers only receive a
// semantic status and cancellation error.
func eventStreamRuntimeRegistrationStatus(ctx context.Context, stateDir, browserMode string, expected daemon.Runtime) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return eventStreamRuntimeUnknown, err
	}

	current, ok, err := daemon.LoadRuntimeForMode(ctx, stateDir, browserMode)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return eventStreamRuntimeUnknown, ctxErr
		}
		// Missing, malformed, unreadable, and otherwise ambiguous state must
		// not prove that the observed daemon retired.
		return eventStreamRuntimeUnknown, nil
	}
	if !ok {
		return eventStreamRuntimeUnknown, nil
	}
	if err := ctx.Err(); err != nil {
		return eventStreamRuntimeUnknown, err
	}

	status := compareEventStreamRuntimeRegistration(current, expected, browserMode)
	if err := ctx.Err(); err != nil {
		return eventStreamRuntimeUnknown, err
	}
	return status, nil
}

func compareEventStreamRuntimeRegistration(current, expected daemon.Runtime, browserMode string) string {
	expectedProcessStart := strings.TrimSpace(expected.ProcessStartTime)
	if expectedProcessStart != "" {
		currentProcessStart := strings.TrimSpace(current.ProcessStartTime)
		if expected.PID <= 0 || current.PID <= 0 || currentProcessStart == "" {
			return eventStreamRuntimeUnknown
		}
		if expected.PID == current.PID && expectedProcessStart == currentProcessStart {
			return eventStreamRuntimeRegistrationPresent
		}
		return eventStreamRuntimeRegistrationRetired
	}

	if !completeLegacyEventStreamRuntimeIdentity(expected, browserMode) || !completeLegacyEventStreamRuntimeIdentity(current, browserMode) {
		return eventStreamRuntimeUnknown
	}
	if expected.PID != current.PID ||
		strings.TrimSpace(expected.StartedAt) != strings.TrimSpace(current.StartedAt) ||
		strings.TrimSpace(expected.SocketPath) != strings.TrimSpace(current.SocketPath) ||
		strings.TrimSpace(expected.ConnectionMode) != strings.TrimSpace(current.ConnectionMode) ||
		eventStreamRuntimeMode(expected.BrowserMode, browserMode) != eventStreamRuntimeMode(current.BrowserMode, browserMode) {
		return eventStreamRuntimeRegistrationRetired
	}
	return eventStreamRuntimeRegistrationPresent
}

func completeLegacyEventStreamRuntimeIdentity(runtime daemon.Runtime, browserMode string) bool {
	return runtime.PID > 0 &&
		strings.TrimSpace(runtime.StartedAt) != "" &&
		strings.TrimSpace(runtime.SocketPath) != "" &&
		strings.TrimSpace(runtime.ConnectionMode) != "" &&
		strings.TrimSpace(eventStreamRuntimeMode(runtime.BrowserMode, browserMode)) != ""
}

func eventStreamRuntimeMode(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(fallback))
	}
	if value == "" {
		return "headed"
	}
	if value == "headless" {
		return "headless"
	}
	return "headed"
}

func (a *app) eventStreamRuntimeRegistrationCheck(client browserEventClient) eventStreamRuntimeRegistrationCheck {
	expected, ok := eventStreamRuntimeFromClient(client)
	if !ok {
		return nil
	}
	store, err := a.stateStore()
	if err != nil {
		// The daemon client is still usable and its ordinary exact-session
		// heartbeat remains the safe fallback when state inspection is unknown.
		return nil
	}
	browserMode := a.browserModeName()
	return func(ctx context.Context) (string, error) {
		return eventStreamRuntimeRegistrationStatus(ctx, store.Dir, browserMode, expected)
	}
}

func eventStreamRuntimeFromClient(client browserEventClient) (daemon.Runtime, bool) {
	switch typed := client.(type) {
	case daemon.RuntimeClient:
		return typed.Runtime, true
	case *daemon.RuntimeClient:
		if typed == nil {
			return daemon.Runtime{}, false
		}
		return typed.Runtime, true
	default:
		return daemon.Runtime{}, false
	}
}
