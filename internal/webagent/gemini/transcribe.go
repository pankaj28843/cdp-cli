package gemini

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const (
	TranscriptionSchemaVersion = "gemini-transcription/v1"
	speechHost                 = "speechs3proto2-pa.clients6.google.com"
	speechChannelPath          = "/s3web/prod/streaming/channel"
	speechChannelURL           = "https://" + speechHost + speechChannelPath
	geminiAudioChunkBytes      = 16 << 10
	maxTranscriptionAudioBytes = 50 << 20
	maxTranscriptionDurationMS = 10 * 60 * 1000
	maxWebChannelFrameBytes    = 2 << 20
	geminiFinalTimeout         = 30 * time.Second
)

type TranscribeConfig struct {
	Store       *Store
	BuildCommit string
	HTTPClient  *http.Client
	RefreshAuth func(context.Context, string) error
	Language    string
	Now         func() time.Time
}

type TranscriptionData struct {
	SchemaVersion string `json:"schema_version"`
	Transport     string `json:"transport"`
	EndpointPath  string `json:"endpoint_path"`
	FileName      string `json:"file_name"`
	AudioBytes    int64  `json:"audio_bytes"`
	DurationMS    int64  `json:"duration_ms"`
	Chunks        int    `json:"chunks"`
	Attempts      int    `json:"attempts"`
	Transcript    string `json:"transcript,omitempty"`
}

type transcribeFailure struct {
	code     string
	errClass string
	message  string
	auth     bool
}

func Transcribe(ctx context.Context, config TranscribeConfig, filePath string, durationMS int64) webagent.Result {
	runID := webagent.NewRunID()
	data := TranscriptionData{
		SchemaVersion: TranscriptionSchemaVersion,
		Transport:     "direct_http_webchannel",
		EndpointPath:  speechChannelPath,
		FileName:      filepath.Base(filePath),
		DurationMS:    durationMS,
		Attempts:      1,
	}
	if durationMS <= 0 || durationMS > maxTranscriptionDurationMS {
		return transcriptionFailureResult(runID, config.BuildCommit, data, transcribeFailure{
			code: "gemini_transcription_duration_invalid", errClass: "usage",
			message: "Gemini transcription duration must be between 1 ms and 10 minutes",
		})
	}
	audio, failure := readGeminiAudio(filePath)
	if failure != nil {
		return transcriptionFailureResult(runID, config.BuildCommit, data, *failure)
	}
	data.AudioBytes = int64(len(audio))

	transcript, chunks, generation, failure := transcribeAttempt(ctx, config, audio)
	if failure != nil && failure.auth && config.RefreshAuth != nil {
		if err := config.RefreshAuth(ctx, generation); err != nil {
			return transcriptionFailureResult(runID, config.BuildCommit, data, transcribeFailure{
				code: "gemini_auth_refresh_failed", errClass: "auth",
				message: "Gemini auth refresh could not complete",
			})
		}
		data.Attempts = 2
		transcript, chunks, _, failure = transcribeAttempt(ctx, config, audio)
	}
	data.Chunks = chunks
	if failure != nil {
		return transcriptionFailureResult(runID, config.BuildCommit, data, *failure)
	}
	data.Transcript = transcript
	return webagent.Result{
		OK: true, SchemaVersion: webagent.OperationSchemaVersion,
		Provider: webagent.ProviderGemini, Operation: webagent.OperationTranscribe,
		State: webagent.StateTerminal, Stage: webagent.StageObserveTerminal, Data: data,
		Evidence: webagent.Evidence{
			RunID: runID, BuildCommit: normalizedBuildCommit(config.BuildCommit),
			BrowserMode: "none", ReadMode: "direct_http_webchannel",
		},
		Cleanup:      webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{},
	}
}

