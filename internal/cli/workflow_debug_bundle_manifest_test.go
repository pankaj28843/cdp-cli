package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

func TestDebugBundleCommandAndInterruptedStageRecords(t *testing.T) {
	command := newDebugBundleCommandRecord(debugBundleCommandRecordOptions{
		Name:         "workflow debug-bundle",
		BrowserMode:  "headless",
		Timeout:      "30s",
		ExitCode:     ExitConnection,
		Status:       "error",
		TaskID:       "task-42",
		RunID:        "run-42",
		Stage:        "selection",
		Attempt:      2,
		ArtifactPath: "tmp/debug-bundle.bundle.json",
		Argv:         []string{"cdp", "workflow", "debug-bundle", "--target", "page-1"},
	})
	if command.Name == "" || command.BrowserMode != "headless" || command.Timeout != "30s" || command.ExitCode != ExitConnection || command.Status != "error" || command.TaskID != "task-42" || command.RunID != "run-42" || command.Stage != "selection" || command.Attempt != 2 || command.ArtifactPath == "" {
		t.Fatalf("command record = %+v, want required command-log fields", command)
	}

	artifact := debugBundleArtifactRecord{
		Type:    "workflow-debug-bundle-network",
		Path:    "tmp/debug-bundle.network.json",
		Content: "network-summary",
		Safety: artifacts.SafetyMetadata{
			RedactionMode:  artifacts.ModeSafe,
			Classification: "public_safe",
			Shareable:      true,
		},
	}
	stage := newDebugBundleStageRecord("selection", "interrupted", "task-42", "run-42", 1500*time.Millisecond, []debugBundleCommandRecord{command}, []debugBundleArtifactRecord{artifact})
	if stage.Name != "selection" || stage.Status != "interrupted" || stage.TaskID != "task-42" || stage.RunID != "run-42" || stage.AttemptCount != 1 || stage.ElapsedMS != 1500 || len(stage.Commands) != 1 || len(stage.Artifacts) != 1 {
		t.Fatalf("stage record = %+v, want interrupted stage with command and artifact refs", stage)
	}

	lines, err := debugBundleCommandLogJSONL([]debugBundleCommandRecord{command})
	if err != nil {
		t.Fatalf("marshal command log: %v", err)
	}
	line := string(lines)
	for _, want := range []string{`"name":"workflow debug-bundle"`, `"browser_mode":"headless"`, `"timeout":"30s"`, fmt.Sprintf(`"exit_code":%d`, ExitConnection), `"status":"error"`, `"task_id":"task-42"`, `"artifact_path":"tmp/debug-bundle.bundle.json"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("command log line = %s, want %s", line, want)
		}
	}

	summary := newDebugBundleSummary(debugBundleLayout{Manifest: "tmp/debug-bundle.bundle.json"}, artifacts.ModeSafe, false, []debugBundleArtifactRecord{artifact}, []debugBundleCommandRecord{command}, []debugBundleStageRecord{stage})
	if summary.SchemaVersion != debugBundleSchemaVersion || summary.DefaultJSON != "artifact_references" || summary.PublicSafeArtifacts != 1 || summary.LocalOnlyArtifacts != 0 || len(summary.Commands) != 1 || len(summary.Stages) != 1 {
		t.Fatalf("bundle summary = %+v, want command, stage, and safety counts", summary)
	}
}
