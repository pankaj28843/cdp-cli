package m365

import (
	"fmt"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func operationSuccess(
	runID string,
	buildCommit string,
	operation webagent.Operation,
	stage webagent.Stage,
	readMode string,
	target *webagent.TargetEvidence,
	cleanup webagent.CleanupEvidence,
	data any,
	nextCommands []string,
) webagent.Result {
	return webagent.Result{
		OK:            true,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderM365,
		Operation:     operation,
		State:         webagent.StateReady,
		Stage:         stage,
		Data:          data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: browserModeForTarget(target),
			ReadMode:    readMode,
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: append([]string{}, nextCommands...),
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
	code string,
	errClass string,
	message string,
	data any,
	nextCommands []string,
) webagent.Result {
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderM365,
		Operation:     operation,
		State:         webagent.StateFailed,
		Stage:         stage,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: true,
		},
		Data: data,
		Evidence: webagent.Evidence{
			RunID:       runID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: browserModeForTarget(target),
			ReadMode:    readMode,
			Target:      target,
		},
		Cleanup:      cleanup,
		NextCommands: append([]string{}, nextCommands...),
	}
}

func UnavailableOperation(
	buildCommit string,
	operation webagent.Operation,
	code string,
	errClass string,
	message string,
) webagent.Result {
	result := operationFailure(
		webagent.NewRunID(),
		buildCommit,
		operation,
		webagent.StagePlanned,
		"unavailable",
		nil,
		webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		code,
		errClass,
		message,
		map[string]any{"schema_version": "m365-unavailable/v1"},
		[]string{"cdp workflow agent m365 doctor --json"},
	)
	result.Evidence.BrowserMode = "none"
	return result
}

func normalizedBuildCommit(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func browserModeForTarget(target *webagent.TargetEvidence) string {
	if target == nil {
		return "none"
	}
	return "headed"
}

func cleanupCommands(runID string, operation webagent.Operation, cleanup webagent.CleanupEvidence) []string {
	commands := []string{"cdp workflow agent m365 doctor --json"}
	if cleanup.TargetID != "" {
		commands = append(commands, fmt.Sprintf(
			"cdp --browser-mode headed page close --target %s --json",
			cleanup.TargetID,
		))
	}
	if runID != "" {
		if operation == webagent.OperationCapabilities {
			commands = append(commands, "cdp workflow agent m365 capabilities refresh --json")
		} else {
			commands = append(commands, "cdp workflow agent m365 auth refresh --json")
		}
	}
	return commands
}
