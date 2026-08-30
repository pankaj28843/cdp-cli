package cli

import (
	"errors"
	"fmt"
)

const (
	ExitOK             = 0
	ExitCheckFailed    = 1
	ExitUsage          = 2
	ExitConnection     = 3
	ExitPermission     = 4
	ExitTimeout        = 5
	ExitNotImplemented = 8
	ExitInternal       = 10
)

type CommandError struct {
	Code                string
	Class               string
	Message             string
	ExitCode            int
	RemediationCommands []string
	Data                any
	Err                 error
}

type renderedResultExit struct {
	ExitCode int
}

func (e *renderedResultExit) Error() string {
	return "result already rendered"
}

func (e *CommandError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func commandError(code, class, message string, exitCode int, remediation []string) error {
	return &CommandError{
		Code:                code,
		Class:               class,
		Message:             message,
		ExitCode:            exitCode,
		RemediationCommands: remediation,
	}
}

func commandErrorWithData(code, class, message string, exitCode int, remediation []string, data any) error {
	return &CommandError{
		Code:                code,
		Class:               class,
		Message:             message,
		ExitCode:            exitCode,
		RemediationCommands: remediation,
		Data:                data,
	}
}

func plainTargetRecoveryLines(data any) []string {
	fields, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	lines := plainTargetRecoverySection(fields, "candidate_ids", "candidate_short_ids", "candidate_indexes", "Candidate targets:")
	return append(lines, plainTargetRecoverySection(fields, "available_ids", "available_short_ids", "available_indexes", "Available targets:")...)
}

func plainTargetRecoverySection(fields map[string]any, idKey, shortIDKey, indexKey, heading string) []string {
	ids, ok := fields[idKey].([]string)
	if !ok || len(ids) == 0 {
		return nil
	}
	shortIDs, _ := fields[shortIDKey].([]string)
	indexes, _ := fields[indexKey].([]int)
	if len(ids) > 10 {
		ids = ids[:10]
	}
	rows := make([]string, 0, len(ids))
	for i, id := range ids {
		if !safePlainTargetID(id) {
			continue
		}
		shortID := shortTargetID(id)
		if i < len(shortIDs) && safePlainTargetID(shortIDs[i]) {
			shortID = shortIDs[i]
		}
		if i < len(indexes) && indexes[i] > 0 {
			rows = append(rows, fmt.Sprintf("  [%d]\t%s\t%s", indexes[i], shortID, id))
		} else {
			rows = append(rows, fmt.Sprintf("  %s\t%s", shortID, id))
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return append([]string{heading}, rows...)
}

func safePlainTargetID(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		char := value[i]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func commandErrorSummary(err error) map[string]any {
	summary := map[string]any{"message": err.Error()}
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		summary["code"] = commandErr.Code
		summary["err_class"] = commandErr.Class
		summary["exit_code"] = commandErr.ExitCode
		summary["remediation_commands"] = commandErr.RemediationCommands
		if commandErr.Data != nil {
			summary["data"] = commandErr.Data
		}
	}
	return summary
}

func notImplemented(command string) error {
	return commandError(
		"not_implemented",
		"not_implemented",
		fmt.Sprintf("%s is planned but not implemented yet", command),
		ExitNotImplemented,
		[]string{"cdp describe --json", "cdp --help"},
	)
}

func exitCode(err error) int {
	if err == nil {
		return ExitOK
	}

	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.ExitCode
	}

	return ExitInternal
}
