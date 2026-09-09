package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseFrequencyBounds(t *testing.T) {
	tests := map[string]int{
		"10m": 10,
		"2h":  120,
		"14d": 14 * 24 * 60,
		"31d": 31 * 24 * 60,
	}
	for value, want := range tests {
		got, err := parseFrequency(value)
		if err != nil {
			t.Fatalf("parse %s: %v", value, err)
		}
		if got != want {
			t.Fatalf("parse %s = %d, want %d", value, got, want)
		}
	}
	for _, value := range []string{"", "9m", "32d", "1w", "-1h"} {
		if _, err := parseFrequency(value); err == nil {
			t.Fatalf("parse %q unexpectedly succeeded", value)
		}
	}
}

func TestLegacyHistoryIsReplacedByMetadataOnlyLedger(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CDP_STATE_DIR", root)
	directory := filepath.Join(root, "meta")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{
	  "schema_version": 2,
	  "updated_at": 10,
	  "last_runs": {
	    "ask-chatgpt": {
	      "actions": [{
	        "command": ["ask-chatgpt", "auth", "refresh"],
	        "stdout_json": {"target_id": "must-not-survive"},
	        "stderr_tail": "must-not-survive"
	      }]
	    }
	  }
	}`
	path := filepath.Join(directory, "maintenance-last-run.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	history, err := loadLastRunState()
	if err != nil {
		t.Fatalf("load legacy history: %v", err)
	}
	if len(history.LastRuns) != 0 ||
		history.SchemaVersion != historySchemaVersion {
		t.Fatalf("legacy history was trusted: %+v", history)
	}
	code := 0
	history = mergeHistory(history, []ProviderRunResult{
		{
			IntegrationID: "ask-chatgpt",
			OK:            true,
			State:         "terminal",
			Started:       20,
			Finished:      21,
			Actions: []ActionResult{
				{
					ActionID: "auth",
					Started:  20,
					Finished: 21,
					CommandResult: CommandResult{
						OK:         true,
						ReturnCode: &code,
						ProviderError: &ProviderDiagnostic{
							Code:     "runtime_only_code",
							ErrClass: "runtime_only_class",
							RetryAt:  "2026-07-25T18:15:00Z",
						},
						Payload: map[string]any{
							"target_id": "runtime-only",
						},
					},
				},
			},
		},
	})
	if err := saveLastRunState(history); err != nil {
		t.Fatalf("save sanitized history: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{
		"must-not-survive",
		"runtime-only",
		"payload",
		"stdout",
		"stderr",
		"target_id",
		"runtime_only_code",
		"runtime_only_class",
		"2026-07-25T18:15:00Z",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sanitized history contains %q:\n%s", forbidden, text)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("history mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRunJSONCommandReportsOnlyAllowlistedProviderDiagnostics(t *testing.T) {
	t.Setenv("GO_WANT_META_COMMAND_HELPER", "provider-error")
	result := runJSONCommand(
		context.Background(),
		[]string{os.Args[0], "-test.run=^TestMetaCommandHelper$"},
		5*time.Second,
	)
	if result.OK || result.ErrorType != "nonzero-exit" {
		t.Fatalf("result classification = %+v", result)
	}
	if result.ReturnCode == nil || *result.ReturnCode != 4 {
		t.Fatalf("return code = %v, want 4", result.ReturnCode)
	}
	if result.ProviderError == nil ||
		result.ProviderError.Code != "alex_auth_evidence_not_observed" ||
		result.ProviderError.ErrClass != "auth" ||
		result.ProviderError.RetryAt != "2026-07-25T18:15:00Z" {
		t.Fatalf("provider diagnostic = %+v", result.ProviderError)
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	text := string(raw)
	for _, forbidden := range []string{
		"PROMPT_CANARY",
		"ANSWER_CANARY",
		"TOKEN_CANARY",
		"TARGET_CANARY",
		"provider message",
		"payload",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public result contains %q:\n%s", forbidden, text)
		}
	}
}

func TestProviderDiagnosticRejectsUnboundedOrUnstructuredValues(t *testing.T) {
	diagnostic := providerDiagnostic(map[string]any{
		"ok": false,
		"error": map[string]any{
			"code":      "PROMPT_CANARY",
			"err_class": "this is not a class",
			"retry_at":  "tomorrow",
		},
	})
	if diagnostic != nil {
		t.Fatalf("unsafe diagnostic was accepted: %+v", diagnostic)
	}
}

func TestMetaCommandHelper(t *testing.T) {
	switch os.Getenv("GO_WANT_META_COMMAND_HELPER") {
	case "":
		return
	case "sequence":
		step := filepath.Base(os.Args[0])
		logFile, err := os.OpenFile(
			os.Getenv("META_COMMAND_HELPER_LOG"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY,
			0o600,
		)
		if err != nil {
			os.Exit(90)
		}
		_, _ = logFile.WriteString(step + "\n")
		_ = logFile.Close()
		if step == os.Getenv("META_COMMAND_HELPER_FAIL_STEP") {
			_, _ = os.Stdout.Write([]byte(
				`{"ok":false,"error":{"code":"validation_failed","err_class":"provider"}}`,
			))
			os.Exit(4)
		}
		_, _ = os.Stdout.Write([]byte(`{"ok":true}`))
		os.Exit(0)
	case "provider-error":
	case "wait-for-interrupt":
		marker := os.Getenv("META_COMMAND_HELPER_MARKER")
		if err := os.WriteFile(marker, []byte("ready"), 0o600); err != nil {
			os.Exit(92)
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		<-signals
		signal.Stop(signals)
		if err := os.WriteFile(marker, []byte("clean"), 0o600); err != nil {
			os.Exit(93)
		}
		_, _ = os.Stdout.Write([]byte(`{"ok":false}`))
		os.Exit(9)
	default:
		os.Exit(91)
	}
	_, _ = os.Stdout.Write([]byte(`{
	  "ok": false,
	  "error": {
	    "code": "alex_auth_evidence_not_observed",
	    "err_class": "auth",
	    "message": "provider message PROMPT_CANARY",
	    "retry_at": "2026-07-25T18:15:00Z"
	  },
	  "prompt": "PROMPT_CANARY",
	  "answer": "ANSWER_CANARY",
	  "token": "TOKEN_CANARY",
	  "target_id": "TARGET_CANARY"
	}`))
	os.Exit(4)
}

func TestDefaultAndNewMaintenanceEnrollmentsAreDisabled(t *testing.T) {
	state := defaultSchedulerState()
	for id, enrollment := range state.Enrollments {
		if enrollment.Enabled {
			t.Fatalf("%s was enabled without explicit enrollment", id)
		}
	}

	state.Enrollments["ask-chatgpt"] = Enrollment{
		IntegrationID:    "ask-chatgpt",
		Frequency:        "14d",
		FrequencyMinutes: 14 * 24 * 60,
		Enabled:          true,
	}
	delete(state.Enrollments, "ask-claude")
	state = withDefaultEnrollments(state)
	if !state.Enrollments["ask-chatgpt"].Enabled {
		t.Fatal("explicit ChatGPT enrollment was not preserved")
	}
	if state.Enrollments["ask-claude"].Enabled {
		t.Fatal("new Claude enrollment was enabled implicitly")
	}
}

func TestEffectiveMaintenanceParallelUsesSelectedJobsAndTabHeadroom(
	t *testing.T,
) {
	tests := []struct {
		name      string
		requested int
		jobs      int
		headroom  int
		want      int
		wantError bool
	}{
		{
			name:     "auto uses all seven when budget allows",
			jobs:     7,
			headroom: 9,
			want:     7,
		},
		{
			name:      "explicit cap",
			requested: 4,
			jobs:      7,
			headroom:  9,
			want:      4,
		},
		{
			name:     "tab budget caps auto",
			jobs:     7,
			headroom: 3,
			want:     3,
		},
		{
			name:      "no headroom fails closed",
			jobs:      7,
			headroom:  0,
			wantError: true,
		},
		{
			name:      "invalid explicit cap",
			requested: 8,
			jobs:      7,
			headroom:  9,
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := effectiveMaintenanceParallel(
				test.requested,
				test.jobs,
				test.headroom,
			)
			if test.wantError {
				if err == nil {
					t.Fatalf("parallel = %d, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf(
					"parallel = %d, error %v, want %d",
					got,
					err,
					test.want,
				)
			}
		})
	}
}

func TestHeadedTabHeadroomValidatesBudgetPayload(t *testing.T) {
	got, err := headedTabHeadroom(map[string]any{
		"budget": map[string]any{
			"tab_count": float64(6),
			"max_tabs":  float64(15),
		},
	})
	if err != nil || got != 9 {
		t.Fatalf("headroom = %d, error %v, want 9", got, err)
	}
	for _, payload := range []map[string]any{
		{},
		{"budget": map[string]any{"tab_count": "6", "max_tabs": 15.0}},
		{"budget": map[string]any{"tab_count": 6.5, "max_tabs": 15.0}},
		{"budget": map[string]any{"tab_count": 6.0, "max_tabs": 0.0}},
	} {
		if _, err := headedTabHeadroom(payload); err == nil {
			t.Fatalf("invalid budget was accepted: %+v", payload)
		}
	}
}

func TestMaintenanceWorkerPoolBoundsConcurrencyAndPreservesOrder(
	t *testing.T,
) {
	jobs := make([]maintenanceJob, 7)
	for index := range jobs {
		jobs[index] = maintenanceJob{
			index: index,
			integration: Integration{
				ID: fmt.Sprintf("provider-%d", index),
			},
		}
	}
	var active atomic.Int32
	var maximum atomic.Int32
	results := runMaintenanceJobs(
		context.Background(),
		3,
		jobs,
		func(
			_ context.Context,
			integration Integration,
			_ []MaintenanceAction,
		) ProviderRunResult {
			current := active.Add(1)
			for {
				seen := maximum.Load()
				if current <= seen || maximum.CompareAndSwap(seen, current) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
			return ProviderRunResult{
				IntegrationID: integration.ID,
				OK:            true,
				State:         "terminal",
			}
		},
	)
	if maximum.Load() != 3 {
		t.Fatalf("maximum concurrency = %d, want 3", maximum.Load())
	}
	for index, result := range results {
		want := fmt.Sprintf("provider-%d", index)
		if result.IntegrationID != want || !result.OK {
			t.Fatalf("result %d = %+v, want %s success", index, result, want)
		}
	}
}

func TestMaintenanceWorkerPoolStopsSchedulingAfterCancellation(
	t *testing.T,
) {
	jobs := make([]maintenanceJob, 20)
	for index := range jobs {
		jobs[index] = maintenanceJob{
			index: index,
			integration: Integration{
				ID: fmt.Sprintf("provider-%d", index),
			},
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var started atomic.Int32
	results := runMaintenanceJobs(
		ctx,
		4,
		jobs,
		func(
			ctx context.Context,
			integration Integration,
			_ []MaintenanceAction,
		) ProviderRunResult {
			if started.Add(1) == 1 {
				cancel()
			}
			<-ctx.Done()
			return ProviderRunResult{
				IntegrationID: integration.ID,
				OK:            false,
				State:         "interrupted",
			}
		},
	)
	if got := started.Load(); got < 1 || got > 4 {
		t.Fatalf("started jobs = %d, want between 1 and 4", got)
	}
	for index, result := range results {
		want := fmt.Sprintf("provider-%d", index)
		if result.IntegrationID != want || result.State != "interrupted" {
			t.Fatalf("result %d = %+v, want %s interrupted", index, result, want)
		}
	}
}

func TestDueActionsRespectSuccessIntervalsAndFailureBackoff(t *testing.T) {
	actions := []MaintenanceAction{
		{ID: "auth", IntervalMinutes: 60},
		{ID: "catalog", IntervalMinutes: 120},
	}
	enrollment := Enrollment{FrequencyMinutes: 500}
	now := time.Unix(10_000, 0)
	history := ProviderHistory{
		Actions: []ActionHistory{
			{
				ActionID:   "auth",
				OK:         true,
				FinishedAt: float64(now.Add(-61 * time.Minute).Unix()),
			},
			{
				ActionID:            "catalog",
				OK:                  false,
				FinishedAt:          float64(now.Add(-19 * time.Minute).Unix()),
				ConsecutiveFailures: 2,
			},
		},
	}
	due := dueActions(actions, enrollment, history, now)
	if len(due) != 1 || due[0].ID != "auth" {
		t.Fatalf("due actions = %+v, want auth only", due)
	}
	history.Actions[1].FinishedAt = float64(
		now.Add(-21 * time.Minute).Unix(),
	)
	due = dueActions(actions, enrollment, history, now)
	if len(due) != 2 {
		t.Fatalf("due actions after backoff = %+v, want both", due)
	}
}

func TestRegistrySchedulesOnlyReadOnlyMaintenance(t *testing.T) {
	for _, integration := range Integrations() {
		actions := integration.MaintenanceActions
		for _, action := range actions {
			var want []string
			switch action.ID {
			case "session":
				want = []string{
					integration.ID, "auth", "refresh", "--json",
				}
			case "capabilities":
				want = []string{
					integration.ID, "capabilities", "refresh", "--json",
				}
			case "catalog":
				if integration.ID == "ask-alex" {
					want = []string{
						"ask-alex", "catalog", "refresh", "--json",
					}
				}
			}
			if !slices.Equal(action.Command, want) {
				t.Fatalf(
					"%s registered mutating or unknown maintenance action %+v",
					integration.ID,
					action,
				)
			}
			if action.ID != "session" &&
				len(action.ValidationCommand) != 0 {
				t.Fatalf(
					"%s registered unexpected validation action %+v",
					integration.ID,
					action,
				)
			}
		}
		sessionIndex := slices.IndexFunc(actions, func(action MaintenanceAction) bool {
			return action.ID == "session"
		})
		if sessionIndex < 0 {
			t.Fatalf("%s has no session keepalive", integration.ID)
		}
		session := actions[sessionIndex]
		if session.IntervalMinutes != 60 ||
			!slices.Equal(
				session.Command,
				[]string{integration.ID, "auth", "refresh", "--json"},
			) {
			t.Fatalf("%s session keepalive = %+v", integration.ID, session)
		}

		if integration.ID == "ask-alex" {
			if len(session.ValidationCommand) != 0 {
				t.Fatalf("Ask Alex unexpectedly validates conversation history")
			}
			continue
		}
		if !slices.Equal(
			session.ValidationCommand,
			[]string{
				integration.ID,
				"conversations",
				"list",
				"--limit",
				"1",
				"--json",
			},
		) {
			t.Fatalf(
				"%s session validation = %+v",
				integration.ID,
				session,
			)
		}
	}
}

func TestSessionRetryAlwaysRefreshesBeforeConversationValidation(t *testing.T) {
	originalQuiet := maintenanceActionQuiet
	maintenanceActionQuiet = 0
	t.Cleanup(func() {
		maintenanceActionQuiet = originalQuiet
	})

	directory := t.TempDir()
	logPath := filepath.Join(directory, "sequence.log")
	authPath := filepath.Join(directory, "auth-refresh")
	validationPath := filepath.Join(directory, "conversation-list")
	for _, path := range []string{authPath, validationPath} {
		if err := os.Symlink(os.Args[0], path); err != nil {
			t.Fatalf("link helper %s: %v", path, err)
		}
	}
	t.Setenv("GO_WANT_META_COMMAND_HELPER", "sequence")
	t.Setenv("META_COMMAND_HELPER_LOG", logPath)
	action := MaintenanceAction{
		ID: "session",
		Command: []string{
			authPath,
			"-test.run=^TestMetaCommandHelper$",
		},
		ValidationCommand: []string{
			validationPath,
			"-test.run=^TestMetaCommandHelper$",
		},
		IntervalMinutes: 15,
	}

	t.Setenv("META_COMMAND_HELPER_FAIL_STEP", "auth-refresh")
	result := runMaintenanceAction(context.Background(), action)
	if result.OK || result.FailedStep != "refresh" {
		t.Fatalf("auth failure result = %+v", result)
	}
	assertHelperSequence(t, logPath, []string{"auth-refresh"})

	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("META_COMMAND_HELPER_FAIL_STEP", "conversation-list")
	result = runMaintenanceAction(context.Background(), action)
	if result.OK || result.FailedStep != "validation" {
		t.Fatalf("validation failure result = %+v", result)
	}
	assertHelperSequence(
		t,
		logPath,
		[]string{"auth-refresh", "conversation-list"},
	)

	now := time.Now()
	due := dueActions(
		[]MaintenanceAction{action},
		Enrollment{FrequencyMinutes: 14 * 24 * 60},
		ProviderHistory{Actions: []ActionHistory{
			{
				ActionID:            "session",
				OK:                  false,
				FinishedAt:          float64(now.Add(-11 * time.Minute).Unix()),
				ConsecutiveFailures: 1,
			},
		}},
		now,
	)
	if len(due) != 1 || due[0].ID != "session" {
		t.Fatalf("due actions = %+v, want composite session", due)
	}

	t.Setenv("META_COMMAND_HELPER_FAIL_STEP", "")
	result = runMaintenanceAction(context.Background(), due[0])
	if !result.OK || result.FailedStep != "" {
		t.Fatalf("successful retry result = %+v", result)
	}
	assertHelperSequence(
		t,
		logPath,
		[]string{
			"auth-refresh",
			"conversation-list",
			"auth-refresh",
			"conversation-list",
		},
	)
}

func assertHelperSequence(t *testing.T, path string, want []string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(raw))
	if !slices.Equal(got, want) {
		t.Fatalf("helper sequence = %v, want %v", got, want)
	}
}

func TestMergeHistoryKeepsAggregateFailureAfterPartialSuccess(t *testing.T) {
	code := 0
	history := LastRunState{
		SchemaVersion: historySchemaVersion,
		LastRuns: map[string]ProviderHistory{
			"ask-chatgpt": {
				OK: false,
				Actions: []ActionHistory{
					{
						ActionID:   "capabilities",
						OK:         false,
						FinishedAt: 10,
					},
				},
			},
		},
	}
	history = mergeHistory(history, []ProviderRunResult{
		{
			IntegrationID: "ask-chatgpt",
			OK:            true,
			State:         "terminal",
			Started:       20,
			Finished:      21,
			Actions: []ActionResult{
				{
					ActionID: "auth",
					Started:  20,
					Finished: 21,
					CommandResult: CommandResult{
						OK:         true,
						ReturnCode: &code,
					},
				},
			},
		},
	})
	got := history.LastRuns["ask-chatgpt"]
	if got.OK || !got.LastInvocationOK ||
		got.FailedActionID != "capabilities" {
		t.Fatalf("merged history = %+v", got)
	}
	if got.LastSuccessAt != nil {
		t.Fatalf("partial success advanced last success: %+v", got)
	}
}

func TestCacheCleanDeletesOnlyExactAlexCacheTargets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CDP_STATE_DIR", root)
	alex := filepath.Join(root, "webagent", "alex")
	content := filepath.Join(alex, "content", "course")
	if err := os.MkdirAll(content, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := `{
	  "refreshed_at": "2026-07-25T00:00:00Z",
	  "courses": {"one": {}},
	  "chapters": {"one": [{}, {}]}
	}`
	if err := os.WriteFile(
		filepath.Join(alex, "catalog.json"),
		[]byte(catalog),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(content, "chapter.json"),
		[]byte("{}"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(alex, "request-template.json")
	if err := os.WriteFile(template, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := cacheStatus(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status.Entries[0].CourseCount != 1 ||
		status.Entries[0].ChapterCount != 2 ||
		status.Entries[1].FileCount != 1 {
		t.Fatalf("cache status = %+v", status)
	}
	preview, err := cleanCache("ask-alex", "all", true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.WouldDeleteCount != 2 || preview.DeletedCount != 0 {
		t.Fatalf("preview = %+v", preview)
	}
	report, err := cleanCache("ask-alex", "all", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.DeletedCount != 2 {
		t.Fatalf("clean report = %+v", report)
	}
	if _, err := os.Stat(template); err != nil {
		t.Fatalf("auth template was touched: %v", err)
	}
}

func TestManagedCronReplacementPreservesUnrelatedEntries(t *testing.T) {
	current := "MAILTO=\"\"\n0 4 * * * keep-this\n" +
		cronBeginMarker + "\nold\n" + cronEndMarker + "\n"
	block := cronBeginMarker + "\nnew\n" + cronEndMarker + "\n"
	updated, err := replaceManagedBlock(current, block)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, "keep-this") ||
		!strings.Contains(updated, "\nnew\n") ||
		strings.Contains(updated, "\nold\n") {
		t.Fatalf("updated cron:\n%s", updated)
	}
	if _, err := removeManagedBlock(cronBeginMarker + "\n"); err == nil {
		t.Fatal("unterminated managed block unexpectedly accepted")
	}
}

func TestSchedulerStateJSONRemainsCompatible(t *testing.T) {
	state := defaultSchedulerState()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SchedulerState
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Enrollments) != len(registry) {
		t.Fatalf(
			"enrollments = %d, want %d",
			len(decoded.Enrollments),
			len(registry),
		)
	}
}
