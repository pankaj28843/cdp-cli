package cli

import (
	"errors"
	"testing"
)

func TestEventDomainEnableMethodSupportsCanonicalGenericDomains(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{domain: "DOM", want: "DOM.enable"},
		{domain: "Performance", want: "Performance.enable"},
		{domain: "page", want: "Page.enable"},
		{domain: "NETWORK", want: "Network.enable"},
	}
	for _, test := range tests {
		got, ok := eventDomainEnableMethod(test.domain)
		if !ok || got != test.want {
			t.Errorf("eventDomainEnableMethod(%q) = %q, %v; want %q, true", test.domain, got, ok, test.want)
		}
	}
}

func TestEventDomainEnableMethodRejectsUnsafeIdentifiers(t *testing.T) {
	for _, domain := range []string{"", "DOM.enable", "DOM-child", "DOM;Runtime.enable", "DOM Runtime"} {
		if method, ok := eventDomainEnableMethod(domain); ok {
			t.Errorf("eventDomainEnableMethod(%q) = %q, true; want rejection", domain, method)
		}
	}
}

func TestParseEventDomainsCanonicalizesKnownNamesAndRejectsBrowserScope(t *testing.T) {
	domains, err := parseEventDomains("page,DOM,dom,Performance,network")
	if err != nil {
		t.Fatalf("parseEventDomains returned error: %v", err)
	}
	if got := domains.names(); len(got) != 4 || got[0] != "DOM" {
		t.Fatalf("canonical event domains = %#v, want one DOM and three legacy names", got)
	}
	for _, name := range []string{"DOM", "Performance"} {
		if !domains.contains(name) {
			t.Errorf("canonical event domains %#v do not contain %q", domains, name)
		}
	}
	for _, name := range []string{"page", "network"} {
		if !domains.contains(name) {
			t.Errorf("canonical event domains %#v do not contain legacy alias %q", domains, name)
		}
	}

	_, err = parseEventDomains("Target")
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "invalid_event_domain" || commandErr.ExitCode != ExitUsage {
		t.Fatalf("browser-scoped domain error = %v, want invalid_event_domain usage error", err)
	}

	_, err = parseEventDomains("DOM-child")
	if !errors.As(err, &commandErr) || commandErr.Code != "invalid_event_domain" {
		t.Fatalf("unsafe domain error = %v, want invalid_event_domain", err)
	}
}
