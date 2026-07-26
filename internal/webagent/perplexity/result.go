package perplexity

import (
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func operationSuccess(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	state webagent.State,
	stage webagent.Stage,
	readMode string,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	data any,
	nextCommands []string,
) webagent.Result {
	commands := make([]string, 0, len(nextCommands))
	commands = append(commands, nextCommands...)
	return webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderPerplexity,
		Operation:     operation,
		State:         state,
		Stage:         stage,
		Action:        action,
		Conversation:  conversation,
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: browserModeForTarget(target),
			ReadMode:    readMode,
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: commands,
	}
}

func operationFailure(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	stage webagent.Stage,
	readMode string,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	action *webagent.ActionEvidence,
	conversation *webagent.ConversationRef,
	code string,
	errClass string,
	message string,
	retryAt string,
	data any,
	nextCommands []string,
) webagent.Result {
	commands := make([]string, 0, len(nextCommands))
	commands = append(commands, nextCommands...)
	retrySafe := true
	if action != nil {
		retrySafe = action.RetrySafe
	}
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderPerplexity,
		Operation:     operation,
		State:         webagent.StateFailed,
		Stage:         stage,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: retrySafe,
			RetryAt:   retryAt,
		},
		Action:       action,
		Conversation: conversation,
		Data:         data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: browserModeForTarget(target),
			ReadMode:    readMode,
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: commands,
	}
}

func replaceFailure(
	result webagent.Result,
	code string,
	errClass string,
	message string,
	nextCommands []string,
) webagent.Result {
	result.OK = false
	result.State = webagent.StateFailed
	retrySafe := true
	if result.Action != nil {
		retrySafe = result.Action.RetrySafe
	}
	result.Error = &webagent.OperationError{
		Code:      code,
		ErrClass:  errClass,
		Message:   message,
		RetrySafe: retrySafe,
	}
	result.NextCommands = make([]string, 0, len(nextCommands))
	result.NextCommands = append(result.NextCommands, nextCommands...)
	return result
}

func notPerformedAction() *webagent.ActionEvidence {
	return &webagent.ActionEvidence{
		Dispatch:      webagent.DispatchNotPerformed,
		AttemptCount:  0,
		RawInputCount: 0,
		RetrySafe:     true,
	}
}

func actionEvidence(record browserflow.Record) *webagent.ActionEvidence {
	dispatch := webagent.DispatchNotPerformed
	switch record.Dispatch {
	case browserflow.DispatchPerformed:
		dispatch = webagent.DispatchPerformed
	case browserflow.DispatchUnknown:
		dispatch = webagent.DispatchUnknown
	}
	return &webagent.ActionEvidence{
		Dispatch:         dispatch,
		AttemptCount:     record.ActionAttemptCount,
		RawInputCount:    record.RawInputCount,
		RetrySafe:        dispatch == webagent.DispatchNotPerformed,
		PendingPersisted: record.PendingPersisted,
	}
}

func conversationRef(id string) *webagent.ConversationRef {
	return &webagent.ConversationRef{
		ID:  id,
		URL: fmt.Sprintf("%s/search/%s", Origin, id),
	}
}

func normalizedBuildCommit(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "unknown"
}

func browserModeForTarget(target *webagent.TargetEvidence) string {
	if target == nil {
		return "none"
	}
	return "headed"
}
