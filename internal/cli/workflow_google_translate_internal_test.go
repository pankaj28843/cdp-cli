package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

type googleTranslateLifecycleFakeClient struct {
	mu            sync.Mutex
	targets       map[string]cdp.TargetInfo
	events        []string
	closeErr      map[string]error
	closeDelay    map[string]int
	pendingClose  map[string]int
	targetListErr error
}

func (f *googleTranslateLifecycleFakeClient) Call(_ context.Context, method string, params any, result any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch method {
	case "Target.attachToTarget":
		var input struct {
			TargetID string `json:"targetId"`
		}
		if err := marshalUnmarshalGoogleTranslateLifecycle(params, &input); err != nil {
			return err
		}
		return marshalUnmarshalGoogleTranslateLifecycle(map[string]any{"sessionId": "session-" + input.TargetID}, result)
	case "Target.detachFromTarget":
		var input struct {
			SessionID string `json:"sessionId"`
		}
		if err := marshalUnmarshalGoogleTranslateLifecycle(params, &input); err != nil {
			return err
		}
		f.events = append(f.events, "detach:"+input.SessionID)
		return nil
	case "Target.closeTarget":
		var input struct {
			TargetID string `json:"targetId"`
		}
		if err := marshalUnmarshalGoogleTranslateLifecycle(params, &input); err != nil {
			return err
		}
		f.events = append(f.events, "close:"+input.TargetID)
		if err := f.closeErr[input.TargetID]; err != nil {
			return err
		}
		if delay := f.closeDelay[input.TargetID]; delay > 0 {
			if f.pendingClose == nil {
				f.pendingClose = map[string]int{}
			}
			f.pendingClose[input.TargetID] = delay
			return marshalUnmarshalGoogleTranslateLifecycle(map[string]any{"success": true}, result)
		}
		delete(f.targets, input.TargetID)
		return marshalUnmarshalGoogleTranslateLifecycle(map[string]any{"success": true}, result)
	case "Target.getTargets":
		if f.targetListErr != nil {
			return f.targetListErr
		}
		for targetID, remaining := range f.pendingClose {
			if remaining <= 1 {
				delete(f.targets, targetID)
				delete(f.pendingClose, targetID)
				continue
			}
			f.pendingClose[targetID] = remaining - 1
		}
		rows := make([]cdp.TargetInfo, 0, len(f.targets))
		for _, target := range f.targets {
			rows = append(rows, target)
		}
		return marshalUnmarshalGoogleTranslateLifecycle(map[string]any{"targetInfos": rows}, result)
	default:
		return nil
	}
}

func (f *googleTranslateLifecycleFakeClient) CallSession(_ context.Context, _ string, method string, _ any, _ any) error {
	return errors.New("unexpected session method " + method)
}

func (f *googleTranslateLifecycleFakeClient) eventSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func marshalUnmarshalGoogleTranslateLifecycle(input, output any) error {
	if output == nil {
		return nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, output)
}

func TestGoogleTranslateLifecycleTracksChildBeforeSessionCompletion(t *testing.T) {
	fake := &googleTranslateLifecycleFakeClient{
		targets: map[string]cdp.TargetInfo{
			"baseline": {TargetID: "baseline", Type: "page", URL: "https://example.test/keep"},
			"main":     {TargetID: "main", Type: "page", URL: "https://translate.google.com/"},
			"child":    {TargetID: "child", Type: "page", URL: "https://example.translate.goog/"},
		},
		closeErr:   map[string]error{},
		closeDelay: map[string]int{"main": 1, "child": 2},
	}
	lifecycle := newGoogleTranslateTargetLifecycle(fake, "headed")
	lifecycle.addTarget(fake.targets["main"])
	lifecycle.addTarget(fake.targets["child"])
	cleanup := lifecycle.close()
	if !cleanup.Closed || len(cleanup.Reports) != 2 {
		t.Fatalf("cleanup = %+v, want both owned targets settled", cleanup)
	}
	for _, report := range cleanup.Reports {
		if !report.Closed || !report.TargetGone || report.AttemptCount != 1 {
			t.Fatalf("cleanup report = %+v, want one bounded delayed target-gone attempt", report)
		}
	}
	if _, ok := fake.targets["baseline"]; !ok {
		t.Fatal("cleanup closed the baseline page")
	}
	if len(fake.targets) != 1 {
		t.Fatalf("remaining targets = %+v, want only baseline", fake.targets)
	}
}

