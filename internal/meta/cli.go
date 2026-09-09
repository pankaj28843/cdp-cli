package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const agentWebVersion = "0.1.0"

func Main(
	args []string,
	_ io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) int {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	return MainContext(ctx, args, stdout, stderr)
}

func MainContext(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || isHelp(args[0]) {
		printRootHelp(stdout)
		return 0
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Fprintf(stdout, "cdp meta %s\n", agentWebVersion)
		return 0
	}
	switch args[0] {
	case "doctor":
		return commandDoctor(ctx, args[1:], stdout, stderr)
	case "repair":
		return commandRepair(ctx, args[1:], stdout, stderr)
	case "integrations":
		return commandIntegrations(args[1:], stdout, stderr)
	case "cache":
		return commandCache(args[1:], stdout, stderr)
	case "maintenance":
		return commandMaintenance(ctx, args[1:], stdout, stderr)
	default:
		return writeCLIError(
			stdout,
			stderr,
			wantsJSON(args),
			2,
			"usage",
			fmt.Sprintf("unknown cdp meta command %q", args[0]),
		)
	}
}

func printRootHelp(output io.Writer) {
	fmt.Fprintln(output, `Usage: cdp meta <command> [options]

Optional diagnostics and maintenance for the installed Go ask-* provider CLIs.

Commands:
  doctor                         Check every provider without mutation
  repair <ask-provider>          Refresh auth, validate a recent read, and refresh discovery
  integrations list             List the fixed provider registry
  cache status|clean             Inspect or remove exact Ask Alex caches
  maintenance status|plan       Inspect the due-only coordinator
  maintenance enroll|disable    Change owner-only scheduling state
  maintenance run               Run explicit safe maintenance
  maintenance cron ...          Preview or manage the single cron block

These commands are not ask preflights. If headed pages is healthy, run ask-*
directly. Diagnostics and maintenance never submit prompts.`)
}

func commandDoctor(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	parsed, err := parseArguments(
		args,
		[]string{"--json"},
		nil,
		nil,
	)
	if err != nil || len(parsed.positionals) != 0 {
		return argumentError(stdout, stderr, args, err, "usage: cdp meta doctor [--json]")
	}
	report := runDoctors(ctx)
	writeReport(stdout, report, parsed.flag("--json"))
	if report.OK {
		return 0
	}
	return 1
}

func commandRepair(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	parsed, err := parseArguments(
		args,
		[]string{"--json"},
		nil,
		nil,
	)
	if err != nil || len(parsed.positionals) != 1 {
		return argumentError(
			stdout,
			stderr,
			args,
			err,
			"usage: cdp meta repair <ask-provider> [--json]",
		)
	}
	report, runErr := runMaintenance(ctx, RunOptions{
		Providers: []string{parsed.positionals[0]},
		Parallel:  1,
	})
	if runErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"maintenance",
			runErr.Error(),
		)
	}
	writeReport(stdout, report, parsed.flag("--json"))
	if report.OK {
		return 0
	}
	return 1
}

func commandIntegrations(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(stdout, "usage: cdp meta integrations list [--json]")
		return 0
	}
	if args[0] != "list" {
		return writeCLIError(
			stdout,
			stderr,
			wantsJSON(args),
			2,
			"usage",
			"integrations supports only list",
		)
	}
	parsed, err := parseArguments(
		args[1:],
		[]string{"--json"},
		nil,
		nil,
	)
	if err != nil || len(parsed.positionals) != 0 {
		return argumentError(
			stdout,
			stderr,
			args,
			err,
			"usage: cdp meta integrations list [--json]",
		)
	}
	payload := struct {
		OK           bool          `json:"ok"`
		Integrations []Integration `json:"integrations"`
	}{
		OK:           true,
		Integrations: Integrations(),
	}
	if parsed.flag("--json") {
		writeJSON(stdout, payload)
	} else {
		for _, integration := range payload.Integrations {
			fmt.Fprintf(
				stdout,
				"%s\t%s\n",
				integration.ID,
				integration.DisplayName,
			)
		}
	}
	return 0
}

