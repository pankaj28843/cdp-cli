package cli

import (
	"encoding/binary"
	"testing"
)

func TestPCM24To16ResamplerKeepsChunkBoundariesLossless(t *testing.T) {
	input := make([]byte, 6*2)
	for index, sample := range []int16{1, 2, 3, 4, 5, 6} {
		binary.LittleEndian.PutUint16(input[index*2:index*2+2], uint16(sample))
	}
	var whole pcm24To16Resampler
	all, err := whole.convert(input, true)
	if err != nil {
		t.Fatal(err)
	}
	var split pcm24To16Resampler
	first, err := split.convert(input[:4], false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := split.convert(input[4:], true)
	if err != nil {
		t.Fatal(err)
	}
	if string(append(first, second...)) != string(all) {
		t.Fatalf("split conversion differs: all=%v split=%v", all, append(first, second...))
	}
	got := make([]int16, len(all)/2)
	for index := range got {
		got[index] = int16(binary.LittleEndian.Uint16(all[index*2 : index*2+2]))
	}
	want := []int16{1, 2, 4, 5}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("sample %d = %d, want %d", index, got[index], want[index])
		}
	}
}
