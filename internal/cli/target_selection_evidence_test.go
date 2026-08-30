package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

func TestTargetRowsPublishDirectSelectionMetadata(t *testing.T) {
	targets := []cdp.TargetInfo{
		{TargetID: "workerabcdef1234", Type: "worker"},
		{TargetID: "pageabcdef123456", Type: "page"},
		{TargetID: "second1234567890", Type: "page"},
	}
	all := targetRows(targets)
	if got := all[0]["short_id"]; got != "WORKERAB" {
		t.Fatalf("target short_id = %v, want WORKERAB", got)
	}
	pages := pageRows(targets)
	if len(pages) != 2 || pages[0]["index"] != 1 || pages[1]["index"] != 2 {
		t.Fatalf("page indexes = %+v, want page-only 1,2 in listed order", pages)
	}
}

func TestHumanTargetRowsPublishCopyReadySelectors(t *testing.T) {
	targets := []cdp.TargetInfo{
		{TargetID: "workerabcdef1234", Type: "worker", URL: "https://example.test/worker.js", Title: "Worker\tTitle"},
		{TargetID: "pageabcdef123456", Type: "page", URL: "https://example.test/app", Title: "App\nTitle"},
	}

	targetLines := targetHumanLines(targetRows(targets))
	if len(targetLines) != 2 || targetLines[0] != "WORKERAB\tworkerabcdef1234\tworker\thttps://example.test/worker.js\t\"Worker\\tTitle\"" {
		t.Fatalf("target human lines = %q, want short/full/type/URL/quoted-title", targetLines)
	}
	pageLines := pageHumanLines(pageRows(targets))
	if len(pageLines) != 1 || pageLines[0] != "[1]\tPAGEABCD\tpageabcdef123456\thttps://example.test/app\t\"App\\nTitle\"" {
		t.Fatalf("page human lines = %q, want index/short/full/URL/quoted-title", pageLines)
	}
}

func TestProtocolTargetFilterHelpDescribesUniqueConjunction(t *testing.T) {
	cmd := (&app{}).newProtocolExecCommand()
	for _, name := range []string{"url-contains", "title-contains"} {
		usage := cmd.Flags().Lookup(name).Usage
		if strings.Contains(usage, "first matching") || !strings.Contains(usage, "combines") || !strings.Contains(usage, "one target") {
			t.Fatalf("--%s usage = %q, want unique conjunctive filter contract", name, usage)
		}
	}
}

func TestCustomCommandsRejectTextSelectorConflictsBeforeConnection(t *testing.T) {
	commands := map[string][]string{
		"events stream":       {"events", "stream", "--target", "page-one", "--url-contains", "example.test", "--json"},
		"events tap":          {"events", "tap", "--target", "page-one", "--url-contains", "example.test", "--json"},
		"events interactions": {"events", "interactions", "--target", "page-one", "--url-contains", "example.test", "--json"},
		"click":               {"click", "main", "--target", "page-one", "--url-contains", "example.test", "--json"},
		"page select":         {"page", "select", "page-one", "--url-contains", "example.test", "--json"},
		"page reload":         {"page", "reload", "--target", "page-one", "--url-contains", "example.test", "--json"},
		"page back":           {"page", "back", "--target", "page-one", "--url-contains", "example.test", "--json"},
		"page activate":       {"page", "activate", "--target", "page-one", "--url-contains", "example.test", "--json"},
		"page close":          {"page", "close", "--target", "page-one", "--url-contains", "example.test", "--json"},
	}
	for name, args := range commands {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			args = append([]string{"--state-dir", t.TempDir(), "--browser-mode", "headed"}, args...)
			code := Execute(context.Background(), args, &out, &errOut, BuildInfo{})
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode output: %v; stdout=%s stderr=%s", err, out.String(), errOut.String())
			}
			if code != ExitUsage || result["code"] != "invalid_target_selector" {
				t.Fatalf("exit=%d result=%#v, want preflight invalid_target_selector", code, result)
			}
		})
	}
}

func TestPopupAndDownloadSelectorHelpRequiresUniquePage(t *testing.T) {
	commands := map[string]*cobra.Command{
		"popup":    (&app{}).newWaitPopupCommand(),
		"download": (&app{}).newWaitDownloadCommand(),
	}
	for commandName, cmd := range commands {
		for _, flagName := range []string{"url-contains", "title-contains"} {
			usage := cmd.Flags().Lookup(flagName).Usage
			if strings.Contains(usage, "first") || !strings.Contains(usage, "unique") {
				t.Fatalf("%s --%s usage = %q, want unique page", commandName, flagName, usage)
			}
		}
	}
}

func TestAmbiguousTargetErrorsIncludeBoundedShortIDs(t *testing.T) {
	targets := make([]cdp.TargetInfo, 0, 12)
	for i := 0; i < 12; i++ {
		targets = append(targets, cdp.TargetInfo{
			TargetID: "ABC" + string(rune('A'+i)) + "1234567890",
			Type:     "page",
		})
	}

	assertAmbiguousTargetEvidence(t, func() error {
		_, err := resolvePageTarget(targets, "abc", "", "")
		return err
	}())
	assertAmbiguousTargetEvidence(t, func() error {
		_, err := resolveProtocolTarget(targets, "abc", "", "", "page")
		return err
	}())
}

