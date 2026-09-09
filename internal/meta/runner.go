package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	commandOutputLimit = 4 << 20
	commandErrorLimit  = 1000
)

var (
	doctorTimeout          = 45 * time.Second
	actionTimeout          = 5 * time.Minute
	maintenanceActionQuiet = 3 * time.Second
)

type CommandResult struct {
	OK                bool                `json:"ok"`
	Command           []string            `json:"command"`
	ValidationCommand []string            `json:"validation_command,omitempty"`
	ReturnCode        *int                `json:"returncode"`
	ProviderError     *ProviderDiagnostic `json:"provider_error,omitempty"`
	ErrorType         string              `json:"error_type,omitempty"`
	FailedStep        string              `json:"failed_step,omitempty"`
	StdoutTruncated   bool                `json:"stdout_truncated"`
	StderrTruncated   bool                `json:"stderr_truncated"`
	ElapsedSeconds    float64             `json:"elapsed_seconds"`
	Payload           map[string]any      `json:"-"`
	Error             string              `json:"-"`
}

type ProviderDiagnostic struct {
	Code     string `json:"code,omitempty"`
	ErrClass string `json:"err_class,omitempty"`
	RetryAt  string `json:"retry_at,omitempty"`
}

type DoctorResult struct {
	IntegrationID string `json:"integration_id"`
	DisplayName   string `json:"display_name"`
	Ready         bool   `json:"ready"`
	CommandResult
}

type DoctorReport struct {
	OK         bool           `json:"ok"`
	State      string         `json:"state"`
	Count      int            `json:"count"`
	ReadyCount int            `json:"ready_count"`
	Results    []DoctorResult `json:"results"`
}

type ActionResult struct {
	ActionID string  `json:"action_id"`
	Started  float64 `json:"started_at"`
	Finished float64 `json:"finished_at"`
	CommandResult
	ConsecutiveFailures int `json:"consecutive_failures"`
}

type ProviderRunResult struct {
	IntegrationID string         `json:"integration_id"`
	OK            bool           `json:"ok"`
	State         string         `json:"state"`
	Started       float64        `json:"started_at"`
	Finished      float64        `json:"finished_at"`
	FailedAction  string         `json:"failed_action_id,omitempty"`
	Actions       []ActionResult `json:"actions"`
}

type RunReport struct {
	OK          bool                `json:"ok"`
	State       string              `json:"state"`
	Count       int                 `json:"count"`
	Parallel    int                 `json:"parallel,omitempty"`
	TabHeadroom int                 `json:"tab_headroom,omitempty"`
	Results     []ProviderRunResult `json:"results"`
}

type RunOptions struct {
	Providers []string
	DueOnly   bool
	Parallel  int
}

type maintenanceJob struct {
	index       int
	integration Integration
	actions     []MaintenanceAction
}

func runDoctors(ctx context.Context) DoctorReport {
	integrations := Integrations()
	results := make([]DoctorResult, len(integrations))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(2, len(integrations)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				integration := integrations[index]
				command := runJSONCommand(
					ctx,
					integration.DoctorCommand,
					doctorTimeout,
				)
				results[index] = DoctorResult{
					IntegrationID: integration.ID,
					DisplayName:   integration.DisplayName,
					Ready:         command.OK,
					CommandResult: command,
				}
			}
		}()
	}
	for index := range integrations {
		jobs <- index
	}
	close(jobs)
	workers.Wait()

	ready := 0
	for _, result := range results {
		if result.Ready {
			ready++
		}
	}
	state := "attention-required"
	if ready == len(results) {
		state = "ready"
	}
	return DoctorReport{
		OK:         ready == len(results),
		State:      state,
		Count:      len(results),
		ReadyCount: ready,
		Results:    results,
	}
}

