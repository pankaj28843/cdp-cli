package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

const (
	maxDaemonHoldEnvironmentBytes      = 64 << 10
	maxDaemonHoldEnvironmentValueBytes = 16 << 10
	daemonHoldExitWait                 = 2 * time.Second
)

var daemonHoldEnvironmentKeys = map[string]struct{}{
	"CDP_DAEMON_STATE_DIR":                 {},
	"CDP_DAEMON_BROWSER_MODE":              {},
	"CDP_DAEMON_CONNECTION_MODE":           {},
	"CDP_DAEMON_SOCKET":                    {},
	"CDP_DAEMON_HOLD_ENDPOINT":             {},
	"CDP_DAEMON_USER_DATA_DIR":             {},
	"CDP_DAEMON_MANAGED_PROFILE_PATH":      {},
	"CDP_DAEMON_PROFILE_SEED_STRATEGY":     {},
	"CDP_DAEMON_CHROME_PID":                {},
	"CDP_DAEMON_CHROME_PORT":               {},
	"CDP_DAEMON_CHROME_PROCESS_START_TIME": {},
}

// daemonHoldProcess is intentionally private. Its command line and parent
// relationship are used only during the ephemeral ownership decision; they
// are never part of the public reconciliation result.
type daemonHoldProcess struct {
	PID        int
	ParentPID  int
	Executable string
	Args       []string
}

type daemonHoldProcessTableOutput struct {
	data      []byte
	truncated bool
}

func (output *daemonHoldProcessTableOutput) Write(p []byte) (int, error) {
	remaining := (4 << 20) - len(output.data)
	if remaining <= 0 {
		output.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		output.data = append(output.data, p[:remaining]...)
		output.truncated = true
		return len(p), nil
	}
	output.data = append(output.data, p...)
	return len(p), nil
}

