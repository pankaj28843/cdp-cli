package m365

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync/atomic"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
)

const (
	voiceTileHeaderVersion byte = 3
	pcmSampleRate               = 16_000
	pcmBytesPerTile             = 16_000
)

var messageSequence atomic.Uint64

var dictationSettings = map[string]any{
	"dictationLanguage":  "en-US",
	"useAutoPunctuation": "Intelligent",
	"useCorrections":     "true",
	"properties": map[string]string{
		"SpeechContext-PhraseOutput.interimResults.resultType": "Hypothesis",
		"setFeature": "emailplm,offtrt,copilot",
		"Profanity":  "masked",
		"SpeechConfig-Context.DataCollection.Mode": "0",
	},
}

var activatedAnnotationTypes = []string{
	"AugLoop_Voice_SpeechErrorEvent",
	"AugLoop_Voice_SpeechSessionEvent",
	"AugLoop_Voice_SpeechToTextPartialResult",
	"AugLoop_Voice_SpeechToTextFinalResult",
	"AugLoop_Voice_SpeechQualityEvent",
}

type sessionInitResponse struct {
	SessionURLBase         string     `json:"sessionUrlBase"`
	SliceURL               string     `json:"sliceUrl"`
	SessionKey             string     `json:"sessionKey"`
	Origin                 string     `json:"origin"`
	AnonymousToken         string     `json:"anonymousToken"`
	TokenExpirationSeconds int        `json:"tokenExpirationSeconds"`
	WorkflowInputTypes     []string   `json:"workflowInputTypes"`
	H                      typeHeader `json:"H_"`
	MessageID              string     `json:"messageId"`
}

type typeHeader struct {
	Type string `json:"T_"`
}

type annotationMessage struct {
	H              typeHeader `json:"H_"`
	MessageID      string     `json:"messageId"`
	AnnotationType string     `json:"annotationType"`
	Ops            []struct {
		Items []struct {
			Body map[string]any `json:"body"`
		} `json:"items"`
	} `json:"ops"`
}

type protocolEnvelope struct {
	H         typeHeader `json:"H_"`
	MessageID string     `json:"messageId"`
}

type Socket = augloop.Socket
type SocketDialer = func(context.Context, string, string) (Socket, error)

// Keep the short names local to this package for protocol-focused tests while
// exposing the transport shape at the CLI boundary without leaking a private
// interface into another package.
type socket = Socket
type socketDialer = SocketDialer

func newMessageID(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, messageSequence.Add(1))
}

func newCorrelationVector() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var raw [22]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "VoxInputM365Correlation"
	}
	var builder strings.Builder
	builder.Grow(len(raw))
	for _, value := range raw {
		builder.WriteByte(alphabet[int(value)%len(alphabet)])
	}
	return builder.String()
}

func newSessionID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "VoxInputM365Session"
	}
	return strings.ToUpper(hex.EncodeToString(raw[:]))
}

func sessionInitMessage(metadata ClientMetadata) ([]byte, string, error) {
	correlationVector := newCorrelationVector()
	clientMetadata := map[string]any{
		"appName":              metadata.AppName,
		"appPlatform":          metadata.AppPlatform,
		"appVersion":           metadata.AppVersion,
		"releaseAudienceGroup": metadata.ReleaseAudienceGroup,
		"releaseChannel":       metadata.ReleaseChannel,
		"releaseFork":          metadata.ReleaseFork,
		"sessionId":            newSessionID(),
		"flights":              metadata.Flights,
		"userSystemTimezone":   metadata.UserSystemTimezone,
		"runtimeVersion":       metadata.RuntimeVersion,
		"docSessionId":         newSessionID(),
	}
	payload := map[string]any{
		"protocolVersion":                   2,
		"clientMetadata":                    clientMetadata,
		"extensionConfigs":                  []any{},
		"returnWorkflowInputTypes":          true,
		"enableRemoteExecutionNotification": false,
		"createBlobStorageContainer":        false,
		"H_": map[string]any{
			"T_": "AugLoop_Session_Protocol_SessionInitMessage",
			"B_": []string{"AugLoop_Session_Protocol_Message"},
		},
		"cv":        correlationVector,
		"messageId": newMessageID("c"),
	}
	encoded, err := json.Marshal(payload)
	return encoded, correlationVector, err
}

func tokenProvisionMessage(authToken, correlationVector string) ([]byte, error) {
	payload := map[string]any{
		"authToken": authToken,
		"version":   1,
		"H_": map[string]any{
			"T_": "AugLoop_Session_Protocol_TokenProvisionMessage",
			"B_": []string{"AugLoop_Session_Protocol_Message"},
		},
		"cv":        correlationVector,
		"messageId": newMessageID("c"),
	}
	return json.Marshal(payload)
}

