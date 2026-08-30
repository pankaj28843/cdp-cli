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
