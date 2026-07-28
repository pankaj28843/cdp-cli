package browserflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
)

const RecoverySchemaVersion = "browserflow-recovery/v1"

type Phase string

const (
	PhasePlanned             Phase = "planned"
	PhaseBudgetChecked       Phase = "budget_checked"
	PhaseTargetCreatePending Phase = "target_create_pending"
	PhaseTargetOwned         Phase = "target_owned"
	PhaseAttached            Phase = "attached"
	PhasePrepared            Phase = "prepared"
	PhaseActionPending       Phase = "action_pending"
	PhaseActionPerformed     Phase = "action_performed"
	PhaseActionUnknown       Phase = "action_unknown"
	PhaseAcknowledged        Phase = "acknowledged"
	PhaseTerminal            Phase = "terminal"
	PhaseIncomplete          Phase = "incomplete"
	PhaseCleanupPending      Phase = "cleanup_pending"
	PhaseClosed              Phase = "closed"
	PhaseFailed              Phase = "failed"
)

type Dispatch string

const (
	DispatchPerformed    Dispatch = "performed"
	DispatchNotPerformed Dispatch = "not_performed"
	DispatchUnknown      Dispatch = "unknown"
)

type CleanupState string

const (
	CleanupNotRequired CleanupState = "not_required"
	CleanupPending     CleanupState = "pending"
	CleanupClosed      CleanupState = "closed"
	CleanupFailed      CleanupState = "failed"
)

type BudgetEvidence struct {
	TabCount          int  `json:"tab_count"`
	MaxTabs           int  `json:"max_tabs"`
	WindowCount       int  `json:"window_count"`
	MaxWindows        int  `json:"max_windows"`
	WindowCountKnown  bool `json:"window_count_known"`
	OverBudget        bool `json:"over_budget"`
	OverridePermitted bool `json:"override_permitted"`
}

type CompletedAction struct {
	Name               string   `json:"name"`
	Dispatch           Dispatch `json:"dispatch"`
	ActionAttemptCount int      `json:"action_attempt_count"`
	RawInputCount      int      `json:"raw_input_count"`
	PendingPersisted   bool     `json:"pending_persisted"`
	CompletionPhase    Phase    `json:"completion_phase"`
	Postcondition      string   `json:"postcondition,omitempty"`
}

type Record struct {
	SchemaVersion      string            `json:"schema_version"`
	RunID              string            `json:"run_id"`
	Provider           string            `json:"provider"`
	Operation          string            `json:"operation"`
	Phase              Phase             `json:"phase"`
	ActionName         string            `json:"action_name,omitempty"`
	Dispatch           Dispatch          `json:"dispatch,omitempty"`
	ActionAttemptCount int               `json:"action_attempt_count"`
	RawInputCount      int               `json:"raw_input_count"`
	PendingPersisted   bool              `json:"pending_persisted"`
	TargetID           string            `json:"target_id,omitempty"`
	SessionID          string            `json:"session_id,omitempty"`
	ConversationID     string            `json:"conversation_id,omitempty"`
	InputFingerprint   string            `json:"input_fingerprint,omitempty"`
	Postcondition      string            `json:"postcondition,omitempty"`
	CompletedActions   []CompletedAction `json:"completed_actions,omitempty"`
	Budget             *BudgetEvidence   `json:"budget,omitempty"`
	Cleanup            CleanupState      `json:"cleanup"`
	RetryAt            string            `json:"retry_at,omitempty"`
	LastErrorClass     string            `json:"last_error_class,omitempty"`
	CreatedAt          string            `json:"created_at"`
	UpdatedAt          string            `json:"updated_at"`
}

