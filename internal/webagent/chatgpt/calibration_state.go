package chatgpt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	CalibrationStateSchemaVersion = "chatgpt-calibration-state/v1"
	RelativeCalibrationStatePath  = "webagent/chatgpt/calibration.json"
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

func NewCalibrationStore(stateDir string) (*CalibrationStore, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf(
			"ChatGPT calibration state directory is required",
		)
	}
	return &CalibrationStore{
		path: filepath.Join(
			stateDir,
			filepath.FromSlash(RelativeCalibrationStatePath),
		),
	}, nil
}

func (s *CalibrationStore) Save(
	ctx context.Context,
	record CalibrationStateRecord,
) error {
	if s == nil || s.path == "" {
		return fmt.Errorf(
			"ChatGPT calibration store is not configured",
		)
	}
	if err := record.Validate(); err != nil {
		return err
	}
	return saveOwnerJSON(ctx, s.path, record)
}

func (s *CalibrationStore) Load(
	ctx context.Context,
) (CalibrationStateRecord, bool, error) {
	if s == nil || s.path == "" {
		return CalibrationStateRecord{}, false, fmt.Errorf(
			"ChatGPT calibration store is not configured",
		)
	}
	var record CalibrationStateRecord
	if err := loadOwnerJSON(ctx, s.path, &record); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CalibrationStateRecord{}, false, nil
		}
		return CalibrationStateRecord{}, false, err
	}
	if err := record.Validate(); err != nil {
		return CalibrationStateRecord{}, false, err
	}
	return record, true, nil
}

