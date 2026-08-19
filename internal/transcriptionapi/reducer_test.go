package transcriptionapi

import (
	"errors"
	"testing"
)

func TestSessionReducerReplacesCumulativeHypothesesAndFinalizes(t *testing.T) {
	session, err := NewSessionState("sess-1")
	if err != nil {
		t.Fatal(err)
	}

	changed, err := session.Apply(ProviderEvent{
		SessionID: "sess-1",
		ItemID:    "item-1",
		Sequence:  1,
		Kind:      EventHypothesis,
		Text:      "hello wor",
		Replace:   true,
	})
	if err != nil || !changed {
		t.Fatalf("first hypothesis: changed=%v err=%v", changed, err)
	}
	changed, err = session.Apply(ProviderEvent{
		SessionID: "sess-1",
		ItemID:    "item-1",
		Sequence:  2,
		Kind:      EventHypothesis,
		Text:      "hello world",
		Replace:   true,
	})
	if err != nil || !changed {
		t.Fatalf("replacement hypothesis: changed=%v err=%v", changed, err)
	}
	if got := session.Text("item-1"); got != "hello world" {
		t.Fatalf("hypothesis text = %q", got)
	}

	changed, err = session.Apply(ProviderEvent{
		SessionID: "sess-1",
		ItemID:    "item-1",
		Sequence:  3,
		Kind:      EventFinal,
		Text:      "hello world.",
	})
	if err != nil || !changed {
		t.Fatalf("final event: changed=%v err=%v", changed, err)
	}
	if got := session.Text("item-1"); got != "hello world." {
		t.Fatalf("final text = %q", got)
	}
	if session.Phase != SessionCompleted || session.LastSequence != 3 || !session.AllCompleted() {
		t.Fatalf("session = %+v", session)
	}

	changed, err = session.Apply(ProviderEvent{
		SessionID: "sess-1",
		ItemID:    "item-1",
		Sequence:  3,
		Kind:      EventFinal,
		Text:      "hello world.",
	})
	if err != nil || changed {
		t.Fatalf("duplicate final: changed=%v err=%v", changed, err)
	}

	if _, err = session.Apply(ProviderEvent{
		SessionID: "sess-1",
		ItemID:    "item-1",
		Sequence:  4,
		Kind:      EventFinal,
		Text:      "different final",
	}); !errors.Is(err, ErrConflictingFinal) {
		t.Fatalf("conflicting final error = %v", err)
	}
}

func TestSessionReducerAppendsDeltasAndIgnoresStaleEvents(t *testing.T) {
	session, err := NewSessionState("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []ProviderEvent{
		{SessionID: "sess-1", ItemID: "item-1", Sequence: 1, Kind: EventHypothesis, Text: "one "},
		{SessionID: "sess-1", ItemID: "item-1", Sequence: 2, Kind: EventHypothesis, Text: "two"},
	} {
		if _, err := session.Apply(ProviderEvent{
			SessionID: event.SessionID,
			ItemID:    event.ItemID,
			Sequence:  event.Sequence,
			Kind:      event.Kind,
			Text:      event.Text,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := session.Text("item-1"); got != "one two" {
		t.Fatalf("delta text = %q", got)
	}
	changed, err := session.Apply(ProviderEvent{
		SessionID: "sess-1",
		ItemID:    "item-1",
		Sequence:  1,
		Kind:      EventHypothesis,
		Text:      "stale",
		Replace:   true,
	})
	if err != nil || changed {
		t.Fatalf("stale hypothesis: changed=%v err=%v", changed, err)
	}
	if got := session.Text("item-1"); got != "one two" {
		t.Fatalf("stale event changed text to %q", got)
	}
}

func TestSessionReducerRejectsWrongSessionAndMalformedFailure(t *testing.T) {
	session, err := NewSessionState("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Apply(ProviderEvent{
		SessionID: "other",
		ItemID:    "item-1",
		Kind:      EventFinal,
		Text:      "text",
	}); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("wrong session error = %v", err)
	}
	if _, err := session.Apply(ProviderEvent{
		SessionID: "sess-1",
		ItemID:    "item-1",
		Kind:      EventFailure,
	}); err == nil {
		t.Fatal("failure without error should be rejected")
	}
}