func (r Record) Validate() error {
	if r.SchemaVersion != RecoverySchemaVersion {
		return fmt.Errorf("schema_version must be %q", RecoverySchemaVersion)
	}
	for name, value := range map[string]string{
		"run_id":    r.RunID,
		"provider":  r.Provider,
		"operation": r.Operation,
	} {
		if err := validateIdentity(name, value, 128); err != nil {
			return err
		}
	}
	if !validPhase(r.Phase) {
		return fmt.Errorf("invalid phase %q", r.Phase)
	}
	if !validCleanup(r.Cleanup) {
		return fmt.Errorf("invalid cleanup state %q", r.Cleanup)
	}
	if r.Dispatch != "" && r.Dispatch != DispatchPerformed && r.Dispatch != DispatchNotPerformed && r.Dispatch != DispatchUnknown {
		return fmt.Errorf("invalid dispatch %q", r.Dispatch)
	}
	if r.ActionAttemptCount < 0 {
		return fmt.Errorf("action_attempt_count must not be negative")
	}
	if r.RawInputCount < 0 || r.RawInputCount > 1 {
		return fmt.Errorf("raw_input_count must be zero or one")
	}
	if r.Dispatch == DispatchNotPerformed && r.RawInputCount != 0 {
		return fmt.Errorf("not_performed dispatch must have raw_input_count=0")
	}
	if (r.Dispatch == DispatchPerformed || r.Dispatch == DispatchUnknown) && r.RawInputCount != 1 {
		return fmt.Errorf("%s dispatch must have raw_input_count=1", r.Dispatch)
	}
	if phaseAtOrAfterTarget(r.Phase) && r.TargetID == "" {
		return fmt.Errorf("phase %q requires target_id", r.Phase)
	}
	if phaseRequiresSession(r.Phase) && r.SessionID == "" {
		return fmt.Errorf("phase %q requires session_id", r.Phase)
	}
	if phaseRequiresPendingProof(r.Phase) && !r.PendingPersisted {
		return fmt.Errorf("phase %q requires pending_persisted=true", r.Phase)
	}
	if r.Phase == PhaseClosed && r.Cleanup != CleanupClosed {
		return fmt.Errorf("closed phase requires cleanup=closed")
	}
	if r.Cleanup == CleanupClosed && r.TargetID == "" {
		return fmt.Errorf("cleanup=closed requires target_id")
	}
	for name, value := range map[string]string{
		"action_name":       r.ActionName,
		"target_id":         r.TargetID,
		"session_id":        r.SessionID,
		"conversation_id":   r.ConversationID,
		"input_fingerprint": r.InputFingerprint,
		"postcondition":     r.Postcondition,
		"last_error_class":  r.LastErrorClass,
	} {
		if value == "" {
			continue
		}
		if err := validateSafeValue(name, value, 512); err != nil {
			return err
		}
	}
	if r.InputFingerprint != "" {
		decoded, err := hex.DecodeString(r.InputFingerprint)
		if err != nil || len(decoded) != sha256.Size ||
			r.InputFingerprint != strings.ToLower(r.InputFingerprint) {
			return fmt.Errorf("input_fingerprint must be a lowercase SHA-256 digest")
		}
	}
	if len(r.CompletedActions) > 16 {
		return fmt.Errorf("completed_actions exceeds 16 entries")
	}
	for index, action := range r.CompletedActions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("completed_actions[%d]: %w", index, err)
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, r.CreatedAt); err != nil {
		return fmt.Errorf("created_at must be RFC3339: %w", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, r.UpdatedAt); err != nil {
		return fmt.Errorf("updated_at must be RFC3339: %w", err)
	}
	if r.RetryAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, r.RetryAt); err != nil {
			return fmt.Errorf("retry_at must be RFC3339: %w", err)
		}
	}
	return nil
}

