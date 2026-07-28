package chatgpt

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func artifactDetail(text string, messageID string) map[string]any {
	return map[string]any{
		"conversation_id": "conversation-id",
		"current_node":    messageID,
		"mapping": map[string]any{
			"user-node": map[string]any{
				"id": "user-node",
				"message": map[string]any{
					"author": map[string]any{"role": "user"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"create a csv"},
					},
					"status": "finished_successfully",
				},
			},
			messageID: map[string]any{
				"id": messageID,
				"message": map[string]any{
					"author": map[string]any{"role": "assistant"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{text},
					},
					"status":   "finished_successfully",
					"end_turn": true,
				},
			},
		},
	}
}

func TestLocateArtifactRequiresOneFinishedExactNamedPath(t *testing.T) {
	const (
		fileName = "agent-cli-web-artifact.csv"
		link     = "[download](sandbox:/mnt/data/agent-cli-web-artifact.csv)"
	)
	detail := artifactDetail(link, "assistant-artifact")
	locator, err := locateArtifact(detail, "conversation-id", fileName)
	if err != nil {
		t.Fatalf("locateArtifact: %v", err)
	}
	if locator.ConversationID != "conversation-id" ||
		locator.MessageID != "assistant-artifact" ||
		locator.FileName != fileName ||
		locator.SandboxPath != "/mnt/data/"+fileName {
		t.Fatalf("locator = %+v", locator)
	}

	detail["mapping"].(map[string]any)["assistant-artifact"].(map[string]any)["message"].(map[string]any)["end_turn"] = false
	if _, err := locateArtifact(
		detail,
		"conversation-id",
		fileName,
	); err == nil || !strings.Contains(err.Error(), "unfinished") {
		t.Fatalf("unfinished locator error = %v", err)
	}
}

func TestLocateArtifactRejectsMismatchAmbiguityAndUnsafePath(t *testing.T) {
	const fileName = "artifact.csv"
	for _, test := range []struct {
		name   string
		detail map[string]any
		match  string
	}{
		{
			name: "mismatched_filename",
			detail: artifactDetail(
				"sandbox:/mnt/data/other.csv",
				"assistant-one",
			),
			match: "no finished artifact",
		},
		{
			name: "unsafe_traversal",
			detail: artifactDetail(
				"sandbox:/mnt/data/../artifact.csv",
				"assistant-one",
			),
			match: "no finished artifact",
		},
		{
			name: "invalid_conversation_identity",
			detail: func() map[string]any {
				detail := artifactDetail(
					"sandbox:/mnt/data/artifact.csv",
					"assistant-one",
				)
				detail["conversation_id"] = 7
				return detail
			}(),
			match: "detail id",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := locateArtifact(
				test.detail,
				"conversation-id",
				fileName,
			); err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("locateArtifact error = %v", err)
			}
		})
	}

	detail := artifactDetail(
		"sandbox:/mnt/data/artifact.csv",
		"assistant-one",
	)
	detail["mapping"].(map[string]any)["assistant-two"] =
		artifactDetail(
			"sandbox:/mnt/data/artifact.csv",
			"assistant-two",
		)["mapping"].(map[string]any)["assistant-two"]
	if _, err := locateArtifact(
		detail,
		"conversation-id",
		fileName,
	); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous locator error = %v", err)
	}
}

func TestValidateArtifactMetadataRequiresObservedSameOriginRoute(t *testing.T) {
	valid := map[string]any{
		"status":          "success",
		"file_name":       "artifact.csv",
		"file_size_bytes": float64(15),
		"mime_type":       "text/csv",
		"download_url": "https://chatgpt.com/backend-api/estuary/content" +
			"?cid=synthetic&sig=redacted",
	}
	metadata, err := validateArtifactMetadata(valid, "artifact.csv")
	if err != nil {
		t.Fatalf("validateArtifactMetadata: %v", err)
	}
	if metadata.FileName != "artifact.csv" ||
		metadata.Size != 15 ||
		!metadata.SizePresent ||
		metadata.SizeEncoding != "number" ||
		metadata.MIMEType != "text/csv" {
		t.Fatalf("metadata = %+v", metadata)
	}

	valid["file_size_bytes"] = "15"
	metadata, err = validateArtifactMetadata(valid, "artifact.csv")
	if err != nil {
		t.Fatalf("validate decimal string size: %v", err)
	}
	if metadata.Size != 15 ||
		metadata.SizeEncoding != "decimal_string" {
		t.Fatalf("decimal string metadata = %+v", metadata)
	}

	valid["file_size_bytes"] = nil
	metadata, err = validateArtifactMetadata(valid, "artifact.csv")
	if err != nil {
		t.Fatalf("validate absent size: %v", err)
	}
	if metadata.SizePresent ||
		metadata.SizeEncoding != "absent" {
		t.Fatalf("absent size metadata = %+v", metadata)
	}

	for _, downloadURL := range []string{
		"https://example.invalid/backend-api/estuary/content",
		"https://chatgpt.com/other",
		"http://chatgpt.com/backend-api/estuary/content",
	} {
		payload := map[string]any{}
		for key, value := range valid {
			payload[key] = value
		}
		payload["download_url"] = downloadURL
		if _, err := validateArtifactMetadata(
			payload,
			"artifact.csv",
		); err == nil {
			t.Fatalf("unsafe download URL accepted: %s", downloadURL)
		}
	}
}

