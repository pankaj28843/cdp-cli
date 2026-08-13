package chatgpt

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestTerminalAttachmentKeepsPrivateLocatorOutOfJSON(t *testing.T) {
	payload := map[string]any{
		"conversation_id": "conversation-attachments",
		"current_node":    "answer-current",
		"mapping": map[string]any{
			"answer-current": map[string]any{
				"id":     "answer-current",
				"parent": "answer-earlier",
				"message": map[string]any{
					"author":   map[string]any{"role": "assistant"},
					"status":   "finished_successfully",
					"end_turn": true,
					"content": map[string]any{
						"content_type": "multimodal_text",
						"parts": []any{map[string]any{
							"content_type": "image_asset_pointer",
							"asset_pointer": "https://chatgpt.com/backend-api/files/download/file_current" +
								"?conversation_id=conversation-attachments&sig=private-value",
							"width":  float64(64),
							"height": float64(64),
						}},
					},
				},
			},
			"answer-earlier": map[string]any{
				"id":     "answer-earlier",
				"parent": "prompt",
				"message": map[string]any{
					"author":   map[string]any{"role": "assistant"},
					"status":   "finished_successfully",
					"end_turn": true,
					"content": map[string]any{
						"content_type": "multimodal_text",
						"parts": []any{map[string]any{
							"content_type":  "image_asset_pointer",
							"asset_pointer": "sediment://file_earlier",
						}},
					},
				},
			},
			"prompt": map[string]any{
				"id":     "prompt",
				"parent": "",
				"message": map[string]any{
					"author": map[string]any{"role": "user"},
					"content": map[string]any{
						"content_type": "text",
						"parts":        []any{"Generate an icon."},
					},
				},
			},
		},
	}

	extracted := extractConversationText(payload)
	if extracted.completionState != conversationCompletionTerminal ||
		len(extracted.attachments) != 1 {
		t.Fatalf("extracted = %+v", extracted)
	}
	attachment := extracted.attachments[0]
	if attachment.Source !=
		"https://chatgpt.com/backend-api/files/download/file_current" ||
		attachment.sourceLocator == "" ||
		!strings.Contains(attachment.sourceLocator, "private-value") ||
		attachment.messageID != "answer-current" {
		t.Fatalf("attachment = %+v", attachment)
	}
	encoded, err := json.Marshal(attachment)
	if err != nil {
		t.Fatalf("marshal attachment: %v", err)
	}
	serialized := string(encoded)
	for _, forbidden := range []string{
		"private-value", "sourceLocator", "source_locator", "messageID",
		"message_id", "sandboxLocator", "sandbox_locator", "file_earlier",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("public attachment leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestTerminalAttachmentsKeepDistinctSandboxPathsWithSameName(t *testing.T) {
	first := map[string]any{
		"content_type":  "file",
		"asset_pointer": "sandbox:/mnt/data/first/report.csv",
		"file_name":     "report.csv",
		"mime_type":     "text/csv",
	}
	message := map[string]any{
		"content": map[string]any{
			"content_type": "multimodal_text",
			"parts": []any{
				first,
				map[string]any{
					"content_type":  "file",
					"asset_pointer": "sandbox:/mnt/data/second/report.csv",
					"file_name":     "report.csv",
					"mime_type":     "text/csv",
				},
			},
		},
		"metadata": map[string]any{
			"attachments": []any{first},
		},
	}
	attachments, truncated := conversationAttachments(message)
	if truncated || len(attachments) != 2 {
		t.Fatalf("attachments = %+v, truncated = %v", attachments, truncated)
	}
	locators := map[string]bool{}
	for _, attachment := range attachments {
		if attachment.Source != "sandbox_artifact" ||
			attachment.FileName != "report.csv" {
			t.Fatalf("attachment = %+v", attachment)
		}
		locators[attachment.sourceLocator] = true
		encoded, err := json.Marshal(attachment)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte("/mnt/data/")) {
			t.Fatalf("public attachment leaked private path: %s", encoded)
		}
	}
	for _, locator := range []string{
		"sandbox:/mnt/data/first/report.csv",
		"sandbox:/mnt/data/second/report.csv",
	} {
		if !locators[locator] {
			t.Fatalf("missing private locator %q in %+v", locator, attachments)
		}
	}
}

func TestPrepareAttachmentOutputDirCreatesOwnerOnlyAndRejectsSymlinks(
	t *testing.T,
) {
	root := localAttachmentTestDir(t)
	output := filepath.Join(root, "missing", "export")
	resolved, err := prepareAttachmentOutputDir(output)
	if err != nil {
		t.Fatalf("prepare output: %v", err)
	}
	if resolved != output {
		t.Fatalf("resolved = %q, want %q", resolved, output)
	}
	for current := filepath.Join(root, "missing"); ; {
		info, statErr := os.Lstat(current)
		if statErr != nil {
			t.Fatalf("stat %s: %v", current, statErr)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("mode for %s = %s", current, info.Mode())
		}
		if current == output {
			break
		}
		current = output
	}

	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareAttachmentOutputDir(
		filepath.Join(link, "export"),
	); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink component error = %v", err)
	}
	if _, err := prepareAttachmentOutputDir(realDir); err != nil {
		t.Fatalf("existing real directory: %v", err)
	}
}