func TestGoogleTranslateLifecycleDiscoversOnlyNewTranslatedWebsiteTargets(t *testing.T) {
	fake := &googleTranslateLifecycleFakeClient{
		targets: map[string]cdp.TargetInfo{
			"baseline":           {TargetID: "baseline", Type: "page", URL: "https://example.test/keep"},
			"existing-translate": {TargetID: "existing-translate", Type: "page", URL: "https://old.translate.goog/"},
			"main":               {TargetID: "main", Type: "page", URL: "https://translate.google.com/"},
			"child":              {TargetID: "child", Type: "page", URL: "https://new.translate.goog/"},
			"unrelated":          {TargetID: "unrelated", Type: "page", URL: "https://example.test/new"},
		},
		closeErr: map[string]error{},
	}
	lifecycle := newGoogleTranslateTargetLifecycle(fake, "headed")
	lifecycle.setWebsiteBaseline([]cdp.TargetInfo{fake.targets["baseline"], fake.targets["existing-translate"]}, "main")
	lifecycle.addTarget(fake.targets["main"])
	lifecycle.discoverWebsiteTargets(context.Background())
	cleanup := lifecycle.close()
	if !cleanup.Closed || len(cleanup.Reports) != 2 {
		t.Fatalf("cleanup = %+v, want main and newly created child only", cleanup)
	}
	for _, targetID := range []string{"baseline", "existing-translate", "unrelated"} {
		if _, ok := fake.targets[targetID]; !ok {
			t.Fatalf("discovery cleanup removed caller-owned target %q", targetID)
		}
	}
}

func TestGoogleTranslateLifecycleDoesNotClaimClosedWhenDiscoveryFails(t *testing.T) {
	fake := &googleTranslateLifecycleFakeClient{
		targets: map[string]cdp.TargetInfo{
			"baseline": {TargetID: "baseline", Type: "page", URL: "https://example.test/keep"},
			"main":     {TargetID: "main", Type: "page", URL: "https://translate.google.com/"},
		},
		closeErr:      map[string]error{},
		targetListErr: errors.New("synthetic discovery failure"),
	}
	lifecycle := newGoogleTranslateTargetLifecycle(fake, "headed")
	lifecycle.setWebsiteBaseline([]cdp.TargetInfo{fake.targets["baseline"]}, "main")
	lifecycle.addTarget(fake.targets["main"])
	lifecycle.discoverWebsiteTargets(context.Background())
	cleanup := lifecycle.close()
	if cleanup.Closed || !cleanup.Attempted || len(cleanup.Errors) == 0 {
		t.Fatalf("cleanup = %+v, want conservative failure evidence", cleanup)
	}
}