func readGeminiAudio(filePath string) ([]byte, *transcribeFailure) {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &transcribeFailure{code: "gemini_audio_file_missing", errClass: "usage", message: "Gemini transcription audio must be an existing regular file"}
	}
	if info.Size() <= 0 || info.Size() > maxTranscriptionAudioBytes {
		return nil, &transcribeFailure{code: "gemini_audio_file_invalid", errClass: "usage", message: "Gemini transcription audio must be between 1 byte and 50 MiB"}
	}
	audio, err := os.ReadFile(filePath)
	if err != nil {
		return nil, &transcribeFailure{code: "gemini_audio_file_unreadable", errClass: "connection", message: "Gemini transcription audio could not be read"}
	}
	if len(audio) < 4 || audio[0] != 0x1a || audio[1] != 0x45 || audio[2] != 0xdf || audio[3] != 0xa3 {
		return nil, &transcribeFailure{code: "gemini_audio_format_unsupported", errClass: "usage", message: "Gemini dictation currently requires WebM/Opus audio"}
	}
	return audio, nil
}

func transcribeAttempt(ctx context.Context, config TranscribeConfig, audio []byte) (string, int, string, *transcribeFailure) {
	if config.Store == nil {
		return "", 0, "", internalTranscriptionFailure("Gemini owner-only dictation template is unavailable")
	}
	now := time.Now()
	if config.Now != nil {
		now = config.Now().UTC()
	}
	template, status, err := config.Store.LoadTemplateStatus(ctx, now, DefaultAuthTTL)
	if err != nil || !status.Ready {
		return "", 0, status.CapturedAt, &transcribeFailure{
			code: "gemini_auth_not_ready", errClass: "auth",
			message: "Gemini owner-only dictation template is not ready", auth: true,
		}
	}
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	transcript, chunks, failure := replayGeminiWebChannel(ctx, client, template, audio, config.Language, now)
	return transcript, chunks, template.CapturedAt, failure
}