func TestExportAttachmentCandidatesIsDeterministicPrivateAndPartial(
	t *testing.T,
) {
	pngBytes := syntheticPNG(t, 3, 2)
	textBytes := []byte("synthetic export\n")
	candidates := []ConversationAttachment{
		{
			Kind:          "image",
			MIMEType:      "image/png",
			Width:         3,
			Height:        2,
			sourceLocator: "sediment://file_private_image",
			messageID:     "answer-current",
		},
		{
			Kind:          "file",
			FileName:      "notes.txt",
			MIMEType:      "text/plain",
			SizeBytes:     int64(len(textBytes)),
			sourceLocator: "sandbox:/mnt/data/notes.txt",
			messageID:     "answer-current",
		},
		{
			Kind:          "file",
			FileName:      "notes.txt",
			MIMEType:      "text/plain",
			SizeBytes:     int64(len(textBytes)),
			sourceLocator: "sandbox:/mnt/data/notes-copy.txt",
			messageID:     "answer-current",
		},
		{
			Kind:      "image",
			MIMEType:  "image/png",
			messageID: "answer-current",
		},
	}
	resolve := attachmentResolverFunc(func(
		_ context.Context,
		candidate ConversationAttachment,
		_ int64,
	) (attachmentPayload, *attachmentItemFailure) {
		switch candidate.sourceLocator {
		case "sediment://file_private_image":
			return attachmentPayload{
				Content:     pngBytes,
				ContentType: "image/png",
			}, nil
		case "sandbox:/mnt/data/notes.txt",
			"sandbox:/mnt/data/notes-copy.txt":
			return attachmentPayload{
				Content:     textBytes,
				ContentType: "text/plain; charset=utf-8",
			}, nil
		default:
			return attachmentPayload{}, &attachmentItemFailure{
				Code: "chatgpt_attachment_source_unsupported",
			}
		}
	})

	firstDir := filepath.Join(localAttachmentTestDir(t), "first")
	first, err := exportAttachmentCandidates(
		context.Background(),
		firstDir,
		candidates,
		false,
		resolve,
		defaultAttachmentBatchLimits(),
	)
	if err != nil {
		t.Fatalf("first export: %v", err)
	}
	if first.Status != attachmentBatchPartial ||
		first.DiscoveredCount != 4 ||
		first.SucceededCount != 3 ||
		first.FailedCount != 1 ||
		len(first.Items) != 4 {
		t.Fatalf("first = %+v", first)
	}
	imageHash := sha256.Sum256(pngBytes)
	wantImageName := "generated-image-01-" +
		hex.EncodeToString(imageHash[:])[:8] + ".png"
	wantNames := []string{
		wantImageName,
		"notes.txt",
		"notes-2.txt",
		"",
	}
	for index, want := range wantNames {
		if first.Items[index].FileName != want {
			t.Fatalf(
				"item %d filename = %q, want %q",
				index,
				first.Items[index].FileName,
				want,
			)
		}
	}
	if first.Items[3].ErrorCode !=
		"chatgpt_attachment_source_unsupported" {
		t.Fatalf("failed item = %+v", first.Items[3])
	}

	manifestBytes, err := os.ReadFile(first.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifestInfo, err := os.Stat(first.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifestInfo.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode = %o", manifestInfo.Mode().Perm())
	}
	for _, forbidden := range []string{
		"file_private_image", "/mnt/data/", "answer-current",
		"source_locator", "message_id", "conversation_id",
	} {
		if bytes.Contains(manifestBytes, []byte(forbidden)) {
			t.Fatalf("manifest leaked %q: %s", forbidden, manifestBytes)
		}
	}
	if bytes.Contains(manifestBytes, []byte(firstDir)) {
		t.Fatalf("manifest contains absolute output path: %s", manifestBytes)
	}

	secondDir := filepath.Join(localAttachmentTestDir(t), "second")
	second, err := exportAttachmentCandidates(
		context.Background(),
		secondDir,
		candidates,
		false,
		resolve,
		defaultAttachmentBatchLimits(),
	)
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	secondManifest, err := os.ReadFile(second.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(manifestBytes, secondManifest) {
		t.Fatalf(
			"manifest is not deterministic\nfirst: %s\nsecond: %s",
			manifestBytes,
			secondManifest,
		)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("file_private_image")) ||
		bytes.Contains(encoded, []byte("answer-current")) {
		t.Fatalf("result leaked private locator: %s", encoded)
	}
}

func TestExportAttachmentCandidatesDoesNotOverwriteAndEnforcesBounds(
	t *testing.T,
) {
	root := localAttachmentTestDir(t)
	output := filepath.Join(root, "export")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(output, "report.txt")
	if err := os.WriteFile(existing, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates := []ConversationAttachment{
		{
			Kind:          "file",
			FileName:      "report.txt",
			MIMEType:      "text/plain",
			sourceLocator: "file-service://one",
		},
		{
			Kind:          "file",
			FileName:      "second.txt",
			MIMEType:      "text/plain",
			sourceLocator: "file-service://two",
		},
	}
	resolve := attachmentResolverFunc(func(
		_ context.Context,
		_ ConversationAttachment,
		_ int64,
	) (attachmentPayload, *attachmentItemFailure) {
		return attachmentPayload{
			Content:     []byte("12345"),
			ContentType: "text/plain",
		}, nil
	})
	limits := attachmentBatchLimits{
		MaxItems:      2,
		MaxItemBytes:  5,
		MaxTotalBytes: 5,
	}
	data, err := exportAttachmentCandidates(
		context.Background(),
		output,
		candidates,
		false,
		resolve,
		limits,
	)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if data.Status != attachmentBatchPartial ||
		data.SucceededCount != 0 ||
		data.FailedCount != 2 ||
		data.Items[0].ErrorCode != "chatgpt_attachment_destination_exists" ||
		data.Items[1].ErrorCode != "chatgpt_attachment_total_bytes_exceeded" {
		t.Fatalf("bounded result = %+v", data)
	}
	content, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep me" {
		t.Fatalf("existing destination changed: %q", content)
	}

	manifestPath := filepath.Join(output, attachmentManifestFileName)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exportAttachmentCandidates(
		context.Background(),
		output,
		candidates,
		false,
		resolve,
		limits,
	); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("existing manifest error = %v", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("existing manifest was modified")
	}
}

func TestExportAttachmentCandidatesWritesCompleteEmptyManifest(t *testing.T) {
	output := filepath.Join(localAttachmentTestDir(t), "empty")
	data, err := exportAttachmentCandidates(
		context.Background(),
		output,
		nil,
		false,
		attachmentResolverFunc(func(
			context.Context,
			ConversationAttachment,
			int64,
		) (attachmentPayload, *attachmentItemFailure) {
			t.Fatal("resolver called for an empty batch")
			return attachmentPayload{}, nil
		}),
		defaultAttachmentBatchLimits(),
	)
	if err != nil {
		t.Fatalf("empty export: %v", err)
	}
	if data.Status != attachmentBatchComplete ||
		data.DiscoveredCount != 0 ||
		data.SucceededCount != 0 ||
		data.FailedCount != 0 ||
		len(data.Items) != 0 {
		t.Fatalf("empty data = %+v", data)
	}
	manifest, err := os.ReadFile(data.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(manifest, []byte(`"status": "complete"`)) ||
		!bytes.Contains(manifest, []byte(`"items": []`)) {
		t.Fatalf("empty manifest = %s", manifest)
	}
}

func TestVerifyAttachmentPayloadRejectsMetadataMismatch(t *testing.T) {
	pngBytes := syntheticPNG(t, 3, 2)
	base := ConversationAttachment{
		Kind:      "image",
		MIMEType:  "image/png",
		SizeBytes: int64(len(pngBytes)),
		Width:     3,
		Height:    2,
	}
	tests := []struct {
		name      string
		candidate ConversationAttachment
		payload   attachmentPayload
		wantCode  string
	}{
		{
			name:      "size",
			candidate: func() ConversationAttachment { value := base; value.SizeBytes++; return value }(),
			payload:   attachmentPayload{Content: pngBytes, ContentType: "image/png"},
			wantCode:  "chatgpt_attachment_size_mismatch",
		},
		{
			name:      "mime",
			candidate: func() ConversationAttachment { value := base; value.SizeBytes = 0; return value }(),
			payload:   attachmentPayload{Content: []byte("not an image"), ContentType: "image/png"},
			wantCode:  "chatgpt_attachment_mime_mismatch",
		},
		{
			name:      "malformed response mime",
			candidate: base,
			payload: attachmentPayload{
				Content: pngBytes, ContentType: "image/png; broken",
			},
			wantCode: "chatgpt_attachment_mime_mismatch",
		},
		{
			name:      "dimensions",
			candidate: func() ConversationAttachment { value := base; value.Width = 4; return value }(),
			payload:   attachmentPayload{Content: pngBytes, ContentType: "image/png"},
			wantCode:  "chatgpt_attachment_dimension_mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, failure := verifyAttachmentPayload(test.candidate, test.payload)
			if failure == nil || failure.Code != test.wantCode {
				t.Fatalf("failure = %+v, want %s", failure, test.wantCode)
			}
		})
	}
}

func TestVerifyAttachmentPayloadAcceptsGenericImageResponseType(t *testing.T) {
	content := syntheticPNG(t, 3, 2)
	verified, failure := verifyAttachmentPayload(
		ConversationAttachment{
			Kind:      "image",
			MIMEType:  "image/png",
			SizeBytes: int64(len(content)),
			Width:     3,
			Height:    2,
		},
		attachmentPayload{
			Content: content, ContentType: "application/octet-stream",
		},
	)
	if failure != nil ||
		verified.MIMEType != "image/png" ||
		verified.Width != 3 ||
		verified.Height != 2 {
		t.Fatalf("verified = %+v, failure = %+v", verified, failure)
	}
}

func TestVerifyAttachmentPayloadAcceptsWebPOriginals(t *testing.T) {
	fixtures := map[string]string{
		"lossless": "UklGRkYAAABXRUJQVlA4TDkAAAAvA8AAADegsG0bFI4ZFLZtg+LCeGrbtmFjbil75sx/3F+icYhocND/y1kgCATNBTPDHjMc0f/IXgAA",
		"lossy":    "UklGRjAAAABXRUJQVlA4ICQAAACwAQCdASoEAAQAAgA0JZwCdAEO/gLsAP79oz7y7jRghFXYAAA=",
		"extended": "UklGRoIAAABXRUJQVlA4WAoAAAAIAAAAAwAAAwAAVlA4ICQAAACwAQCdASoEAAQAAgA0JZwCdAEO/gLsAP79oz7y7jRghFXYAABFWElGOAAAAFJJRkYwAAAAV0VCUFZQOCAkAAAAsAEAnQEqBAAEAAIANCWcAnQBDv4C7AD+/aM+8u40YIRV2AAA",
	}
	for name, encoded := range fixtures {
		t.Run(name, func(t *testing.T) {
			content, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatalf("decode WebP fixture: %v", err)
			}
			if detected := normalizedAttachmentMIME(
				http.DetectContentType(content),
			); detected != "image/webp" {
				t.Fatalf("WebP fixture detected MIME = %q", detected)
			}
			verified, failure := verifyAttachmentPayload(
				ConversationAttachment{
					Kind:      "image",
					MIMEType:  "image/webp",
					SizeBytes: int64(len(content)),
					Width:     4,
					Height:    4,
				},
				attachmentPayload{
					Content: content, ContentType: "image/webp",
				},
			)
			if failure != nil ||
				verified.MIMEType != "image/webp" ||
				verified.Width != 4 ||
				verified.Height != 4 {
				t.Fatalf("verified = %+v, failure = %+v", verified, failure)
			}
		})
	}
}

func TestVerifyAttachmentPayloadRejectsMalformedWebP(t *testing.T) {
	valid, err := base64.StdEncoding.DecodeString(
		"UklGRkYAAABXRUJQVlA4TDkAAAAvA8AAADegsG0bFI4ZFLZtg+LCeGrbtmFjbil75sx/3F+icYhocND/y1kgCATNBTPDHjMc0f/IXgAA",
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"truncated":           append([]byte{}, valid[:len(valid)-1]...),
		"invalid VP8L header": append([]byte{}, valid...),
	}
	tests["invalid VP8L header"][20] = 0
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			_, failure := verifyAttachmentPayload(
				ConversationAttachment{Kind: "image", MIMEType: "image/webp"},
				attachmentPayload{Content: content, ContentType: "image/webp"},
			)
			if failure == nil || failure.Code != "chatgpt_attachment_image_invalid" {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
}

func TestVerifyAttachmentPayloadAcceptsCoarseSniffedOriginalFiles(t *testing.T) {
	jsonBytes := []byte(`{"ok":true,"items":[1,2,3]}`)
	docxBytes := syntheticOfficeZip(t, "word/document.xml")
	tests := []struct {
		name     string
		fileName string
		mimeType string
		content  []byte
		detected string
	}{
		{
			name: "csv", fileName: "report.csv",
			mimeType: "text/csv",
			content:  []byte("kind,value\nsynthetic,ordinary-file\n"),
			detected: "text/plain",
		},
		{
			name: "json", fileName: "report.json",
			mimeType: "application/json", content: jsonBytes,
			detected: "text/plain",
		},
		{
			name: "docx", fileName: "report.docx",
			mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			content:  docxBytes, detected: "application/zip",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if detected := normalizedAttachmentMIME(
				http.DetectContentType(test.content),
			); detected != test.detected {
				t.Fatalf("test fixture detected MIME = %q, want %q", detected, test.detected)
			}
			verified, failure := verifyAttachmentPayload(
				ConversationAttachment{
					Kind: "file", FileName: test.fileName,
					MIMEType:  test.mimeType,
					SizeBytes: int64(len(test.content)),
				},
				attachmentPayload{
					Content: test.content, ContentType: test.detected,
				},
			)
			if failure != nil || verified.MIMEType != test.mimeType {
				t.Fatalf("verified = %+v, failure = %+v", verified, failure)
			}
		})
	}
}

func TestVerifyAttachmentPayloadRejectsInvalidCoarseStructuredFile(t *testing.T) {
	tests := []struct {
		name        string
		fileName    string
		mimeType    string
		contentType string
		content     []byte
	}{
		{
			name: "invalid json", fileName: "broken.json",
			mimeType: "application/json", contentType: "text/plain",
			content: []byte(`{"not-valid-json"`),
		},
		{
			name: "wrong office package", fileName: "broken.docx",
			mimeType:    "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			contentType: "application/zip",
			content:     syntheticOfficeZip(t, "xl/workbook.xml"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, failure := verifyAttachmentPayload(
				ConversationAttachment{
					Kind: "file", FileName: test.fileName,
					MIMEType: test.mimeType,
				},
				attachmentPayload{
					Content: test.content, ContentType: test.contentType,
				},
			)
			if failure == nil ||
				failure.Code != "chatgpt_attachment_mime_mismatch" {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
}

func TestAllocateAttachmentFileNameAvoidsDerivedNameCollisions(t *testing.T) {
	used := map[string]int{}
	got := []string{
		allocateAttachmentFileName("notes.txt", used),
		allocateAttachmentFileName("notes.txt", used),
		allocateAttachmentFileName("notes-2.txt", used),
		allocateAttachmentFileName("NOTES.TXT", used),
	}
	want := []string{
		"notes.txt", "notes-2.txt", "notes-2-2.txt", "NOTES-3.TXT",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("allocated names = %v, want %v", got, want)
	}
}

func TestPreferredAttachmentExtensionKeepsVerifiedOfficeType(t *testing.T) {
	tests := map[string]string{
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   ".docx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
		"application/vnd.ms-word.document.macroenabled.12":                          ".docm",
		"application/vnd.ms-excel.sheet.macroenabled.12":                            ".xlsm",
		"application/vnd.ms-powerpoint.presentation.macroenabled.12":                ".pptm",
	}
	for mimeType, want := range tests {
		if got := preferredAttachmentExtension(mimeType); got != want {
			t.Errorf("preferredAttachmentExtension(%q) = %q, want %q", mimeType, got, want)
		}
	}
}

func TestAttachmentOutputFileNamePrefersVerifiedProviderName(t *testing.T) {
	verified := verifiedAttachmentPayload{
		MIMEType: "text/plain",
		FileName: "provider-report.txt",
		SHA256:   strings.Repeat("a", 64),
	}
	if got := attachmentOutputFileName(
		ConversationAttachment{Kind: "file"},
		verified,
		1,
	); got != "provider-report.txt" {
		t.Fatalf("provider filename = %q", got)
	}
	verified.FileName = "../outside.txt"
	if got := attachmentOutputFileName(
		ConversationAttachment{Kind: "file"},
		verified,
		1,
	); got != "attachment-01-aaaaaaaa.txt" {
		t.Fatalf("unsafe provider filename fallback = %q", got)
	}
	verified.FileName = strings.Repeat("🙂", 100) + ".txt"
	if got := attachmentOutputFileName(
		ConversationAttachment{Kind: "file"},
		verified,
		1,
	); got != "attachment-01-aaaaaaaa.txt" {
		t.Fatalf("oversized provider filename fallback = %q", got)
	}
}

func TestAttachmentSourceRequestAdmitsOnlyObservedExactRoutes(t *testing.T) {
	tests := []struct {
		name      string
		candidate ConversationAttachment
		wantKind  attachmentRequestKind
		wantPath  string
		wantCode  string
	}{
		{
			name: "generated sediment pointer",
			candidate: ConversationAttachment{
				Kind:          "image",
				sourceLocator: "sediment://file_synthetic_image",
			},
			wantKind: attachmentRequestMetadata,
			wantPath: "/backend-api/files/download/file_synthetic_image" +
				"?conversation_id=conversation-safe&inline=false",
		},
		{
			name: "exact signed content path",
			candidate: ConversationAttachment{
				Kind: "image",
				sourceLocator: "https://chatgpt.com/backend-api/estuary/content" +
					"?cd=attachment&cid=synthetic&fn=generated.png&id=file_synthetic&sig=private&ts=1&v=1",
			},
			wantKind: attachmentRequestContent,
			wantPath: "/backend-api/estuary/content" +
				"?cd=attachment&cid=synthetic&fn=generated.png&id=file_synthetic&sig=private&ts=1&v=1",
		},
		{
			name: "named sandbox artifact",
			candidate: ConversationAttachment{
				Kind:          "file",
				FileName:      "report.csv",
				sourceLocator: "sandbox:/mnt/data/report.csv",
				messageID:     "answer-safe",
			},
			wantKind: attachmentRequestSandbox,
		},
		{
			name: "exact conversation-bound metadata path",
			candidate: ConversationAttachment{
				Kind: "file",
				sourceLocator: "/backend-api/files/download/file_synthetic" +
					"?conversation_id=conversation-safe&inline=false",
			},
			wantKind: attachmentRequestMetadata,
			wantPath: "/backend-api/files/download/file_synthetic" +
				"?conversation_id=conversation-safe&inline=false",
		},
		{
			name: "unbound metadata path",
			candidate: ConversationAttachment{
				Kind:          "file",
				sourceLocator: "/backend-api/files/download/file_synthetic",
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
		{
			name: "noncanonical sediment host and path",
			candidate: ConversationAttachment{
				Kind:          "image",
				sourceLocator: "sediment://file_synthetic/extra",
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
		{
			name: "unicode sediment identifier",
			candidate: ConversationAttachment{
				Kind:          "image",
				sourceLocator: "sediment://synthetic_føø",
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
		{
			name: "overlength structured file identifier",
			candidate: ConversationAttachment{
				Kind:   "file",
				FileID: strings.Repeat("a", 257),
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
		{
			name: "outside origin",
			candidate: ConversationAttachment{
				Kind:          "image",
				sourceLocator: "https://example.invalid/backend-api/estuary/content?sig=private",
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
		{
			name: "scheme relative origin",
			candidate: ConversationAttachment{
				Kind: "image",
				sourceLocator: "//chatgpt.com/backend-api/estuary/content" +
					"?cid=synthetic&sig=private",
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
		{
			name: "legacy unobserved route",
			candidate: ConversationAttachment{
				Kind:          "image",
				sourceLocator: "https://chatgpt.com/backend-api/files/file_synthetic/download",
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
		{
			name: "encoded alias of exact content path",
			candidate: ConversationAttachment{
				Kind: "image",
				sourceLocator: "https://chatgpt.com/backend-api/estuary/%63ontent" +
					"?cid=synthetic&sig=private",
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
		{
			name: "unknown signed parameter",
			candidate: ConversationAttachment{
				Kind: "image",
				sourceLocator: "/backend-api/files/download/file_synthetic" +
					"?conversation_id=conversation-safe&unknown=private",
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
		{
			name: "unicode download intent",
			candidate: ConversationAttachment{
				Kind: "file",
				sourceLocator: "/backend-api/files/download/file_synthetic" +
					"?conversation_id=conversation-safe&download_intent=føø",
			},
			wantCode: "chatgpt_attachment_source_unsupported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, failure := attachmentRequestForCandidate(
				"conversation-safe",
				test.candidate,
			)
			if test.wantCode != "" {
				if failure == nil || failure.Code != test.wantCode {
					t.Fatalf("failure = %+v, want %s", failure, test.wantCode)
				}
				return
			}
			if failure != nil {
				t.Fatalf("request failure: %+v", failure)
			}
			if request.Kind != test.wantKind {
				t.Fatalf("kind = %q, want %q", request.Kind, test.wantKind)
			}
			if test.wantPath != "" && request.Endpoint != Origin+test.wantPath {
				t.Fatalf("endpoint = %q, want %q", request.Endpoint, Origin+test.wantPath)
			}
		})
	}
}

func TestDirectAttachmentResolverGetsMetadataThenOriginalBytes(t *testing.T) {
	pngBytes := syntheticPNG(t, 4, 3)
	requests := []string{}
	client := &http.Client{Transport: roundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		requests = append(requests, request.URL.String())
		switch request.URL.Path {
		case "/backend-api/files/download/file_synthetic_image":
			if request.URL.Query().Get("conversation_id") != "conversation-safe" ||
				request.URL.Query().Get("inline") != "false" {
				t.Fatalf("metadata query = %s", request.URL.RawQuery)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(`{
					"status":"success",
					"file_name":"provider-original.png",
					"file_size_bytes":` + fmt.Sprint(len(pngBytes)) + `,
					"mime_type":"image/png",
					"download_url":"https://chatgpt.com/backend-api/estuary/content?cid=synthetic&id=file_synthetic_image&sig=private&ts=1&v=1"
				}`)),
			}, nil
		case artifactContentRoute:
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type":   []string{"image/png"},
					"Content-Length": []string{fmt.Sprint(len(pngBytes))},
				},
				Body: io.NopCloser(bytes.NewReader(pngBytes)),
			}, nil
		default:
			t.Fatalf("unexpected request: %s", request.URL.String())
			return nil, nil
		}
	})}
	resolver := directAttachmentResolver{
		config: ReadConfig{HTTPClient: client},
		template: RequestTemplate{
			Headers:          map[string]string{"authorization": "private-test"},
			CookieHeader:     "private-cookie",
			BrowserUserAgent: "synthetic-agent",
		},
		conversationID: "conversation-safe",
	}
	payload, failure := resolver.Resolve(
		context.Background(),
		ConversationAttachment{
			Kind:          "image",
			MIMEType:      "image/png",
			Width:         4,
			Height:        3,
			sourceLocator: "sediment://file_synthetic_image",
			messageID:     "answer-safe",
		},
		maxArtifactBytes,
	)
	if failure != nil {
		t.Fatalf("resolve: %+v", failure)
	}
	if !bytes.Equal(payload.Content, pngBytes) ||
		payload.ContentType != "image/png" ||
		payload.FileName != "provider-original.png" ||
		len(requests) != 2 {
		t.Fatalf("payload = %+v, requests = %v", payload, requests)
	}
}

func TestDirectAttachmentResolverRejectsRedirectAndMetadataMismatch(
	t *testing.T,
) {
	client := &http.Client{Transport: roundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{
				"Location": []string{"https://example.invalid/private"},
			},
			Body: io.NopCloser(strings.NewReader("redirect")),
		}, nil
	})}
	resolver := directAttachmentResolver{
		config:         ReadConfig{HTTPClient: client},
		template:       RequestTemplate{},
		conversationID: "conversation-safe",
	}
	_, failure := resolver.Resolve(
		context.Background(),
		ConversationAttachment{
			Kind: "image",
			sourceLocator: "https://chatgpt.com/backend-api/estuary/content" +
				"?cid=synthetic&id=file_synthetic&sig=private&ts=1&v=1",
		},
		maxArtifactBytes,
	)
	if failure == nil || failure.Code != "chatgpt_attachment_redirect_rejected" {
		t.Fatalf("redirect failure = %+v", failure)
	}
}

func TestDownloadAttachmentsDirectPathPublishesCompleteBatchWithoutBrowser(
	t *testing.T,
) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	pngBytes := syntheticPNG(t, 4, 3)
	detail := fmt.Sprintf(`{
		"conversation_id":"conversation-safe",
		"current_node":"answer-safe",
		"mapping":{
			"answer-safe":{
				"id":"answer-safe",
				"parent":"prompt-safe",
				"message":{
					"author":{"role":"assistant"},
					"status":"finished_successfully",
					"end_turn":true,
					"content":{"content_type":"multimodal_text","parts":[{
						"content_type":"image_asset_pointer",
						"asset_pointer":"sediment://file_synthetic_image",
						"mime_type":"image/png",
						"size_bytes":%d,
						"width":4,
						"height":3
					}]}
				}
			},
			"prompt-safe":{
				"id":"prompt-safe",
				"parent":"",
				"message":{"author":{"role":"user"},"content":{"content_type":"text","parts":["Synthetic."]}}
			}
		}
	}`, len(pngBytes))
	requests := []string{}
	client := &http.Client{Transport: roundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		requests = append(requests, request.URL.Path)
		switch request.URL.Path {
		case "/backend-api/conversation/conversation-safe":
			return jsonHTTPResponse(http.StatusOK, detail), nil
		case "/backend-api/files/download/file_synthetic_image":
			return jsonHTTPResponse(http.StatusOK, fmt.Sprintf(`{
				"status":"success",
				"file_size_bytes":%d,
				"mime_type":"image/png",
				"download_url":"https://chatgpt.com/backend-api/estuary/content?cid=synthetic&id=file_synthetic_image&sig=private&ts=1&v=1"
			}`, len(pngBytes))), nil
		case artifactContentRoute:
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type":   []string{"image/png"},
					"Content-Length": []string{fmt.Sprint(len(pngBytes))},
				},
				Body: io.NopCloser(bytes.NewReader(pngBytes)),
			}, nil
		default:
			t.Fatalf("unexpected request: %s", request.URL.String())
			return nil, nil
		}
	})}
	browserCalled := false
	output := filepath.Join(localAttachmentTestDir(t), "export")
	result := DownloadAttachments(
		context.Background(),
		AttachmentBatchConfig{
			ReadConfig: ReadConfig{
				Store:       newReadTestStore(t, now),
				HTTPClient:  client,
				BuildCommit: "synthetic-build",
				Now:         func() time.Time { return now },
				BrowserFallback: func(context.Context) (*BrowserConfig, error) {
					browserCalled = true
					return nil, fmt.Errorf("must not initialize browser")
				},
			},
			OutputDir: output,
		},
		"conversation-safe",
	)
	data, ok := result.Data.(AttachmentBatchData)
	if !ok ||
		!result.OK ||
		result.Operation != webagent.OperationAttachmentsDownload ||
		result.State != webagent.StateTerminal ||
		result.Evidence.BrowserMode != "none" ||
		result.Evidence.Target != nil ||
		result.Cleanup.State != webagent.CleanupNotRequired ||
		result.Conversation != nil ||
		data.Status != attachmentBatchComplete ||
		data.SucceededCount != 1 ||
		data.FailedCount != 0 ||
		browserCalled ||
		len(requests) != 3 {
		t.Fatalf(
			"browser_called=%v requests=%v result=%+v data=%+v",
			browserCalled,
			requests,
			result,
			data,
		)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"conversation-safe", "file_synthetic_image", "private",
		"authorization", "cookie", "target_id", "session_id",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestDownloadAttachmentsRejectsInvalidInputBeforeReadOrWrite(t *testing.T) {
	root := localAttachmentTestDir(t)
	output := filepath.Join(root, "must-not-exist")
	result := DownloadAttachments(
		context.Background(),
		AttachmentBatchConfig{OutputDir: output},
		"invalid/id",
	)
	if result.OK ||
		result.Error == nil ||
		result.Error.Code != "chatgpt_invalid_conversation_id" ||
		result.Stage != webagent.StagePlanned ||
		result.Cleanup.State != webagent.CleanupNotRequired {
		t.Fatalf("invalid result = %+v", result)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("invalid input created output directory: %v", err)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
}

func TestDownloadAttachmentsUsesOneExactClosedBrowserFallback(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 30, 0, 0, time.UTC)
	pngBytes := syntheticPNG(t, 4, 3)
	detail := fmt.Sprintf(`{
		"conversation_id":"conversation-safe",
		"current_node":"answer-safe",
		"mapping":{
			"answer-safe":{
				"id":"answer-safe",
				"parent":"prompt-safe",
				"message":{
					"author":{"role":"assistant"},
					"status":"finished_successfully",
					"end_turn":true,
					"content":{"content_type":"multimodal_text","parts":[{
						"content_type":"image_asset_pointer",
						"asset_pointer":"sediment://file_synthetic_image",
						"mime_type":"image/png",
						"size_bytes":%d,
						"width":4,
						"height":3
					}]}
				}
			},
			"prompt-safe":{
				"id":"prompt-safe",
				"parent":"",
				"message":{"author":{"role":"user"},"content":{"content_type":"text","parts":["Synthetic."]}}
			}
		}
	}`, len(pngBytes))
	metadata := fmt.Sprintf(`{
		"status":"success",
		"file_size_bytes":%d,
		"mime_type":"image/png",
		"download_url":"https://chatgpt.com/backend-api/estuary/content?cid=synthetic&id=file_synthetic_image&sig=private&ts=1&v=1"
	}`, len(pngBytes))
	detailFetches := 0
	authenticated := newAuthenticatedReadBrowser(func(
		expression string,
		_ *testsupport.Browser,
	) (any, error) {
		switch {
		case strings.Contains(expression, "signed_in:"):
			return map[string]any{
				"signed_in": true, "signed_out": false,
			}, nil
		case strings.Contains(expression, "body_base64"):
			return map[string]any{
				"ok":           true,
				"status_code":  http.StatusOK,
				"body_base64":  base64.StdEncoding.EncodeToString(pngBytes),
				"body_bytes":   len(pngBytes),
				"content_type": "image/png",
			}, nil
		case strings.Contains(
			expression,
			"/backend-api/files/download/file_synthetic_image",
		):
			return map[string]any{
				"ok":          true,
				"status_code": http.StatusOK,
				"body":        metadata,
				"body_bytes":  len(metadata),
			}, nil
		case strings.Contains(
			expression,
			"/backend-api/conversation/conversation-safe",
		):
			detailFetches++
			if detailFetches == 1 {
				return map[string]any{
					"ok":          false,
					"status_code": http.StatusUnauthorized,
				}, nil
			}
			return map[string]any{
				"ok":          true,
				"status_code": http.StatusOK,
				"body":        detail,
				"body_bytes":  len(detail),
			}, nil
		default:
			return map[string]any{}, nil
		}
	})
	client := &refreshingAttachmentBrowser{
		authenticatedReadBrowser: authenticated,
	}
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	output := filepath.Join(localAttachmentTestDir(t), "export")
	result := DownloadAttachments(
		context.Background(),
		AttachmentBatchConfig{
			ReadConfig: ReadConfig{
				Store:      newReadTestStore(t, now),
				HTTPClient: fixedHTTPClient(http.StatusUnauthorized, ""),
				BrowserConfig: &BrowserConfig{
					Client: client, Engine: engine, Journal: journal,
				},
				Now: func() time.Time { return now },
			},
			OutputDir: output,
		},
		"conversation-safe",
	)
	data, ok := result.Data.(AttachmentBatchData)
	counts, _, _, _, _, _, targets := client.Snapshot()
	if !ok ||
		!result.OK ||
		result.State != webagent.StateTerminal ||
		data.Status != attachmentBatchComplete ||
		data.SucceededCount != 1 ||
		result.Evidence.BrowserMode != "headed" ||
		result.Evidence.Target != nil ||
		!result.Cleanup.Required ||
		result.Cleanup.State != webagent.CleanupClosed ||
		!result.Cleanup.IdentityOmitted ||
		result.Cleanup.TargetID != "" ||
		!result.Cleanup.TargetClosed ||
		counts["Target.createTarget"] != 1 ||
		counts["Target.closeTarget"] != 1 ||
		detailFetches != 2 ||
		client.exactNavigations != 1 ||
		len(targets) != 1 ||
		targets["user-page"].TargetID != "user-page" {
		t.Fatalf(
			"counts=%v targets=%v detail_fetches=%d exact_navigations=%d result=%+v data=%+v",
			counts,
			targets,
			detailFetches,
			client.exactNavigations,
			result,
			data,
		)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"conversation-safe", "file_synthetic_image", "private",
		"owned-1", "session-owned-1", "target_id", "session_id",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("fallback result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestSanitizeAttachmentTargetEvidencePreservesCleanupFailure(t *testing.T) {
	result := sanitizeAttachmentTargetEvidence(webagent.Result{
		Evidence: webagent.Evidence{
			Target: &webagent.TargetEvidence{
				TargetID: "private-target", Owned: true, Created: true,
			},
		},
		Cleanup: webagent.CleanupEvidence{
			Required:          true,
			State:             webagent.CleanupFailed,
			TargetID:          "private-target",
			CloseAttemptCount: 2,
			CloseSent:         true,
			FailurePhase:      "poll",
		},
	})

	if result.Evidence.Target != nil ||
		!result.Cleanup.Required ||
		result.Cleanup.State != webagent.CleanupFailed ||
		!result.Cleanup.IdentityOmitted ||
		result.Cleanup.TargetID != "" ||
		result.Cleanup.CloseAttemptCount != 2 ||
		!result.Cleanup.CloseSent ||
		result.Cleanup.FailurePhase != "poll" {
		t.Fatalf("sanitized cleanup failure = %+v", result)
	}
	if err := result.Cleanup.Validate(); err != nil {
		t.Fatalf("sanitized cleanup validation: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-target", "target_id"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("sanitized cleanup leaked %q: %s", forbidden, encoded)
		}
	}
}

type refreshingAttachmentBrowser struct {
	*authenticatedReadBrowser
	exactNavigations int
}

func (browser *refreshingAttachmentBrowser) CallSession(
	ctx context.Context,
	sessionID string,
	method string,
	params any,
	result any,
) error {
	err := browser.authenticatedReadBrowser.CallSession(
		ctx,
		sessionID,
		method,
		params,
		result,
	)
	if err != nil || method != "Page.navigate" {
		return err
	}
	encoded, marshalErr := json.Marshal(params)
	if marshalErr != nil {
		return marshalErr
	}
	var navigation struct {
		URL string `json:"url"`
	}
	if unmarshalErr := json.Unmarshal(encoded, &navigation); unmarshalErr != nil {
		return unmarshalErr
	}
	if navigation.URL != Origin+"/c/conversation-safe" {
		return nil
	}
	browser.exactNavigations++
	browser.events = append(browser.events,
		cdp.Event{
			SessionID: sessionID,
			Method:    "Network.requestWillBeSent",
			Params: json.RawMessage(`{
				"requestId":"attachment-refresh",
				"request":{
					"url":"https://chatgpt.com/backend-api/conversation/conversation-safe",
					"method":"GET",
					"headers":{
						"authorization":"Bearer refreshed-test-only",
						"cookie":"__Secure-next-auth.session-token=test-only"
					}
				}
			}`),
		},
		cdp.Event{
			SessionID: sessionID,
			Method:    "Network.responseReceived",
			Params: json.RawMessage(`{
				"requestId":"attachment-refresh",
				"response":{
					"url":"https://chatgpt.com/backend-api/conversation/conversation-safe",
					"status":200
				}
			}`),
		},
	)
	return nil
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func localAttachmentTestDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(".", ".attachments-test-*")
	if err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(absolute); err != nil {
			t.Errorf("remove test directory: %v", err)
		}
	})
	return absolute
}

func syntheticPNG(t *testing.T, width int, height int) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value.Set(x, y, color.RGBA{B: 200, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, value); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buffer.Bytes()
}

func syntheticOfficeZip(t *testing.T, documentPath string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		documentPath:          `<document/>`,
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