// DaemonHoldOwnershipEvidence is the metadata-only explanation for one
// candidate daemon hold. It deliberately excludes command lines, environment
// values, endpoints, sockets, profiles, and opaque process identities.
type DaemonHoldOwnershipEvidence struct {
	PID             int      `json:"pid"`
	ParentPID       int      `json:"parent_pid,omitempty"`
	State           string   `json:"state"`
	GenerationState string   `json:"generation_state"`
	OwnershipChecks []string `json:"ownership_checks,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

type DaemonHoldSignalFailure struct {
	PID   int    `json:"pid"`
	Error string `json:"error"`
}

// DaemonHoldReconcileResult is safe to return to an agent. It contains only
// PIDs, stable state/reason labels, and bounded counts; private process
// environment and generation tokens stay inside the daemon package.
type DaemonHoldReconcileResult struct {
	Checked        bool                          `json:"checked"`
	State          string                        `json:"state"`
	BrowserMode    string                        `json:"browser_mode"`
	ActivePID      int                           `json:"active_pid,omitempty"`
	ConsideredPIDs []int                         `json:"considered_pids,omitempty"`
	EligiblePIDs   []int                         `json:"eligible_pids,omitempty"`
	ReclaimedPIDs  []int                         `json:"reclaimed_pids,omitempty"`
	SkippedPIDs    []int                         `json:"skipped_pids,omitempty"`
	SkipReasons    map[string]int                `json:"skip_reasons"`
	SignalFailures []DaemonHoldSignalFailure     `json:"signal_failures,omitempty"`
	SafetyChecks   []string                      `json:"safety_checks,omitempty"`
	Candidates     []DaemonHoldOwnershipEvidence `json:"candidates,omitempty"`
	Reason         string                        `json:"reason,omitempty"`
	NextCommands   []string                      `json:"next_commands,omitempty"`
}

var (
	daemonHoldProcessLister      = listDaemonHoldProcesses
	daemonHoldProcessEnvironment = readDaemonHoldEnvironment
	daemonHoldProcessStartTime   = processgroup.ProcessStartTime
	daemonHoldProcessRunning     = ProcessRunningContext
	daemonHoldProcessSignal      = processgroup.TerminatePID
	daemonHoldExecutable         = os.Executable
)

// ReconcileOrphanedDaemonHolds inventories and, when reap is true, reclaims
// exact superseded detached headless daemon holds. The active mode-scoped
// runtime must be strongly identified and live before any candidate is
// inspected for destructive cleanup. Ambiguous candidates are returned as
// skips and are never signaled.
func ReconcileOrphanedDaemonHolds(ctx context.Context, stateDir, browserMode string, reap bool) (DaemonHoldReconcileResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := DaemonHoldReconcileResult{
		Checked:     true,
		State:       "pending",
		BrowserMode: runtimeModeName(browserMode),
		SkipReasons: map[string]int{},
		SafetyChecks: []string{
			"headless_mode_scoped",
			"exact_executable_and_arguments",
			"adopted_parent_boundary",
			"state_profile_mode_socket_match",
			"strong_process_generation",
			"active_runtime_recheck",
			"candidate_generation_recheck",
			"exact_process_group_signal",
		},
		NextCommands: []string{
			"cdp --browser-mode headless daemon health --json",
			"cdp --browser-mode headless daemon logs --tail 50 --json",
		},
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if result.BrowserMode != "headless" {
		result.State = "unsupported_mode"
		result.Reason = "orphaned hold reconciliation is headless-only"
		addDaemonHoldSkipReason(&result, "unsupported_mode")
		return result, nil
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		result.State = "unsupported_platform"
		result.Reason = "process ownership inspection is unavailable on this platform"
		addDaemonHoldSkipReason(&result, "unsupported_platform")
		return result, nil
	}

	current, ok, err := LoadRuntimeForMode(ctx, stateDir, result.BrowserMode)
	if err != nil {
		return result, err
	}
	if !ok {
		result.State = "no_runtime"
		result.Reason = "no current mode-scoped runtime is recorded"
		addDaemonHoldSkipReason(&result, "no_current_runtime")
		return result, nil
	}
	result.ActivePID = current.PID
	if !processgroup.IsStrongProcessStartIdentity(current.ProcessStartTime) {
		result.State = "runtime_unverified"
		result.Reason = "current runtime has no strong process generation identity"
		addDaemonHoldSkipReason(&result, "missing_current_generation")
		return result, nil
	}
	currentCheck := CheckRuntimeProcess(ctx, current)
	if currentCheck.State == RuntimeProcessStateCanceled && ctx.Err() != nil {
		return result, ctx.Err()
	}
	if !currentCheck.Running {
		result.State = "runtime_unverified"
		result.Reason = "current runtime process is not strongly verified as live"
		addDaemonHoldSkipReason(&result, "current_runtime_unverified")
		return result, nil
	}

	executable, err := daemonHoldExecutable()
	if err != nil || strings.TrimSpace(executable) == "" {
		result.State = "inspection_unavailable"
		result.Reason = "current executable identity is unavailable"
		addDaemonHoldSkipReason(&result, "executable_unavailable")
		return result, nil
	}
	executable, ok = canonicalDaemonHoldPath(executable)
	if !ok {
		result.State = "inspection_unavailable"
		result.Reason = "current executable identity is not canonical"
		addDaemonHoldSkipReason(&result, "executable_unavailable")
		return result, nil
	}
	holds, err := daemonHoldProcessLister(ctx)
	if err != nil {
		return result, err
	}
	seen := map[int]bool{}
	for _, hold := range holds {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if hold.PID <= 0 || seen[hold.PID] {
			continue
		}
		seen[hold.PID] = true
		result.ConsideredPIDs = append(result.ConsideredPIDs, hold.PID)
		evidence := DaemonHoldOwnershipEvidence{
			PID:             hold.PID,
			ParentPID:       hold.ParentPID,
			State:           "skipped",
			GenerationState: "unknown",
		}
		addCheck := func(check string) {
			evidence.OwnershipChecks = append(evidence.OwnershipChecks, check)
		}
		skip := func(reason string) {
			evidence.Reason = reason
			result.Candidates = append(result.Candidates, evidence)
			addDaemonHoldSkip(&result, hold.PID, reason)
		}
		if hold.PID == current.PID {
			evidence.State = "current"
			evidence.GenerationState = "current"
			addCheck("current_runtime_pid")
			skip("current_runtime")
			continue
		}
		if !daemonHoldInvocationMatches(hold) {
			skip("invocation_mismatch")
			continue
		}
		if !daemonHoldExecutableMatches(hold, executable) {
			skip("executable_mismatch")
			continue
		}
		addCheck("exact_executable")
		addCheck("exact_arguments")
		if hold.ParentPID != 1 {
			skip("not_orphaned")
			continue
		}
		addCheck("adopted_parent")
		environment, environmentErr := daemonHoldProcessEnvironment(ctx, hold.PID)
		if environmentErr != nil {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			skip("environment_unavailable")
			continue
		}
		if reason := daemonHoldEnvironmentMismatch(environment, stateDir, current); reason != "" {
			skip(reason)
			continue
		}
		addCheck("state_root_match")
		addCheck("headless_mode_match")
		addCheck("connection_mode_match")
		addCheck("socket_match")
		addCheck("profile_match")
		candidateStart, startErr := daemonHoldProcessStartTime(ctx, hold.PID)
		if startErr != nil {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			skip("generation_unavailable")
			continue
		}
		if !processgroup.IsStrongProcessStartIdentity(candidateStart) {
			skip("generation_unavailable")
			continue
		}
		if strings.TrimSpace(candidateStart) == strings.TrimSpace(current.ProcessStartTime) {
			skip("generation_not_distinct")
			continue
		}
		addCheck("strong_process_generation")
		addCheck("generation_distinct_from_current")
		candidateRunning, runningErr := daemonHoldProcessRunning(ctx, hold.PID)
		if runningErr != nil {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			skip("liveness_unavailable")
			continue
		}
		if !candidateRunning {
			skip("process_not_running")
			continue
		}
		addCheck("candidate_live")
		evidence.GenerationState = "superseded"
		if !reap {
			evidence.State = "eligible"
			result.Candidates = append(result.Candidates, evidence)
			result.EligiblePIDs = append(result.EligiblePIDs, hold.PID)
			continue
		}

		current, currentOK, recheckErr := LoadRuntimeForMode(ctx, stateDir, result.BrowserMode)
		if recheckErr != nil {
			return result, recheckErr
		}
		if !currentOK || !sameDaemonRuntimeGeneration(current, result.ActivePID, currentCheck, current.ProcessStartTime) {
			skip("runtime_replaced")
			continue
		}
		if !CheckRuntimeProcess(ctx, current).Running {
			skip("runtime_recheck_unverified")
			continue
		}
		addCheck("active_runtime_recheck")

		latestHolds, latestErr := daemonHoldProcessLister(ctx)
		if latestErr != nil {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			skip("candidate_recheck_unavailable")
			continue
		}
		latest, latestOK := daemonHoldByPID(latestHolds, hold.PID)
		if !latestOK || !daemonHoldProcessEquivalent(hold, latest) {
			skip("candidate_changed")
			continue
		}
		latestEnvironment, latestEnvironmentErr := daemonHoldProcessEnvironment(ctx, hold.PID)
		if latestEnvironmentErr != nil {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			skip("candidate_environment_recheck_unavailable")
			continue
		}
		if reason := daemonHoldEnvironmentMismatch(latestEnvironment, stateDir, current); reason != "" {
			skip("candidate_ownership_changed")
			continue
		}
		latestStart, latestStartErr := daemonHoldProcessStartTime(ctx, hold.PID)
		if latestStartErr != nil {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			skip("candidate_generation_recheck_unavailable")
			continue
		}
		if strings.TrimSpace(latestStart) != strings.TrimSpace(candidateStart) {
			skip("pid_reused")
			continue
		}
		addCheck("candidate_generation_recheck")
		latestRunning, latestRunningErr := daemonHoldProcessRunning(ctx, hold.PID)
		if latestRunningErr != nil {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			skip("candidate_liveness_recheck_unavailable")
			continue
		}
		if !latestRunning {
			skip("process_not_running")
			continue
		}
		addCheck("candidate_liveness_recheck")
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if signalErr := daemonHoldProcessSignal(hold.PID); signalErr != nil {
			evidence.Reason = "signal_failed"
			result.SignalFailures = append(result.SignalFailures, DaemonHoldSignalFailure{PID: hold.PID, Error: "signal_failed"})
			result.Candidates = append(result.Candidates, evidence)
			addDaemonHoldSkip(&result, hold.PID, "signal_failed")
			continue
		}
		gone, goneErr := waitForDaemonHoldExit(ctx, hold.PID)
		if goneErr != nil {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			gone = false
		}
		if !gone {
			evidence.Reason = "post_signal_liveness_unverified"
			result.SignalFailures = append(result.SignalFailures, DaemonHoldSignalFailure{PID: hold.PID, Error: "post_signal_liveness_unverified"})
			result.Candidates = append(result.Candidates, evidence)
			addDaemonHoldSkip(&result, hold.PID, "post_signal_liveness_unverified")
			continue
		}
		evidence.State = "reclaimed"
		evidence.GenerationState = "superseded"
		result.Candidates = append(result.Candidates, evidence)
		result.ReclaimedPIDs = append(result.ReclaimedPIDs, hold.PID)
		appendLogForMode(context.Background(), stateDir, result.BrowserMode, LogEntry{
			Level:   "info",
			Event:   "hold_reclaimed",
			Message: "orphaned daemon hold reclaimed after exact ownership verification",
			PID:     hold.PID,
		})
	}

	switch {
	case len(result.ReclaimedPIDs) > 0 && len(result.SignalFailures) == 0:
		result.State = "reclaimed"
		result.Reason = "verified superseded daemon holds reclaimed"
	case len(result.SignalFailures) > 0:
		result.State = "degraded"
		result.Reason = "one or more verified hold candidates could not be reclaimed"
	case !reap && len(result.EligiblePIDs) > 0:
		result.State = "inspected"
		result.Reason = "verified superseded daemon holds found in read-only mode"
	case len(result.SkippedPIDs) > 0:
		result.State = "skipped"
		result.Reason = "candidate holds were not all safe to reclaim"
	default:
		result.State = "healthy"
		result.Reason = "no superseded daemon holds required reconciliation"
	}
	return result, nil
}

func parseDaemonHoldEnvironment(raw []byte) map[string]string {
	if len(raw) > maxDaemonHoldEnvironmentBytes {
		return map[string]string{}
	}
	environment := map[string]string{}
	for _, item := range bytes.Split(raw, []byte{0}) {
		key, value, ok := strings.Cut(string(item), "=")
		if !ok {
			continue
		}
		if _, allow := daemonHoldEnvironmentKeys[key]; !allow || len(value) > maxDaemonHoldEnvironmentValueBytes {
			continue
		}
		environment[key] = value
	}
	return environment
}

// parseDaemonHoldEnvironmentText parses the owner-visible, space-delimited
// environment suffix emitted by Darwin ps. Values are recovered by locating
// the next allowlisted key instead of splitting on spaces, so state/profile
// paths containing spaces remain exact while unrelated environment content is
// discarded.
func parseDaemonHoldEnvironmentText(raw []byte) map[string]string {
	if len(raw) > maxDaemonHoldEnvironmentBytes {
		return map[string]string{}
	}
	text := string(raw)
	environment := map[string]string{}
	for key := range daemonHoldEnvironmentKeys {
		marker := key + "="
		start := daemonHoldEnvironmentAssignment(text, marker)
		if start < 0 {
			continue
		}
		valueStart := start + len(marker)
		valueEnd := nextDaemonHoldEnvironmentAssignment(text, valueStart)
		value := strings.TrimSpace(text[valueStart:valueEnd])
		if len(value) <= maxDaemonHoldEnvironmentValueBytes {
			environment[key] = value
		}
	}
	return environment
}

func daemonHoldEnvironmentAssignment(text, marker string) int {
	searchFrom := 0
	for searchFrom < len(text) {
		start := strings.Index(text[searchFrom:], marker)
		if start < 0 {
			return -1
		}
		start += searchFrom
		if start == 0 || text[start-1] == ' ' || text[start-1] == '\t' {
			return start
		}
		searchFrom = start + len(marker)
	}
	return -1
}

func nextDaemonHoldEnvironmentAssignment(text string, from int) int {
	for index := from; index < len(text); index++ {
		if text[index] != ' ' && text[index] != '\t' {
			continue
		}
		start := index + 1
		if start >= len(text) || !daemonEnvironmentKeyStart(text[start]) {
			continue
		}
		end := start + 1
		for end < len(text) && daemonEnvironmentKeyPart(text[end]) {
			end++
		}
		if end < len(text) && text[end] == '=' {
			return index
		}
	}
	return len(text)
}

func daemonEnvironmentKeyStart(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || value == '_'
}

func daemonEnvironmentKeyPart(value byte) bool {
	return daemonEnvironmentKeyStart(value) || (value >= '0' && value <= '9')
}

func daemonHoldInvocationMatches(hold daemonHoldProcess) bool {
	return len(hold.Args) == 3 && hold.Args[1] == "daemon" && hold.Args[2] == "hold"
}

func daemonHoldExecutableMatches(hold daemonHoldProcess, executable string) bool {
	if !daemonHoldInvocationMatches(hold) {
		return false
	}
	actual, ok := canonicalDaemonHoldPath(hold.Executable)
	return ok && actual == executable && hold.Args[0] == hold.Executable
}

func daemonHoldProcessEquivalent(left, right daemonHoldProcess) bool {
	if left.PID != right.PID || left.ParentPID != right.ParentPID || left.Executable != right.Executable || len(left.Args) != len(right.Args) {
		return false
	}
	for index := range left.Args {
		if left.Args[index] != right.Args[index] {
			return false
		}
	}
	return true
}

func daemonHoldByPID(processes []daemonHoldProcess, pid int) (daemonHoldProcess, bool) {
	for _, process := range processes {
		if process.PID == pid {
			return process, true
		}
	}
	return daemonHoldProcess{}, false
}

func daemonHoldEnvironmentMismatch(environment map[string]string, stateDir string, current Runtime) string {
	if !sameDaemonHoldPath(environment["CDP_DAEMON_STATE_DIR"], stateDir) {
		return "state_root_mismatch"
	}
	if environment["CDP_DAEMON_BROWSER_MODE"] != "headless" {
		return "browser_mode_mismatch"
	}
	if strings.TrimSpace(current.ConnectionMode) == "" || environment["CDP_DAEMON_CONNECTION_MODE"] != current.ConnectionMode {
		return "connection_mode_mismatch"
	}
	expectedSocket := current.SocketPath
	if strings.TrimSpace(expectedSocket) == "" {
		expectedSocket = RuntimeSocketPathForMode(stateDir, "headless")
	}
	if !sameDaemonHoldPath(environment["CDP_DAEMON_SOCKET"], expectedSocket) {
		return "socket_mismatch"
	}
	if strings.TrimSpace(environment["CDP_DAEMON_HOLD_ENDPOINT"]) == "" {
		return "missing_endpoint_evidence"
	}
	profile := strings.TrimSpace(environment["CDP_DAEMON_USER_DATA_DIR"])
	if profile == "" || strings.TrimSpace(current.UserDataDir) == "" {
		return "missing_profile_evidence"
	}
	if !sameDaemonHoldPath(profile, current.UserDataDir) {
		return "profile_mismatch"
	}
	if reason := matchOptionalDaemonHoldPath(environment, "CDP_DAEMON_MANAGED_PROFILE_PATH", current.ManagedProfilePath, "managed_profile_mismatch"); reason != "" {
		return reason
	}
	if strings.TrimSpace(current.ProfileSeedStrategy) != "" && environment["CDP_DAEMON_PROFILE_SEED_STRATEGY"] != current.ProfileSeedStrategy {
		return "profile_seed_strategy_mismatch"
	}
	return ""
}

func matchOptionalDaemonHoldPath(environment map[string]string, key, current, mismatchReason string) string {
	value := strings.TrimSpace(environment[key])
	current = strings.TrimSpace(current)
	if current == "" {
		if value != "" {
			return mismatchReason
		}
		return ""
	}
	if value == "" || !sameDaemonHoldPath(value, current) {
		return mismatchReason
	}
	return ""
}

func sameDaemonRuntimeGeneration(runtime Runtime, activePID int, previous RuntimeProcessCheck, previousStart string) bool {
	if runtime.PID != activePID || strings.TrimSpace(runtime.ProcessStartTime) != strings.TrimSpace(previousStart) {
		return false
	}
	return previous.Running && processgroup.IsStrongProcessStartIdentity(runtime.ProcessStartTime)
}

func sameDaemonHoldPath(left, right string) bool {
	leftPath, leftOK := canonicalDaemonHoldPath(left)
	rightPath, rightOK := canonicalDaemonHoldPath(right)
	return leftOK && rightOK && leftPath == rightPath
}

func canonicalDaemonHoldPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

func waitForDaemonHoldExit(ctx context.Context, pid int) (bool, error) {
	deadline := time.Now().Add(daemonHoldExitWait)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		running, err := daemonHoldProcessRunning(ctx, pid)
		if err != nil {
			return false, err
		}
		if !running {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}

func addDaemonHoldSkip(result *DaemonHoldReconcileResult, pid int, reason string) {
	if result == nil {
		return
	}
	result.SkippedPIDs = appendUniqueDaemonHoldPID(result.SkippedPIDs, pid)
	addDaemonHoldSkipReason(result, reason)
}

func addDaemonHoldSkipReason(result *DaemonHoldReconcileResult, reason string) {
	if result == nil {
		return
	}
	if result.SkipReasons == nil {
		result.SkipReasons = map[string]int{}
	}
	result.SkipReasons[reason]++
}

func appendUniqueDaemonHoldPID(values []int, pid int) []int {
	for _, value := range values {
		if value == pid {
			return values
		}
	}
	return append(values, pid)
}
