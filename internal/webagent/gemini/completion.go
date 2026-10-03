package gemini

import "strings"

// Provider completion controls must agree with two consecutive exact snapshots.
// A transport error or changed response invalidates the previous confirmation.
type answerCompletion struct {
	text  string
	count int
}

func (c *answerCompletion) observe(observation detailObservation, conversationID string, err error) bool {
	if err != nil || !observation.RouteMatches || observation.ConversationID != conversationID || observation.AnswerCount == 0 || strings.TrimSpace(observation.Text) == "" || observation.Streaming || !observation.CompletionReady {
		*c = answerCompletion{}
		return false
	}
	confirmed := c.text == observation.Text && c.count == observation.AnswerCount
	c.text, c.count = observation.Text, observation.AnswerCount
	return confirmed
}
