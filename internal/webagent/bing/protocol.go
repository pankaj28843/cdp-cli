// Package bing contains the narrow public Speech SDK WebSocket adapter used
// by the transcription service. It deliberately does not automate Bing
// search or submit a query; the socket is the provider boundary.
package bing

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
)

const (
	SpeechEndpoint          = "wss://sr.bing.com/opaluqu/speech/recognition/interactive/cognitiveservices/v1"
	defaultLanguage         = "en-US"
	defaultSpeechSDKVersion = "1.15.0-alpha.0.1"
)

type Socket = augloop.Socket
type SocketDialer = func(context.Context, string, string) (Socket, error)

type speechMessage struct {
	Path              string
	RecognitionStatus string
	DisplayText       string
	Message           string
}

func speechURL(language, requestID, connectionID string) (string, error) {
	language = strings.TrimSpace(language)
	if language == "" {
		language = defaultLanguage
	}
	requestID = strings.TrimSpace(requestID)
	connectionID = strings.TrimSpace(connectionID)
	if requestID == "" || connectionID == "" {
		return "", fmt.Errorf("Bing speech request and connection IDs are required")
	}
	parsed, err := url.Parse(SpeechEndpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("clientbuild", "bingDesktop")
	query.Set("referer", "https://www.bing.com/")
	query.Set("form", "QBLH")
	query.Set("uqurequestid", requestID)
	query.Set("language", language)
	query.Set("format", "simple")
	// Bing's public voice-search front end uses this fixed public routing key;
	// it is not an account credential and no Cookie/Authorization header is
	// sent by the adapter.
	query.Set("Ocp-Apim-Subscription-Key", "key")
	query.Set("X-ConnectionId", connectionID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return strings.ToUpper(hex.EncodeToString(raw[:]))
}

func speechConfigMessage(requestID string, at time.Time, userAgent string) ([]byte, error) {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		userAgent = "Mozilla/5.0"
	}
	body, err := json.Marshal(map[string]any{
		"context": map[string]any{
			"system": map[string]string{
				"name":    "SpeechSDK",
				"version": defaultSpeechSDKVersion,
				"build":   "JavaScript",
				"lang":    "JavaScript",
			},
			"os": map[string]string{
				"platform": "VoxInput",
				"name":     userAgent,
				"version":  "1",
			},
			"audio": map[string]any{
				"source": map[string]any{
					"bitspersample": 16,
					"channelcount":  1,
					"connectivity":  "Unknown",
					"manufacturer":  "VoxInput",
					"model":         "VoxInput PCM capture",
					"samplerate":    16_000,
					"type":          "Microphones",
				},
			},
		},
		"recognition": "interactive",
	})
	if err != nil {
		return nil, err
	}
	return speechControlFrame("speech.config", requestID, at, "application/json", body), nil
}

func speechContextMessage(requestID string, at time.Time) []byte {
	return speechControlFrame("speech.context", requestID, at, "application/json", []byte("{}"))
}

func speechControlFrame(path, requestID string, at time.Time, contentType string, body []byte) []byte {
	header := fmt.Sprintf(
		"Path: %s\r\nX-RequestId: %s\r\nX-Timestamp: %s\r\nContent-Type: %s\r\n\r\n",
		strings.TrimSpace(path),
		strings.TrimSpace(requestID),
		at.UTC().Format("2006-01-02T15:04:05.000Z"),
		strings.TrimSpace(contentType),
	)
	frame := make([]byte, 0, len(header)+len(body))
	frame = append(frame, header...)
	return append(frame, body...)
}

func audioFrame(requestID string, at time.Time, pcm []byte, first bool) []byte {
	marker := byte('c')
	header := fmt.Sprintf(
		"Path: audio\r\nX-RequestId: %s\r\nX-Timestamp: %s\r\n",
		strings.TrimSpace(requestID),
		at.UTC().Format("2006-01-02T15:04:05.000Z"),
	)
	if first {
		marker = '~'
		header += "Content-Type: audio/x-wav\r\n"
		wav := wavHeader(len(pcm))
		frame := make([]byte, 0, 2+len(header)+len(wav)+len(pcm))
		frame = append(frame, 0, marker)
		frame = append(frame, header...)
		frame = append(frame, wav...)
		return append(frame, pcm...)
	}
	frame := make([]byte, 0, 2+len(header)+len(pcm))
	frame = append(frame, 0, marker)
	frame = append(frame, header...)
	return append(frame, pcm...)
}

func wavHeader(dataBytes int) []byte {
	if dataBytes < 0 {
		dataBytes = 0
	}
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataBytes))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], 16_000)
	binary.LittleEndian.PutUint32(header[28:32], 32_000)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataBytes))
	return header
}

func parseSpeechMessage(payload []byte) (speechMessage, error) {
	text := string(payload)
	separator := strings.Index(text, "\r\n\r\n")
	if separator < 0 {
		return speechMessage{}, fmt.Errorf("Bing speech message has no header separator")
	}
	message := speechMessage{}
	for _, line := range strings.Split(text[:separator], "\r\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Path":
			message.Path = strings.TrimSpace(value)
		}
	}
	body := strings.TrimSpace(text[separator+4:])
	if body == "" {
		return message, nil
	}
	var fields struct {
		RecognitionStatus string `json:"RecognitionStatus"`
		DisplayText       string `json:"DisplayText"`
		Message           string `json:"Message"`
	}
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		return speechMessage{}, fmt.Errorf("parse Bing speech message body: %w", err)
	}
	message.RecognitionStatus = fields.RecognitionStatus
	message.DisplayText = fields.DisplayText
	message.Message = fields.Message
	return message, nil
}