func annotationActivationMessage(
	annotationType, correlationVector string,
	activationNumber int,
) ([]byte, error) {
	payload := map[string]any{
		"annotationType":               annotationType,
		"token":                        fmt.Sprintf("%s-%d", annotationType, activationNumber),
		"ignoreExistingAnnotations":    false,
		"sendStateUpdates":             false,
		"returnAnnotationDoesNotExist": true,
		"sendApologies":                false,
		"H_": map[string]any{
			"T_": "AugLoop_Session_Protocol_AnnotationActivationMessage",
			"B_": []string{"AugLoop_Session_Protocol_Message"},
		},
		"cv":        correlationVector,
		"messageId": newMessageID("c"),
	}
	return json.Marshal(payload)
}

func syncMessage(correlationVector string) ([]byte, error) {
	payload := map[string]any{
		"cv":  correlationVector,
		"seq": 0,
		"ops": []any{map[string]any{
			"parentPath": nil,
			"items": []any{map[string]any{
				"id": "doc",
				"body": map[string]any{
					"isReadonly": false,
					"H_": map[string]any{
						"T_": "AugLoop_Core_Document",
						"B_": []string{"AugLoop_Core_TileGroup"},
					},
				},
			}},
			"H_": map[string]any{
				"T_": "AugLoop_Core_AddOperation",
				"B_": []string{"AugLoop_Core_OperationWithSiblingContext", "AugLoop_Core_Operation"},
			},
		}},
		"H_": map[string]any{
			"T_": "AugLoop_Session_Protocol_SyncMessage",
			"B_": []string{"AugLoop_Session_Protocol_Message"},
		},
		"messageId": newMessageID("c"),
	}
	return json.Marshal(payload)
}

func responseAck(messageID string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"H_": map[string]any{
			"T_": "AugLoop_Session_Protocol_Response",
			"B_": []string{},
		},
		"messageId": messageID,
	})
}

func voiceTileMessage(
	correlationVector string,
	seq int,
	pcm []byte,
	end bool,
	warmup bool,
) ([]byte, error) {
	body := map[string]any{
		"sampleRate":           pcmSampleRate,
		"useFrontdoorWorkflow": true,
		"seq":                  seq,
		"dictationSettings":    dictationSettings,
		"responseVersion":      "2",
		"data":                 ":b0",
		"speechToTextProfile":  "Dictation",
		"commandSet":           []string{},
		"H_": map[string]any{
			"T_": "AugLoop_Voice_VoiceTile",
			"B_": []string{"AugLoop_Core_Binary"},
		},
	}
	if warmup {
		body["commandSet"] = []string{"warm-up"}
	}
	if end {
		body["endVoiceSession"] = true
		delete(body, "data")
	}
	envelope := map[string]any{
		"item": map[string]any{
			"id":   fmt.Sprintf("%d", seq),
			"body": body,
		},
		"H_": map[string]any{
			"T_": "AugLoop_Session_Protocol_MicroSyncMessage",
			"B_": []string{"AugLoop_Session_Protocol_Message"},
		},
		"cv":        correlationVector,
		"messageId": newMessageID("c"),
	}
	header, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if len(header) > math.MaxUint32 || len(pcm) > math.MaxUint32 {
		return nil, fmt.Errorf("Microsoft 365 VoiceTile frame exceeds protocol bounds")
	}
	frame := make([]byte, 0, 1+4+len(header)+4+len(pcm))
	frame = append(frame, voiceTileHeaderVersion)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(header)))
	frame = append(frame, length[:]...)
	frame = append(frame, header...)
	binary.BigEndian.PutUint32(length[:], uint32(len(pcm)))
	frame = append(frame, length[:]...)
	frame = append(frame, pcm...)
	return frame, nil
}

func parseProtocolEnvelope(payload []byte) (protocolEnvelope, error) {
	var envelope protocolEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return protocolEnvelope{}, err
	}
	return envelope, nil
}

func parseAnnotationMessage(payload []byte) (annotationMessage, error) {
	var message annotationMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return annotationMessage{}, err
	}
	return message, nil
}

func findString(value any, keys ...string) string {
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	var visit func(any) string
	visit = func(current any) string {
		switch typed := current.(type) {
		case map[string]any:
			for _, key := range keys {
				if value, ok := typed[key]; ok && wanted[key] {
					if stringValue, ok := value.(string); ok && strings.TrimSpace(stringValue) != "" {
						return stringValue
					}
				}
			}
			for _, child := range typed {
				if found := visit(child); found != "" {
					return found
				}
			}
		case []any:
			for _, child := range typed {
				if found := visit(child); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return visit(value)
}
