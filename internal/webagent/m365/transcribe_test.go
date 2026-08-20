package m365

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/augloop"
)

var require testAssertions

func TestRunSessionSendsObservedVoiceTileProtocolAndReturnsFinal(t *testing.T) {
	fake := newFakeSocket(
		textFrame(sessionInitResponsePayload()),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechSessionEvent",
			map[string]any{"eventId": "SpeechRecognitionStarted"},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechToTextPartialResult",
			map[string]any{"text": "hello from"},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechToTextFinalResult",
			map[string]any{"text": "Hello from Microsoft 365."},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechSessionEvent",
			map[string]any{"eventId": "SpeechRecognitionStopped"},
		)),
	)
	pcm := bytes.Repeat([]byte{0x01, 0x02}, pcmBytesPerTile)
	transcript, tiles, failure := runSession(
		context.Background(),
		testAuthTemplate(t),
		pcm,
		TranscribeConfig{Dial: func(context.Context, string, string) (socket, error) {
			return fake, nil
		}},
	)

	require.Nil(t, failure)
	require.Equal(t, "Hello from Microsoft 365.", transcript)
	require.Equal(t, 2, tiles)
	writes := fake.writesSnapshot()
	require.GreaterOrEqual(t, len(writes), 11)
	require.Equal(t, "~", string(writes[0].payload))
	require.Equal(t, augloop.MessageText, writes[1].messageType)
	require.Contains(t, string(writes[1].payload), "SessionInitMessage")

	var binaryFrames [][]byte
	for _, write := range writes {
		if write.messageType == augloop.MessageBinary {
			binaryFrames = append(binaryFrames, write.payload)
		}
	}
	require.Len(t, binaryFrames, 4)
	require.Equal(t, 0, voiceTileSequence(t, binaryFrames[0]))
	require.Equal(t, 0, voiceTilePCMSize(t, binaryFrames[0]))
	require.Equal(t, pcmBytesPerTile, voiceTilePCMSize(t, binaryFrames[1]))
	require.Equal(t, pcmBytesPerTile, voiceTilePCMSize(t, binaryFrames[2]))
	require.Equal(t, 0, voiceTilePCMSize(t, binaryFrames[3]))
	require.True(t, voiceTileIsEnd(t, binaryFrames[3]))
}

func TestStitchTranscriptSegmentsPreservesAllFinalSegments(t *testing.T) {
	got := stitchTranscriptSegments([]string{
		"So even though I switched to Microsoft 365 as the online transcription provider.",
		"It is like streaming.",
		"So it is ASR audio streaming response.",
	})

	require.Equal(t,
		"So even though I switched to Microsoft 365 as the online transcription provider. It is like streaming. So it is ASR audio streaming response.",
		got,
	)
}

func TestStitchTranscriptSegmentsCollapsesCumulativeAndBoundaryOverlap(t *testing.T) {
	got := stitchTranscriptSegments([]string{
		"hello world",
		"hello world again",
		"again and welcome",
	})

	require.Equal(t, "hello world again and welcome", got)
}

func TestRunSessionReturnsAllFinalSegmentsAfterSpeechStops(t *testing.T) {
	fake := newFakeSocket(
		textFrame(sessionInitResponsePayload()),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechSessionEvent",
			map[string]any{"eventId": "SpeechRecognitionStarted"},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechToTextFinalResult",
			map[string]any{"text": "first sentence."},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechToTextFinalResult",
			map[string]any{"text": "second sentence."},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechSessionEvent",
			map[string]any{"eventId": "SpeechRecognitionStopped"},
		)),
	)
	transcript, _, failure := runSession(
		context.Background(),
		testAuthTemplate(t),
		bytes.Repeat([]byte{0x01, 0x02}, 2_000),
		TranscribeConfig{Dial: func(context.Context, string, string) (socket, error) {
			return fake, nil
		}},
	)

	require.Nil(t, failure)
	require.Equal(t, "first sentence. second sentence.", transcript)
}

