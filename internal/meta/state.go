package meta

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	schedulerSchemaVersion = 2
	historySchemaVersion   = 3
	defaultFrequency       = "14d"
	defaultFrequencyMinute = 14 * 24 * 60
	minFrequencyMinutes    = 10
	maxFrequencyMinutes    = 31 * 24 * 60
)

type Enrollment struct {
	IntegrationID    string  `json:"integration_id"`
	Frequency        string  `json:"frequency"`
	FrequencyMinutes int     `json:"frequency_minutes"`
	Enabled          bool    `json:"enabled"`
	CreatedAt        float64 `json:"created_at"`
	UpdatedAt        float64 `json:"updated_at"`
}

type SchedulerState struct {
	SchemaVersion int                   `json:"schema_version"`
	UpdatedAt     float64               `json:"updated_at"`
	Enrollments   map[string]Enrollment `json:"enrollments"`
}

type ActionHistory struct {
	ActionID            string  `json:"action_id"`
	OK                  bool    `json:"ok"`
	ReturnCode          *int    `json:"returncode"`
	StartedAt           float64 `json:"started_at"`
	FinishedAt          float64 `json:"finished_at"`
	ErrorType           string  `json:"error_type,omitempty"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
}

type ProviderHistory struct {
	OK               bool            `json:"ok"`
	LastInvocationOK bool            `json:"last_invocation_ok"`
	StartedAt        float64         `json:"started_at"`
	FinishedAt       float64         `json:"finished_at"`
	FailedActionID   string          `json:"failed_action_id,omitempty"`
	Actions          []ActionHistory `json:"actions"`
	LastSuccessAt    *float64        `json:"last_success_at"`
}

type LastRunState struct {
	SchemaVersion int                        `json:"schema_version"`
	UpdatedAt     float64                    `json:"updated_at"`
	LastRuns      map[string]ProviderHistory `json:"last_runs"`
}

func stateRoot() (string, error) {
	return cdpStateRoot()
}

func cdpStateRoot() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CDP_STATE_DIR")); configured != "" {
		return absoluteClean(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".cdp-cli"), nil
}

func absoluteClean(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if absolute == string(filepath.Separator) {
		return "", fmt.Errorf("state root must not be the filesystem root")
	}
	return absolute, nil
}

func schedulerPath() (string, error) {
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "meta", "maintenance.json"), nil
}

func historyPath() (string, error) {
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "meta", "maintenance-last-run.json"), nil
}

func lockPath() (string, error) {
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "meta", "maintenance.lock"), nil
}

func logPath() (string, error) {
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "meta", "maintenance.log"), nil
}

func defaultSchedulerState() SchedulerState {
	state := SchedulerState{
		SchemaVersion: schedulerSchemaVersion,
		Enrollments:   make(map[string]Enrollment),
	}
	return withDefaultEnrollments(state)
}

func withDefaultEnrollments(state SchedulerState) SchedulerState {
	if state.Enrollments == nil {
		state.Enrollments = make(map[string]Enrollment)
	}
	for _, integration := range registry {
		if len(integration.MaintenanceActions) == 0 {
			continue
		}
		if _, exists := state.Enrollments[integration.ID]; exists {
			continue
		}
		state.Enrollments[integration.ID] = Enrollment{
			IntegrationID:    integration.ID,
			Frequency:        defaultFrequency,
			FrequencyMinutes: defaultFrequencyMinute,
			Enabled:          false,
		}
	}
	state.SchemaVersion = schedulerSchemaVersion
	return state
}

func loadSchedulerState() (SchedulerState, error) {
	path, err := schedulerPath()
	if err != nil {
		return SchedulerState{}, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return defaultSchedulerState(), nil
	} else if err != nil {
		return SchedulerState{}, fmt.Errorf(
			"inspect maintenance state: %w",
			err,
		)
	}
	var state SchedulerState
	if err := readJSONFile(path, &state); err != nil {
		return SchedulerState{}, fmt.Errorf(
			"read maintenance state: %w",
			err,
		)
	}
	if state.SchemaVersion != schedulerSchemaVersion {
		return SchedulerState{}, fmt.Errorf(
			"maintenance state schema %d is unsupported",
			state.SchemaVersion,
		)
	}
	for id, enrollment := range state.Enrollments {
		if id != enrollment.IntegrationID {
			return SchedulerState{}, fmt.Errorf(
				"maintenance enrollment key %q does not match integration %q",
				id,
				enrollment.IntegrationID,
			)
		}
		if _, err := integrationByID(id); err != nil {
			return SchedulerState{}, err
		}
		if enrollment.FrequencyMinutes < minFrequencyMinutes ||
			enrollment.FrequencyMinutes > maxFrequencyMinutes {
			return SchedulerState{}, fmt.Errorf(
				"maintenance enrollment %q has invalid frequency",
				id,
			)
		}
	}
	return withDefaultEnrollments(state), nil
}

func saveSchedulerState(state SchedulerState) error {
	path, err := schedulerPath()
	if err != nil {
		return err
	}
	return writePrivateJSON(path, state)
}

func loadLastRunState() (LastRunState, error) {
	path, err := historyPath()
	if err != nil {
		return LastRunState{}, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return LastRunState{
			SchemaVersion: historySchemaVersion,
			LastRuns:      make(map[string]ProviderHistory),
		}, nil
	} else if err != nil {
		return LastRunState{}, fmt.Errorf(
			"inspect maintenance history: %w",
			err,
		)
	}
	raw, err := readFileLimited(path, 4<<20)
	if err != nil {
		return LastRunState{}, fmt.Errorf(
			"read maintenance history: %w",
			err,
		)
	}
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return LastRunState{}, fmt.Errorf(
			"read maintenance history: %w",
			err,
		)
	}
	if envelope.SchemaVersion == 2 {
		// The retired runtime persisted provider JSON and stderr tails. Treat
		// that history as unknown; the next Go maintenance run
		// atomically replaces it with the metadata-only v3 ledger.
		return LastRunState{
			SchemaVersion: historySchemaVersion,
			LastRuns:      make(map[string]ProviderHistory),
		}, nil
	}
	var state LastRunState
	if err := decodeStrictJSON(raw, &state); err != nil {
		return LastRunState{}, fmt.Errorf(
			"read maintenance history: %w",
			err,
		)
	}
	if state.SchemaVersion != historySchemaVersion {
		return LastRunState{}, fmt.Errorf(
			"maintenance history schema %d is unsupported",
			state.SchemaVersion,
		)
	}
	if state.LastRuns == nil {
		state.LastRuns = make(map[string]ProviderHistory)
	}
	return state, nil
}

func saveLastRunState(state LastRunState) error {
	path, err := historyPath()
	if err != nil {
		return err
	}
	return writePrivateJSON(path, state)
}

func readJSONFile(path string, target any) error {
	raw, err := readFileLimited(path, 4<<20)
	if err != nil {
		return err
	}
	return decodeStrictJSON(raw, target)
}

func readFileLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("JSON file exceeds %d bytes", limit)
	}
	return raw, nil
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("JSON file contains trailing data")
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create private state directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protect private state directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".cdp-cli-meta-state-*")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("protect temporary state file: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish private state: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect private state file: %w", err)
	}
	published = true
	return syncDirectory(directory)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func parseFrequency(value string) (int, error) {
	text := strings.ToLower(strings.TrimSpace(value))
	if len(text) < 2 {
		return 0, fmt.Errorf("frequency must look like 90m, 14d, or 31d")
	}
	amount, err := strconv.Atoi(text[:len(text)-1])
	if err != nil || amount <= 0 {
		return 0, fmt.Errorf("frequency amount must be a positive integer")
	}
	var minutes int
	switch text[len(text)-1] {
	case 'm':
		minutes = amount
	case 'h':
		minutes = amount * 60
	case 'd':
		minutes = amount * 24 * 60
	default:
		return 0, fmt.Errorf("frequency unit must be m, h, or d")
	}
	if minutes < minFrequencyMinutes {
		return 0, fmt.Errorf("frequency must be at least 10m")
	}
	if minutes > maxFrequencyMinutes {
		return 0, fmt.Errorf("frequency must be at most 31d")
	}
	return minutes, nil
}

func enrollIntegration(
	state SchedulerState,
	id string,
	frequency string,
	now time.Time,
) (SchedulerState, error) {
	if _, err := integrationByID(id); err != nil {
		return SchedulerState{}, err
	}
	minutes, err := parseFrequency(frequency)
	if err != nil {
		return SchedulerState{}, err
	}
	timestamp := float64(now.UnixNano()) / 1e9
	createdAt := timestamp
	if existing, ok := state.Enrollments[id]; ok {
		createdAt = existing.CreatedAt
	}
	state.Enrollments[id] = Enrollment{
		IntegrationID:    id,
		Frequency:        strings.ToLower(strings.TrimSpace(frequency)),
		FrequencyMinutes: minutes,
		Enabled:          true,
		CreatedAt:        createdAt,
		UpdatedAt:        timestamp,
	}
	state.UpdatedAt = timestamp
	return state, nil
}

func disableIntegration(
	state SchedulerState,
	id string,
	now time.Time,
) (SchedulerState, error) {
	if _, err := integrationByID(id); err != nil {
		return SchedulerState{}, err
	}
	enrollment, ok := state.Enrollments[id]
	if !ok {
		return state, nil
	}
	timestamp := float64(now.UnixNano()) / 1e9
	enrollment.Enabled = false
	enrollment.UpdatedAt = timestamp
	state.Enrollments[id] = enrollment
	state.UpdatedAt = timestamp
	return state, nil
}
