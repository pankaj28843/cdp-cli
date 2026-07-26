package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	CalibrationStateSchemaVersion = "claude-calibration-state/v1"
	RelativeCalibrationStatePath  = "webagent/claude/calibration.json"
)

type CalibrationStateRecord struct {
	SchemaVersion     string               `json:"schema_version"`
	RunID             string               `json:"run_id"`
	State             string               `json:"state"`
	PromptFingerprint string               `json:"prompt_fingerprint"`
	TargetID          string               `json:"target_id,omitempty"`
	ConversationID    string               `json:"conversation_id,omitempty"`
	SendDispatch      browserflow.Dispatch `json:"send_dispatch,omitempty"`
	DeleteDispatch    browserflow.Dispatch `json:"delete_dispatch,omitempty"`
	Postcondition     string               `json:"postcondition,omitempty"`
	TargetClosed      bool                 `json:"target_closed"`
	UpdatedAt         string               `json:"updated_at"`
}

type CalibrationStatusData struct {
	SchemaVersion       string               `json:"schema_version"`
	State               string               `json:"state"`
	RunID               string               `json:"run_id,omitempty"`
	ConversationPresent bool                 `json:"conversation_present"`
	SendDispatch        browserflow.Dispatch `json:"send_dispatch,omitempty"`
	DeleteDispatch      browserflow.Dispatch `json:"delete_dispatch,omitempty"`
	Postcondition       string               `json:"postcondition,omitempty"`
	TargetClosed        bool                 `json:"target_closed"`
	RecoveryRequired    bool                 `json:"recovery_required"`
}

type CalibrationStore struct {
	path string
}

type CalibrationCleanupConfig struct {
	Store       *CalibrationStore
	Journal     browserflow.Journal
	Engine      *browserflow.Engine
	Delete      DeleteConfig
	BuildCommit string
}

func UnavailableCalibrationStatus(
	buildCommit string,
	code string,
	errClass string,
	message string,
) webagent.Result {
	return calibrationMetadataFailure(
		buildCommit,
		code,
		errClass,
		message,
		CalibrationStatusData{
			SchemaVersion:    CalibrationStateSchemaVersion,
			State:            "unavailable",
			RecoveryRequired: true,
		},
	)
}

func NewCalibrationStore(stateDir string) (*CalibrationStore, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("Claude calibration state directory is required")
	}
	return &CalibrationStore{
		path: filepath.Join(stateDir, filepath.FromSlash(RelativeCalibrationStatePath)),
	}, nil
}

func (s *CalibrationStore) Save(ctx context.Context, record CalibrationStateRecord) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("Claude calibration store is not configured")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Claude calibration state: %w", err)
	}
	data = append(data, '\n')
	return artifacts.WithOwnerOnlyFileLock(ctx, s.path+".lock", func() error {
		return artifacts.WriteOwnerOnlyFileAtomic(s.path, data)
	})
}

func (s *CalibrationStore) Load(ctx context.Context) (CalibrationStateRecord, bool, error) {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return CalibrationStateRecord{}, false, fmt.Errorf("Claude calibration store is not configured")
	}
	select {
	case <-ctx.Done():
		return CalibrationStateRecord{}, false, ctx.Err()
	default:
	}
	data, err := artifacts.ReadOwnerOnlyFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CalibrationStateRecord{}, false, nil
		}
		return CalibrationStateRecord{}, false, err
	}
	var record CalibrationStateRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return CalibrationStateRecord{}, false, fmt.Errorf("parse Claude calibration state: %w", err)
	}
	if err := record.Validate(); err != nil {
		return CalibrationStateRecord{}, false, err
	}
	return record, true, nil
}

