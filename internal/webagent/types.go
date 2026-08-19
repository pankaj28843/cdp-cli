package webagent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

var fallbackRunID atomic.Uint64

const (
	OperationSchemaVersion  = "webagent-operation/v1"
	CapabilitySchemaVersion = "webagent-capabilities/v1"
)

type Provider string

const (
	ProviderCatalog     Provider = "catalog"
	ProviderAlex        Provider = "alex"
	ProviderChatGPT     Provider = "chatgpt"
	ProviderClaude      Provider = "claude"
	ProviderGemini      Provider = "gemini"
	ProviderGrok        Provider = "grok"
	ProviderPerplexity  Provider = "perplexity"
	ProviderTripadvisor Provider = "tripadvisor"
)

type Operation string

const (
	OperationProviders             Operation = "providers"
	OperationCapabilities          Operation = "capabilities"
	OperationDoctor                Operation = "doctor"
	OperationAuthRefresh           Operation = "auth.refresh"
	OperationTranscribe            Operation = "transcribe"
	OperationCatalogStatus         Operation = "catalog.status"
	OperationCatalogRefresh        Operation = "catalog.refresh"
	OperationCoursesList           Operation = "courses.list"
	OperationChaptersList          Operation = "chapters.list"
	OperationContentFetch          Operation = "content.fetch"
	OperationAsk                   Operation = "ask"
	OperationConversationsList     Operation = "conversations.list"
	OperationConversationsContinue Operation = "conversations.continue"
	OperationConversationsDetail   Operation = "conversations.detail"
	OperationConversationsAwait    Operation = "conversations.await"
	OperationConversationsDelete   Operation = "conversations.delete"
	OperationArtifactDownload      Operation = "conversations.download_artifact"
	OperationAttachmentsDownload   Operation = "conversations.download_attachments"
	OperationResearch              Operation = "research"
	OperationResearchExport        Operation = "conversations.export_research"
)

type State string

const (
	StateReady       State = "ready"
	StateTerminal    State = "terminal"
	StateIncomplete  State = "incomplete"
	StateUnsupported State = "unsupported"
	StateFailed      State = "failed"
)

type Stage string

const (
	StageMetadata         Stage = "metadata"
	StagePlanned          Stage = "planned"
	StageTargetOwned      Stage = "target_owned"
	StageAttached         Stage = "attached"
	StagePrepared         Stage = "prepared"
	StageActionPending    Stage = "action_pending"
	StageActionDispatched Stage = "action_dispatched"
	StageAcknowledged     Stage = "acknowledged"
	StageObserveTerminal  Stage = "observe_terminal"
	StageCleanupPending   Stage = "cleanup_pending"
	StageClosed           Stage = "closed"
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

type OperationError struct {
	Code      string `json:"code"`
	ErrClass  string `json:"err_class"`
	Message   string `json:"message"`
	RetrySafe bool   `json:"retry_safe"`
	RetryAt   string `json:"retry_at,omitempty"`
}

type ActionEvidence struct {
	Dispatch         Dispatch `json:"dispatch"`
	AttemptCount     int      `json:"attempt_count"`
	RawInputCount    int      `json:"raw_input_count"`
	RetrySafe        bool     `json:"retry_safe"`
	PendingPersisted bool     `json:"pending_persisted"`
}

type ConversationRef struct {
	ID  string `json:"id,omitempty"`
	URL string `json:"url,omitempty"`
}

type TargetEvidence struct {
	TargetID  string `json:"target_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Owned     bool   `json:"owned"`
	Created   bool   `json:"created"`
	Closed    bool   `json:"closed"`
}

type Evidence struct {
	RunID       string          `json:"run_id"`
	BuildCommit string          `json:"build_commit"`
	BrowserMode string          `json:"browser_mode"`
	ReadMode    string          `json:"read_mode"`
	Target      *TargetEvidence `json:"target,omitempty"`
}

type CleanupEvidence struct {
	Required           bool         `json:"required"`
	State              CleanupState `json:"state"`
	TargetID           string       `json:"target_id,omitempty"`
	IdentityOmitted    bool         `json:"identity_omitted,omitempty"`
	TargetClosed       bool         `json:"target_closed,omitempty"`
	CloseAttemptCount  int          `json:"close_attempt_count,omitempty"`
	CloseSent          bool         `json:"close_sent,omitempty"`
	TargetPollObserved bool         `json:"target_poll_observed,omitempty"`
	FailurePhase       string       `json:"failure_phase,omitempty"`
	CloseProof         string       `json:"close_proof,omitempty"`
}

type Result struct {
	OK            bool             `json:"ok"`
	SchemaVersion string           `json:"schema_version"`
	Provider      Provider         `json:"provider"`
	Operation     Operation        `json:"operation"`
	State         State            `json:"state"`
	Stage         Stage            `json:"stage"`
	Error         *OperationError  `json:"error,omitempty"`
	Action        *ActionEvidence  `json:"action,omitempty"`
	Conversation  *ConversationRef `json:"conversation,omitempty"`
	Data          any              `json:"data"`
	Evidence      Evidence         `json:"evidence"`
	Cleanup       CleanupEvidence  `json:"cleanup"`
	NextCommands  []string         `json:"next_commands"`
}

func NewMetadataResult(provider Provider, operation Operation, data any, buildCommit string, nextCommands []string) Result {
	buildCommit = strings.TrimSpace(buildCommit)
	if buildCommit == "" {
		buildCommit = "unknown"
	}
	if nextCommands == nil {
		nextCommands = []string{}
	}
	commands := make([]string, len(nextCommands))
	copy(commands, nextCommands)
	return Result{
		OK:            true,
		SchemaVersion: OperationSchemaVersion,
		Provider:      provider,
		Operation:     operation,
		State:         StateReady,
		Stage:         StageMetadata,
		Data:          data,
		Evidence: Evidence{
			RunID:       NewRunID(),
			BuildCommit: buildCommit,
			BrowserMode: "none",
			ReadMode:    "local_metadata",
		},
		Cleanup: CleanupEvidence{
			Required: false,
			State:    CleanupNotRequired,
		},
		NextCommands: commands,
	}
}

func CloneCommands(commands []string) []string {
	cloned := make([]string, len(commands))
	copy(cloned, commands)
	return cloned
}

func NewRunID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "wa-" + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("wa-fallback-%x-%x", time.Now().UnixNano(), fallbackRunID.Add(1))
}
