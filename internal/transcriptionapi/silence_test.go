package transcriptionapi

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMostlySilentPCM16LEDistinguishesDigitalSilenceFromSpeech(t *testing.T) {
	silence := make([]byte, 24_000*2*3)
	if !mostlySilentPCM16LE(silence, 24_000) {
		t.Fatal("digital silence must be classified as mostly silent")
	}

	speech := make([]byte, len(silence))
	for index := 24_000; index < 48_000; index++ {
		binary.LittleEndian.PutUint16(speech[index*2:], uint16(int16(4_000)))
	}
	if mostlySilentPCM16LE(speech, 24_000) {
		t.Fatal("a clear voiced-energy window must not be classified as silence")
	}
}

func TestMostlySilentPCM16LETreatsLowNoiseAsSilence(t *testing.T) {
	noise := bytes.Repeat([]byte{0x18, 0x00}, 24_000*3)
	if !mostlySilentPCM16LE(noise, 24_000) {
		t.Fatal("low-level stationary noise must be classified as mostly silent")
	}
}

func TestMostlySilentPCM16LEHandlesMinimumSignedSample(t *testing.T) {
	signal := make([]byte, 24_000*2)
	value := int16(-32_768)
	for offset := 0; offset < len(signal); offset += 2 {
		binary.LittleEndian.PutUint16(signal[offset:], uint16(value))
	}
	if mostlySilentPCM16LE(signal, 24_000) {
		t.Fatal("a full-scale negative PCM signal must not be classified as silence")
	}
}

func TestSilentWAVContainsCanonicalMonoPCM(t *testing.T) {
	wav := testSilentWAV(24_000, 24_000)
	pcm, sampleRate, ok := parsePCM16WAV(wav)
	if !ok || sampleRate != 24_000 || len(pcm) != 24_000*2 {
		t.Fatalf("parsed WAV = ok:%v rate:%d bytes:%d", ok, sampleRate, len(pcm))
	}
}

func testSilentWAV(sampleRate, samples int) []byte {
	pcm := make([]byte, samples*2)
	data := bytes.NewBuffer(nil)
	data.WriteString("RIFF")
	_ = binary.Write(data, binary.LittleEndian, uint32(36+len(pcm)))
	data.WriteString("WAVEfmt ")
	_ = binary.Write(data, binary.LittleEndian, uint32(16))
	_ = binary.Write(data, binary.LittleEndian, uint16(1))
	_ = binary.Write(data, binary.LittleEndian, uint16(1))
	_ = binary.Write(data, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(data, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(data, binary.LittleEndian, uint16(2))
	_ = binary.Write(data, binary.LittleEndian, uint16(16))
	data.WriteString("data")
	_ = binary.Write(data, binary.LittleEndian, uint32(len(pcm)))
	_, _ = data.Write(pcm)
	return data.Bytes()
}