func assertAmbiguousTargetEvidence(t *testing.T, err error) {
	t.Helper()
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "ambiguous_target" {
		t.Fatalf("error = %v, want ambiguous_target", err)
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("ambiguous target data = %#v, want object", commandErr.Data)
	}
	shortIDs, ok := data["candidate_short_ids"].([]string)
	ids, idsOK := data["candidate_ids"].([]string)
	if data["candidate_count"] != 12 || data["candidate_truncated"] != true || !ok || !idsOK || len(shortIDs) != 10 || len(ids) != 10 {
		t.Fatalf("ambiguous target data = %#v, want count 12 and ten bounded IDs", data)
	}
}

func TestTargetErrorEvidenceIncludesExactIDsWhenShortIDsCollide(t *testing.T) {
	targets := []cdp.TargetInfo{
		{TargetID: "ABCDEF1211111111", Type: "page"},
		{TargetID: "ABCDEF1222222222", Type: "page"},
	}
	want := []string{"ABCDEF1211111111", "ABCDEF1222222222"}
	tests := map[string]struct {
		err   error
		field string
	}{
		"page ambiguity":     {resolvePageTargetError(targets, "abcdef12"), "candidate_ids"},
		"protocol ambiguity": {resolveProtocolTargetError(targets, "abcdef12"), "candidate_ids"},
		"page miss":          {resolvePageTargetError(targets, "missing"), "available_ids"},
		"protocol miss":      {resolveProtocolTargetError(targets, "missing"), "available_ids"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var commandErr *CommandError
			if !errors.As(test.err, &commandErr) {
				t.Fatalf("error = %v, want command error", test.err)
			}
			data, ok := commandErr.Data.(map[string]any)
			ids, idsOK := data[test.field].([]string)
			if !ok || !idsOK || len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
				t.Fatalf("%s data = %#v, want exact IDs %v", test.field, commandErr.Data, want)
			}
		})
	}
}

func resolvePageTargetError(targets []cdp.TargetInfo, targetID string) error {
	_, err := resolvePageTarget(targets, targetID, "", "")
	return err
}

func resolveProtocolTargetError(targets []cdp.TargetInfo, targetID string) error {
	_, err := resolveProtocolTarget(targets, targetID, "", "", "page")
	return err
}

func TestTargetNotFoundErrorsIncludeTypeScopedAvailableShortIDs(t *testing.T) {
	targets := []cdp.TargetInfo{
		{TargetID: "PAGEAAAA12345678", Type: "page"},
		{TargetID: "PAGEBBBB12345678", Type: "page"},
		{TargetID: "WORKERAA12345678", Type: "service_worker"},
	}

	_, pageErr := resolvePageTarget(targets, "missing", "", "")
	assertAvailableTargetEvidence(t, pageErr, 2, []string{"PAGEAAAA", "PAGEBBBB"})

	_, protocolErr := resolveProtocolTarget(targets, "missing", "", "", "service_worker")
	assertAvailableTargetEvidence(t, protocolErr, 1, []string{"WORKERAA"})

	_, indexErr := resolvePageTargetByIndex(targets, 3)
	assertAvailableTargetEvidence(t, indexErr, 2, []string{"PAGEAAAA", "PAGEBBBB"})
}

func TestPageURLAndTitleSelectorsRejectAmbiguousMatches(t *testing.T) {
	targets := []cdp.TargetInfo{
		{TargetID: "PAGEAAAA12345678", Type: "page", URL: "https://example.test/app/one", Title: "App dashboard one"},
		{TargetID: "PAGEBBBB12345678", Type: "page", URL: "https://example.test/app/two", Title: "App dashboard two"},
		{TargetID: "WORKERAA12345678", Type: "service_worker", URL: "https://example.test/worker", Title: "App dashboard worker"},
	}

	for name, resolve := range map[string]func() error{
		"url": func() error {
			_, err := resolvePageTarget(targets, "", "example.test/app", "")
			return err
		},
		"title": func() error {
			_, err := resolvePageTarget(targets, "", "", "app dashboard")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			var commandErr *CommandError
			if err := resolve(); !errors.As(err, &commandErr) || commandErr.Code != "ambiguous_target" {
				t.Fatalf("error = %v, want ambiguous_target", err)
			}
			data, ok := commandErr.Data.(map[string]any)
			shortIDs, idsOK := data["candidate_short_ids"].([]string)
			if !ok || !idsOK || data["candidate_count"] != 2 || data["candidate_truncated"] != false || len(shortIDs) != 2 || shortIDs[0] != "PAGEAAAA" || shortIDs[1] != "PAGEBBBB" {
				t.Fatalf("ambiguous target data = %#v, want ordered page short IDs", commandErr.Data)
			}
		})
	}
}

