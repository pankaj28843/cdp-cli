package transcriptionapi

import (
	"encoding/binary"
	"math"
	"os"
	"sort"
)

const (
	silenceFrameMilliseconds = 30
	silenceFloorRMS          = 64.0
	silenceNoiseRatio        = 2.5
	silencePeakFloor         = 128
	silenceBaselineRMS       = 128.0
)

// mostlySilentAudio recognizes the canonical WAV emitted by VoxInput. Other
// formats deliberately fail open and remain the provider adapter's concern.
// This keeps the service gate small and prevents a decoder disagreement from
// turning valid provider input into a false silence result.
func mostlySilentAudio(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	pcm, sampleRate, ok := parsePCM16WAV(data)
	if !ok {
		return false, nil
	}
	return mostlySilentPCM16LE(pcm, sampleRate), nil
}

func parsePCM16WAV(data []byte) ([]byte, int, bool) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, false
	}
	var (
		format      uint16
		channels    uint16
		bits        uint16
		sampleRate  uint32
		pcm         []byte
		foundFormat bool
		foundData   bool
	)
	for offset := 12; offset+8 <= len(data); {
		chunkSize := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		end := uint64(start) + chunkSize
		if end > uint64(len(data)) {
			return nil, 0, false
		}
		switch string(data[offset : offset+4]) {
		case "fmt ":
			if chunkSize < 16 {
				return nil, 0, false
			}
			format = binary.LittleEndian.Uint16(data[start : start+2])
			channels = binary.LittleEndian.Uint16(data[start+2 : start+4])
			sampleRate = binary.LittleEndian.Uint32(data[start+4 : start+8])
			bits = binary.LittleEndian.Uint16(data[start+14 : start+16])
			foundFormat = true
		case "data":
			pcm = data[start:int(end)]
			foundData = true
		}
		offset = int(end)
		if chunkSize%2 != 0 {
			offset++
		}
	}
	if !foundFormat || !foundData || format != 1 || channels != 1 || bits != 16 || sampleRate == 0 || sampleRate > 192_000 || len(pcm)%2 != 0 {
		return nil, 0, false
	}
	return pcm, int(sampleRate), true
}

func mostlySilentPCM16LE(pcm []byte, sampleRate int) bool {
	if len(pcm) == 0 {
		return true
	}
	if sampleRate <= 0 || len(pcm)%2 != 0 {
		return false
	}
	frameSamples := sampleRate * silenceFrameMilliseconds / 1_000
	if frameSamples < 1 {
		frameSamples = 1
	}
	energies := make([]float64, 0, (len(pcm)/2+frameSamples-1)/frameSamples)
	peaks := make([]int, 0, cap(energies))
	for start := 0; start < len(pcm); {
		end := start + frameSamples*2
		if end > len(pcm) {
			end = len(pcm)
		}
		var sum float64
		var peak int
		samples := 0
		for offset := start; offset < end; offset += 2 {
			value := int16(binary.LittleEndian.Uint16(pcm[offset : offset+2]))
			magnitude := int(value)
			if magnitude < 0 {
				magnitude = -magnitude
			}
			if magnitude > peak {
				peak = magnitude
			}
			sum += float64(value) * float64(value)
			samples++
		}
		if samples > 0 {
			energies = append(energies, math.Sqrt(sum/float64(samples)))
			peaks = append(peaks, peak)
		}
		start = end
	}
	if len(energies) == 0 {
		return true
	}
	sorted := append([]float64(nil), energies...)
	sort.Float64s(sorted)
	noiseFloor := sorted[len(sorted)*20/100]
	threshold := math.Max(silenceFloorRMS, noiseFloor*silenceNoiseRatio)
	minimumEnergy := sorted[0]
	maximumPeak := 0
	for index, energy := range energies {
		if peaks[index] > maximumPeak {
			maximumPeak = peaks[index]
		}
		if energy >= threshold && peaks[index] >= silencePeakFloor {
			return false
		}
	}
	// A consistently energetic signal has no quiet baseline from which to
	// estimate a noise floor. Keep that signal on the provider path.
	return !(minimumEnergy >= silenceBaselineRMS && maximumPeak >= silencePeakFloor)
}