func runMaintenance(
	ctx context.Context,
	options RunOptions,
) (RunReport, error) {
	if options.Parallel < 0 || options.Parallel > len(registry) {
		return RunReport{}, fmt.Errorf(
			"parallel must be auto or between 1 and %d",
			len(registry),
		)
	}
	release, err := acquireMaintenanceLock()
	if err != nil {
		return RunReport{}, err
	}
	defer release()

	state, err := loadSchedulerState()
	if err != nil {
		return RunReport{}, err
	}
	history, err := loadLastRunState()
	if err != nil {
		return RunReport{}, err
	}
	selected, err := selectedIntegrations(state, options.Providers)
	if err != nil {
		return RunReport{}, err
	}
	now := time.Now()
	jobs := make([]maintenanceJob, 0, len(selected))
	for _, integration := range selected {
		enrollment := state.Enrollments[integration.ID]
		actions := integration.MaintenanceActions
		if options.DueOnly {
			actions = dueActions(
				actions,
				enrollment,
				history.LastRuns[integration.ID],
				now,
			)
		}
		if len(actions) == 0 {
			continue
		}
		jobs = append(jobs, maintenanceJob{
			index:       len(jobs),
			integration: integration,
			actions:     actions,
		})
	}
	if len(jobs) == 0 {
		return RunReport{
			OK:      true,
			State:   "fresh",
			Results: []ProviderRunResult{},
		}, nil
	}
	headroom, err := currentHeadedTabHeadroom(ctx)
	if err != nil {
		return RunReport{}, err
	}
	parallel, err := effectiveMaintenanceParallel(
		options.Parallel,
		len(jobs),
		headroom,
	)
	if err != nil {
		return RunReport{}, err
	}

	results := runMaintenanceJobs(
		ctx,
		parallel,
		jobs,
		runOneIntegration,
	)

	history = mergeHistory(history, results)
	if err := saveLastRunState(history); err != nil {
		return RunReport{}, err
	}
	ok := true
	for _, result := range results {
		ok = ok && result.OK
	}
	return RunReport{
		OK:          ok,
		State:       "completed",
		Count:       len(results),
		Parallel:    parallel,
		TabHeadroom: headroom,
		Results:     results,
	}, nil
}

type maintenanceJobRunner func(
	context.Context,
	Integration,
	[]MaintenanceAction,
) ProviderRunResult

func runMaintenanceJobs(
	ctx context.Context,
	parallel int,
	jobs []maintenanceJob,
	run maintenanceJobRunner,
) []ProviderRunResult {
	results := make([]ProviderRunResult, len(jobs))
	work := make(chan maintenanceJob)
	var workers sync.WaitGroup
	for range parallel {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, open := <-work:
					if !open {
						return
					}
					if ctx.Err() != nil {
						return
					}
					results[job.index] = run(
						ctx,
						job.integration,
						job.actions,
					)
				}
			}
		}()
	}

send:
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			break send
		case work <- job:
		}
	}
	close(work)
	workers.Wait()

	now := float64(time.Now().UnixNano()) / 1e9
	for _, job := range jobs {
		if results[job.index].IntegrationID != "" {
			continue
		}
		results[job.index] = ProviderRunResult{
			IntegrationID: job.integration.ID,
			OK:            false,
			State:         "interrupted",
			Started:       now,
			Finished:      now,
			Actions:       []ActionResult{},
		}
	}
	return results
}

func currentHeadedTabHeadroom(ctx context.Context) (int, error) {
	binary := strings.TrimSpace(os.Getenv("CDP_COMPAT_CDP_BIN"))
	if binary == "" {
		var err error
		binary, err = exec.LookPath("cdp")
		if err != nil {
			return 0, fmt.Errorf("inspect headed tab budget: cdp unavailable")
		}
	}
	result := runJSONCommand(
		ctx,
		[]string{binary, "--browser-mode", "headed", "pages", "--json"},
		15*time.Second,
	)
	if !result.OK {
		return 0, fmt.Errorf(
			"inspect headed tab budget: %s",
			result.ErrorType,
		)
	}
	return headedTabHeadroom(result.Payload)
}

func headedTabHeadroom(payload map[string]any) (int, error) {
	budget, ok := payload["budget"].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("inspect headed tab budget: missing budget")
	}
	tabCount, ok := exactJSONInt(budget["tab_count"])
	if !ok || tabCount < 0 {
		return 0, fmt.Errorf("inspect headed tab budget: invalid tab_count")
	}
	maxTabs, ok := exactJSONInt(budget["max_tabs"])
	if !ok || maxTabs < 1 {
		return 0, fmt.Errorf("inspect headed tab budget: invalid max_tabs")
	}
	return max(maxTabs-tabCount, 0), nil
}

func exactJSONInt(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok {
		return 0, false
	}
	integer := int(number)
	return integer, number == float64(integer)
}

func effectiveMaintenanceParallel(
	requested int,
	jobCount int,
	tabHeadroom int,
) (int, error) {
	if requested < 0 || requested > len(registry) {
		return 0, fmt.Errorf(
			"parallel must be auto or between 1 and %d",
			len(registry),
		)
	}
	if jobCount < 1 {
		return 0, fmt.Errorf("maintenance has no jobs")
	}
	if tabHeadroom < 1 {
		return 0, fmt.Errorf(
			"headed Chrome has no free tab slots for maintenance",
		)
	}
	parallel := jobCount
	if requested > 0 {
		parallel = min(parallel, requested)
	}
	return min(parallel, tabHeadroom), nil
}

