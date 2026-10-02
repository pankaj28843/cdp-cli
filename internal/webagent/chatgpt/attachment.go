package chatgpt

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const (
	chatGPTFileInputSelector         = `form:has([contenteditable="true"][role="textbox"]) input[type="file"]:not([accept])`
	attachmentAssignmentNotAttempted = "not_attempted"
	attachmentAssignmentConfirmed    = "confirmed"
	attachmentAssignmentUnknown      = "unknown"
)

type localUpload struct {
	Path        string
	Name        string
	Size        int64
	ModTime     time.Time
	Info        os.FileInfo
	Fingerprint [sha256.Size]byte
}

type AttachmentData struct {
	Name                    string `json:"name"`
	Size                    int64  `json:"size"`
	Transport               string `json:"transport"`
	AssignmentAttempts      int    `json:"assignment_attempts"`
	AssignmentOutcome       string `json:"assignment_outcome"`
	AttachmentObserved      bool   `json:"attachment_observed"`
	InputMatch              bool   `json:"input_match"`
	RenderedAttachmentAdded bool   `json:"rendered_attachment_added"`
	RenderedNameMatch       bool   `json:"rendered_name_match"`
	RenderedName            string `json:"rendered_name,omitempty"`
	DuplicateRejected       bool   `json:"duplicate_rejected"`
	ProcessingComplete      bool   `json:"processing_complete"`
	SendReadyAfterUpload    bool   `json:"send_ready_after_upload"`
}

type attachmentObservation struct {
	OK                      bool   `json:"ok"`
	InputMatch              bool   `json:"input_match"`
	RenderedAttachmentAdded bool   `json:"rendered_attachment_added"`
	RenderedNameMatch       bool   `json:"rendered_name_match"`
	RenderedName            string `json:"rendered_name"`
	RenderedAttachmentCount int    `json:"rendered_attachment_count"`
	DuplicateRejected       bool   `json:"duplicate_rejected"`
	Processing              bool   `json:"processing"`
}

type attachmentExpectation struct {
	Names                    []string
	PreflightAttachmentCount int
}

type attachmentPreflight struct {
	OK                      bool `json:"ok"`
	InputCount              int  `json:"input_count"`
	InputFileCount          int  `json:"input_file_count"`
	RenderedAttachmentCount int  `json:"rendered_attachment_count"`
}

type attachmentFailure struct {
	Code      string
	Message   string
	RetrySafe bool
	Cause     error
}

func (f *attachmentFailure) Error() string {
	if f == nil {
		return ""
	}
	if f.Cause != nil {
		return fmt.Sprintf("%s: %v", f.Message, f.Cause)
	}
	return f.Message
}

func resolveLocalUpload(rawPath string) (*localUpload, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return nil, nil
	}
	absolute, err := filepath.Abs(rawPath)
	if err != nil {
		return nil, fmt.Errorf("resolve ChatGPT attachment path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("ChatGPT attachment is not readable: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("ChatGPT attachment is not readable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("ChatGPT attachment must be a regular file")
	}
	fingerprint, err := fingerprintLocalFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("ChatGPT attachment is not readable: %w", err)
	}
	return &localUpload{
		Path:        resolved,
		Name:        info.Name(),
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		Info:        info,
		Fingerprint: fingerprint,
	}, nil
}

func resolveLocalUploads(config AskConfig) ([]localUpload, error) {
	paths := config.FilePaths
	uploads := make([]localUpload, 0, len(paths))
	names := make(map[string]bool, len(paths))
	for _, path := range paths {
		upload, err := resolveLocalUpload(path)
		if err != nil {
			return nil, err
		}
		if upload == nil {
			return nil, fmt.Errorf("ChatGPT attachment path must not be empty")
		}
		key := strings.ToLower(upload.Name)
		if names[key] {
			return nil, fmt.Errorf("ChatGPT attachment filenames must be distinct")
		}
		names[key] = true
		uploads = append(uploads, *upload)
	}
	return uploads, nil
}