func commandCache(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(
			stdout,
			"usage: cdp meta cache status|clean [options]",
		)
		return 0
	}
	switch args[0] {
	case "status":
		parsed, err := parseArguments(
			args[1:],
			[]string{"--json"},
			nil,
			nil,
		)
		if err != nil || len(parsed.positionals) != 0 {
			return argumentError(
				stdout,
				stderr,
				args,
				err,
				"usage: cdp meta cache status [--json]",
			)
		}
		report, statusErr := cacheStatus(time.Now())
		if statusErr != nil {
			return writeCLIError(
				stdout,
				stderr,
				parsed.flag("--json"),
				1,
				"cache",
				statusErr.Error(),
			)
		}
		writeReport(stdout, report, parsed.flag("--json"))
		return 0
	case "clean":
		parsed, err := parseArguments(
			args[1:],
			[]string{"--json", "--dry-run"},
			[]string{"--integration", "--kind"},
			nil,
		)
		if err != nil ||
			len(parsed.positionals) != 0 ||
			parsed.value("--integration") == "" {
			return argumentError(
				stdout,
				stderr,
				args,
				err,
				"usage: cdp meta cache clean --integration ask-alex [--kind catalog|content|all] [--dry-run] [--json]",
			)
		}
		kind := parsed.value("--kind")
		if kind == "" {
			kind = "all"
		}
		report, cleanErr := cleanCache(
			parsed.value("--integration"),
			kind,
			parsed.flag("--dry-run"),
		)
		if cleanErr != nil {
			return writeCLIError(
				stdout,
				stderr,
				parsed.flag("--json"),
				1,
				"cache",
				cleanErr.Error(),
			)
		}
		writeReport(stdout, report, parsed.flag("--json"))
		return 0
	default:
		return writeCLIError(
			stdout,
			stderr,
			wantsJSON(args),
			2,
			"usage",
			fmt.Sprintf("unknown cache command %q", args[0]),
		)
	}
}

func commandMaintenance(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(
			stdout,
			"usage: cdp meta maintenance status|enroll|disable|plan|run|cron [options]",
		)
		return 0
	}
	switch args[0] {
	case "status":
		return maintenanceStatusCommand(args[1:], stdout, stderr)
	case "enroll":
		return maintenanceEnrollCommand(args[1:], stdout, stderr)
	case "disable":
		return maintenanceDisableCommand(args[1:], stdout, stderr)
	case "plan":
		return maintenancePlanCommand(args[1:], stdout, stderr)
	case "run":
		return maintenanceRunCommand(ctx, args[1:], stdout, stderr)
	case "cron":
		return maintenanceCronCommand(ctx, args[1:], stdout, stderr)
	default:
		return writeCLIError(
			stdout,
			stderr,
			wantsJSON(args),
			2,
			"usage",
			fmt.Sprintf("unknown maintenance command %q", args[0]),
		)
	}
}

func maintenanceStatusCommand(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	parsed, err := parseArguments(args, []string{"--json"}, nil, nil)
	if err != nil || len(parsed.positionals) != 0 {
		return argumentError(
			stdout,
			stderr,
			args,
			err,
			"usage: cdp meta maintenance status [--json]",
		)
	}
	state, history, loadErr := loadMaintenanceState()
	if loadErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"state",
			loadErr.Error(),
		)
	}
	report, statusErr := maintenanceStatus(state, history, time.Now())
	if statusErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"state",
			statusErr.Error(),
		)
	}
	writeReport(stdout, report, parsed.flag("--json"))
	return 0
}

