// Package transcriptionapi contains the provider-neutral contract and state
// core for the local VoxInput transcription service. Transports and provider
// adapters depend on this package; this package never imports either one.
package transcriptionapi

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	ContractVersion             = "voxinput-transcription/v1"
	DefaultModel                = "whisper-1"
	MaxUploadBytes        int64 = 25 * 1024 * 1024
	MaxSessionDuration          = 10 * time.Minute
	MaxChunkBytes         int64 = 1 * 1024 * 1024
	MaxTranscriptChars          = 100_000
	MaxRealtimeAudioBytes int64 = 64 * 1024 * 1024
)

type ProviderID string

const (
	ProviderLocal   ProviderID = "local"
	ProviderChatGPT ProviderID = "chatgpt-web"
	ProviderM365    ProviderID = "microsoft-365-web"
)

type Task string

const (
	TaskTranscribe Task = "transcribe"
	TaskTranslate  Task = "translate"
)

type ResponseFormat string

const (
	ResponseJSON        ResponseFormat = "json"
	ResponseText        ResponseFormat = "text"
	ResponseVerboseJSON ResponseFormat = "verbose_json"
	ResponseSRT         ResponseFormat = "srt"
	ResponseVTT         ResponseFormat = "vtt"
)

type TimestampGranularity string

const (
	TimestampSegment TimestampGranularity = "segment"
	TimestampWord    TimestampGranularity = "word"
)