func replayGeminiWebChannel(ctx context.Context, client *http.Client, template RequestTemplate, audio []byte, language string, now time.Time) (string, int, *transcribeFailure) {
	rid := randomRID()
	bootstrapURL, _ := url.Parse(speechChannelURL)
	bootstrapURL.RawQuery = url.Values{
		"VER": {"8"}, "RID": {strconv.FormatInt(rid, 10)}, "CVER": {"22"},
		"X-HTTP-Session-Id": {"gsessionid"}, "zx": {randomZX()}, "t": {"1"},
	}.Encode()
	bootstrap, err := http.NewRequestWithContext(ctx, http.MethodPost, bootstrapURL.String(), strings.NewReader("count=0"))
	if err != nil {
		return "", 0, internalTranscriptionFailure("Gemini WebChannel bootstrap could not be prepared")
	}
	setGeminiRequestHeaders(bootstrap, template, true, now)
	response, err := client.Do(bootstrap)
	if err != nil {
		return "", 0, connectionTranscriptionFailure("Gemini dictation WebChannel was unavailable")
	}
	body, readErr := readBoundedBody(response.Body, maxWebChannelFrameBytes)
	response.Body.Close()
	if failure := geminiHTTPFailure(response.StatusCode); failure != nil {
		return "", 0, failure
	}
	if readErr != nil {
		return "", 0, connectionTranscriptionFailure("Gemini WebChannel bootstrap response could not be read")
	}
	sid, parseErr := parseGeminiBootstrap(body)
	gsessionID := strings.TrimSpace(response.Header.Get("X-Http-Session-Id"))
	if parseErr != nil || sid == "" || gsessionID == "" {
		return "", 0, providerTranscriptionFailure("Gemini WebChannel bootstrap response shape changed")
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	var aid atomic.Int64
	received := make(chan receiveResult, 1)
	go func() {
		received <- receiveGeminiTranscript(streamCtx, client, template, gsessionID, sid, &aid)
	}()

	initial := base64.StdEncoding.EncodeToString(geminiInitialMessage(language))
	if failure := sendGeminiMessage(ctx, client, template, gsessionID, sid, rid+1, aid.Load(), 0, initial); failure != nil {
		cancelStream()
		return "", 0, failure
	}
	readyTimer := time.NewTimer(5 * time.Second)
	readyTicker := time.NewTicker(10 * time.Millisecond)
	for aid.Load() == 0 {
		select {
		case result := <-received:
			readyTicker.Stop()
			readyTimer.Stop()
			if result.failure != nil {
				return "", 0, result.failure
			}
			return "", 0, providerTranscriptionFailure("Gemini dictation completed before accepting audio")
		case <-readyTicker.C:
		case <-readyTimer.C:
			readyTicker.Stop()
			return "", 0, connectionTranscriptionFailure("Gemini dictation did not acknowledge its initial configuration")
		case <-ctx.Done():
			readyTicker.Stop()
			readyTimer.Stop()
			return "", 0, connectionTranscriptionFailure("Gemini dictation initialization was canceled")
		}
	}
	readyTicker.Stop()
	readyTimer.Stop()
	chunks := 0
	for offset := 0; offset < len(audio); offset += geminiAudioChunkBytes {
		end := offset + geminiAudioChunkBytes
		if end > len(audio) {
			end = len(audio)
		}
		audioEnvelope := appendProtoBytes(nil, 1, audio[offset:end])
		message := appendProtoBytes(nil, 293101, audioEnvelope)
		encoded := base64.StdEncoding.EncodeToString(message)
		if failure := sendGeminiMessage(ctx, client, template, gsessionID, sid, rid+2+int64(chunks), aid.Load(), chunks+1, encoded); failure != nil {
			cancelStream()
			return "", chunks, failure
		}
		chunks++
	}
	stop := base64.StdEncoding.EncodeToString(appendProtoVarint(nil, 3, 1))
	if failure := sendGeminiMessage(ctx, client, template, gsessionID, sid, rid+2+int64(chunks), aid.Load(), chunks+1, stop); failure != nil {
		cancelStream()
		return "", chunks, failure
	}

	finalCtx, cancelFinal := context.WithTimeout(ctx, geminiFinalTimeout)
	defer cancelFinal()
	select {
	case result := <-received:
		if result.failure != nil {
			return "", chunks, result.failure
		}
		return result.transcript, chunks, nil
	case <-finalCtx.Done():
		cancelStream()
		return "", chunks, &transcribeFailure{code: "gemini_transcription_timeout", errClass: "timeout", message: "Gemini dictation did not return a final transcript"}
	}
}

type receiveResult struct {
	transcript string
	failure    *transcribeFailure
}

func receiveGeminiTranscript(ctx context.Context, client *http.Client, template RequestTemplate, gsessionID, sid string, aid *atomic.Int64) receiveResult {
	receiveURL, _ := url.Parse(speechChannelURL)
	receiveURL.RawQuery = url.Values{
		"gsessionid": {gsessionID}, "VER": {"8"}, "RID": {"rpc"}, "SID": {sid},
		"AID": {"0"}, "CI": {"0"}, "TYPE": {"xmlhttp"}, "zx": {randomZX()}, "t": {"1"},
	}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, receiveURL.String(), nil)
	if err != nil {
		return receiveResult{failure: internalTranscriptionFailure("Gemini receive channel could not be prepared")}
	}
	setGeminiRequestHeaders(request, template, false, time.Time{})
	response, err := client.Do(request)
	if err != nil {
		return receiveResult{failure: connectionTranscriptionFailure("Gemini receive channel was unavailable")}
	}
	defer response.Body.Close()
	if failure := geminiHTTPFailure(response.StatusCode); failure != nil {
		return receiveResult{failure: failure}
	}
	reader := bufio.NewReader(response.Body)
	for {
		payload, err := readWebChannelFrame(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return receiveResult{failure: providerTranscriptionFailure("Gemini dictation ended without a final transcript")}
			}
			return receiveResult{failure: connectionTranscriptionFailure("Gemini receive channel could not be read")}
		}
		transcript, final, frameAID, err := parseGeminiReceivePayload(payload)
		if err != nil {
			return receiveResult{failure: providerTranscriptionFailure("Gemini dictation response shape changed")}
		}
		if frameAID >= 0 {
			aid.Store(frameAID)
		}
		if final {
			if strings.TrimSpace(transcript) == "" {
				return receiveResult{failure: providerTranscriptionFailure("Gemini dictation returned no usable transcript")}
			}
			return receiveResult{transcript: strings.TrimSpace(transcript)}
		}
	}
}