func TestRunSessionReturnsWhenFinalArrivesWithoutSpeechStopped(t *testing.T) {
	fake := newFakeSocket(
		textFrame(sessionInitResponsePayload()),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechSessionEvent",
			map[string]any{"eventId": "SpeechRecognitionStarted"},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechToTextFinalResult",
			map[string]any{"text": "Final before stopped."},
		)),
	)
	started := time.Now()
	transcript, _, failure := runSession(
		context.Background(),
		testAuthTemplate(t),
		bytes.Repeat([]byte{0x01, 0x02}, 2_000),
		TranscribeConfig{Dial: func(context.Context, string, string) (socket, error) {
			return fake, nil
		}},
	)

	require.Nil(t, failure)
	require.Equal(t, "Final before stopped.", transcript)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("final result took %s; it should not wait for the settle timeout", elapsed)
	}
}

func TestStreamTranscribeEmitsReadyPartialAndFinalEvents(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	require.NoError(t, err)
	require.NoError(t, store.SaveTemplate(context.Background(), testAuthTemplate(t)))
	fake := newFakeSocket(
		textFrame(sessionInitResponsePayload()),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechSessionEvent",
			map[string]any{"eventId": "SpeechRecognitionStarted"},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechToTextPartialResult",
			map[string]any{"text": "partial words"},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechToTextFinalResult",
			map[string]any{"text": "Final words."},
		)),
		textFrame(annotationResultsPayload(
			"AugLoop_Voice_SpeechSessionEvent",
			map[string]any{"eventId": "SpeechRecognitionStopped"},
		)),
	)
	input := strings.NewReader(
		`{"type":"start","duration_ms":1000,"sample_rate":16000}` + "\n" +
			`{"type":"audio","sample_rate":16000,"pcm_base64":"` + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x00, 0x01}, 3200)) + `"}` + "\n" +
			`{"type":"end"}` + "\n",
	)
	var output bytes.Buffer
	err = StreamTranscribe(context.Background(), TranscribeConfig{
		Store: store,
		Dial: func(context.Context, string, string) (socket, error) {
			return fake, nil
		},
	}, input, &output)
	require.NoError(t, err)

	var events []StreamEvent
	scanner := bufio.NewScanner(&output)
	for scanner.Scan() {
		var event StreamEvent
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	require.NotEmpty(t, events)
	require.Equal(t, "ready", events[0].Type)
	require.Contains(t, []string{"partial", "final"}, events[len(events)-1].Type)
	require.Equal(t, "final", events[len(events)-1].Type)
	require.Equal(t, "Final words.", events[len(events)-1].Text)
	if !events[len(events)-1].Terminal {
		t.Fatal("the post-end final must be marked terminal")
	}
}

func testAuthTemplate(t *testing.T) AuthTemplate {
	t.Helper()
	return AuthTemplate{
		SchemaVersion: AuthTemplateSchemaVersion,
		AuthToken:     "owner-only-test-token",
		ClientMetadata: ClientMetadata{
			AppName:              "BizChat",
			AppPlatform:          "Web",
			AppVersion:           "Client",
			ReleaseAudienceGroup: "Production",
			Flights:              "_acceptsClaimsChallengeMessages",
			RuntimeVersion:       "2.37.2567",
		},
		BrowserUserAgent: "Mozilla/5.0 test",
		CapturedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Source:           "headed-cdp-observed-token-provision",
	}
}

type fakeFrame struct {
	messageType augloop.MessageType
	payload     []byte
}

type fakeWrite struct {
	messageType augloop.MessageType
	payload     []byte
}

type fakeSocket struct {
	frames chan fakeFrame
	mu     sync.Mutex
	writes []fakeWrite
	closed bool
}

func newFakeSocket(frames ...fakeFrame) *fakeSocket {
	channel := make(chan fakeFrame, len(frames))
	for _, frame := range frames {
		channel <- frame
	}
	return &fakeSocket{frames: channel}
}

func (s *fakeSocket) Write(_ context.Context, messageType augloop.MessageType, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, fakeWrite{
		messageType: messageType,
		payload:     append([]byte{}, payload...),
	})
	return nil
}

func (s *fakeSocket) Read(ctx context.Context) (augloop.MessageType, []byte, error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case frame := <-s.frames:
		return frame.messageType, frame.payload, nil
	}
}