func selectedIntegrations(
	state SchedulerState,
	requested []string,
) ([]Integration, error) {
	if len(requested) == 0 {
		selected := make([]Integration, 0, len(registry))
		for _, integration := range registry {
			enrollment := state.Enrollments[integration.ID]
			if enrollment.Enabled &&
				len(integration.MaintenanceActions) > 0 {
				selected = append(selected, integration)
			}
		}
		return selected, nil
	}
	seen := make(map[string]struct{}, len(requested))
	selected := make([]Integration, 0, len(requested))
	for _, id := range requested {
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("provider %q is duplicated", id)
		}
		seen[id] = struct{}{}
		integration, err := integrationByID(id)
		if err != nil {
			return nil, err
		}
		selected = append(selected, integration)
	}
	return selected, nil
}

func dueActions(
	actions []MaintenanceAction,
	enrollment Enrollment,
	history ProviderHistory,
	now time.Time,
) []MaintenanceAction {
	byID := make(map[string]ActionHistory, len(history.Actions))
	for _, action := range history.Actions {
		byID[action.ActionID] = action
	}
	nowSeconds := float64(now.UnixNano()) / 1e9
	due := make([]MaintenanceAction, 0, len(actions))
	for _, action := range actions {
		previous, exists := byID[action.ID]
		if !exists {
			due = append(due, action)
			continue
		}
		if previous.OK {
			interval := action.IntervalMinutes
			if interval <= 0 {
				interval = enrollment.FrequencyMinutes
			}
			if nowSeconds >= previous.FinishedAt+float64(interval*60) {
				due = append(due, action)
			}
			continue
		}
		backoff := failureBackoffMinutes(previous.ConsecutiveFailures)
		if nowSeconds >= previous.FinishedAt+float64(backoff*60) {
			due = append(due, action)
			continue
		}
		// Ordered actions must not run past a failed predecessor still in backoff.
		break
	}
	return due
}

func failureBackoffMinutes(failures int) int {
	backoffs := []int{10, 20, 40, 60}
	if failures < 1 {
		failures = 1
	}
	return backoffs[min(failures-1, len(backoffs)-1)]
}

func runOneIntegration(
	ctx context.Context,
	integration Integration,
	actions []MaintenanceAction,
) ProviderRunResult {
	started := time.Now()
	result := ProviderRunResult{
		IntegrationID: integration.ID,
		State:         "terminal",
		Started:       float64(started.UnixNano()) / 1e9,
		Actions:       make([]ActionResult, 0, len(actions)),
	}
	result.OK = true
	for index, action := range actions {
		if index > 0 {
			if !waitMaintenanceQuiet(ctx) {
				result.OK = false
				result.State = "interrupted"
				result.FailedAction = action.ID
				result.Finished = float64(time.Now().UnixNano()) / 1e9
				return result
			}
		}
		actionStarted := time.Now()
		command := runMaintenanceAction(ctx, action)
		actionFinished := time.Now()
		result.Actions = append(result.Actions, ActionResult{
			ActionID:      action.ID,
			Started:       float64(actionStarted.UnixNano()) / 1e9,
			Finished:      float64(actionFinished.UnixNano()) / 1e9,
			CommandResult: command,
		})
		if !command.OK {
			result.OK = false
			result.State = "failed"
			result.FailedAction = action.ID
			break
		}
	}
	result.Finished = float64(time.Now().UnixNano()) / 1e9
	return result
}

func runMaintenanceAction(
	ctx context.Context,
	action MaintenanceAction,
) CommandResult {
	started := time.Now()
	result := runJSONCommand(ctx, action.Command, actionTimeout)
	result.ValidationCommand = slices.Clone(action.ValidationCommand)
	if !result.OK {
		if len(action.ValidationCommand) > 0 {
			result.FailedStep = "refresh"
		}
		return result
	}
	if len(action.ValidationCommand) == 0 {
		return result
	}
	if !waitMaintenanceQuiet(ctx) {
		result.OK = false
		result.ReturnCode = nil
		result.ErrorType = "interrupted"
		result.Error = "maintenance interrupted before validation"
		result.FailedStep = "validation"
		result.ElapsedSeconds = time.Since(started).Seconds()
		return result
	}
	validation := runJSONCommand(
		ctx,
		action.ValidationCommand,
		actionTimeout,
	)
	result.OK = validation.OK
	result.ReturnCode = validation.ReturnCode
	result.ProviderError = validation.ProviderError
	result.ErrorType = validation.ErrorType
	result.Error = validation.Error
	result.StdoutTruncated =
		result.StdoutTruncated || validation.StdoutTruncated
	result.StderrTruncated =
		result.StderrTruncated || validation.StderrTruncated
	result.Payload = validation.Payload
	result.ElapsedSeconds = time.Since(started).Seconds()
	if !validation.OK {
		result.FailedStep = "validation"
	}
	return result
}