func sendGeminiMessage(ctx context.Context, client *http.Client, template RequestTemplate, gsessionID, sid string, rid, aid int64, offset int, message string) *transcribeFailure {
	sendURL, _ := url.Parse(speechChannelURL)
	sendURL.RawQuery = url.Values{
		"VER": {"8"}, "gsessionid": {gsessionID}, "SID": {sid}, "RID": {strconv.FormatInt(rid, 10)},
		"AID": {strconv.FormatInt(aid, 10)}, "zx": {randomZX()}, "t": {"1"},
	}.Encode()
	body := url.Values{"count": {"1"}, "ofs": {strconv.Itoa(offset)}, "req0___data__": {message}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL.String(), strings.NewReader(body))
	if err != nil {
		return internalTranscriptionFailure("Gemini WebChannel message could not be prepared")
	}
	setGeminiRequestHeaders(request, template, false, time.Time{})
	response, err := client.Do(request)
	if err != nil {
		return connectionTranscriptionFailure("Gemini WebChannel message could not be sent")
	}
	_, readErr := readBoundedBody(response.Body, maxWebChannelFrameBytes)
	response.Body.Close()
	if failure := geminiHTTPFailure(response.StatusCode); failure != nil {
		return failure
	}
	if readErr != nil {
		return connectionTranscriptionFailure("Gemini WebChannel acknowledgement could not be read")
	}
	return nil
}

