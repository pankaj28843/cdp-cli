package m365

import (
	"strings"
)

// stitchTranscriptSegments turns provider-final ASR segments into one
// transcript. Microsoft 365 may emit independent sentence segments, a
// cumulative replacement, or a segment whose first words overlap the prior
// one. The accumulator accepts all three shapes without duplicating words.
func stitchTranscriptSegments(segments []string) string {
	var stitched []string
	for _, raw := range segments {
		segment := strings.Join(strings.Fields(raw), " ")
		if segment == "" {
			continue
		}
		incoming := strings.Fields(segment)
		if len(stitched) == 0 {
			stitched = append(stitched, incoming...)
			continue
		}

		if tokenPrefix(stitched, incoming) {
			stitched = incoming
			continue
		}
		if tokenPrefix(incoming, stitched) {
			continue
		}

		overlap := maximumTokenOverlap(stitched, incoming)
		stitched = append(stitched, incoming[overlap:]...)
	}
	return strings.Join(stitched, " ")
}

func tokenPrefix(prefix, value []string) bool {
	if len(prefix) > len(value) {
		return false
	}
	for index := range prefix {
		if comparableToken(prefix[index]) != comparableToken(value[index]) {
			return false
		}
	}
	return true
}

func maximumTokenOverlap(existing, incoming []string) int {
	maximum := min(len(existing), len(incoming))
	for size := maximum; size > 0; size-- {
		matches := true
		for index := 0; index < size; index++ {
			left := existing[len(existing)-size+index]
			right := incoming[index]
			if comparableToken(left) != comparableToken(right) {
				matches = false
				break
			}
		}
		if matches {
			return size
		}
	}
	return 0
}

func comparableToken(value string) string {
	return strings.ToLower(strings.Trim(value, ".,!?;:\"'“”‘’()[]{}"))
}
