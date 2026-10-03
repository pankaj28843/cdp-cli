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
	return authreadiness.WaitForHydration(ctx, session, authreadiness.MinimumAttempts, config.ComposerTimeout, config.PollInterval, observe)
}