func validateRecordTransition(before, after Record) error {
	if err := after.Validate(); err != nil {
		return err
	}
	if before.RunID != after.RunID ||
		before.Provider != after.Provider ||
		before.Operation != after.Operation ||
		before.CreatedAt != after.CreatedAt {
		return fmt.Errorf("recovery record identity is immutable")
	}
	if before.TargetID != "" && before.TargetID != after.TargetID {
		return fmt.Errorf("target_id is immutable once recorded")
	}
	if before.SessionID != "" && before.SessionID != after.SessionID {
		return fmt.Errorf("session_id is immutable once recorded")
	}
	if before.ConversationID != "" && before.ConversationID != after.ConversationID {
		return fmt.Errorf("conversation_id is immutable once recorded")
	}
	if before.InputFingerprint != "" &&
		before.InputFingerprint != after.InputFingerprint {
		return fmt.Errorf("input_fingerprint is immutable once recorded")
	}
	if before.InputFingerprint == "" && after.InputFingerprint != "" &&
		(before.Phase != PhaseAttached || after.Phase != PhaseAttached) {
		return fmt.Errorf("input_fingerprint may be bound only while attached")
	}
	if before.Postcondition != "" && before.Postcondition != after.Postcondition {
		return fmt.Errorf("postcondition is immutable once recorded")
	}
	advanced := validActionAdvance(before, after)
	if len(after.CompletedActions) < len(before.CompletedActions) ||
		len(after.CompletedActions) > len(before.CompletedActions)+1 {
		return fmt.Errorf("completed_actions must be append-only one action at a time")
	}
	if !slices.Equal(
		before.CompletedActions,
		after.CompletedActions[:len(before.CompletedActions)],
	) {
		return fmt.Errorf("completed_actions entries are immutable")
	}
	if len(after.CompletedActions) != len(before.CompletedActions) && !advanced {
		return fmt.Errorf("completed action append does not match prior action")
	}
	if !advanced && after.ActionName != before.ActionName {
		return fmt.Errorf("action_name may change only when advancing actions")
	}
	if !advanced && after.ActionAttemptCount < before.ActionAttemptCount {
		return fmt.Errorf("action_attempt_count must not decrease")
	}
	if !advanced && after.RawInputCount < before.RawInputCount {
		return fmt.Errorf("raw_input_count must not decrease")
	}
	if !advanced && before.Dispatch == DispatchPerformed && after.Dispatch != before.Dispatch {
		return fmt.Errorf("terminal dispatch %q is immutable", before.Dispatch)
	}
	if !advanced && before.Dispatch == DispatchUnknown && after.Dispatch != before.Dispatch {
		resolvedByAcknowledgement := after.Dispatch == DispatchPerformed &&
			after.Phase == PhaseAcknowledged &&
			after.ConversationID != "" &&
			after.RawInputCount == 1 &&
			after.PendingPersisted
		resolvedByPostcondition := after.Dispatch == DispatchPerformed &&
			after.Phase == PhaseActionPerformed &&
			after.Postcondition != "" &&
			after.RawInputCount == 1 &&
			after.PendingPersisted
		if !resolvedByAcknowledgement && !resolvedByPostcondition {
			return fmt.Errorf("unknown dispatch may refine only with acknowledgement or postcondition proof")
		}
	}
	if !advanced && !allowedTransition(before.Phase, after.Phase) {
		return fmt.Errorf("invalid phase transition %q -> %q", before.Phase, after.Phase)
	}
	beforeTime, _ := time.Parse(time.RFC3339Nano, before.UpdatedAt)
	afterTime, _ := time.Parse(time.RFC3339Nano, after.UpdatedAt)
	if afterTime.Before(beforeTime) {
		return fmt.Errorf("updated_at must not move backwards")
	}
	return nil
}

func (a CompletedAction) Validate() error {
	if err := validateIdentity("name", a.Name, 128); err != nil {
		return err
	}
	if a.Dispatch != DispatchPerformed && a.Dispatch != DispatchNotPerformed &&
		a.Dispatch != DispatchUnknown {
		return fmt.Errorf("invalid dispatch %q", a.Dispatch)
	}
	if a.ActionAttemptCount < 0 {
		return fmt.Errorf("action_attempt_count must not be negative")
	}
	if a.RawInputCount < 0 || a.RawInputCount > 1 {
		return fmt.Errorf("raw_input_count must be zero or one")
	}
	if a.Dispatch == DispatchNotPerformed && a.RawInputCount != 0 {
		return fmt.Errorf("not_performed dispatch must have raw_input_count=0")
	}
	if (a.Dispatch == DispatchPerformed || a.Dispatch == DispatchUnknown) &&
		a.RawInputCount != 1 {
		return fmt.Errorf("%s dispatch must have raw_input_count=1", a.Dispatch)
	}
	if a.CompletionPhase != PhaseTerminal && a.CompletionPhase != PhaseIncomplete {
		return fmt.Errorf("completion_phase must be terminal or incomplete")
	}
	if a.Postcondition != "" {
		if err := validateSafeValue("postcondition", a.Postcondition, 512); err != nil {
			return err
		}
	}
	return nil
}

