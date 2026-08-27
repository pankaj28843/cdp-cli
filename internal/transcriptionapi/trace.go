package transcriptionapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxTraceFileBytes int64 = 8 << 20

// TraceEvent is deliberately metadata-only. It is safe to keep beside the
// durable request records because it never contains audio, transcript text,
// cookies, bearer tokens, or provider session payloads.
type TraceEvent struct {
	Timestamp    time.Time   `json:"timestamp"`
	Event        string      `json:"event"`
	Transport    string      `json:"transport,omitempty"`
	RequestID    string      `json:"request_id,omitempty"`
	Provider     ProviderID  `json:"provider,omitempty"`
	Model        string      `json:"model,omitempty"`
	Phase        RecordPhase `json:"phase,omitempty"`
	Attempts     int         `json:"attempts,omitempty"`
	AudioBytes   int64       `json:"audio_bytes,omitempty"`
	AudioChunks  int64       `json:"audio_chunks,omitempty"`
	DurationMS   int64       `json:"duration_ms,omitempty"`
	ElapsedMS    int64       `json:"elapsed_ms,omitempty"`
	ErrorType    string      `json:"error_type,omitempty"`
	ErrorCode    string      `json:"error_code,omitempty"`
	ErrorMessage string      `json:"error_message,omitempty"`
}

// TracePath returns the owner-only JSONL trace file used by the API service.
func (s *Store) TracePath() string {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return ""
	}
	return filepath.Join(s.root, "trace.jsonl")
}

// AppendTrace appends one bounded, metadata-only diagnostic event. Trace
// writes are best-effort at call sites so an observability failure never
// changes transcription behavior.
func (s *Store) AppendTrace(ctx context.Context, event TraceEvent) error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return fmt.Errorf("transcription store is not configured")
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	event.Event = traceValue(event.Event)
	if event.Event == "" {
		return fmt.Errorf("trace event is required")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	event.Transport = traceValue(event.Transport)
	event.RequestID = traceValue(event.RequestID)
	event.Model = traceValue(event.Model)
	event.ErrorType = traceValue(event.ErrorType)
	event.ErrorCode = traceValue(event.ErrorCode)
	event.ErrorMessage = traceValue(event.ErrorMessage)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal transcription trace: %w", err)
	}
	data = append(data, '\n')

	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	path := s.TracePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create transcription trace directory: %w", err)
	}
	if info, statErr := os.Stat(path); statErr == nil && info.Size()+int64(len(data)) > maxTraceFileBytes {
		previous := path + ".previous"
		_ = os.Remove(previous)
		if err := os.Rename(path, previous); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate transcription trace: %w", err)
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("stat transcription trace: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open transcription trace: %w", err)
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write transcription trace: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close transcription trace: %w", closeErr)
	}
	return nil
}

func traceValue(value string) string {
	fields := strings.Fields(value)
	for index, field := range fields {
		lower := strings.ToLower(field)
		if lower == "bearer" && index+1 < len(fields) {
			fields[index+1] = "[redacted]"
		}
		for _, marker := range []string{"token=", "access_token=", "auth_token="} {
			if offset := strings.Index(lower, marker); offset >= 0 {
				fields[index] = field[:offset] + marker + "[redacted]"
				break
			}
		}
	}
	value = strings.Join(fields, " ")
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