func setGeminiRequestHeaders(request *http.Request, template RequestTemplate, bootstrap bool, now time.Time) {
	request.Header.Set("User-Agent", template.BrowserUserAgent)
	request.Header.Set("Origin", Origin)
	request.Header.Set("Referer", Origin+"/")
	for name, value := range template.Cookies {
		request.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	if request.Method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bootstrap {
		request.Header.Set("Authorization", geminiAuthorization(template.Cookies, now))
		request.Header.Set("X-Goog-Api-Key", template.APIKey)
		request.Header.Set("X-Goog-Authuser", template.AuthUser)
		request.Header.Set("X-Goog-Encode-Response-If-Executable", "base64")
		request.Header.Set("X-Webchannel-Content-Type", "application/x-protobuf")
	}
}

func geminiAuthorization(cookies map[string]string, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	seconds := now.Unix()
	specs := []struct{ scheme, cookie string }{
		{"SAPISIDHASH", "SAPISID"},
		{"SAPISID1PHASH", "__Secure-1PAPISID"},
		{"SAPISID3PHASH", "__Secure-3PAPISID"},
	}
	parts := make([]string, 0, len(specs))
	for _, spec := range specs {
		value := cookies[spec.cookie]
		digest := sha1.Sum([]byte(fmt.Sprintf("%d %s %s", seconds, value, Origin)))
		parts = append(parts, fmt.Sprintf("%s %d_%s", spec.scheme, seconds, hex.EncodeToString(digest[:])))
	}
	return strings.Join(parts, " ")
}

func parseGeminiBootstrap(body []byte) (string, error) {
	reader := bufio.NewReader(strings.NewReader(string(body)))
	payload, err := readWebChannelFrame(reader)
	if err != nil {
		return "", err
	}
	var frames []json.RawMessage
	if err := json.Unmarshal(payload, &frames); err != nil || len(frames) == 0 {
		return "", fmt.Errorf("invalid bootstrap frame")
	}
	var frame []json.RawMessage
	if err := json.Unmarshal(frames[0], &frame); err != nil || len(frame) != 2 {
		return "", fmt.Errorf("invalid bootstrap tuple")
	}
	var control []json.RawMessage
	if err := json.Unmarshal(frame[1], &control); err != nil || len(control) < 2 {
		return "", fmt.Errorf("invalid bootstrap control")
	}
	var kind, sid string
	if json.Unmarshal(control[0], &kind) != nil || json.Unmarshal(control[1], &sid) != nil || kind != "c" || strings.TrimSpace(sid) == "" {
		return "", fmt.Errorf("invalid bootstrap control")
	}
	return sid, nil
}

func parseGeminiReceivePayload(payload []byte) (string, bool, int64, error) {
	var frames []json.RawMessage
	if err := json.Unmarshal(payload, &frames); err != nil {
		return "", false, -1, err
	}
	latestAID := int64(-1)
	for _, rawFrame := range frames {
		var frame []json.RawMessage
		if err := json.Unmarshal(rawFrame, &frame); err != nil || len(frame) != 2 {
			return "", false, latestAID, fmt.Errorf("invalid receive frame")
		}
		var frameAID int64
		if err := json.Unmarshal(frame[0], &frameAID); err != nil {
			return "", false, latestAID, err
		}
		latestAID = frameAID
		var values []string
		if err := json.Unmarshal(frame[1], &values); err != nil {
			return "", false, latestAID, err
		}
		for _, value := range values {
			if value == "noop" {
				continue
			}
			message, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return "", false, latestAID, err
			}
			if transcript, final := finalGeminiTranscript(message); final {
				return transcript, true, latestAID, nil
			}
		}
	}
	return "", false, latestAID, nil
}

func finalGeminiTranscript(message []byte) (string, bool) {
	fields, ok := parseProtoFields(message)
	if !ok {
		return "", false
	}
	phase := uint64(0)
	var provider []byte
	for _, field := range fields {
		if field.number == 5 && field.wire == 0 {
			phase = field.varint
		}
		if field.number == 1253625 && field.wire == 2 {
			provider = field.bytes
		}
	}
	if phase != 2 || len(provider) == 0 {
		return "", false
	}
	providerFields, ok := parseProtoFields(provider)
	if !ok {
		return "", true
	}
	best := ""
	for _, providerField := range providerFields {
		if providerField.number != 1 || providerField.wire != 2 {
			continue
		}
		resultFields, parsed := parseProtoFields(providerField.bytes)
		if !parsed {
			continue
		}
		for _, resultField := range resultFields {
			if resultField.wire != 2 || (resultField.number != 3 && resultField.number != 5) {
				continue
			}
			if candidate, found := geminiSegmentTranscript(resultField.bytes); found {
				if len(candidate) > len(best) {
					best = candidate
				}
				continue
			}
			for _, candidate := range printableTextCandidates(resultField.bytes) {
				if len(candidate) > len(best) {
					best = candidate
				}
			}
		}
	}
	return strings.TrimSpace(best), true
}

func geminiSegmentTranscript(message []byte) (string, bool) {
	segmentFields, ok := parseProtoFields(message)
	if !ok {
		return "", false
	}
	for _, segmentField := range segmentFields {
		if segmentField.number != 3 || segmentField.wire != 2 {
			continue
		}
		textFields, parsed := parseProtoFields(segmentField.bytes)
		if !parsed {
			continue
		}
		for _, textField := range textFields {
			text := strings.TrimSpace(string(textField.bytes))
			if textField.number == 1 && textField.wire == 2 && utf8.ValidString(text) &&
				strings.IndexFunc(text, unicode.IsLetter) >= 0 {
				return text, true
			}
		}
	}
	return "", false
}

