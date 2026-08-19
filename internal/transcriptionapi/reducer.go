package transcriptionapi

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrSessionMismatch  = errors.New("transcription event belongs to another session")
	ErrConflictingFinal = errors.New("transcription item received a conflicting final")
	ErrInvalidEvent     = errors.New("invalid transcription provider event")
)

type ProviderEventKind string

const (
	EventHypothesis ProviderEventKind = "hypothesis"
	EventFinal      ProviderEventKind = "final"
	EventFailure    ProviderEventKind = "failure"
)

// ProviderEvent is the only event shape that an adapter may send to the core.
// Replace=true is used for cumulative provider hypotheses (for example, M365)
// and false means Text is an incremental delta.
type ProviderEvent struct {
	SessionID string            `json:"session_id"`
	ItemID    string            `json:"item_id"`
	Sequence  int64             `json:"sequence,omitempty"`
	Kind      ProviderEventKind `json:"kind"`
	Text      string            `json:"text,omitempty"`
	Replace   bool              `json:"replace,omitempty"`
	Error     *APIError         `json:"error,omitempty"`
}

type ItemState struct {
	ID            string `json:"id"`
	Hypothesis    string `json:"hypothesis,omitempty"`
	Final         string `json:"final,omitempty"`
	LastSequence  int64  `json:"last_sequence"`
	Completed     bool   `json:"completed"`
	Failed        bool   `json:"failed"`
	FailureReason string `json:"failure_reason,omitempty"`
}

type SessionPhase string

const (
	SessionOpen       SessionPhase = "open"
	SessionCommitting SessionPhase = "committing"
	SessionCompleted  SessionPhase = "completed"
	SessionFailed     SessionPhase = "failed"
	SessionCancelled  SessionPhase = "cancelled"
)

type SessionState struct {
	ID            string                `json:"id"`
	Phase         SessionPhase          `json:"phase"`
	Items         map[string]*ItemState `json:"items"`
	LastSequence  int64                 `json:"last_sequence"`
	FailureReason string                `json:"failure_reason,omitempty"`
}

func NewSessionState(id string) (*SessionState, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: session id is required", ErrInvalidEvent)
	}
	return &SessionState{ID: id, Phase: SessionOpen, Items: make(map[string]*ItemState)}, nil
}

// Apply is idempotent for duplicate events and rejects a conflicting final.
// Stale events are ignored, so a provider reconnect cannot move a completed
// item backwards into a partial state.
func (s *SessionState) Apply(event ProviderEvent) (bool, error) {
	if s == nil || strings.TrimSpace(event.SessionID) == "" || event.SessionID != s.ID {
		return false, ErrSessionMismatch
	}
	if strings.TrimSpace(event.ItemID) == "" {
		return false, fmt.Errorf("%w: item id is required", ErrInvalidEvent)
	}
	if event.Kind != EventHypothesis && event.Kind != EventFinal && event.Kind != EventFailure {
		return false, fmt.Errorf("%w: unknown event kind %q", ErrInvalidEvent, event.Kind)
	}
	if event.Kind == EventFailure && event.Error == nil {
		return false, fmt.Errorf("%w: failure event needs an error", ErrInvalidEvent)
	}
	if event.Kind != EventFailure && len(event.Text) > MaxTranscriptChars {
		return false, fmt.Errorf("%w: transcript text exceeds limit", ErrInvalidEvent)
	}

	item := s.Items[event.ItemID]
	if item == nil {
		item = &ItemState{ID: event.ItemID}
		s.Items[event.ItemID] = item
	}
	if item.Completed {
		if event.Kind == EventFinal {
			if item.Final == event.Text {
				return false, nil
			}
			return false, ErrConflictingFinal
		}
		return false, nil
	}
	if item.Failed {
		return false, nil
	}
	if event.Sequence > 0 && event.Sequence <= item.LastSequence {
		return false, nil
	}

	switch event.Kind {
	case EventHypothesis:
		if event.Replace {
			item.Hypothesis = event.Text
		} else {
			item.Hypothesis += event.Text
		}
		item.LastSequence = maxSequence(item.LastSequence, event.Sequence)
		s.LastSequence = maxSequence(s.LastSequence, event.Sequence)
		if s.Phase == SessionOpen {
			s.Phase = SessionCommitting
		}
		return true, nil
	case EventFinal:
		if item.Final != "" && item.Final != event.Text {
			return false, ErrConflictingFinal
		}
		item.Final = event.Text
		item.Hypothesis = event.Text
		item.Completed = true
		item.LastSequence = maxSequence(item.LastSequence, event.Sequence)
		s.LastSequence = maxSequence(s.LastSequence, event.Sequence)
		s.Phase = SessionCompleted
		return true, nil
	case EventFailure:
		item.Failed = true
		item.FailureReason = event.Error.Message
		item.LastSequence = maxSequence(item.LastSequence, event.Sequence)
		s.LastSequence = maxSequence(s.LastSequence, event.Sequence)
		s.FailureReason = event.Error.Message
		s.Phase = SessionFailed
		return true, nil
	default:
		return false, ErrInvalidEvent
	}
}

func (s *SessionState) Text(itemID string) string {
	if s == nil {
		return ""
	}
	item := s.Items[itemID]
	if item == nil {
		return ""
	}
	if item.Completed {
		return item.Final
	}
	return item.Hypothesis
}

func (s *SessionState) AllCompleted() bool {
	if s == nil || len(s.Items) == 0 {
		return false
	}
	for _, item := range s.Items {
		if !item.Completed && !item.Failed {
			return false
		}
	}
	return true
}

func maxSequence(current, candidate int64) int64 {
	if candidate > current {
		return candidate
	}
	return current
}