func maintenanceEnrollCommand(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	parsed, err := parseArguments(
		args,
		[]string{"--json"},
		[]string{"--frequency"},
		nil,
	)
	if err != nil ||
		len(parsed.positionals) != 1 ||
		parsed.value("--frequency") == "" {
		return argumentError(
			stdout,
			stderr,
			args,
			err,
			"usage: cdp meta maintenance enroll <ask-provider> --frequency 14d [--json]",
		)
	}
	state, loadErr := loadSchedulerState()
	if loadErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"state",
			loadErr.Error(),
		)
	}
	state, err = enrollIntegration(
		state,
		parsed.positionals[0],
		parsed.value("--frequency"),
		time.Now(),
	)
	if err == nil {
		err = saveSchedulerState(state)
	}
	if err != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"state",
			err.Error(),
		)
	}
	payload := map[string]any{
		"ok":         true,
		"state":      "enrolled",
		"enrollment": state.Enrollments[parsed.positionals[0]],
	}
	writeReport(stdout, payload, parsed.flag("--json"))
	return 0
}

func maintenanceDisableCommand(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	parsed, err := parseArguments(args, []string{"--json"}, nil, nil)
	if err != nil || len(parsed.positionals) != 1 {
		return argumentError(
			stdout,
			stderr,
			args,
			err,
			"usage: cdp meta maintenance disable <ask-provider> [--json]",
		)
	}
	state, loadErr := loadSchedulerState()
	if loadErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"state",
			loadErr.Error(),
		)
	}
	state, err = disableIntegration(
		state,
		parsed.positionals[0],
		time.Now(),
	)
	if err == nil {
		err = saveSchedulerState(state)
	}
	if err != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"state",
			err.Error(),
		)
	}
	payload := map[string]any{
		"ok":         true,
		"state":      "disabled",
		"enrollment": state.Enrollments[parsed.positionals[0]],
	}
	writeReport(stdout, payload, parsed.flag("--json"))
	return 0
}

func maintenancePlanCommand(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	parsed, err := parseArguments(args, []string{"--json"}, nil, nil)
	if err != nil || len(parsed.positionals) != 0 {
		return argumentError(
			stdout,
			stderr,
			args,
			err,
			"usage: cdp meta maintenance plan [--json]",
		)
	}
	state, loadErr := loadSchedulerState()
	if loadErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"state",
			loadErr.Error(),
		)
	}
	writeReport(
		stdout,
		maintenancePlan(state),
		parsed.flag("--json"),
	)
	return 0
}

func maintenanceRunCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	parsed, err := parseArguments(
		args,
		[]string{"--json", "--due"},
		[]string{"--parallel"},
		[]string{"--providers"},
	)
	if err != nil || len(parsed.positionals) != 0 {
		return argumentError(
			stdout,
			stderr,
			args,
			err,
			"usage: cdp meta maintenance run [--providers ask-a ask-b] [--due] [--parallel auto|1..7] [--json]",
		)
	}
	parallel := 0
	if value := parsed.value("--parallel"); value != "" && value != "auto" {
		parsedParallel, parseErr := strconv.Atoi(value)
		if parseErr != nil ||
			parsedParallel < 1 ||
			parsedParallel > len(registry) {
			return argumentError(
				stdout,
				stderr,
				args,
				fmt.Errorf(
					"parallel must be auto or between 1 and %d",
					len(registry),
				),
				"",
			)
		}
		parallel = parsedParallel
	}
	report, runErr := runMaintenance(ctx, RunOptions{
		Providers: parsed.values("--providers"),
		DueOnly:   parsed.flag("--due"),
		Parallel:  parallel,
	})
	if runErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"maintenance",
			runErr.Error(),
		)
	}
	writeReport(stdout, report, parsed.flag("--json"))
	if report.OK {
		return 0
	}
	return 1
}

func maintenanceCronCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(
			stdout,
			"usage: cdp meta maintenance cron print|install|uninstall|tick [options]",
		)
		return 0
	}
	switch args[0] {
	case "print":
		parsed, err := parseArguments(
			args[1:],
			[]string{"--json"},
			nil,
			nil,
		)
		if err != nil || len(parsed.positionals) != 0 {
			return argumentError(
				stdout,
				stderr,
				args,
				err,
				"usage: cdp meta maintenance cron print [--json]",
			)
		}
		state, loadErr := loadSchedulerState()
		if loadErr != nil {
			return writeCLIError(
				stdout,
				stderr,
				parsed.flag("--json"),
				1,
				"state",
				loadErr.Error(),
			)
		}
		block, blockErr := cronBlock(state)
		if blockErr != nil {
			return writeCLIError(
				stdout,
				stderr,
				parsed.flag("--json"),
				1,
				"cron",
				blockErr.Error(),
			)
		}
		if parsed.flag("--json") {
			writeJSON(stdout, map[string]any{
				"ok":    true,
				"block": block,
			})
		} else {
			fmt.Fprint(stdout, block)
		}
		return 0
	case "install":
		return cronInstallCommand(args[1:], stdout, stderr)
	case "uninstall":
		return cronUninstallCommand(args[1:], stdout, stderr)
	case "tick":
		if len(args) != 1 {
			return writeCLIError(
				stdout,
				stderr,
				false,
				2,
				"usage",
				"usage: cdp meta maintenance cron tick",
			)
		}
		report, runErr := runMaintenance(
			ctx,
			RunOptions{DueOnly: true, Parallel: 0},
		)
		logErr := appendTickLog(report, runErr)
		if runErr != nil {
			fmt.Fprintln(stderr, "maintenance tick:", runErr)
			return 1
		}
		if logErr != nil {
			fmt.Fprintln(stderr, "maintenance tick log:", logErr)
			return 1
		}
		if report.OK {
			return 0
		}
		return 1
	default:
		return writeCLIError(
			stdout,
			stderr,
			wantsJSON(args),
			2,
			"usage",
			fmt.Sprintf("unknown cron command %q", args[0]),
		)
	}
}

func cronInstallCommand(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	parsed, err := parseArguments(
		args,
		[]string{"--json", "--dry-run"},
		nil,
		nil,
	)
	if err != nil || len(parsed.positionals) != 0 {
		return argumentError(
			stdout,
			stderr,
			args,
			err,
			"usage: cdp meta maintenance cron install [--dry-run] [--json]",
		)
	}
	state, loadErr := loadSchedulerState()
	if loadErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"state",
			loadErr.Error(),
		)
	}
	report, installErr := installCron(
		state,
		parsed.flag("--dry-run"),
	)
	if installErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"cron",
			installErr.Error(),
		)
	}
	writeReport(stdout, report, parsed.flag("--json"))
	if report.OK {
		return 0
	}
	return 1
}

func cronUninstallCommand(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	parsed, err := parseArguments(
		args,
		[]string{"--json", "--dry-run"},
		nil,
		nil,
	)
	if err != nil || len(parsed.positionals) != 0 {
		return argumentError(
			stdout,
			stderr,
			args,
			err,
			"usage: cdp meta maintenance cron uninstall [--dry-run] [--json]",
		)
	}
	report, uninstallErr := uninstallCron(parsed.flag("--dry-run"))
	if uninstallErr != nil {
		return writeCLIError(
			stdout,
			stderr,
			parsed.flag("--json"),
			1,
			"cron",
			uninstallErr.Error(),
		)
	}
	writeReport(stdout, report, parsed.flag("--json"))
	if report.OK {
		return 0
	}
	return 1
}

func loadMaintenanceState() (
	SchedulerState,
	LastRunState,
	error,
) {
	state, err := loadSchedulerState()
	if err != nil {
		return SchedulerState{}, LastRunState{}, err
	}
	history, err := loadLastRunState()
	if err != nil {
		return SchedulerState{}, LastRunState{}, err
	}
	return state, history, nil
}

type parsedArguments struct {
	positionals []string
	flags       map[string]bool
	single      map[string]string
	multiple    map[string][]string
}

func (parsed parsedArguments) flag(name string) bool {
	return parsed.flags[name]
}