func TestGoogleTranslateLifecycleClosesSessionsBeforeTargets(t *testing.T) {
	fake := &googleTranslateLifecycleFakeClient{
		targets: map[string]cdp.TargetInfo{
			"main":  {TargetID: "main", Type: "page", URL: "https://translate.google.com/"},
			"child": {TargetID: "child", Type: "page", URL: "https://example.translate.goog/"},
		},
		closeErr: map[string]error{},
	}
	mainSession, err := cdp.AttachToTargetWithClient(context.Background(), fake, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	childSession, err := cdp.AttachToTargetWithClient(context.Background(), fake, "child", nil)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := newGoogleTranslateTargetLifecycle(fake, "headed")
	lifecycle.addTarget(fake.targets["main"])
	lifecycle.addTarget(fake.targets["child"])
	lifecycle.addSession(mainSession)
	lifecycle.addSession(childSession)
	cleanup := lifecycle.close()
	if !cleanup.Closed {
		t.Fatalf("cleanup = %+v, want success", cleanup)
	}
	events := fake.eventSnapshot()
	if want := []string{"detach:session-child", "detach:session-main", "close:child", "close:main"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}

func TestGoogleTranslateCleanupFailurePreservesPrimaryError(t *testing.T) {
	primary := commandError("artifact_write_failed", "io", "write translated website: disk full", ExitInternal, nil)
	result := googleTranslateResult{
		Cleanup: googleTranslateCleanup{
			Attempted:       true,
			Closed:          false,
			TargetIDs:       []string{"child"},
			Errors:          []string{"child: synthetic close failure"},
			RecoveryCommand: "cdp page cleanup --target child --force --close --json",
		},
	}
	err := googleTranslateResultError(primary, result)
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "google_translate_cleanup_failed" {
		t.Fatalf("error = %v, want google_translate_cleanup_failed", err)
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok || data["primary_error"] == nil || data["cleanup"] == nil {
		t.Fatalf("error data = %#v, want primary_error and cleanup", commandErr.Data)
	}
}

func TestGoogleTranslateCleanupFailureProvidesRecoveryCommand(t *testing.T) {
	fake := &googleTranslateLifecycleFakeClient{
		targets: map[string]cdp.TargetInfo{
			"child": {TargetID: "child", Type: "page", URL: "https://example.translate.goog/"},
		},
		closeErr:      map[string]error{"child": errors.New("synthetic close failure")},
		targetListErr: errors.New("synthetic target listing failure"),
	}
	cleanup := closeGoogleTranslateTargets(fake, []cdp.TargetInfo{fake.targets["child"]}, "headed")
	if cleanup.Closed || cleanup.RecoveryCommand != "cdp page cleanup --target child --force --close --json" {
		t.Fatalf("cleanup = %+v, want failed cleanup with exact recovery", cleanup)
	}
}

func TestGoogleTranslateCreationFailureDoesNotBecomeCleanupFailure(t *testing.T) {
	primary := commandError("connection_failed", "connection", "create target failed", ExitConnection, nil)
	err := googleTranslateResultError(primary, googleTranslateResult{})
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "connection_failed" {
		t.Fatalf("error = %v, want original connection_failed error", err)
	}
}

func TestAttachGoogleTranslateResultPreservesPrimaryErrorData(t *testing.T) {
	primary := commandErrorWithData(
		"pdf_burst_failed",
		"extraction",
		"synthetic Poppler failure",
		ExitCheckFailed,
		nil,
		map[string]any{
			"output_truncated":    true,
			"process_termination": "process_group",
		},
	)
	err := attachGoogleTranslateResult(primary, googleTranslateResult{Mode: "image"})
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "pdf_burst_failed" {
		t.Fatalf("error = %v, want original pdf_burst_failed error", err)
	}
	data, ok := commandErr.Data.(map[string]any)
	if !ok || data["output_truncated"] != true || data["process_termination"] != "process_group" {
		t.Fatalf("attached error data = %#v, want primary Poppler metadata preserved", commandErr.Data)
	}
	if _, ok := data["result"].(googleTranslateResult); !ok {
		t.Fatalf("attached error data = %#v, want Google Translate result context", commandErr.Data)
	}
}

func TestNormalizeGoogleTranslateLanguage(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		source bool
		want   string
	}{
		{name: "source auto", value: "detect", source: true, want: "auto"},
		{name: "Danish", value: "Danish", source: true, want: "da"},
		{name: "English", value: "English", source: false, want: "en"},
		{name: "regional Chinese", value: "zh-cn", source: false, want: "zh-CN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeGoogleTranslateLanguage(test.value, test.source)
			if err != nil || got != test.want {
				t.Fatalf("normalize(%q, source=%t) = %q, %v; want %q", test.value, test.source, got, err, test.want)
			}
		})
	}
	if _, err := normalizeGoogleTranslateLanguage("auto", false); err == nil {
		t.Fatal("target auto must be rejected")
	}
}

func TestGoogleTranslateTextChunksPreserveInputAndBound(t *testing.T) {
	input := strings.Repeat("å", 17) + " first paragraph\n\n" + strings.Repeat("B", 31) + " final"
	chunks := googleTranslateTextChunks(input, 20)
	if len(chunks) < 3 {
		t.Fatalf("chunks = %#v, want multiple chunks", chunks)
	}
	if strings.Join(chunks, "") != input {
		t.Fatalf("chunk concatenation changed input: %q != %q", strings.Join(chunks, ""), input)
	}
	for index, chunk := range chunks {
		if len([]rune(chunk)) > 20 {
			t.Fatalf("chunk %d has %d runes", index, len([]rune(chunk)))
		}
	}
}

func TestGoogleTranslateTextChunksPreferParagraphBoundaries(t *testing.T) {
	input := strings.Repeat("first ", 30) + "\n\n" + strings.Repeat("second ", 30)
	chunks := googleTranslateTextChunks(input, 200)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %#v, want at least two chunks", chunks)
	}
	if !strings.HasSuffix(chunks[0], "\n\n") {
		t.Fatalf("first chunk should end at paragraph boundary: %q", chunks[0])
	}
	if strings.Join(chunks, "") != input {
		t.Fatalf("paragraph-aware chunking changed input")
	}
}