func TestPageSelectorsRejectConflictingModes(t *testing.T) {
	targets := []cdp.TargetInfo{
		{TargetID: "PAGEAAAA12345678", Type: "page", URL: "https://example.test/app", Title: "App dashboard"},
	}

	for name, resolve := range map[string]func() error{
		"target and URL": func() error {
			_, err := resolvePageTarget(targets, "pageaaaa", "ignored.example", "")
			return err
		},
		"URL and title": func() error {
			_, err := resolvePageTarget(targets, "", "example.test", "ignored title")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			var commandErr *CommandError
			if err := resolve(); !errors.As(err, &commandErr) || commandErr.Code != "invalid_target_selector" {
				t.Fatalf("error = %v, want invalid_target_selector", err)
			}
		})
	}

	selected, err := resolveProtocolTarget(targets, "pageaaaa", "example.test", "dashboard", "page")
	if err != nil || selected.TargetID != targets[0].TargetID {
		t.Fatalf("protocol conjunctive selection = (%+v, %v), want original target", selected, err)
	}
}

func TestPageSelectorValidatorRejectsConflictingTextModes(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Int("target-index", 0, "")
	for name, selectors := range map[string][3]string{
		"target and URL": {"page-one", "example.test", ""},
		"URL and title":  {"", "example.test", "dashboard"},
	} {
		t.Run(name, func(t *testing.T) {
			var commandErr *CommandError
			err := validatePageTargetIndexSelector(cmd, selectors[0], selectors[1], selectors[2], 0)
			if !errors.As(err, &commandErr) || commandErr.Code != "invalid_target_selector" {
				t.Fatalf("error = %v, want invalid_target_selector", err)
			}
		})
	}
}

func TestPageSelectorErrorsIncludeBoundedIndexes(t *testing.T) {
	targets := make([]cdp.TargetInfo, 0, 13)
	targets = append(targets, cdp.TargetInfo{TargetID: "WORKERAA12345678", Type: "service_worker"})
	for i := 0; i < 12; i++ {
		targets = append(targets, cdp.TargetInfo{
			TargetID: "PAGE" + string(rune('A'+i)) + "1234567890",
			Type:     "page",
		})
	}

	_, pageAmbiguous := resolvePageTarget(targets, "page", "", "")
	assertBoundedPageIndexes(t, pageAmbiguous, "candidate_indexes")
	_, pageMissing := resolvePageTarget(targets, "missing", "", "")
	assertBoundedPageIndexes(t, pageMissing, "available_indexes")

	_, protocolAmbiguous := resolveProtocolTarget(targets, "page", "", "", "page")
	assertNoPageIndexes(t, protocolAmbiguous)
	_, protocolMissing := resolveProtocolTarget(targets, "missing", "", "", "page")
	assertNoPageIndexes(t, protocolMissing)
}

func assertBoundedPageIndexes(t *testing.T, err error, field string) {
	t.Helper()
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error = %v, want command error", err)
	}
	data, ok := commandErr.Data.(map[string]any)
	indexes, indexesOK := data[field].([]int)
	if !ok || !indexesOK || len(indexes) != 10 {
		t.Fatalf("%s data = %#v, want ten bounded page indexes", field, commandErr.Data)
	}
	for i, index := range indexes {
		if index != i+1 {
			t.Fatalf("%s = %v, want indexes 1-10", field, indexes)
		}
	}
}

func assertNoPageIndexes(t *testing.T, err error) {
	t.Helper()
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error = %v, want command error", err)
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("error data = %#v, want object", commandErr.Data)
	}
	if _, exists := data["candidate_indexes"]; exists {
		t.Fatalf("protocol error leaked page candidate indexes: %#v", data)
	}
	if _, exists := data["available_indexes"]; exists {
		t.Fatalf("protocol error leaked page available indexes: %#v", data)
	}
}

func assertAvailableTargetEvidence(t *testing.T, err error, count int, shortIDs []string) {
	t.Helper()
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "target_not_found" {
		t.Fatalf("error = %v, want target_not_found", err)
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("target-not-found data = %#v, want object", commandErr.Data)
	}
	gotIDs, ok := data["available_short_ids"].([]string)
	fullIDs, fullIDsOK := data["available_ids"].([]string)
	if data["available_count"] != count || data["available_truncated"] != false || !ok || !fullIDsOK || len(gotIDs) != len(shortIDs) || len(fullIDs) != len(shortIDs) {
		t.Fatalf("target-not-found data = %#v, want count %d and IDs %v", data, count, shortIDs)
	}
	for i := range shortIDs {
		if gotIDs[i] != shortIDs[i] {
			t.Fatalf("available short IDs = %v, want %v", gotIDs, shortIDs)
		}
		if !strings.HasPrefix(strings.ToUpper(fullIDs[i]), shortIDs[i]) {
			t.Fatalf("available full IDs = %v, want IDs aligned with %v", fullIDs, shortIDs)
		}
	}
}