func attachLocalFilesOnce(
	ctx context.Context,
	session *cdp.PageSession,
	uploads []localUpload,
	timeout time.Duration,
	poll time.Duration,
) ([]AttachmentData, *attachmentExpectation, *attachmentFailure) {
	data := make([]AttachmentData, len(uploads))
	paths := make([]string, len(uploads))
	names := make([]string, len(uploads))
	if len(uploads) == 0 {
		return data, nil, nil
	}
	for i, upload := range uploads {
		data[i] = AttachmentData{Name: upload.Name, Size: upload.Size, Transport: "headed_cdp_file_input", AssignmentOutcome: attachmentAssignmentNotAttempted}
		paths[i], names[i] = upload.Path, upload.Name
	}
	for _, upload := range uploads {
		if err := validateLocalUploadUnchanged(upload); err != nil {
			return data, nil, &attachmentFailure{Code: "chatgpt_attachment_changed", Message: "ChatGPT attachment changed after validation and before assignment", RetrySafe: true, Cause: err}
		}
	}
	preflight, err := verifyAttachmentPreflight(
		ctx,
		session,
	)
	if err != nil {
		return data, nil, &attachmentFailure{
			Code:      "chatgpt_attachment_preflight_failed",
			Message:   "ChatGPT exact empty attachment input was not proven before assignment",
			RetrySafe: true,
			Cause:     err,
		}
	}

	attempted, err := setFileInputFilesOnce(
		ctx,
		session,
		chatGPTFileInputSelector,
		paths...,
	)
	if attempted {
		for i := range data {
			data[i].AssignmentAttempts = 1
		}
	}
	if err != nil {
		if attempted {
			for i := range data {
				data[i].AssignmentOutcome = attachmentAssignmentUnknown
			}
			return data, nil, &attachmentFailure{
				Code:      "chatgpt_attachment_assignment_unknown",
				Message:   "ChatGPT file assignment outcome is unknown; do not repeat the request",
				RetrySafe: false,
				Cause:     err,
			}
		}
		return data, nil, &attachmentFailure{
			Code:      "chatgpt_attachment_assignment_not_performed",
			Message:   "ChatGPT file assignment was not performed",
			RetrySafe: true,
			Cause:     err,
		}
	}
	for i := range data {
		data[i].AssignmentOutcome = attachmentAssignmentConfirmed
	}

	observations := make([]attachmentObservation, len(uploads))
	_, err = pollUntil(ctx, timeout, poll, func() (bool, error) {
		ready := true
		for i, name := range names {
			if err := observeAttachment(ctx, session, name, preflight.RenderedAttachmentCount, &observations[i], len(uploads)); err != nil {
				return false, err
			}
			if observations[i].DuplicateRejected {
				return true, nil
			}
			ready = ready && observations[i].OK && !observations[i].Processing
		}
		return ready, nil
	})
	observation := attachmentObservation{OK: true}
	for i, current := range observations {
		data[i].AttachmentObserved = current.OK
		data[i].InputMatch = current.InputMatch
		data[i].RenderedAttachmentAdded = current.RenderedAttachmentAdded
		data[i].RenderedNameMatch = current.RenderedNameMatch
		data[i].RenderedName = current.RenderedName
		data[i].DuplicateRejected = current.DuplicateRejected
		data[i].ProcessingComplete = current.OK && !current.Processing
		observation.OK = observation.OK && current.OK && !current.Processing
		observation.DuplicateRejected = observation.DuplicateRejected || current.DuplicateRejected
	}
	if observation.DuplicateRejected {
		return data, nil, &attachmentFailure{
			Code:      "chatgpt_attachment_duplicate_rejected",
			Message:   "ChatGPT rejected the attachment as an already-uploaded identical file before Send; rebuild or rename the review artifact before retrying",
			RetrySafe: true,
		}
	}
	if err != nil || !observation.OK {
		return data, nil, &attachmentFailure{
			Code:      "chatgpt_attachment_observation_incomplete",
			Message:   "ChatGPT confirmed one batch assignment but did not retain the exact active-composer file selection; do not repeat the request",
			RetrySafe: false,
			Cause:     err,
		}
	}
	return data, &attachmentExpectation{
		Names:                    names,
		PreflightAttachmentCount: preflight.RenderedAttachmentCount,
	}, nil
}

func observeExpectedAttachment(
	ctx context.Context,
	session *cdp.PageSession,
	expectation *attachmentExpectation,
	observation *attachmentObservation,
) error {
	if expectation == nil {
		*observation = attachmentObservation{OK: true}
		return nil
	}
	names := expectation.Names
	*observation = attachmentObservation{OK: true, InputMatch: true, RenderedAttachmentAdded: true, RenderedNameMatch: true}
	for _, name := range names {
		var current attachmentObservation
		if err := observeAttachment(ctx, session, name, expectation.PreflightAttachmentCount, &current, len(names)); err != nil {
			return err
		}
		observation.OK = observation.OK && current.OK && !current.Processing
		observation.InputMatch = observation.InputMatch && current.InputMatch
		observation.RenderedAttachmentAdded = observation.RenderedAttachmentAdded && current.RenderedAttachmentAdded
		observation.RenderedNameMatch = observation.RenderedNameMatch && current.RenderedNameMatch
		observation.Processing = observation.Processing || current.Processing
		observation.DuplicateRejected = observation.DuplicateRejected || current.DuplicateRejected
	}
	return nil
}