func (parsed parsedArguments) value(name string) string {
	return parsed.single[name]
}

func (parsed parsedArguments) values(name string) []string {
	return append([]string(nil), parsed.multiple[name]...)
}

func parseArguments(
	args []string,
	booleanFlags []string,
	valueFlags []string,
	multipleFlags []string,
) (parsedArguments, error) {
	parsed := parsedArguments{
		flags:    make(map[string]bool),
		single:   make(map[string]string),
		multiple: make(map[string][]string),
	}
	boolean := stringSet(booleanFlags)
	values := stringSet(valueFlags)
	multiple := stringSet(multipleFlags)
	for index := 0; index < len(args); index++ {
		value := args[index]
		if value == "-h" || value == "--help" {
			return parsedArguments{}, fmt.Errorf("help is available from the parent command")
		}
		if !strings.HasPrefix(value, "--") {
			parsed.positionals = append(parsed.positionals, value)
			continue
		}
		name, inline, hasInline := strings.Cut(value, "=")
		switch {
		case boolean[name]:
			if hasInline {
				return parsedArguments{}, fmt.Errorf(
					"%s does not take a value",
					name,
				)
			}
			if parsed.flags[name] {
				return parsedArguments{}, fmt.Errorf("%s is duplicated", name)
			}
			parsed.flags[name] = true
		case values[name]:
			if _, exists := parsed.single[name]; exists {
				return parsedArguments{}, fmt.Errorf("%s is duplicated", name)
			}
			if !hasInline {
				if index+1 >= len(args) {
					return parsedArguments{}, fmt.Errorf(
						"%s requires a value",
						name,
					)
				}
				index++
				inline = args[index]
			}
			if inline == "" {
				return parsedArguments{}, fmt.Errorf(
					"%s requires a non-empty value",
					name,
				)
			}
			parsed.single[name] = inline
		case multiple[name]:
			if len(parsed.multiple[name]) > 0 {
				return parsedArguments{}, fmt.Errorf("%s is duplicated", name)
			}
			if hasInline {
				if inline == "" {
					return parsedArguments{}, fmt.Errorf(
						"%s requires at least one value",
						name,
					)
				}
				parsed.multiple[name] = strings.Split(inline, ",")
				continue
			}
			for index+1 < len(args) &&
				!strings.HasPrefix(args[index+1], "--") {
				index++
				parsed.multiple[name] = append(
					parsed.multiple[name],
					args[index],
				)
			}
			if len(parsed.multiple[name]) == 0 {
				return parsedArguments{}, fmt.Errorf(
					"%s requires at least one value",
					name,
				)
			}
		default:
			return parsedArguments{}, fmt.Errorf("unknown option %s", name)
		}
	}
	return parsed, nil
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func argumentError(
	stdout io.Writer,
	stderr io.Writer,
	args []string,
	err error,
	usage string,
) int {
	message := usage
	if err != nil && message != "" {
		message = err.Error() + "; " + usage
	} else if err != nil {
		message = err.Error()
	}
	return writeCLIError(
		stdout,
		stderr,
		wantsJSON(args),
		2,
		"usage",
		message,
	)
}

func writeCLIError(
	stdout io.Writer,
	stderr io.Writer,
	jsonOutput bool,
	code int,
	errorType string,
	message string,
) int {
	payload := map[string]any{
		"ok":         false,
		"state":      "error",
		"error_type": errorType,
		"message":    message,
	}
	if jsonOutput {
		writeJSON(stdout, payload)
	} else {
		fmt.Fprintln(stderr, message)
	}
	return code
}

func writeReport(output io.Writer, report any, jsonOutput bool) {
	if jsonOutput {
		writeJSON(output, report)
		return
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(report)
}

func writeJSON(output io.Writer, value any) {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func wantsJSON(args []string) bool {
	for _, value := range args {
		if value == "--json" {
			return true
		}
	}
	return false
}

func isHelp(value string) bool {
	return value == "-h" || value == "--help" || value == "help"
}
