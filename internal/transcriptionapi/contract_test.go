package transcriptionapi

import (
	"errors"
	"testing"
)

func TestFileRequestNormalizesWhisperDefaults(t *testing.T) {
	request := FileRequest{
		RequestID: "req-1",
		Model:     DefaultModel,
		Audio: AudioAsset{
			FileName: "speech.webm",
			MIMEType: "audio/webm;codecs=opus",
			Bytes:    128,
		},
	}

	normalized := request.Normalized()
	if normalized.Task != TaskTranscribe {
		t.Fatalf("task = %q, want %q", normalized.Task, TaskTranscribe)
	}
	if normalized.ResponseFormat != ResponseJSON {
		t.Fatalf("response format = %q, want %q", normalized.ResponseFormat, ResponseJSON)
	}
	if err := normalized.Validate(); err != nil {
		t.Fatalf("normalized request should validate: %v", err)
	}
}

func TestFileRequestRejectsInvalidCompatibilityCombinations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*FileRequest)
		field  string
	}{
		{
			name: "translation model",
			mutate: func(request *FileRequest) {
				request.Task = TaskTranslate
				request.Model = "gpt-4o-transcribe"
			},
			field: "model",
		},
		{
			name: "unsupported format",
			mutate: func(request *FileRequest) {
				request.Audio.FileName = "speech.flac"
				request.Audio.MIMEType = "audio/flac"
			},
			field: "file",
		},
		{
			name: "timestamps without verbose json",
			mutate: func(request *FileRequest) {
				request.TimestampGranularities = []TimestampGranularity{TimestampWord}
			},
			field: "timestamp_granularities",
		},
		{
			name: "oversized upload",
			mutate: func(request *FileRequest) {
				request.Audio.Bytes = MaxUploadBytes + 1
			},
			field: "file",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := FileRequest{
				RequestID: "req-1",
				Model:     DefaultModel,
				Audio: AudioAsset{
					FileName: "speech.mp3",
					MIMEType: "audio/mpeg",
					Bytes:    128,
				},
			}
			testCase.mutate(&request)
			var validationError *ValidationError
			if err := request.Validate(); !errors.As(err, &validationError) {
				t.Fatalf("error = %v, want ValidationError", err)
			} else if validationError.Field != testCase.field {
				t.Fatalf("field = %q, want %q", validationError.Field, testCase.field)
			}
		})
	}
}

func TestRealtimeSessionConfigRequiresPCM24kWhenSpecified(t *testing.T) {
	valid := RealtimeSessionConfig{
		Type: "transcription",
		InputFormat: RealtimeAudioFormat{
			Type: "audio/pcm",
			Rate: 24_000,
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid realtime config should validate: %v", err)
	}

	invalid := valid
	invalid.InputFormat.Rate = 16_000
	var validationError *ValidationError
	if err := invalid.Validate(); !errors.As(err, &validationError) {
		t.Fatalf("error = %v, want ValidationError", err)
	} else if validationError.Field != "session.audio.input.format.rate" {
		t.Fatalf("field = %q, want rate field", validationError.Field)
	}
}

func TestRequestRecordRequiresDurableAudio(t *testing.T) {
	record := RequestRecord{
		SchemaVersion: ContractVersion,
		RequestID:     "req-1",
		Model:         DefaultModel,
		Audio: AudioAsset{
			FileName: "speech.webm",
			Bytes:    128,
		},
	}
	var validationError *ValidationError
	if err := record.Validate(); !errors.As(err, &validationError) {
		t.Fatalf("error = %v, want ValidationError", err)
	} else if validationError.Code != "not_durable" {
		t.Fatalf("code = %q, want not_durable", validationError.Code)
	}
}