func TestValidateGoogleTranslateOutput(t *testing.T) {
	textInput := googleTranslateInput{Path: "notes.txt"}
	if err := validateGoogleTranslateOutput("result.txt", textInput, "text"); err != nil {
		t.Fatal(err)
	}
	if err := validateGoogleTranslateOutput("result.pdf", textInput, "text"); err == nil {
		t.Fatal("text output should reject PDF extension")
	}
	documentInput := googleTranslateInput{Path: "report.docx"}
	if err := validateGoogleTranslateOutput("translated.docx", documentInput, "document"); err != nil {
		t.Fatal(err)
	}
	if err := validateGoogleTranslateOutput("translated.pdf", documentInput, "document"); err == nil {
		t.Fatal("document output should match the input extension")
	}
	imageInput := googleTranslateInput{Path: "page.png"}
	if err := validateGoogleTranslateOutput("translated.pdf", imageInput, "image"); err != nil {
		t.Fatal(err)
	}
	scanInput := googleTranslateInput{Path: "scan.pdf"}
	if err := validateGoogleTranslateOutput("translated.png", scanInput, "image"); err == nil {
		t.Fatal("scanned PDF output should be a PDF")
	}
}

func TestAssembleGooglePNGPDF(t *testing.T) {
	tempDir := t.TempDir()
	paths := []string{}
	for index, fill := range []color.RGBA{{R: 220, A: 255}, {B: 220, A: 255}} {
		path := filepath.Join(tempDir, "page-"+string(rune('1'+index))+".png")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		imageData := image.NewRGBA(image.Rect(0, 0, 12, 18))
		for y := 0; y < 18; y++ {
			for x := 0; x < 12; x++ {
				imageData.SetRGBA(x, y, fill)
			}
		}
		if err := png.Encode(file, imageData); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	output := filepath.Join(tempDir, "translated.pdf")
	if err := assembleGooglePNGPDF(paths, output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) || !bytes.Contains(data, []byte("/Count 2")) || !bytes.Contains(data, []byte("%%EOF")) {
		t.Fatalf("assembled PDF has unexpected header/trailer: %q", data[:minGoogleTranslateTestInt(len(data), 80)])
	}
}

func TestGoogleDocumentFileSelector(t *testing.T) {
	if got := googleDocumentFileSelector("report.pdf"); got != `input[type=file][accept*=".pdf"]` {
		t.Fatalf("PDF selector = %q", got)
	}
	if got := googleDocumentFileSelector("report.docx"); got != `input[type=file][accept*=".docx"]` {
		t.Fatalf("DOCX selector = %q", got)
	}
}

func minGoogleTranslateTestInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
