package webagent

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

func (r Result) Validate() error {
	if r.SchemaVersion != OperationSchemaVersion {
		return fmt.Errorf("schema_version must be %q", OperationSchemaVersion)
	}
	if !validResultProvider(r.Provider, r.Operation) {
		return fmt.Errorf("invalid provider %q for operation %q", r.Provider, r.Operation)
	}
	if !validOperation(r.Operation) {
		return fmt.Errorf("invalid operation %q", r.Operation)
	}
	if !validState(r.State) {
		return fmt.Errorf("invalid state %q", r.State)
	}
	if !validStage(r.Stage) {
		return fmt.Errorf("invalid stage %q", r.Stage)
	}
	switch r.State {
	case StateReady, StateTerminal, StateIncomplete:
		if !r.OK {
			return fmt.Errorf("state %q requires ok=true", r.State)
		}
		if r.Error != nil {
			return fmt.Errorf("successful state %q must not include error", r.State)
		}
	case StateUnsupported, StateFailed:
		if r.OK {
			return fmt.Errorf("state %q requires ok=false", r.State)
		}
		if r.Error == nil {
			return fmt.Errorf("state %q requires typed error", r.State)
		}
	}
	if r.Data == nil {
		return fmt.Errorf("data is required; use an empty object when no provider fields apply")
	}
	if r.NextCommands == nil {
		return fmt.Errorf("next_commands is required; use an empty array when no command applies")
	}
	if r.Error != nil {
		if err := r.Error.Validate(); err != nil {
			return err
		}
	}
	if r.Action != nil {
		if err := r.Action.Validate(); err != nil {
			return err
		}
		if r.Error != nil && r.Error.RetrySafe != r.Action.RetrySafe {
			return fmt.Errorf("error.retry_safe must match action.retry_safe")
		}
	}
	if r.State == StateIncomplete &&
		operationCanMutate(r.Operation) {
		if r.Action == nil {
			return fmt.Errorf(
				"incomplete mutating operation %q requires action evidence",
				r.Operation,
			)
		}
		if r.Action.Dispatch == DispatchNotPerformed {
			return fmt.Errorf(
				"incomplete mutating operation %q requires performed or unknown dispatch",
				r.Operation,
			)
		}
	}
	if r.Conversation != nil {
		if r.Conversation.ID == "" && r.Conversation.URL == "" {
			return fmt.Errorf("conversation requires id or url")
		}
		for name, value := range map[string]string{
			"conversation.id":  r.Conversation.ID,
			"conversation.url": r.Conversation.URL,
		} {
			if value != "" {
				if err := validateSafeString(name, value, 2048); err != nil {
					return err
				}
			}
		}
	}
	if err := r.Evidence.Validate(); err != nil {
		return err
	}
	if err := r.Cleanup.Validate(); err != nil {
		return err
	}
	if err := validateTargetCleanup(r.Evidence.Target, r.Cleanup); err != nil {
		return err
	}
	for index, command := range r.NextCommands {
		if err := validateSafeString(fmt.Sprintf("next_commands[%d]", index), command, 4096); err != nil {
			return err
		}
	}
	return nil
}

func validateTargetCleanup(
	target *TargetEvidence,
	cleanup CleanupEvidence,
) error {
	if target == nil || (!target.Owned && !target.Created) {
		return nil
	}
	if cleanup.TargetID != target.TargetID {
		return fmt.Errorf(
			"owned target cleanup.target_id must match evidence.target.target_id",
		)
	}
	if target.Closed {
		if cleanup.State != CleanupClosed ||
			!cleanup.Required ||
			!cleanup.TargetClosed {
			return fmt.Errorf(
				"closed owned target requires cleanup state closed with target_closed=true",
			)
		}
		return nil
	}
	if cleanup.State != CleanupPending &&
		cleanup.State != CleanupFailed {
		return fmt.Errorf(
			"unclosed owned target requires pending or failed cleanup",
		)
	}
	return nil
}

func (e OperationError) Validate() error {
	for name, value := range map[string]string{
		"error.code":      e.Code,
		"error.err_class": e.ErrClass,
		"error.message":   e.Message,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
		if err := validateSafeString(name, value, 2048); err != nil {
			return err
		}
	}
	if e.RetryAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, e.RetryAt); err != nil {
			return fmt.Errorf("error.retry_at must be RFC3339: %w", err)
		}
	}
	return nil
}

