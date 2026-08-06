package perplexity

import "testing"

func TestRenderedAnswerCandidateRequiresCompletedVisibleAnswer(t *testing.T) {
	base := askObservation{
		RouteMatches:   true,
		ConversationID: "conversation-1",
		Text:           "A complete answer.",
		AnswerCount:    1,
	}
	if !renderedAnswerCandidate(base, "conversation-1") {
		t.Fatal("completed visible answer should be a candidate")
	}
	cases := []struct {
		name string
		edit func(*askObservation)
	}{
		{
			name: "wrong conversation",
			edit: func(value *askObservation) {
				value.ConversationID = "conversation-2"
			},
		},
		{
			name: "still streaming",
			edit: func(value *askObservation) {
				value.Streaming = true
			},
		},
		{
			name: "no answer",
			edit: func(value *askObservation) {
				value.AnswerCount = 0
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			observation := base
			test.edit(&observation)
			if renderedAnswerCandidate(observation, "conversation-1") {
				t.Fatal("observation should not be a completed answer candidate")
			}
		})
	}
}