func validateLocalUploadUnchanged(upload localUpload) error {
	info, err := os.Stat(upload.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() ||
		!os.SameFile(upload.Info, info) ||
		info.Size() != upload.Size ||
		!info.ModTime().Equal(upload.ModTime) {
		return fmt.Errorf("local file identity or metadata changed")
	}
	fingerprint, err := fingerprintLocalFile(upload.Path)
	if err != nil {
		return err
	}
	if fingerprint != upload.Fingerprint {
		return fmt.Errorf("local file content changed")
	}
	return nil
}

func fingerprintLocalFile(path string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return result, err
	}
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func verifyAttachmentPreflight(ctx context.Context, session *cdp.PageSession) (attachmentPreflight, error) {
	var preflight attachmentPreflight
	expression := `(() => {
  const inputs = Array.from(document.querySelectorAll('` + chatGPTFileInputSelector + `'));
  const editors = Array.from(document.querySelectorAll('[contenteditable="true"][role="textbox"]')).filter(node => node.isContentEditable);
  const input = inputs.length === 1 ? inputs[0] : null;
  const composer = input ? input.closest('form') : null;
  const activeComposer = Boolean(composer && editors.length === 1 && composer.contains(editors[0]));
  const candidates = composer ? composer.querySelectorAll('[data-composer-attachments] button[aria-label^="Remove "]') : [];
  const inputFileCount = input && input.files ? input.files.length : -1;
  return {ok:activeComposer && input.type === 'file' && inputFileCount === 0 && candidates.length === 0,
   input_count:inputs.length,input_file_count:inputFileCount,rendered_attachment_count:candidates.length};
 })()`
	if err := evaluateInto(ctx, session, expression, &preflight); err != nil {
		return preflight, err
	}
	if !preflight.OK {
		return preflight, fmt.Errorf("exact empty attachment input was not proven")
	}
	return preflight, nil
}

func setFileInputFilesOnce(
	ctx context.Context,
	session *cdp.PageSession,
	selector string,
	paths ...string,
) (bool, error) {
	if session == nil {
		return false, fmt.Errorf("ChatGPT attachment session is unavailable")
	}
	var document struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := execPageSessionJSON(
		ctx,
		session,
		"DOM.getDocument",
		map[string]any{"depth": 0, "pierce": true},
		&document,
	); err != nil {
		return false, fmt.Errorf("inspect ChatGPT attachment DOM: %w", err)
	}
	if document.Root.NodeID == 0 {
		return false, fmt.Errorf("ChatGPT attachment DOM root is unavailable")
	}
	var query struct {
		NodeID int `json:"nodeId"`
	}
	if err := execPageSessionJSON(
		ctx,
		session,
		"DOM.querySelector",
		map[string]any{
			"nodeId":   document.Root.NodeID,
			"selector": selector,
		},
		&query,
	); err != nil {
		return false, fmt.Errorf("query ChatGPT exact file input: %w", err)
	}
	if query.NodeID == 0 {
		return false, fmt.Errorf("ChatGPT exact file input was not found")
	}
	var described struct {
		Node struct {
			NodeName   string   `json:"nodeName"`
			Attributes []string `json:"attributes"`
		} `json:"node"`
	}
	if err := execPageSessionJSON(
		ctx,
		session,
		"DOM.describeNode",
		map[string]any{"nodeId": query.NodeID, "depth": 0},
		&described,
	); err != nil {
		return false, fmt.Errorf("describe ChatGPT exact file input: %w", err)
	}
	if !strings.EqualFold(described.Node.NodeName, "input") ||
		!attributeEquals(described.Node.Attributes, "type", "file") {
		return false, fmt.Errorf("ChatGPT exact attachment node is not input[type=file]")
	}
	if err := execPageSessionJSON(
		ctx,
		session,
		"DOM.setFileInputFiles",
		map[string]any{
			"nodeId": query.NodeID,
			"files":  paths,
		},
		nil,
	); err != nil {
		return true, fmt.Errorf("assign ChatGPT file input: %w", err)
	}
	return true, nil
}

func attributeEquals(attributes []string, name string, expected string) bool {
	for index := 0; index+1 < len(attributes); index += 2 {
		if strings.EqualFold(attributes[index], name) &&
			strings.EqualFold(attributes[index+1], expected) {
			return true
		}
	}
	return false
}

func observeAttachment(
	ctx context.Context,
	session *cdp.PageSession,
	fileName string,
	preflightAttachmentCount int,
	observation *attachmentObservation,
	expectedCount int,
) error {
	encodedName, err := json.Marshal(fileName)
	if err != nil {
		return fmt.Errorf("encode ChatGPT attachment name: %w", err)
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const preflightAttachmentCount = %d;
	  const expectedCount = %d;
	  const visible = element => {
	    if (!(element instanceof HTMLElement)) return false;
	    const style = getComputedStyle(element);
	    const rect = element.getBoundingClientRect();
	    return style.display !== 'none' && style.visibility !== 'hidden' &&
	      Number(style.opacity || '1') !== 0 &&
	      rect.width > 0 && rect.height > 0;
	  };
	  const inputs = Array.from(document.querySelectorAll('`+chatGPTFileInputSelector+`'));
	  const editors = Array.from(document.querySelectorAll(
	    '[contenteditable="true"][role="textbox"]'
	  )).filter(node => node.isContentEditable);
	  const input = inputs.length === 1 ? inputs[0] : null;
	  const composer = input ? input.closest('form') : null;
	  const activeComposer = Boolean(
	    composer && editors.length === 1 && composer.contains(editors[0])
	  );
	  const files = input && input.files ? Array.from(input.files) : [];
	  const inputMatch = files.length === expectedCount && files.filter(file => file.name === expected).length === 1;
	  const escapeRegExp = value => String(value || '')
	    .replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	  const providerNameMatches = (actual, wanted) => {
	    actual = String(actual || '').trim();
	    wanted = String(wanted || '').trim();
	    if (actual === wanted) return true;
	    const dot = wanted.lastIndexOf('.');
	    const stem = dot > 0 ? wanted.slice(0, dot) : wanted;
	    const extension = dot > 0 ? wanted.slice(dot) : '';
	    return new RegExp(
	      '^' + escapeRegExp(stem) + '\\s*\\(\\d+\\)' +
	      escapeRegExp(extension) + '$'
	    ).test(actual);
	  };
	  const candidates = composer ? Array.from(composer.querySelectorAll(
    '[data-composer-attachments] button[aria-label^="Remove "]'
  )) : [];
	  const matchingCandidates = candidates.filter(node =>
	    providerNameMatches(node.getAttribute('aria-label').slice('Remove '.length), expected)
	  );
	  const matchingNames = matchingCandidates.map(node =>
	    String(node.getAttribute('aria-label') || '').slice('Remove '.length).trim()
	  );
	  const duplicateRejected = Array.from(document.querySelectorAll(
	    '[role="dialog"]'
	  )).filter(visible).some(dialog => {
	    const text = String(dialog.innerText || dialog.textContent || '')
	      .replace(/\s+/g, ' ').trim().toLowerCase();
	    return text.includes('already uploaded this file') &&
	      text.includes('try uploading something new');
	  });
	  const renderedNameMatch = matchingNames.length === 1;
	  const processing = matchingCandidates.some(node =>
	    Array.from((node.parentElement.parentElement || node.parentElement).querySelectorAll('[class*="animate-spin"],[role="progressbar"],[aria-busy="true"]'))
	      .some(visible)
	  );
	  const renderedAttachmentAdded =
	    candidates.length === preflightAttachmentCount + expectedCount;
	  return {
	    ok: !duplicateRejected && activeComposer &&
	      renderedAttachmentAdded && renderedNameMatch,
	    input_match: inputMatch,
	    rendered_attachment_added: renderedAttachmentAdded,
	    rendered_name_match: renderedNameMatch,
	    rendered_name: renderedNameMatch ? matchingNames[0] : '',
	    rendered_attachment_count: candidates.length,
	    duplicate_rejected: duplicateRejected,
	    processing
	  };
	})()`, encodedName, preflightAttachmentCount, expectedCount)
	if err := evaluateInto(ctx, session, expression, observation); err != nil {
		return err
	}
	return nil
}

func execPageSessionJSON(
	ctx context.Context,
	session *cdp.PageSession,
	method string,
	params any,
	out any,
) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode %s parameters: %w", method, err)
	}
	raw, err := session.Exec(ctx, method, encoded)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	return nil
}