func (r CalibrationStateRecord) Validate() error {
	if r.SchemaVersion != CalibrationStateSchemaVersion {
		return fmt.Errorf("schema_version must be %q", CalibrationStateSchemaVersion)
	}
	if !organizationPattern.MatchString(r.RunID) {
		return fmt.Errorf("invalid calibration run_id")
	}
	switch r.State {
	case "planned", "target_owned", "acknowledged", "delete_pending",
		"deleted", "send_not_performed", "submission_unacknowledged",
		"delete_not_performed", "deletion_ambiguous", "failed":
	default:
		return fmt.Errorf("invalid calibration state %q", r.State)
	}
	if len(r.PromptFingerprint) != 64 {
		return fmt.Errorf("calibration prompt fingerprint must be SHA-256")
	}
	if r.TargetID != "" && !organizationPattern.MatchString(r.TargetID) {
		return fmt.Errorf("invalid calibration target_id")
	}
	if r.ConversationID != "" && !organizationPattern.MatchString(r.ConversationID) {
		return fmt.Errorf("invalid calibration conversation_id")
	}
	for name, dispatch := range map[string]browserflow.Dispatch{
		"send_dispatch":   r.SendDispatch,
		"delete_dispatch": r.DeleteDispatch,
	} {
		if dispatch != "" &&
			dispatch != browserflow.DispatchNotPerformed &&
			dispatch != browserflow.DispatchPerformed &&
			dispatch != browserflow.DispatchUnknown {
			return fmt.Errorf("invalid %s %q", name, dispatch)
		}
	}
	if r.Postcondition != "" && r.Postcondition != deletePostconditionProof {
		return fmt.Errorf("invalid calibration postcondition")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.UpdatedAt); err != nil {
		return fmt.Errorf("updated_at must be RFC3339: %w", err)
	}
	return nil
}

func CalibrationStatus(
	ctx context.Context,
	store *CalibrationStore,
	journal browserflow.Journal,
	buildCommit string,
) webagent.Result {
	data := CalibrationStatusData{
		SchemaVersion: CalibrationStateSchemaVersion,
		State:         "not_run",
		TargetClosed:  true,
	}
	record, found, err := store.Load(ctx)
	if err != nil {
		return calibrationMetadataFailure(
			buildCommit,
			"claude_calibration_state_unavailable",
			"internal",
			"Claude calibration state is unavailable",
			data,
		)
	}
	if !found {
		return calibrationMetadataSuccess(buildCommit, data)
	}
	data.State = record.State
	data.RunID = record.RunID
	data.ConversationPresent = calibrationConversationPresent(record)
	data.SendDispatch = record.SendDispatch
	data.DeleteDispatch = record.DeleteDispatch
	data.Postcondition = record.Postcondition
	data.TargetClosed = record.TargetClosed || record.TargetID == ""
	if journal != nil {
		if recovery, loadErr := journal.Load(ctx, record.RunID); loadErr == nil {
			data.TargetClosed = recovery.Phase == browserflow.PhaseClosed &&
				recovery.Cleanup == browserflow.CleanupClosed
		}
	}
	data.RecoveryRequired = calibrationRecoveryRequired(record, data.TargetClosed)
	return calibrationMetadataSuccess(buildCommit, data)
}

