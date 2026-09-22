package availability

import "context"

// CheckDesktop passively checks whether headed Chrome can request approval.
// It does not activate applications, request accessibility permission, or
// expose session identity. Headless work does not require this check.
func CheckDesktop(ctx context.Context) Result {
	if ctx.Err() != nil {
		return Result{State: "unknown", Network: "not_checked", Reason: "desktop_check_cancelled"}
	}
	return desktopResult(readDesktopState())
}

type desktopState struct {
	known     bool
	onConsole bool
	unlocked  bool
	lidOpen   bool
}

func desktopResult(state desktopState) Result {
	result := Result{State: "unavailable", Network: "not_checked"}
	switch {
	case !state.known:
		result.State = "unknown"
		result.Reason = "desktop_state_unknown"
	case !state.onConsole:
		result.Reason = "desktop_session_inactive"
	case !state.unlocked:
		result.Reason = "screen_locked"
	case !state.lidOpen:
		result.Reason = "lid_closed"
	default:
		result.Allowed = true
		result.State = "ready"
		result.Reason = "desktop_ready"
	}
	return result
}
