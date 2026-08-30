package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestTargetIDPrefixesAreCaseInsensitiveAndRemainUnique(t *testing.T) {
	targets := []cdp.TargetInfo{
		{TargetID: "ABCDEF1234567890", Type: "page", URL: "https://one.test"},
		{TargetID: "ABC9999999999999", Type: "page", URL: "https://two.test"},
	}

	page, err := resolvePageTarget(targets, "abcdef12", "", "")
	if err != nil || page.TargetID != targets[0].TargetID {
		t.Fatalf("resolvePageTarget(lowercase prefix) = %+v, %v", page, err)
	}
	protocol, err := resolveProtocolTarget(targets, "abcdef12", "", "", "page")
	if err != nil || protocol.TargetID != targets[0].TargetID {
		t.Fatalf("resolveProtocolTarget(lowercase prefix) = %+v, %v", protocol, err)
	}

	_, err = resolvePageTarget(targets, "abc", "", "")
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "ambiguous_target" {
		t.Fatalf("resolvePageTarget(ambiguous lowercase prefix) error = %v, want ambiguous_target", err)
	}
}

func TestPageRowsPublishCopyReadyShortTargetID(t *testing.T) {
	row := pageRow(cdp.TargetInfo{TargetID: "abcdef1234567890", Type: "page"})
	if got := row["short_id"]; got != "ABCDEF12" {
		t.Fatalf("page short_id = %v, want ABCDEF12", got)
	}
	if got := row["id"]; got != "abcdef1234567890" {
		t.Fatalf("page full id = %v, want unchanged full ID", got)
	}
}

func TestForceCleanupAcceptsCaseInsensitiveUniquePrefix(t *testing.T) {
	targets := []cdp.TargetInfo{
		{TargetID: "ABCDEF1234567890", Type: "page"},
		{TargetID: "9999999999999999", Type: "page"},
	}
	candidates := cleanupCandidates(context.Background(), nil, targets, cleanupOptions{
		BrowserMode: "headed",
		Connection:  "default",
		Force:       true,
		ForceTarget: "abcdef12",
		Now:         time.Now(),
		Records:     map[string]pageCleanupRecord{},
	})
	if len(candidates) != 1 || candidates[0].Target.TargetID != targets[0].TargetID || !candidates[0].Ready {
		t.Fatalf("cleanup candidates = %+v, want one ready case-insensitive prefix match", candidates)
	}
}
