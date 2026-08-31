package cli

import "testing"

func TestEventSubscriptionsCanExcludeFromWildcardMode(t *testing.T) {
	subscriptions, err := parseEventSubscriptions("")
	if err != nil {
		t.Fatalf("parse wildcard subscriptions: %v", err)
	}
	if !subscriptions.matches("Runtime.consoleAPICalled") {
		t.Fatal("wildcard subscriptions did not match an event")
	}
	if !subscriptions.remove("Runtime.consoleAPICalled") {
		t.Fatal("wildcard remove did not change subscriptions")
	}
	if subscriptions.matches("Runtime.consoleAPICalled") {
		t.Fatal("removed wildcard event still matched")
	}
	if !subscriptions.matches("Runtime.exceptionThrown") {
		t.Fatal("wildcard exclusion removed unrelated event")
	}
	if !subscriptions.add("Runtime.consoleAPICalled") || !subscriptions.matches("Runtime.consoleAPICalled") {
		t.Fatal("wildcard add did not restore the event")
	}
}