func (r CalibrationStateRecord) Validate() error {
	if r.SchemaVersion != CalibrationStateSchemaVersion {
		return fmt.Errorf(
			"schema_version must be %q",
			CalibrationStateSchemaVersion,
		)
	}
	if !conversationIDPattern.MatchString(r.RunID) {
		return fmt.Errorf("invalid ChatGPT calibration run_id")
	}
	switch r.State {
	case "planned", "target_owned", "acknowledged",
		"answer_captured", "delete_pending", "deleted",
		"send_not_performed", "submission_unacknowledged",
		"answer_incomplete", "delete_not_performed",
		"deletion_ambiguous", "failed":
	default:
		return fmt.Errorf(
			"invalid ChatGPT calibration state %q",
			r.State,
		)
	}
	if len(r.PromptFingerprint) != 64 {
		return fmt.Errorf(
			"ChatGPT calibration prompt fingerprint must be SHA-256",
		)
	}
	if r.TargetID != "" &&
		!conversationIDPattern.MatchString(r.TargetID) {
		return fmt.Errorf("invalid ChatGPT calibration target_id")
	}
	if r.ConversationID != "" &&
		!conversationIDPattern.MatchString(r.ConversationID) {
		return fmt.Errorf(
			"invalid ChatGPT calibration conversation_id",
		)
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
	if r.Postcondition != "" &&
		r.Postcondition != deletePostconditionProof {
		return fmt.Errorf(
			"invalid ChatGPT calibration postcondition",
		)
	}
	if _, err := time.Parse(
		time.RFC3339Nano,
		r.UpdatedAt,
	); err != nil {
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
	if store == nil {
		return calibrationMetadataFailure(
			buildCommit,
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration state is unavailable",
			data,
		)
	}
	record, found, err := store.Load(ctx)
	if err != nil {
		return calibrationMetadataFailure(
			buildCommit,
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration state is unavailable",
			data,
		)
	}
	if !found {
		return calibrationMetadataSuccess(buildCommit, data)
	}
	mergeChatGPTCalibrationRecovery(ctx, &record, journal)
	return calibrationMetadataSuccess(
		buildCommit,
		chatgptCalibrationStatusData(record),
	)
}

func CleanupCalibration(
	ctx context.Context,
	config CalibrationCleanupConfig,
) webagent.Result {
	if config.Store == nil || config.Journal == nil {
		return calibrationMetadataFailure(
			config.BuildCommit,
			"chatgpt_calibration_cleanup_unavailable",
			"internal",
			"ChatGPT calibration cleanup is not configured",
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
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration state is unavailable",
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
	mergeChatGPTCalibrationRecovery(
		ctx,
		&record,
		config.Journal,
	)
	if !record.TargetClosed && record.TargetID != "" {
		if config.Engine == nil {
			return calibrationCleanupFailure(
				config.BuildCommit,
				record,
				"chatgpt_calibration_target_cleanup_unavailable",
				"cleanup",
				"ChatGPT calibration exact target cleanup is not configured",
				calibrationTopAction(record.SendDispatch),
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
		cleanup, recoverErr := config.Engine.Recover(
			ctx,
			record.RunID,
		)
		if recoverErr != nil ||
			cleanup.State != browserflow.CleanupClosed ||
			!cleanup.TargetGone {
			return calibrationCleanupFailure(
				config.BuildCommit,
				record,
				"chatgpt_calibration_target_cleanup_failed",
				"cleanup",
				"ChatGPT calibration exact target cleanup could not be proved",
				calibrationTopAction(record.SendDispatch),
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
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := config.Store.Save(ctx, record); err != nil {
		return calibrationMetadataFailure(
			config.BuildCommit,
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration cleanup state could not be persisted",
			chatgptCalibrationStatusData(record),
		)
	}
	if record.State == "deleted" &&
		record.Postcondition == deletePostconditionProof &&
		record.TargetClosed {
		return calibrationMetadataSuccess(
			config.BuildCommit,
			chatgptCalibrationStatusData(record),
		)
	}
	if record.ConversationID == "" {
		if record.SendDispatch != browserflow.DispatchPerformed &&
			record.SendDispatch != browserflow.DispatchUnknown &&
			record.TargetClosed {
			return calibrationMetadataSuccess(
				config.BuildCommit,
				chatgptCalibrationStatusData(record),
			)
		}
		return calibrationCleanupFailure(
			config.BuildCommit,
			record,
			"chatgpt_calibration_identity_unavailable",
			"completion",
			"ChatGPT calibration has no exact acknowledged conversation identity; no delete was attempted",
			calibrationTopAction(record.SendDispatch),
			webagent.CleanupEvidence{
				State: webagent.CleanupNotRequired,
			},
		)
	}
	if record.DeleteDispatch == browserflow.DispatchPerformed ||
		record.DeleteDispatch == browserflow.DispatchUnknown {
		return calibrationCleanupFailure(
			config.BuildCommit,
			record,
			"chatgpt_calibration_delete_ambiguous",
			"completion",
			"ChatGPT calibration deletion was already attempted without a terminal postcondition; it will not be repeated",
			calibrationTopAction(record.DeleteDispatch),
			webagent.CleanupEvidence{
				State: webagent.CleanupNotRequired,
			},
		)
	}
	deleteResult := DeleteConversation(
		ctx,
		config.Delete,
		record.ConversationID,
	)
	record.DeleteDispatch = calibrationActionDispatch(
		deleteResult.Action,
	)
	switch {
	case deleteResult.OK:
		record.State = "deleted"
		record.Postcondition = deletePostconditionProof
		record.TargetClosed = true
	case deleteResult.Action != nil &&
		(deleteResult.Action.Dispatch == webagent.DispatchPerformed ||
			deleteResult.Action.Dispatch == webagent.DispatchUnknown):
		record.State = "deletion_ambiguous"
	default:
		record.State = "delete_not_performed"
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := config.Store.Save(
		context.Background(),
		record,
	); err != nil {
		return calibrationMetadataFailure(
			config.BuildCommit,
			"chatgpt_calibration_state_unavailable",
			"internal",
			"ChatGPT calibration cleanup outcome could not be persisted",
			chatgptCalibrationStatusData(record),
		)
	}
	deleteResult.Operation = webagent.OperationCalibrate
	deleteResult.Data = chatgptCalibrationStatusData(record)
	if deleteResult.OK {
		deleteResult.NextCommands = []string{}
	}
	return deleteResult
}

func mergeChatGPTCalibrationRecovery(
	ctx context.Context,
	record *CalibrationStateRecord,
	journal browserflow.Journal,
) {
	if record == nil || journal == nil {
		return
	}
	recovery, err := journal.Load(ctx, record.RunID)
	if err != nil {
		return
	}
	if record.TargetID == "" {
		record.TargetID = recovery.TargetID
	}
	if record.ConversationID == "" &&
		conversationIDPattern.MatchString(recovery.ConversationID) {
		record.ConversationID = recovery.ConversationID
	}
	record.TargetClosed =
		recovery.Phase == browserflow.PhaseClosed &&
			recovery.Cleanup == browserflow.CleanupClosed
	for _, action := range recovery.CompletedActions {
		switch action.Name {
		case "send":
			record.SendDispatch = action.Dispatch
		case "delete":
			record.DeleteDispatch = action.Dispatch
		}
	}
	switch recovery.ActionName {
	case "send":
		if recovery.Dispatch != "" {
			record.SendDispatch = recovery.Dispatch
		}
	case "delete":
		if recovery.Dispatch != "" {
			record.DeleteDispatch = recovery.Dispatch
		}
	}
	if recovery.Postcondition == deletePostconditionProof {
		record.Postcondition = recovery.Postcondition
		record.State = "deleted"
	}
}

func chatgptCalibrationStatusData(
	record CalibrationStateRecord,
) CalibrationStatusData {
	conversationPresent := record.ConversationID != "" &&
		record.Postcondition != deletePostconditionProof &&
		record.State != "deleted"
	targetClosed := record.TargetClosed || record.TargetID == ""
	recoveryRequired := !targetClosed || conversationPresent
	if record.ConversationID == "" &&
		(record.SendDispatch == browserflow.DispatchPerformed ||
			record.SendDispatch == browserflow.DispatchUnknown) {
		recoveryRequired = true
	}
	return CalibrationStatusData{
		SchemaVersion:       CalibrationStateSchemaVersion,
		State:               record.State,
		RunID:               record.RunID,
		ConversationPresent: conversationPresent,
		SendDispatch:        record.SendDispatch,
		DeleteDispatch:      record.DeleteDispatch,
		Postcondition:       record.Postcondition,
		TargetClosed:        targetClosed,
		RecoveryRequired:    recoveryRequired,
	}
}

func calibrationMetadataSuccess(
	buildCommit string,
	data CalibrationStatusData,
) webagent.Result {
	result := webagent.NewMetadataResult(
		webagent.ProviderChatGPT,
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
	result := operationFailure(
		webagent.NewRunID(),
		buildCommit,
		webagent.OperationCalibrate,
		webagent.StageMetadata,
		"owner_only_local_state",
		nil,
		webagent.CleanupEvidence{
			State: webagent.CleanupNotRequired,
		},
		code,
		errClass,
		message,
		data,
		[]string{
			"cdp workflow agent chatgpt calibration status --json",
		},
	)
	result.Evidence.BrowserMode = "none"
	return result
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
	result := operationFailure(
		record.RunID,
		buildCommit,
		webagent.OperationCalibrate,
		webagent.StageCleanupPending,
		"owner_only_local_state",
		nil,
		cleanup,
		code,
		errClass,
		message,
		chatgptCalibrationStatusData(record),
		[]string{
			"cdp workflow agent chatgpt calibration status --json",
		},
	)
	result.Action = action
	if result.Error != nil && action != nil {
		result.Error.RetrySafe = action.RetrySafe
	}
	if cleanup.State == webagent.CleanupNotRequired {
		result.Stage = webagent.StageObserveTerminal
	}
	return result
}

func calibrationTopAction(
	dispatch browserflow.Dispatch,
) *webagent.ActionEvidence {
	switch dispatch {
	case browserflow.DispatchPerformed,
		browserflow.DispatchUnknown:
		return &webagent.ActionEvidence{
			Dispatch:         webagent.Dispatch(dispatch),
			AttemptCount:     1,
			RawInputCount:    1,
			RetrySafe:        false,
			PendingPersisted: true,
		}
	case browserflow.DispatchNotPerformed:
		return notPerformedAction()
	default:
		return nil
	}
}

func calibrationActionDispatch(
	action *webagent.ActionEvidence,
) browserflow.Dispatch {
	if action == nil {
		return ""
	}
	return browserflow.Dispatch(action.Dispatch)
}