type AudioAsset struct {
	FileName      string `json:"file_name"`
	MIMEType      string `json:"mime_type,omitempty"`
	Bytes         int64  `json:"bytes"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	PersistedPath string `json:"persisted_path,omitempty"`
	Ephemeral     bool   `json:"ephemeral,omitempty"`
}

type FileRequest struct {
	RequestID              string                 `json:"request_id"`
	Task                   Task                   `json:"task"`
	Provider               ProviderID             `json:"provider,omitempty"`
	Model                  string                 `json:"model"`
	Audio                  AudioAsset             `json:"audio"`
	Language               string                 `json:"language,omitempty"`
	Prompt                 string                 `json:"prompt,omitempty"`
	ResponseFormat         ResponseFormat         `json:"response_format"`
	Temperature            *float64               `json:"temperature,omitempty"`
	Stream                 bool                   `json:"stream,omitempty"`
	TimestampGranularities []TimestampGranularity `json:"timestamp_granularities,omitempty"`
	Include                []string               `json:"include,omitempty"`
	// SyntheticProbe is an internal adapter signal. Health probes must not
	// trigger a headed auth repair from inside a provider's lazy retry path.
	SyntheticProbe bool `json:"-"`
}

func (r FileRequest) Normalized() FileRequest {
	if r.Task == "" {
		r.Task = TaskTranscribe
	}
	if r.ResponseFormat == "" {
		r.ResponseFormat = ResponseJSON
	}
	r.Model = strings.TrimSpace(r.Model)
	r.Language = strings.TrimSpace(r.Language)
	return r
}

func (r FileRequest) Validate() error {
	r = r.Normalized()
	if strings.TrimSpace(r.RequestID) == "" {
		return invalid("request_id", "required", "request_id is required")
	}
	if r.Task != TaskTranscribe && r.Task != TaskTranslate {
		return invalid("task", "unsupported", "task must be transcribe or translate")
	}
	if r.Model == "" {
		return invalid("model", "required", "model is required")
	}
	if r.Task == TaskTranslate && r.Model != DefaultModel {
		return invalid("model", "unsupported", "translation currently supports whisper-1 only")
	}
	if !validResponseFormat(r.ResponseFormat) {
		return invalid("response_format", "unsupported", "response_format is not supported")
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 1) {
		return invalid("temperature", "out_of_range", "temperature must be between 0 and 1")
	}
	if len(r.Prompt) > MaxTranscriptChars {
		return invalid("prompt", "too_large", "prompt exceeds the transcript character limit")
	}
	if r.Audio.Bytes <= 0 {
		return invalid("file", "empty", "audio file must not be empty")
	}
	if r.Audio.Bytes > MaxUploadBytes {
		return invalid("file", "too_large", "audio file exceeds the 25 MB compatibility limit")
	}
	if r.Audio.DurationMS < 0 || r.Audio.DurationMS > int64(MaxSessionDuration/time.Millisecond) {
		return invalid("file", "duration_out_of_range", "audio duration exceeds the ten minute limit")
	}
	if !supportedAudio(r.Audio.FileName, r.Audio.MIMEType) {
		return invalid("file", "unsupported_format", "audio format is not one of the supported Whisper inputs")
	}
	if len(r.TimestampGranularities) > 0 && r.ResponseFormat != ResponseVerboseJSON {
		return invalid("timestamp_granularities", "requires_verbose_json", "timestamps require response_format=verbose_json")
	}
	for _, granularity := range r.TimestampGranularities {
		if granularity != TimestampSegment && granularity != TimestampWord {
			return invalid("timestamp_granularities", "unsupported", "timestamp granularity must be segment or word")
		}
	}
	return nil
}

type ValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "invalid transcription request"
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func invalid(field, code, message string) error {
	return &ValidationError{Field: field, Code: code, Message: message}
}

func validResponseFormat(format ResponseFormat) bool {
	switch format {
	case ResponseJSON, ResponseText, ResponseVerboseJSON, ResponseSRT, ResponseVTT:
		return true
	default:
		return false
	}
}

func supportedAudio(fileName, mimeType string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	if ext == ".mp3" || ext == ".mp4" || ext == ".mpeg" || ext == ".mpga" ||
		ext == ".m4a" || ext == ".wav" || ext == ".webm" {
		return true
	}
	baseMIME := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	switch baseMIME {
	case "audio/mpeg", "audio/mp3", "audio/mp4", "audio/m4a", "audio/wav", "audio/x-wav", "audio/webm":
		return true
	default:
		return false
	}
}

type Word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type Segment struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type Result struct {
	Task     Task      `json:"task,omitempty"`
	Language string    `json:"language,omitempty"`
	Duration float64   `json:"duration,omitempty"`
	Text     string    `json:"text"`
	Words    []Word    `json:"words,omitempty"`
	Segments []Segment `json:"segments,omitempty"`
	rawText  string
}

type APIError struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Param   string `json:"param,omitempty"`
	EventID string `json:"event_id,omitempty"`
}

type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

type ProviderCapability struct {
	Provider       ProviderID       `json:"provider"`
	Models         []string         `json:"models"`
	File           bool             `json:"file"`
	Translation    bool             `json:"translation"`
	Streaming      bool             `json:"streaming"`
	Realtime       bool             `json:"realtime"`
	Ready          bool             `json:"ready"`
	EvidenceAgeSec int64            `json:"evidence_age_seconds,omitempty"`
	ProbeReady     bool             `json:"probe_ready"`
	ProbeAgeSec    int64            `json:"probe_age_seconds,omitempty"`
	LastProbeAt    string           `json:"last_probe_at,omitempty"`
	ProbeReason    string           `json:"probe_reason,omitempty"`
	FileProbe      *ProbePathStatus `json:"file_probe,omitempty"`
	RealtimeProbe  *ProbePathStatus `json:"realtime_probe,omitempty"`
	Reason         string           `json:"reason,omitempty"`
}

// ProbePathStatus reports the latest successful bounded probe for one
// provider operation. It is intentionally separate from the provider's static
// capability flags: a provider may support realtime while that path is
// temporarily unhealthy and the file fallback remains usable.
type ProbePathStatus struct {
	Ready         bool   `json:"ready"`
	AgeSec        int64  `json:"age_seconds,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type Model struct {
	ID       string     `json:"id"`
	Object   string     `json:"object"`
	OwnedBy  string     `json:"owned_by"`
	Provider ProviderID `json:"provider,omitempty"`
	Ready    bool       `json:"ready"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type RealtimeAudioFormat struct {
	Type string `json:"type"`
	Rate int    `json:"rate"`
}

type RealtimeSessionConfig struct {
	Type          string              `json:"type"`
	Model         string              `json:"model,omitempty"`
	Language      string              `json:"language,omitempty"`
	Prompt        string              `json:"prompt,omitempty"`
	InputFormat   RealtimeAudioFormat `json:"input_format,omitempty"`
	TurnDetection any                 `json:"turn_detection,omitempty"`
	// SyntheticProbe is an internal adapter signal. Health probes must not
	// trigger a headed auth repair from inside a provider's lazy setup path.
	SyntheticProbe bool `json:"-"`
}

func (c RealtimeSessionConfig) Validate() error {
	if c.Type != "" && c.Type != "transcription" {
		return invalid("session.type", "unsupported", "realtime sessions must use type=transcription")
	}
	if c.Model != "" && strings.TrimSpace(c.Model) == "" {
		return invalid("session.model", "invalid", "session model must not be blank")
	}
	if c.InputFormat.Type != "" && c.InputFormat.Type != "audio/pcm" {
		return invalid("session.audio.input.format.type", "unsupported", "only audio/pcm is supported")
	}
	if c.InputFormat.Rate != 0 && c.InputFormat.Rate != 24_000 {
		return invalid("session.audio.input.format.rate", "unsupported", "audio/pcm realtime input must be 24 kHz")
	}
	return nil
}

type RecordPhase string

const (
	PhaseReceived   RecordPhase = "received"
	PhasePersisted  RecordPhase = "persisted"
	PhaseDispatched RecordPhase = "dispatched"
	PhaseStreaming  RecordPhase = "streaming"
	PhaseCommitting RecordPhase = "committing"
	PhaseCompleted  RecordPhase = "completed"
	PhaseFailed     RecordPhase = "failed"
	PhaseCancelled  RecordPhase = "cancelled"
)

type RequestRecord struct {
	SchemaVersion string      `json:"schema_version"`
	RequestID     string      `json:"request_id"`
	SessionID     string      `json:"session_id,omitempty"`
	ItemID        string      `json:"item_id,omitempty"`
	Provider      ProviderID  `json:"provider,omitempty"`
	Model         string      `json:"model"`
	Task          Task        `json:"task"`
	Audio         AudioAsset  `json:"audio"`
	Phase         RecordPhase `json:"phase"`
	Text          string      `json:"text,omitempty"`
	Attempts      int         `json:"attempts"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	Error         *APIError   `json:"error,omitempty"`
}

func (r RequestRecord) Validate() error {
	if r.SchemaVersion != ContractVersion {
		return invalid("schema_version", "unsupported", "record schema version is not recognized")
	}
	if strings.TrimSpace(r.RequestID) == "" || strings.TrimSpace(r.Model) == "" {
		return invalid("record", "identity_required", "record request_id and model are required")
	}
	if r.Audio.Bytes <= 0 || strings.TrimSpace(r.Audio.PersistedPath) == "" {
		return invalid("audio", "not_durable", "record audio must be persisted before dispatch")
	}
	if r.Attempts < 0 || r.Attempts > 3 {
		return invalid("attempts", "out_of_range", "record attempts must be between zero and three")
	}
	return nil
}