func waitMaintenanceQuiet(ctx context.Context) bool {
	timer := time.NewTimer(maintenanceActionQuiet)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func mergeHistory(
	history LastRunState,
	results []ProviderRunResult,
) LastRunState {
	if history.LastRuns == nil {
		history.LastRuns = make(map[string]ProviderHistory)
	}
	for resultIndex := range results {
		result := &results[resultIndex]
		previous := history.LastRuns[result.IntegrationID]
		byID := make(map[string]ActionHistory, len(previous.Actions))
		for _, action := range previous.Actions {
			byID[action.ActionID] = action
		}
		for actionIndex := range result.Actions {
			action := &result.Actions[actionIndex]
			prior := byID[action.ActionID]
			failures := 0
			if !action.OK {
				failures = prior.ConsecutiveFailures + 1
			}
			action.ConsecutiveFailures = failures
			byID[action.ActionID] = ActionHistory{
				ActionID:            action.ActionID,
				OK:                  action.OK,
				ReturnCode:          action.ReturnCode,
				StartedAt:           action.Started,
				FinishedAt:          action.Finished,
				ErrorType:           action.ErrorType,
				ConsecutiveFailures: failures,
			}
		}
		integration, _ := integrationByID(result.IntegrationID)
		ordered := make([]ActionHistory, 0, len(byID))
		for _, action := range integration.MaintenanceActions {
			if item, exists := byID[action.ID]; exists {
				ordered = append(ordered, item)
			}
		}
		overallOK := true
		failedAction := result.FailedAction
		for _, action := range integration.MaintenanceActions {
			item, exists := byID[action.ID]
			if !exists || !item.OK {
				overallOK = false
				if failedAction == "" && exists {
					failedAction = action.ID
				}
			}
		}
		lastSuccess := previous.LastSuccessAt
		if overallOK {
			value := result.Finished
			lastSuccess = &value
		}
		history.LastRuns[result.IntegrationID] = ProviderHistory{
			OK:               overallOK,
			LastInvocationOK: result.OK,
			StartedAt:        result.Started,
			FinishedAt:       result.Finished,
			FailedActionID:   failedAction,
			Actions:          ordered,
			LastSuccessAt:    lastSuccess,
		}
	}
	history.SchemaVersion = historySchemaVersion
	history.UpdatedAt = float64(time.Now().UnixNano()) / 1e9
	return history
}

func runJSONCommand(
	parent context.Context,
	command []string,
	timeout time.Duration,
) CommandResult {
	started := time.Now()
	result := CommandResult{
		Command: slices.Clone(command),
	}
	if len(command) == 0 {
		result.ErrorType = "invalid-command"
		result.Error = "command is empty"
		result.ElapsedSeconds = time.Since(started).Seconds()
		return result
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		result.ErrorType = commandContextErrorType(err)
		result.Error = "command was canceled before start"
		result.ElapsedSeconds = time.Since(started).Seconds()
		return result
	}
	process := exec.Command(command[0], command[1:]...)
	prepareOwnedCommand(process)
	var stdout, stderr limitedBuffer
	stdout.limit = commandOutputLimit
	stderr.limit = commandOutputLimit
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Start()
	if err != nil {
		result.ElapsedSeconds = time.Since(started).Seconds()
		if errors.Is(err, exec.ErrNotFound) ||
			errors.Is(err, os.ErrNotExist) {
			result.ErrorType = "command-unavailable"
		} else {
			result.ErrorType = "process-error"
		}
		result.Error = err.Error()
		return result
	}
	processGroup := ownedCommandGroup(process)
	waited := make(chan error, 1)
	go func() {
		waited <- process.Wait()
	}()
	interrupted := false
	select {
	case err = <-waited:
	case <-ctx.Done():
		interrupted = true
		err = stopOwnedCommand(process, processGroup, waited)
	}
	result.StdoutTruncated = stdout.truncated
	result.StderrTruncated = stderr.truncated
	result.ElapsedSeconds = time.Since(started).Seconds()
	if process.ProcessState != nil {
		code := process.ProcessState.ExitCode()
		result.ReturnCode = &code
	}
	if interrupted {
		result.ErrorType = commandContextErrorType(ctx.Err())
		if result.ErrorType == "timeout" {
			result.Error = "command exceeded its bounded runtime"
		} else {
			result.Error = "command was interrupted"
		}
		return result
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.ErrorType = "nonzero-exit"
		} else if errors.Is(err, exec.ErrNotFound) ||
			errors.Is(err, os.ErrNotExist) {
			result.ErrorType = "command-unavailable"
		} else {
			result.ErrorType = "process-error"
		}
		result.Error = boundedTail(
			strings.TrimSpace(stderr.String()),
			commandErrorLimit,
		)
		if result.Error == "" {
			result.Error = err.Error()
		}
	}
	if stdout.truncated {
		result.ErrorType = "output-limit"
		result.Error = "command JSON exceeded the bounded output limit"
		return result
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if decodeErr := decoder.Decode(&payload); decodeErr != nil {
		result.ErrorType = "invalid-json"
		result.Error = decodeErr.Error()
		return result
	}
	if decoder.Decode(new(any)) != io.EOF {
		result.ErrorType = "invalid-json"
		result.Error = "command emitted trailing JSON data"
		return result
	}
	result.Payload = payload
	result.ProviderError = providerDiagnostic(payload)
	payloadOK, valid := payload["ok"].(bool)
	if !valid {
		result.ErrorType = "invalid-payload"
		result.Error = "command JSON must contain a boolean ok field"
		return result
	}
	result.OK = err == nil && payloadOK
	if err == nil && !payloadOK {
		result.ErrorType = "provider-not-ready"
		result.Error = "command returned ok=false"
	}
	return result
}

func commandContextErrorType(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "interrupted"
}

func providerDiagnostic(payload map[string]any) *ProviderDiagnostic {
	sources := make([]map[string]any, 0, 3)
	for _, key := range []string{"error", "error_envelope"} {
		if value, ok := payload[key].(map[string]any); ok {
			sources = append(sources, value)
		}
	}
	sources = append(sources, payload)

	diagnostic := ProviderDiagnostic{}
	for _, source := range sources {
		if diagnostic.Code == "" {
			diagnostic.Code = diagnosticToken(source["code"])
		}
		if diagnostic.ErrClass == "" {
			diagnostic.ErrClass = diagnosticToken(source["err_class"])
			if diagnostic.ErrClass == "" {
				diagnostic.ErrClass = diagnosticToken(source["class"])
			}
		}
		if diagnostic.RetryAt == "" {
			diagnostic.RetryAt = diagnosticTime(source["retry_at"])
		}
	}
	if diagnostic.Code == "" {
		diagnostic.Code = diagnosticToken(payload["error_type"])
	}
	if diagnostic.Code == "" &&
		diagnostic.ErrClass == "" &&
		diagnostic.RetryAt == "" {
		return nil
	}
	return &diagnostic
}

func diagnosticToken(value any) string {
	text, ok := value.(string)
	if !ok || len(text) == 0 || len(text) > 96 {
		return ""
	}
	for index, char := range text {
		if index == 0 {
			if char < 'a' || char > 'z' {
				return ""
			}
			continue
		}
		if (char < 'a' || char > 'z') &&
			(char < '0' || char > '9') &&
			char != '_' &&
			char != '-' {
			return ""
		}
	}
	return text
}

func diagnosticTime(value any) string {
	text, ok := value.(string)
	if !ok || len(text) == 0 || len(text) > len(time.RFC3339Nano)+20 {
		return ""
	}
	if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
		return ""
	}
	return text
}

type limitedBuffer struct {
	data      bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := buffer.limit - buffer.data.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
			buffer.truncated = true
		}
		_, _ = buffer.data.Write(value)
	} else if original > 0 {
		buffer.truncated = true
	}
	return original, nil
}

func (buffer *limitedBuffer) Bytes() []byte {
	return buffer.data.Bytes()
}

func (buffer *limitedBuffer) String() string {
	return buffer.data.String()
}

func boundedTail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func acquireMaintenanceLock() (func(), error) {
	path, err := lockPath()
	if err != nil {
		return nil, err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create maintenance state directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect maintenance state directory: %w", err)
	}
	release, busy, err := acquireProcessLock(path)
	if busy {
		return nil, fmt.Errorf(
			"maintenance coordinator is already active; inspect owner-only lock %s",
			displayPath(path),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("acquire maintenance lock: %w", err)
	}
	return release, nil
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
