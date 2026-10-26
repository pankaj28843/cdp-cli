package chatgpt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
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
	maxBrowserUploadFileBytes        = 16 << 20
	maxBrowserUploadBatchBytes       = 32 << 20
	maxBrowserUploadFiles            = 20
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
	if info.Size() > maxBrowserUploadFileBytes {
		return nil, fmt.Errorf("ChatGPT attachment exceeds the 16 MiB browser transfer limit")
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
	if len(paths) > maxBrowserUploadFiles {
		return nil, fmt.Errorf("ChatGPT attachment batch exceeds 20 files")
	}
	var total int64
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
		total += upload.Size
		if total > maxBrowserUploadBatchBytes {
			return nil, fmt.Errorf("ChatGPT attachment batch exceeds the 32 MiB browser transfer limit")
		}
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
	names := make([]string, len(uploads))
	if len(uploads) == 0 {
		return data, nil, nil
	}
	for i, upload := range uploads {
		data[i] = AttachmentData{Name: upload.Name, Size: upload.Size, Transport: "headed_cdp_browser_file", AssignmentOutcome: attachmentAssignmentNotAttempted}
		names[i] = upload.Name
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

	attempted, err := assignBrowserFilesOnce(
		ctx,
		session,
		uploads,
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

// DOM.setFileInputFiles passes host paths to Chrome. Headed Chrome can run in
// another mount namespace, where those paths exist only as empty uploads. Move
// bytes through the selected page's CDP session and dispatch one change event.
func assignBrowserFilesOnce(ctx context.Context, session *cdp.PageSession, uploads []localUpload) (bool, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return false, err
	}
	key, _ := json.Marshal(fmt.Sprintf("__cdp_upload_%x", nonce))
	var result struct {
		OK bool `json:"ok"`
	}
	if err := evaluateInto(ctx, session, fmt.Sprintf(`(() => { Object.defineProperty(window, %s, {value:{parts:[],files:[]}, configurable:true}); return {ok:true}; })()`, key), &result); err != nil || !result.OK {
		return false, browserUploadError("prepare browser-local ChatGPT files", err)
	}
	defer func() {
		var ignored any
		_ = evaluateInto(ctx, session, fmt.Sprintf(`(() => { delete window[%s]; return true; })()`, key), &ignored)
	}()

	for _, upload := range uploads {
		file, err := os.Open(upload.Path)
		if err != nil {
			return false, err
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(upload.Info, opened) {
			_ = file.Close()
			return false, fmt.Errorf("ChatGPT attachment identity changed before browser transfer")
		}
		buffer := make([]byte, 12*1024)
		var sent int64
		streamHash := sha256.New()
		for {
			n, readErr := file.Read(buffer)
			if n > 0 {
				_, _ = streamHash.Write(buffer[:n])
				chunk, _ := json.Marshal(base64.StdEncoding.EncodeToString(buffer[:n]))
				var received struct {
					Bytes int `json:"bytes"`
				}
				expression := fmt.Sprintf(`(() => { const state=window[%s]; if (!state) return {bytes:-1}; const raw=atob(%s); const bytes=Uint8Array.from(raw, c => c.charCodeAt(0)); state.parts.push(bytes); return {bytes:bytes.length}; })()`, key, chunk)
				if err := evaluateInto(ctx, session, expression, &received); err != nil || received.Bytes != n {
					_ = file.Close()
					return false, browserUploadError("copy browser-local ChatGPT file chunk", err)
				}
				sent += int64(n)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = file.Close()
				return false, readErr
			}
		}
		if err := file.Close(); err != nil {
			return false, err
		}
		if sent != upload.Size {
			return false, fmt.Errorf("ChatGPT file size changed while copying")
		}
		var streamed [sha256.Size]byte
		copy(streamed[:], streamHash.Sum(nil))
		if streamed != upload.Fingerprint {
			return false, fmt.Errorf("ChatGPT file content changed while copying")
		}
		if err := validateLocalUploadUnchanged(upload); err != nil {
			return false, err
		}
		name, _ := json.Marshal(upload.Name)
		kind, _ := json.Marshal(mime.TypeByExtension(filepath.Ext(upload.Name)))
		var finished struct {
			Size int64 `json:"size"`
		}
		expression := fmt.Sprintf(`(() => { const state=window[%s]; if (!state) return {size:-1}; const file=new File(state.parts,%s,{type:%s}); state.files.push(file); state.parts=[]; return {size:file.size}; })()`, key, name, kind)
		if err := evaluateInto(ctx, session, expression, &finished); err != nil || finished.Size != upload.Size {
			return false, browserUploadError("finish browser-local ChatGPT file", err)
		}
	}

	// A transport error here is ambiguous: the change event may have reached the
	// provider. The caller must never assign or submit this request again.
	result.OK = false
	expression := fmt.Sprintf(`(() => {
  const state=window[%s];
  const inputs=Array.from(document.querySelectorAll('%s'));
  const editors=Array.from(document.querySelectorAll('[contenteditable="true"][role="textbox"]')).filter(node=>node.isContentEditable);
  const input=inputs.length===1?inputs[0]:null;
  const form=input?.closest('form');
  if (!state || !form || editors.length!==1 || !form.contains(editors[0]) ||
      input.files.length!==0 || form.querySelectorAll('[data-composer-attachments] button[aria-label^="Remove "]').length!==0 ||
      state.files.length!==%d) return {ok:false};
  const transfer=new DataTransfer();
  for (const file of state.files) transfer.items.add(file);
  input.files=transfer.files;
  input.dispatchEvent(new Event('change',{bubbles:true}));
  return {ok:true};
})()`, key, chatGPTFileInputSelector, len(uploads))
	if err := evaluateInto(ctx, session, expression, &result); err != nil || !result.OK {
		return true, browserUploadError("assign browser-local ChatGPT files", err)
	}
	return true, nil
}

func browserUploadError(stage string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", stage, err)
	}
	return fmt.Errorf("%s: provider rejected the expected state", stage)
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
	      '^' + escapeRegExp(stem) + '\\s*\\((?:\\d+|\\d{8}-\\d{6})\\)' +
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
