package bing

import (
	"encoding/binary"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSpeechURLUsesPublicBingRecognitionContract(t *testing.T) {
	rawURL, err := speechURL("en-US", "REQUEST-ID", "CONNECTION-ID")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "sr.bing.com" {
		t.Fatalf("speech URL = %q, want Bing secure WebSocket", rawURL)
	}
	if parsed.Path != "/opaluqu/speech/recognition/interactive/cognitiveservices/v1" {
		t.Fatalf("speech path = %q", parsed.Path)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"clientbuild":               "bingDesktop",
		"referer":                   "https://www.bing.com/",
		"form":                      "QBLH",
		"uqurequestid":              "REQUEST-ID",
		"language":                  "en-US",
		"format":                    "simple",
		"Ocp-Apim-Subscription-Key": "key",
		"X-ConnectionId":            "CONNECTION-ID",
	} {
		if got := query.Get(key); got != want {
			t.Fatalf("query[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestAudioFramesMatchBingSpeechSDKWireShape(t *testing.T) {
	first := audioFrame("REQUEST-ID", time.Date(2026, time.January, 2, 3, 4, 5, 6e6, time.UTC), nil, true)
	if len(first) < 2 || first[0] != 0 || first[1] != '~' {
		t.Fatalf("first frame prefix = %#v, want 00 7e", first[:minFrameBytes(len(first), 2)])
	}
	firstText := string(first)
	if !strings.Contains(firstText, "Path: audio\r\n") ||
		!strings.Contains(firstText, "Content-Type: audio/x-wav\r\n") {
		t.Fatalf("first frame does not contain the WAV audio header")
	}
	riff := strings.Index(firstText, "RIFF")
	if riff < 0 || len(first) < riff+44 {
		t.Fatalf("first frame does not contain a complete WAV header")
	}
	if channels := binary.LittleEndian.Uint16(first[riff+22 : riff+24]); channels != 1 {
		t.Fatalf("WAV channels = %d, want mono", channels)
	}
	if rate := binary.LittleEndian.Uint32(first[riff+24 : riff+28]); rate != 16000 {
		t.Fatalf("WAV rate = %d, want 16000", rate)
	}

	pcm := []byte{1, 2, 3, 4}
	continuation := audioFrame("REQUEST-ID", time.Date(2026, time.January, 2, 3, 4, 5, 6e6, time.UTC), pcm, false)
	if len(continuation) < 2 || continuation[0] != 0 || continuation[1] != 'c' {
		t.Fatalf("continuation prefix = %#v, want 00 63", continuation[:minFrameBytes(len(continuation), 2)])
	}
	if strings.Contains(string(continuation), "RIFF") || !strings.HasSuffix(string(continuation), string(pcm)) {
		t.Fatalf("continuation frame did not carry raw PCM after its header")
	}
}

func TestSpeechControlFramesMatchBingSpeechSDKWireShape(t *testing.T) {
	frame, err := speechConfigMessage("REQUEST-ID", time.Date(2026, time.January, 2, 3, 4, 5, 6e6, time.UTC), "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	text := string(frame)
	if !strings.HasPrefix(text, "Path: speech.config\r\nX-RequestId: REQUEST-ID\r\n") ||
		!strings.Contains(text, "Content-Type: application/json\r\n\r\n") ||
		!strings.HasSuffix(text, "\"recognition\":\"interactive\"}") {
		t.Fatalf("speech config frame = %q", text)
	}
	contextFrame := string(speechContextMessage("REQUEST-ID", time.Date(2026, time.January, 2, 3, 4, 5, 6e6, time.UTC)))
	if !strings.Contains(contextFrame, "Path: speech.context\r\n") || !strings.HasSuffix(contextFrame, "\r\n\r\n{}") {
		t.Fatalf("speech context frame = %q", contextFrame)
	}
}

func TestParseSpeechPhrase(t *testing.T) {
	event, err := parseSpeechMessage([]byte("Content-Type:application/json; charset=utf-8\r\nPath:speech.phrase\r\n\r\n{\"RecognitionStatus\":\"Success\",\"DisplayText\":\"hello from Bing\"}"))
	if err != nil {
		t.Fatal(err)
	}
	if event.Path != "speech.phrase" || event.RecognitionStatus != "Success" || event.DisplayText != "hello from Bing" {
		t.Fatalf("parsed event = %+v", event)
	}
}

func minFrameBytes(length, limit int) int {
	if length < limit {
		return length
	}
	return limit
}