func validActionAdvance(before, after Record) bool {
	if before.ActionName == "" ||
		(before.Phase != PhaseTerminal && before.Phase != PhaseIncomplete) ||
		after.Phase != PhaseAttached ||
		after.ActionName == "" ||
		after.Dispatch != "" ||
		after.ActionAttemptCount != 0 ||
		after.RawInputCount != 0 ||
		after.PendingPersisted ||
		after.Postcondition != "" ||
		after.RetryAt != "" ||
		after.LastErrorClass != "" ||
		len(after.CompletedActions) != len(before.CompletedActions)+1 {
		return false
	}
	archived := after.CompletedActions[len(after.CompletedActions)-1]
	return archived == (CompletedAction{
		Name:               before.ActionName,
		Dispatch:           before.Dispatch,
		ActionAttemptCount: before.ActionAttemptCount,
		RawInputCount:      before.RawInputCount,
		PendingPersisted:   before.PendingPersisted,
		CompletionPhase:    before.Phase,
		Postcondition:      before.Postcondition,
	})
}

func allowedTransition(from, to Phase) bool {
	if from == to {
		return true
	}
	if to == PhaseCleanupPending && from != PhaseClosed {
		return true
	}
	switch from {
	case PhasePlanned:
		return to == PhaseBudgetChecked || to == PhaseFailed
	case PhaseBudgetChecked:
		return to == PhaseTargetCreatePending || to == PhaseFailed
	case PhaseTargetCreatePending:
		return to == PhaseTargetOwned || to == PhaseFailed
	case PhaseTargetOwned:
		return to == PhaseAttached || to == PhaseFailed
	case PhaseAttached:
		return to == PhasePrepared || to == PhaseFailed
	case PhasePrepared:
		return to == PhaseActionPending || to == PhaseTerminal || to == PhaseIncomplete || to == PhaseFailed
	case PhaseActionPending:
		return to == PhasePrepared || to == PhaseActionPerformed || to == PhaseActionUnknown
	case PhaseActionPerformed:
		return to == PhaseAcknowledged || to == PhaseTerminal || to == PhaseIncomplete || to == PhaseFailed
	case PhaseActionUnknown:
		return to == PhaseActionPerformed || to == PhaseAcknowledged ||
			to == PhaseIncomplete || to == PhaseFailed
	case PhaseAcknowledged:
		return to == PhaseTerminal || to == PhaseIncomplete || to == PhaseFailed
	case PhaseTerminal, PhaseIncomplete, PhaseFailed:
		return false
	case PhaseCleanupPending:
		return to == PhaseClosed
	case PhaseClosed:
		return false
	default:
		return false
	}
}

func validPhase(phase Phase) bool {
	switch phase {
	case PhasePlanned, PhaseBudgetChecked, PhaseTargetCreatePending,
		PhaseTargetOwned, PhaseAttached, PhasePrepared, PhaseActionPending,
		PhaseActionPerformed, PhaseActionUnknown, PhaseAcknowledged,
		PhaseTerminal, PhaseIncomplete, PhaseCleanupPending, PhaseClosed,
		PhaseFailed:
		return true
	default:
		return false
	}
}

func validCleanup(state CleanupState) bool {
	switch state {
	case CleanupNotRequired, CleanupPending, CleanupClosed, CleanupFailed:
		return true
	default:
		return false
	}
}

func phaseAtOrAfterTarget(phase Phase) bool {
	switch phase {
	case PhaseTargetOwned, PhaseAttached, PhasePrepared, PhaseActionPending,
		PhaseActionPerformed, PhaseActionUnknown, PhaseAcknowledged,
		PhaseTerminal, PhaseIncomplete, PhaseCleanupPending, PhaseClosed:
		return true
	default:
		return false
	}
}

func phaseRequiresSession(phase Phase) bool {
	switch phase {
	case PhaseAttached, PhasePrepared, PhaseActionPending, PhaseActionPerformed,
		PhaseActionUnknown, PhaseAcknowledged, PhaseTerminal, PhaseIncomplete:
		return true
	default:
		return false
	}
}

func phaseRequiresPendingProof(phase Phase) bool {
	switch phase {
	case PhaseActionPending, PhaseActionPerformed, PhaseActionUnknown,
		PhaseAcknowledged:
		return true
	default:
		return false
	}
}

func validateIdentity(name, value string, max int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > max {
		return fmt.Errorf("%s exceeds %d bytes", name, max)
	}
	for _, r := range value {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.') {
			return fmt.Errorf("%s contains unsupported character %q", name, r)
		}
	}
	return nil
}

func validateSafeValue(name, value string, max int) error {
	if len(value) > max {
		return fmt.Errorf("%s exceeds %d bytes", name, max)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}