type protoField struct {
	number int
	wire   int
	varint uint64
	bytes  []byte
}

func parseProtoFields(message []byte) ([]protoField, bool) {
	fields := make([]protoField, 0, 8)
	for offset := 0; offset < len(message); {
		tag, next, ok := readProtoVarint(message, offset)
		if !ok || tag == 0 {
			return nil, false
		}
		offset = next
		field := protoField{number: int(tag >> 3), wire: int(tag & 7)}
		switch field.wire {
		case 0:
			field.varint, offset, ok = readProtoVarint(message, offset)
		case 1:
			if offset+8 <= len(message) {
				field.bytes = message[offset : offset+8]
				offset += 8
			} else {
				ok = false
			}
		case 2:
			var size uint64
			size, offset, ok = readProtoVarint(message, offset)
			if ok && size <= uint64(len(message)-offset) {
				field.bytes = message[offset : offset+int(size)]
				offset += int(size)
			} else {
				ok = false
			}
		case 5:
			if offset+4 <= len(message) {
				field.bytes = message[offset : offset+4]
				offset += 4
			} else {
				ok = false
			}
		default:
			ok = false
		}
		if !ok {
			return nil, false
		}
		fields = append(fields, field)
	}
	return fields, true
}

func geminiAudioField(message []byte) ([]byte, bool) {
	fields, ok := parseProtoFields(message)
	if !ok || len(fields) != 1 || fields[0].number != 293101 || fields[0].wire != 2 {
		return nil, false
	}
	nested, ok := parseProtoFields(fields[0].bytes)
	if !ok || len(nested) != 1 || nested[0].number != 1 || nested[0].wire != 2 {
		return nil, false
	}
	return nested[0].bytes, true
}

// geminiInitialMessage reproduces the stable, non-secret 98-byte configuration
// observed before Gemini sends any WebM/Opus audio. Only the language varies.
func geminiInitialMessage(language string) []byte {
	language = strings.TrimSpace(language)
	if language == "" {
		language = "en"
	}
	message := appendProtoBytes(nil, 1, []byte("beyond-a2a-recognizer"))
	message = appendProtoVarint(message, 2, 1)
	languageValue := appendProtoBytes(nil, 1, []byte(language))
	message = appendProtoBytes(message, 293000, appendProtoBytes(nil, 2, languageValue))
	media := appendProtoFixed32(nil, 2, []byte{0x00, 0x00, 0x7a, 0x46})
	media = appendProtoVarint(media, 3, 11)
	media = appendProtoVarint(media, 4, 1)
	message = appendProtoBytes(message, 293100, media)
	client := appendProtoBytes(nil, 2, []byte("bard-web-frontend"))
	client = appendProtoBytes(client, 8, []byte("Web"))
	message = appendProtoBytes(message, 294000, client)
	options := appendProtoBytes(nil, 1, appendProtoBytes(nil, 10, []byte(language)))
	options = appendProtoVarint(options, 5, 1)
	options = appendProtoVarint(options, 40, 1)
	options = appendProtoVarint(options, 52, 1)
	return appendProtoBytes(message, 294500, options)
}

func appendProtoVarint(target []byte, field int, value uint64) []byte {
	target = appendRawVarint(target, uint64(field<<3))
	return appendRawVarint(target, value)
}

func appendProtoBytes(target []byte, field int, value []byte) []byte {
	target = appendRawVarint(target, uint64(field<<3|2))
	target = appendRawVarint(target, uint64(len(value)))
	return append(target, value...)
}

func appendProtoFixed32(target []byte, field int, value []byte) []byte {
	target = appendRawVarint(target, uint64(field<<3|5))
	return append(target, value...)
}

func appendRawVarint(target []byte, value uint64) []byte {
	for value >= 0x80 {
		target = append(target, byte(value)|0x80)
		value >>= 7
	}
	return append(target, byte(value))
}

