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
	chatGPTFileInputSelector         = "#upload-files"
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
	SendReadyAfterUpload    bool   `json:"send_ready_after_upload"`
}

type attachmentObservation struct {
	OK                      bool `json:"ok"`
	InputMatch              bool `json:"input_match"`
	RenderedAttachmentAdded bool `json:"rendered_attachment_added"`
	RenderedNameMatch       bool `json:"rendered_name_match"`
	RenderedAttachmentCount int  `json:"rendered_attachment_count"`
}

type attachmentPreflight struct {
	OK                      bool `json:"ok"`
	InputCount              int  `json:"input_count"`
	InputFileCount          int  `json:"input_file_count"`
	PreexistingNameMatch    bool `json:"preexisting_name_match"`
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

func attachLocalFileOnce(
	ctx context.Context,
	session *cdp.PageSession,
	upload localUpload,
	timeout time.Duration,
	poll time.Duration,
) (AttachmentData, *attachmentFailure) {
	data := AttachmentData{
		Name:              upload.Name,
		Size:              upload.Size,
		Transport:         "headed_cdp_file_input",
		AssignmentOutcome: attachmentAssignmentNotAttempted,
	}
	if err := validateLocalUploadUnchanged(upload); err != nil {
		return data, &attachmentFailure{
			Code:      "chatgpt_attachment_changed",
			Message:   "ChatGPT attachment changed after validation and before assignment",
			RetrySafe: true,
			Cause:     err,
		}
	}
	preflight, err := verifyAttachmentPreflight(
		ctx,
		session,
		upload.Name,
	)
	if err != nil {
		return data, &attachmentFailure{
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
		upload.Path,
	)
	if attempted {
		data.AssignmentAttempts = 1
	}
	if err != nil {
		if attempted {
			data.AssignmentOutcome = attachmentAssignmentUnknown
			return data, &attachmentFailure{
				Code:      "chatgpt_attachment_assignment_unknown",
				Message:   "ChatGPT file assignment outcome is unknown; do not repeat the request",
				RetrySafe: false,
				Cause:     err,
			}
		}
		return data, &attachmentFailure{
			Code:      "chatgpt_attachment_assignment_not_performed",
			Message:   "ChatGPT file assignment was not performed",
			RetrySafe: true,
			Cause:     err,
		}
	}
	data.AssignmentOutcome = attachmentAssignmentConfirmed

	var observation attachmentObservation
	_, err = pollUntil(ctx, timeout, poll, func() (bool, error) {
		if err := observeAttachment(
			ctx,
			session,
			upload.Name,
			preflight.RenderedAttachmentCount,
			&observation,
		); err != nil {
			return false, err
		}
		return observation.OK, nil
	})
	data.AttachmentObserved = observation.OK
	data.InputMatch = observation.InputMatch
	data.RenderedAttachmentAdded = observation.RenderedAttachmentAdded
	data.RenderedNameMatch = observation.RenderedNameMatch
	if err != nil || !observation.OK {
		return data, &attachmentFailure{
			Code:      "chatgpt_attachment_observation_incomplete",
			Message:   "ChatGPT confirmed one file assignment but did not retain the exact active-composer file selection; do not repeat the request",
			RetrySafe: false,
			Cause:     err,
		}
	}
	return data, nil
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

func verifyAttachmentPreflight(
	ctx context.Context,
	session *cdp.PageSession,
	fileName string,
) (attachmentPreflight, error) {
	var preflight attachmentPreflight
	encodedName, err := json.Marshal(fileName)
	if err != nil {
		return preflight, fmt.Errorf(
			"encode ChatGPT attachment name: %w",
			err,
		)
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const inputs = Array.from(document.querySelectorAll('#upload-files'));
	  const editors = Array.from(document.querySelectorAll(
	    '#prompt-textarea,[contenteditable="true"][role="textbox"]'
	  )).filter(node => node.isContentEditable);
	  const input = inputs.length === 1 ? inputs[0] : null;
	  const composer = input ? input.closest('form') : null;
	  const activeComposer = Boolean(
	    composer && editors.length === 1 && composer.contains(editors[0])
	  );
	  const exactLine = value => String(value || '').split(/\r?\n/)
	    .map(line => line.trim()).some(line => line === expected);
	  const candidates = composer ? Array.from(composer.querySelectorAll(
	    '[data-testid*="attachment"],[data-testid*="file"],[class*="attachment"]'
	  )) : [];
	  const preexistingNameMatch = candidates.some(node =>
	    [node.textContent, node.getAttribute('aria-label'), node.getAttribute('title')]
	      .some(exactLine)
	  );
	  const inputFileCount = input && input.files ? input.files.length : -1;
	  return {
	    ok: activeComposer && input.type === 'file' &&
	      inputFileCount === 0 && !preexistingNameMatch,
	    input_count: inputs.length,
	    input_file_count: inputFileCount,
	    preexisting_name_match: preexistingNameMatch,
	    rendered_attachment_count: candidates.length
	  };
	})()`, encodedName)
	if err := evaluateInto(ctx, session, expression, &preflight); err != nil {
		return preflight, err
	}
	if !preflight.OK {
		return preflight, fmt.Errorf(
			"active composer file input is not uniquely empty",
		)
	}
	return preflight, nil
}

func setFileInputFilesOnce(
	ctx context.Context,
	session *cdp.PageSession,
	selector string,
	path string,
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
			"files":  []string{path},
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
) error {
	encodedName, err := json.Marshal(fileName)
	if err != nil {
		return fmt.Errorf("encode ChatGPT attachment name: %w", err)
	}
	expression := fmt.Sprintf(`(() => {
	  const expected = %s;
	  const preflightAttachmentCount = %d;
	  const inputs = Array.from(document.querySelectorAll('#upload-files'));
	  const editors = Array.from(document.querySelectorAll(
	    '#prompt-textarea,[contenteditable="true"][role="textbox"]'
	  )).filter(node => node.isContentEditable);
	  const input = inputs.length === 1 ? inputs[0] : null;
	  const composer = input ? input.closest('form') : null;
	  const activeComposer = Boolean(
	    composer && editors.length === 1 && composer.contains(editors[0])
	  );
	  const files = input && input.files ? Array.from(input.files) : [];
	  const inputMatch = files.length === 1 && files[0].name === expected;
	  const exactLine = value => String(value || '').split(/\r?\n/)
	    .map(line => line.trim()).some(line => line === expected);
	  const candidates = composer ? Array.from(composer.querySelectorAll(
	    '[data-testid*="attachment"],[data-testid*="file"],[class*="attachment"]'
	  )) : [];
	  const candidateNodes = Array.from(new Set(candidates.flatMap(node =>
	    [node, ...node.querySelectorAll('*')]
	  )));
	  const renderedNameMatch = candidateNodes.some(node =>
	    [node.textContent, node.getAttribute('aria-label'), node.getAttribute('title')]
	      .some(exactLine)
	  );
	  const renderedAttachmentAdded =
	    candidates.length > preflightAttachmentCount;
	  return {
	    ok: activeComposer && inputMatch,
	    input_match: inputMatch,
	    rendered_attachment_added: renderedAttachmentAdded,
	    rendered_name_match: renderedNameMatch,
	    rendered_attachment_count: candidates.length
	  };
	})()`, encodedName, preflightAttachmentCount)
	return evaluateInto(ctx, session, expression, observation)
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