func (s *fakeSocket) Close(augloop.StatusCode, string) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *fakeSocket) writesSnapshot() []fakeWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fakeWrite{}, s.writes...)
}

func textFrame(payload []byte) fakeFrame {
	return fakeFrame{messageType: augloop.MessageText, payload: payload}
}

func sessionInitResponsePayload() []byte {
	return mustJSON(map[string]any{
		"sessionKey":     "session-key",
		"origin":         Origin,
		"anonymousToken": "anonymous-token",
		"H_": map[string]any{
			"T_": "AugLoop_Session_Protocol_SessionInitResponse",
		},
	})
}

func annotationResultsPayload(annotationType string, body map[string]any) []byte {
	return mustJSON(map[string]any{
		"messageId":      "result-1",
		"annotationType": annotationType,
		"ops": []any{map[string]any{
			"items": []any{map[string]any{"body": body}},
		}},
		"H_": map[string]any{
			"T_": "AugLoop_Session_Protocol_AnnotationResultsMessage",
		},
	})
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func voiceTileSequence(t *testing.T, frame []byte) int {
	t.Helper()
	header, _ := decodeVoiceTile(t, frame)
	return int(header["item"].(map[string]any)["body"].(map[string]any)["seq"].(float64))
}

func voiceTilePCMSize(t *testing.T, frame []byte) int {
	t.Helper()
	_, body := decodeVoiceTile(t, frame)
	return len(body)
}

func voiceTileIsEnd(t *testing.T, frame []byte) bool {
	t.Helper()
	header, _ := decodeVoiceTile(t, frame)
	body := header["item"].(map[string]any)["body"].(map[string]any)
	value, _ := body["endVoiceSession"].(bool)
	return value
}

func decodeVoiceTile(t *testing.T, frame []byte) (map[string]any, []byte) {
	t.Helper()
	require.GreaterOrEqual(t, len(frame), 9)
	headerLength := int(binary.BigEndian.Uint32(frame[1:5]))
	require.Less(t, 5+headerLength+4, len(frame)+1)
	var header map[string]any
	require.NoError(t, json.Unmarshal(frame[5:5+headerLength], &header))
	bodyOffset := 5 + headerLength
	bodyLength := int(binary.BigEndian.Uint32(frame[bodyOffset : bodyOffset+4]))
	require.Equal(t, len(frame)-bodyOffset-4, bodyLength)
	return header, frame[bodyOffset+4:]
}

type testAssertions struct{}

func (testAssertions) Nil(t *testing.T, value any) {
	t.Helper()
	if !isNil(value) {
		t.Fatalf("expected nil, got %#v", value)
	}
}

func (testAssertions) NoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func (testAssertions) Equal(t *testing.T, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}

func (testAssertions) GreaterOrEqual(t *testing.T, got, want int) {
	t.Helper()
	if got < want {
		t.Fatalf("got %d, want >= %d", got, want)
	}
}

func (testAssertions) Less(t *testing.T, got, want int) {
	t.Helper()
	if got >= want {
		t.Fatalf("got %d, want < %d", got, want)
	}
}

func (testAssertions) Contains(t *testing.T, container any, item string) {
	t.Helper()
	switch value := container.(type) {
	case string:
		if !strings.Contains(value, item) {
			t.Fatalf("%q does not contain %q", value, item)
		}
	case []string:
		for _, candidate := range value {
			if candidate == item {
				return
			}
		}
		t.Fatalf("%#v does not contain %q", value, item)
	default:
		t.Fatalf("unsupported Contains value %#v", container)
	}
}

func (testAssertions) Len(t *testing.T, value any, want int) {
	t.Helper()
	got := reflect.ValueOf(value).Len()
	if got != want {
		t.Fatalf("length = %d, want %d", got, want)
	}
}

func (testAssertions) True(t *testing.T, value bool) {
	t.Helper()
	if !value {
		t.Fatal("expected true")
	}
}

func (testAssertions) NotEmpty(t *testing.T, value any) {
	t.Helper()
	if reflect.ValueOf(value).Len() == 0 {
		t.Fatal("expected non-empty value")
	}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
