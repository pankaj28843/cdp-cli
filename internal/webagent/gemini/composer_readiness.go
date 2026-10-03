package gemini

import (
	"context"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/authreadiness"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

// waitForComposerReadiness lets the fresh application hydrate before recovery
// reloads. Each phase is bounded by ComposerTimeout and the command context.
func waitForComposerReadiness(ctx context.Context, session *cdp.PageSession, config AskConfig, composer *composerObservation) (authreadiness.Result, error) {
	observe := func(observationCtx context.Context) (bool, error) {
		if err := observeComposer(observationCtx, session, "", composer); err != nil {
			return false, err
		}
		return composer.RouteReady && composer.EditorReady && composer.EditorCount == 1 &&
			composer.PickerCount == 1 && composer.AnswerCount == 0 && strings.TrimSpace(composer.CurrentMode) != "", nil
	}
	initial := authreadiness.Result{Attempt: 1, Stage: authreadiness.StageInitialLoad}
	initialCtx, cancel := context.WithTimeout(ctx, config.ComposerTimeout)
	_, err := pollUntil(initialCtx, config.ComposerTimeout, config.PollInterval, func() (bool, error) {
		ready, err := observe(initialCtx)
		if initialCtx.Err() != nil {
			return false, initialCtx.Err()
		}
		initial.LastObservationError = err
		if err == nil {
			initial.SuccessfulObservations++
			initial.StageObservations++
		}
		return ready, err
	})
	cancel()
	if err == nil {
		initial.Observed = true
		return initial, nil
	}
	if ctx.Err() != nil {
		return initial, ctx.Err()
	}
	recovered, recoveryErr := authreadiness.WaitForEvidence(ctx, session, authreadiness.MinimumAttempts, config.ComposerTimeout, config.PollInterval, observe)
	recovered.SuccessfulObservations += initial.SuccessfulObservations
	return recovered, recoveryErr
}