func readProtoVarint(message []byte, offset int) (uint64, int, bool) {
	var value uint64
	for shift := uint(0); shift < 64 && offset < len(message); shift += 7 {
		current := message[offset]
		offset++
		value |= uint64(current&0x7f) << shift
		if current&0x80 == 0 {
			return value, offset, true
		}
	}
	return 0, offset, false
}

func printableTextCandidates(value []byte) []string {
	result := make([]string, 0, 2)
	start := -1
	flush := func(end int) {
		if start < 0 || end <= start {
			start = -1
			return
		}
		candidate := strings.TrimSpace(string(value[start:end]))
		if utf8.ValidString(candidate) && len([]rune(candidate)) >= 2 && strings.IndexFunc(candidate, unicode.IsLetter) >= 0 {
			result = append(result, candidate)
		}
		start = -1
	}
	for index, current := range value {
		if current == '\t' || current == '\n' || current == '\r' || current >= 0x20 {
			if start < 0 {
				start = index
			}
			continue
		}
		flush(index)
	}
	flush(len(value))
	return result
}

func readWebChannelFrame(reader *bufio.Reader) ([]byte, error) {
	var line string
	for strings.TrimSpace(line) == "" {
		var err error
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
	}
	size, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || size <= 0 || size > maxWebChannelFrameBytes {
		return nil, fmt.Errorf("invalid WebChannel frame length")
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func readBoundedBody(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeded limit")
	}
	return data, nil
}

func geminiHTTPFailure(status int) *transcribeFailure {
	if status >= 200 && status < 300 {
		return nil
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return &transcribeFailure{code: "gemini_auth_rejected", errClass: "auth", message: "Gemini dictation requires refreshed browser auth state", auth: true}
	}
	return &transcribeFailure{code: "gemini_dictation_http_failed", errClass: "provider", message: "Gemini dictation returned an unsuccessful HTTP status"}
}

func internalTranscriptionFailure(message string) *transcribeFailure {
	return &transcribeFailure{code: "gemini_transcription_internal", errClass: "internal", message: message}
}

func connectionTranscriptionFailure(message string) *transcribeFailure {
	return &transcribeFailure{code: "gemini_dictation_unavailable", errClass: "connection", message: message}
}

func providerTranscriptionFailure(message string) *transcribeFailure {
	return &transcribeFailure{code: "gemini_dictation_response_changed", errClass: "provider", message: message}
}

func transcriptionFailureResult(runID, buildCommit string, data TranscriptionData, failure transcribeFailure) webagent.Result {
	return webagent.Result{
		OK: false, SchemaVersion: webagent.OperationSchemaVersion,
		Provider: webagent.ProviderGemini, Operation: webagent.OperationTranscribe,
		State: webagent.StateFailed, Stage: webagent.StageObserveTerminal,
		Error: &webagent.OperationError{Code: failure.code, ErrClass: failure.errClass, Message: failure.message, RetrySafe: false},
		Data:  data,
		Evidence: webagent.Evidence{
			RunID: runID, BuildCommit: normalizedBuildCommit(buildCommit),
			BrowserMode: "none", ReadMode: "direct_http_webchannel",
		},
		Cleanup:      webagent.CleanupEvidence{State: webagent.CleanupNotRequired},
		NextCommands: []string{"cdp workflow agent gemini auth refresh --json"},
	}
}

func randomRID() int64 {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 10_000 + time.Now().UnixNano()%90_000
	}
	value := uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])
	return 10_000 + int64(value%90_000)
}

func randomZX() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	value := uint64(raw[0])<<56 | uint64(raw[1])<<48 | uint64(raw[2])<<40 | uint64(raw[3])<<32 |
		uint64(raw[4])<<24 | uint64(raw[5])<<16 | uint64(raw[6])<<8 | uint64(raw[7])
	return strconv.FormatUint(value, 36)
}