func (a ActionEvidence) Validate() error {
	if a.AttemptCount < 0 {
		return fmt.Errorf("action.attempt_count must not be negative")
	}
	if a.RawInputCount < 0 || a.RawInputCount > 1 {
		return fmt.Errorf("action.raw_input_count must be zero or one")
	}
	switch a.Dispatch {
	case DispatchNotPerformed:
		if a.RawInputCount != 0 {
			return fmt.Errorf("action not_performed requires raw_input_count=0")
		}
		if !a.RetrySafe {
			return fmt.Errorf("action not_performed requires retry_safe=true")
		}
	case DispatchPerformed, DispatchUnknown:
		if a.RawInputCount != 1 {
			return fmt.Errorf("action %s requires raw_input_count=1", a.Dispatch)
		}
		if a.RetrySafe {
			return fmt.Errorf("action %s requires retry_safe=false", a.Dispatch)
		}
		if !a.PendingPersisted {
			return fmt.Errorf("action %s requires pending_persisted=true", a.Dispatch)
		}
	default:
		return fmt.Errorf("invalid action dispatch %q", a.Dispatch)
	}
	if a.AttemptCount == 0 && (a.RawInputCount != 0 || a.PendingPersisted) {
		return fmt.Errorf("zero action attempts cannot have raw input or pending proof")
	}
	return nil
}

func (e Evidence) Validate() error {
	for name, value := range map[string]string{
		"evidence.run_id":       e.RunID,
		"evidence.build_commit": e.BuildCommit,
		"evidence.browser_mode": e.BrowserMode,
		"evidence.read_mode":    e.ReadMode,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
		if err := validateSafeString(name, value, 512); err != nil {
			return err
		}
	}
	if e.Target != nil {
		if (e.Target.Owned || e.Target.Created || e.Target.Closed) && e.Target.TargetID == "" {
			return fmt.Errorf("target lifecycle evidence requires target_id")
		}
		for name, value := range map[string]string{
			"evidence.target.target_id":  e.Target.TargetID,
			"evidence.target.session_id": e.Target.SessionID,
		} {
			if value != "" {
				if err := validateSafeString(name, value, 512); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c CleanupEvidence) Validate() error {
	if c.CloseAttemptCount < 0 || c.CloseAttemptCount > 2 {
		return fmt.Errorf("cleanup close_attempt_count must be between 0 and 2")
	}
	switch c.FailurePhase {
	case "", "deadline", "close", "poll", "close_and_poll", "unsettled":
	default:
		return fmt.Errorf("invalid cleanup failure_phase %q", c.FailurePhase)
	}
	switch c.State {
	case CleanupNotRequired:
		if c.Required || c.IdentityOmitted {
			return fmt.Errorf("cleanup not_required requires required=false")
		}
	case CleanupPending, CleanupFailed:
		if !c.Required || (c.TargetID == "" && !c.IdentityOmitted) {
			return fmt.Errorf(
				"cleanup %s requires required=true and target identity evidence",
				c.State,
			)
		}
	case CleanupClosed:
		if !c.Required ||
			(c.TargetID == "" && !c.IdentityOmitted) ||
			!c.TargetClosed {
			return fmt.Errorf(
				"cleanup closed requires required=true, target identity evidence, and target_closed=true",
			)
		}
	default:
		return fmt.Errorf("invalid cleanup state %q", c.State)
	}
	if c.IdentityOmitted && c.TargetID != "" {
		return fmt.Errorf(
			"cleanup identity_omitted cannot accompany target_id",
		)
	}
	for name, value := range map[string]string{
		"cleanup.target_id":     c.TargetID,
		"cleanup.failure_phase": c.FailurePhase,
		"cleanup.close_proof":   c.CloseProof,
	} {
		if value != "" {
			if err := validateSafeString(name, value, 4096); err != nil {
				return err
			}
		}
	}
	return nil
}

func validResultProvider(provider Provider, operation Operation) bool {
	if operation == OperationProviders {
		return provider == ProviderCatalog
	}
	if provider == ProviderCatalog {
		return operation == OperationAuthRefresh || operation == OperationCapabilities
	}
	_, ok := ParseProvider(string(provider))
	return ok
}

func validOperation(operation Operation) bool {
	switch operation {
	case OperationProviders, OperationCapabilities, OperationDoctor,
		OperationAuthRefresh, OperationTranscribe, OperationCatalogStatus,
		OperationCatalogRefresh, OperationCoursesList,
		OperationChaptersList, OperationContentFetch,
		OperationAsk, OperationConversationsList,
		OperationConversationsContinue, OperationConversationsDetail,
		OperationConversationsAwait,
		OperationConversationsDelete, OperationArtifactDownload,
		OperationAttachmentsDownload,
		OperationResearch, OperationResearchExport:
		return true
	default:
		return false
	}
}

func operationCanMutate(operation Operation) bool {
	switch operation {
	case OperationAsk, OperationConversationsContinue,
		OperationConversationsDelete, OperationResearch,
		OperationResearchExport:
		return true
	default:
		return false
	}
}

func validState(state State) bool {
	switch state {
	case StateReady, StateTerminal, StateIncomplete, StateUnsupported, StateFailed:
		return true
	default:
		return false
	}
}

func validStage(stage Stage) bool {
	switch stage {
	case StageMetadata, StagePlanned, StageTargetOwned, StageAttached, StagePrepared,
		StageActionPending, StageActionDispatched, StageAcknowledged,
		StageObserveTerminal, StageCleanupPending, StageClosed:
		return true
	default:
		return false
	}
}

func validateSafeString(name, value string, max int) error {
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