func TestArtifactDestinationIsExplicitAtomicAndNoOverwrite(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "artifact.csv")
	resolved, err := validateArtifactDestination(destination, false)
	if err != nil {
		t.Fatalf("validate new destination: %v", err)
	}
	if resolved != destination {
		t.Fatalf("resolved = %q, want %q", resolved, destination)
	}
	if err := writeArtifactAtomic(
		destination,
		[]byte("name,value\nx,1\n"),
		false,
	); err != nil {
		t.Fatalf("write new artifact: %v", err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("artifact mode = %o, want 600", got)
	}
	if err := writeArtifactAtomic(
		destination,
		[]byte("must not replace"),
		false,
	); err == nil {
		t.Fatal("no-overwrite write replaced an existing artifact")
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if string(content) != "name,value\nx,1\n" {
		t.Fatalf("artifact content changed: %q", content)
	}
	if err := writeArtifactAtomic(
		destination,
		[]byte("replacement"),
		true,
	); err != nil {
		t.Fatalf("overwrite artifact: %v", err)
	}
	content, err = os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read replacement: %v", err)
	}
	if string(content) != "replacement" {
		t.Fatalf("replacement content = %q", content)
	}
}

func TestArtifactDestinationRejectsSymlinkAndInvalidInputBeforeBrowser(t *testing.T) {
	directory := t.TempDir()
	regular := filepath.Join(directory, "regular")
	if err := os.WriteFile(regular, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "link")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := validateArtifactDestination(symlink, true); err == nil {
		t.Fatal("symlink destination was accepted")
	}

	result := DownloadArtifact(
		context.Background(),
		ArtifactDownloadConfig{
			OutputPath: filepath.Join(directory, "out.csv"),
		},
		"invalid/id",
		"artifact.csv",
	)
	if result.OK ||
		result.Stage != webagent.StagePlanned ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_invalid_conversation_id" ||
		!result.Error.RetrySafe ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("invalid artifact result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("invalid artifact result validation: %v", err)
	}
}

func TestOversizedArtifactRateLimitKeepsRateLimitFailure(t *testing.T) {
	observedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	client := testsupport.NewBrowser()
	client.Evaluate = func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		if !strings.Contains(expression, "const response = await fetch") {
			return map[string]any{}, nil
		}
		return map[string]any{
			"ok":                   false,
			"status_code":          http.StatusTooManyRequests,
			"retry_after":          "1",
			"response_observed_at": observedAt.Format(time.RFC3339Nano),
			"error":                "response_too_large",
		}, nil
	}
	engine, _, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	lease, err := engine.Acquire(
		context.Background(),
		browserflow.AcquireRequest{
			RunID:      "artifact-rate-limit",
			Provider:   "chatgpt",
			Operation:  string(webagent.OperationArtifactDownload),
			InitialURL: "about:blank",
		},
	)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() {
		if _, closeErr := lease.Close(context.Background()); closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	}()

	_, failure := browserFetchBinary(
		context.Background(),
		lease.Session(),
		RequestTemplate{},
		Origin+"/backend-api/estuary/content",
		"/backend-api/estuary/content",
		1,
	)
	if failure == nil ||
		failure.code != "chatgpt_rate_limited" ||
		failure.errClass != "rate_limit" ||
		failure.statusCode != http.StatusTooManyRequests ||
		!failure.retryAt.Equal(observedAt.Add(time.Second)) {
		t.Fatalf("artifact failure = %+v", failure)
	}
}