func CleanupCalibration(
	ctx context.Context,
	config CalibrationCleanupConfig,
) webagent.Result {
	if config.Store == nil || config.Journal == nil {
		return calibrationMetadataFailure(
			config.BuildCommit,
			"claude_calibration_cleanup_unavailable",
			"internal",
			"Claude calibration cleanup is not configured",
			CalibrationStatusData{
				SchemaVersion:    CalibrationStateSchemaVersion,
				State:            "unavailable",
				RecoveryRequired: true,
			},
		)
	}
	record, found, err := config.Store.Load(ctx)
	if err != nil {
		return calibrationMetadataFailure(
			config.BuildCommit,
			"claude_calibration_state_unavailable",
			"internal",
			"Claude calibration state is unavailable",
			CalibrationStatusData{
				SchemaVersion:    CalibrationStateSchemaVersion,
				State:            "unavailable",
				RecoveryRequired: true,
			},
		)
	}
	if !found {
		return calibrationMetadataSuccess(
			config.BuildCommit,
			CalibrationStatusData{
				SchemaVersion: CalibrationStateSchemaVersion,
				State:         "not_run",
				TargetClosed:  true,
			},
		)
	}
	if record.TargetID == "" {
		record.TargetClosed = true
	}
	recovery, loadErr := config.Journal.Load(ctx, record.RunID)
	if !record.TargetClosed && loadErr != nil {
		return calibrationCleanupFailure(
			config.BuildCommit,
			record,
			"claude_calibration_recovery_unavailable",
			"cleanup",
			"Claude calibration exact target recovery state is unavailable",
			nil,
			webagent.CleanupEvidence{
				Required: true,
				State:    webagent.CleanupFailed,
				TargetID: record.TargetID,
				RecoveryCommand: fmt.Sprintf(
					"cdp workflow agent recovery inspect %s --json",
					record.RunID,
				),
			},
		)
	}
	if loadErr == nil &&
		(recovery.Phase != browserflow.PhaseClosed ||
			recovery.Cleanup != browserflow.CleanupClosed) {
		if config.Engine == nil {
			return calibrationCleanupFailure(
				config.BuildCommit,
				record,
				"claude_calibration_cleanup_unavailable",
				"internal",
				"Claude calibration exact target cleanup is not configured",
				nil,
				webagent.CleanupEvidence{
					Required: true,
					State:    webagent.CleanupPending,
					TargetID: record.TargetID,
					RecoveryCommand: fmt.Sprintf(
						"cdp workflow agent recovery close %s --json",
						record.RunID,
					),
				},
			)
		}
		cleanup, recoverErr := config.Engine.Recover(ctx, record.RunID)
		if recoverErr != nil || cleanup.State != browserflow.CleanupClosed ||
			!cleanup.TargetGone {
			return calibrationCleanupFailure(
				config.BuildCommit,
				record,
				"claude_calibration_target_cleanup_failed",
				"cleanup",
				"Claude calibration exact target cleanup could not be proved",
				nil,
				webagent.CleanupEvidence{
					Required: true,
					State:    webagent.CleanupFailed,
					TargetID: record.TargetID,
					RecoveryCommand: fmt.Sprintf(
						"cdp workflow agent recovery close %s --json",
						record.RunID,
					),
				},
			)
		}
		record.TargetClosed = true
	}
	if loadErr == nil && recovery.Phase == browserflow.PhaseClosed {
		record.TargetClosed = true
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := config.Store.Save(ctx, record); err != nil {
		return calibrationMetadataFailure(
			config.BuildCommit,
			"claude_calibration_state_unavailable",
			"internal",
			"Claude calibration cleanup state could not be persisted",
			calibrationStatusData(record),
		)
	}
	if record.State == "deleted" && record.TargetClosed {
		return calibrationMetadataSuccess(config.BuildCommit, calibrationStatusData(record))
	}
	if record.ConversationID == "" {
		if record.SendDispatch != browserflow.DispatchPerformed &&
			record.SendDispatch != browserflow.DispatchUnknown &&
			record.TargetClosed {
			return calibrationMetadataSuccess(config.BuildCommit, calibrationStatusData(record))
		}
		return calibrationCleanupFailure(
			config.BuildCommit,
			record,
			"claude_calibration_identity_unavailable",
			"completion",
			"Claude calibration has no exact acknowledged conversation identity; no delete was attempted",
			nil,
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		)
	}
	if record.DeleteDispatch == browserflow.DispatchPerformed ||
		record.DeleteDispatch == browserflow.DispatchUnknown {
		return calibrationCleanupFailure(
			config.BuildCommit,
			record,
			"claude_calibration_delete_ambiguous",
			"completion",
			"Claude calibration deletion was already attempted without a terminal postcondition; it will not be repeated",
			&webagent.ActionEvidence{
				Dispatch:         webagent.Dispatch(record.DeleteDispatch),
				AttemptCount:     1,
				RawInputCount:    1,
				RetrySafe:        false,
				PendingPersisted: true,
			},
			webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		)
	}

	deleteResult := DeleteConversation(ctx, config.Delete, record.ConversationID)
	record.DeleteDispatch = actionDispatch(deleteResult.Action)
	if deleteResult.OK {
		record.State = "deleted"
		record.Postcondition = deletePostconditionProof
		record.TargetClosed = true
	} else if deleteResult.Action != nil &&
		deleteResult.Action.Dispatch == webagent.DispatchUnknown {
		record.State = "deletion_ambiguous"
	} else {
		record.State = "delete_not_performed"
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := config.Store.Save(context.Background(), record); err != nil {
		return calibrationMetadataFailure(
			config.BuildCommit,
			"claude_calibration_state_unavailable",
			"internal",
			"Claude calibration cleanup outcome could not be persisted",
			calibrationStatusData(record),
		)
	}
	return calibrationCleanupFromDelete(deleteResult, record)
}

func calibrationStatusData(record CalibrationStateRecord) CalibrationStatusData {
	targetClosed := record.TargetClosed || record.TargetID == ""
	return CalibrationStatusData{
		SchemaVersion:       CalibrationStateSchemaVersion,
		State:               record.State,
		RunID:               record.RunID,
		ConversationPresent: calibrationConversationPresent(record),
		SendDispatch:        record.SendDispatch,
		DeleteDispatch:      record.DeleteDispatch,
		Postcondition:       record.Postcondition,
		TargetClosed:        targetClosed,
		RecoveryRequired:    calibrationRecoveryRequired(record, targetClosed),
	}
}

func calibrationConversationPresent(record CalibrationStateRecord) bool {
	return record.ConversationID != "" &&
		record.State != "deleted" &&
		record.Postcondition != deletePostconditionProof
}

func calibrationRecoveryRequired(
	record CalibrationStateRecord,
	targetClosed bool,
) bool {
	if !targetClosed || calibrationConversationPresent(record) {
		return true
	}
	return record.ConversationID == "" &&
		(record.SendDispatch == browserflow.DispatchPerformed ||
			record.SendDispatch == browserflow.DispatchUnknown)
}

func calibrationCleanupFromDelete(
	deleteResult webagent.Result,
	record CalibrationStateRecord,
) webagent.Result {
	deleteResult.Operation = webagent.OperationCalibrate
	deleteResult.Data = calibrationStatusData(record)
	if deleteResult.OK {
		deleteResult.State = webagent.StateTerminal
		deleteResult.NextCommands = []string{}
	}
	return deleteResult
}

func calibrationCleanupFailure(
	buildCommit string,
	record CalibrationStateRecord,
	code string,
	errClass string,
	message string,
	action *webagent.ActionEvidence,
	cleanup webagent.CleanupEvidence,
) webagent.Result {
	retrySafe := true
	if action != nil {
		retrySafe = action.RetrySafe
	}
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationCalibrate,
		State:         webagent.StateFailed,
		Stage:         webagent.StageCleanupPending,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: retrySafe,
		},
		Action: action,
		Data:   calibrationStatusData(record),
		Evidence: webagent.Evidence{
			RunID:       record.RunID,
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "headed",
			ReadMode:    "owner_only_local_state",
		},
		Cleanup: cleanup,
		NextCommands: []string{
			"cdp workflow agent claude calibration status --json",
		},
	}
}

func calibrationMetadataSuccess(
	buildCommit string,
	data CalibrationStatusData,
) webagent.Result {
	result := webagent.NewMetadataResult(
		webagent.ProviderClaude,
		webagent.OperationCalibrate,
		data,
		buildCommit,
		[]string{},
	)
	result.Evidence.ReadMode = "owner_only_local_state"
	return result
}

func calibrationMetadataFailure(
	buildCommit string,
	code string,
	errClass string,
	message string,
	data CalibrationStatusData,
) webagent.Result {
	return webagent.Result{
		OK:            false,
		SchemaVersion: webagent.OperationSchemaVersion,
		Provider:      webagent.ProviderClaude,
		Operation:     webagent.OperationCalibrate,
		State:         webagent.StateFailed,
		Stage:         webagent.StageMetadata,
		Error: &webagent.OperationError{
			Code:      code,
			ErrClass:  errClass,
			Message:   message,
			RetrySafe: true,
		},
		Data: data,
		Evidence: webagent.Evidence{
			RunID:       webagent.NewRunID(),
			BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "none",
			ReadMode:    "owner_only_local_state",
		},
		Cleanup:      webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{"cdp workflow agent claude calibration status --json"},
	}
}
