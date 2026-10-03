package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browserflow"
	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const documentInputSelector = `[role="menu"][aria-label="Upload file options"] images-files-uploader input[type="file"]`

type InputAttachment struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	AssignmentAttempts int    `json:"assignment_attempts"`
	AssignmentOutcome  string `json:"assignment_outcome"`
	ProcessingComplete bool   `json:"processing_complete"`
}

type localAttachment struct {
	path string
	info os.FileInfo
}

func resolveAttachments(paths []string) ([]localAttachment, []InputAttachment, error) {
	files := make([]localAttachment, 0, len(paths))
	evidence := make([]InputAttachment, 0, len(paths))
	names := make(map[string]bool, len(paths))
	for _, raw := range paths {
		if strings.TrimSpace(raw) == "" {
			return nil, nil, fmt.Errorf("attachment path must not be empty")
		}
		path, err := filepath.Abs(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve attachment path: %w", err)
		}
		handle, err := os.Open(path)
		if err != nil {
			return nil, nil, fmt.Errorf("attachment must be readable")
		}
		info, err := handle.Stat()
		closeErr := handle.Close()
		if err != nil || closeErr != nil || !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("attachment must be a readable regular file")
		}
		key := strings.ToLower(info.Name())
		if names[key] {
			return nil, nil, fmt.Errorf("attachment filenames must be distinct")
		}
		names[key] = true
		files = append(files, localAttachment{path: path, info: info})
		evidence = append(evidence, InputAttachment{Name: info.Name(), Size: info.Size(), AssignmentOutcome: "not_attempted"})
	}
	return files, evidence, nil
}

// Attach only on a fresh owned target, under the Ask input lease. Assignment is
// never retried: an uncertain upload stops before prompt mutation and Send.
func prepareAttachments(ctx context.Context, session *cdp.PageSession, files []localAttachment, evidence []InputAttachment, timeout, interval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var control struct {
		Count int     `json:"count"`
		Ready bool    `json:"ready"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
	}
	if err := evaluateInto(ctx, session, `(() => {
  const matches = [...document.querySelectorAll('button[aria-label="Upload & tools"]')].filter(e => e.getBoundingClientRect().width > 0);
  const e = matches.length === 1 ? matches[0] : null, r = e?.getBoundingClientRect();
  const x = r ? r.left+r.width/2 : 0, y = r ? r.top+r.height/2 : 0;
  const top = e ? document.elementFromPoint(x,y) : null;
  return {count:matches.length, x,y,ready:Boolean(e && !e.disabled && e.getAttribute('aria-disabled') !== 'true' && top && (top === e || e.contains(top)))};
 })()`, &control); err != nil {
		return fmt.Errorf("observe upload control: %w", err)
	}
	if control.Count != 1 || !control.Ready {
		return fmt.Errorf("upload control is not uniquely actionable")
	}
	outcome, err := browserflow.ClickPoint(ctx, session, control.X, control.Y)
	if err != nil || outcome.Dispatch != browserflow.DispatchPerformed {
		return fmt.Errorf("upload menu click was not confirmed")
	}
	names := make([]string, len(evidence))
	paths := make([]string, len(files))
	for i := range files {
		names[i] = evidence[i].Name
		paths[i] = files[i].path
	}
	encodedNames, _ := json.Marshal(names)
	encodedSelector, _ := json.Marshal(documentInputSelector)
	var input struct {
		Count     int  `json:"count"`
		Ready     bool `json:"ready"`
		Supported bool `json:"supported"`
	}
	_, err = pollUntil(ctx, timeout, interval, func() (bool, error) {
		err := evaluateInto(ctx, session, `(() => {
   const geminiAttachmentInput = `+string(encodedSelector)+`, names = `+string(encodedNames)+`;
   const inputs = [...document.querySelectorAll(geminiAttachmentInput)], e = inputs.length === 1 ? inputs[0] : null;
   const accept = (e?.accept || '').toLowerCase().split(',').map(x=>x.trim());
   const supported = names.every(name => accept.includes('.'+name.split('.').pop().toLowerCase()));
   return {count:inputs.length, ready:Boolean(e && !e.disabled && e.files.length === 0 && document.querySelectorAll('.attachment-preview-wrapper .file-preview-container').length === 0 && (names.length === 1 || e.multiple)), supported};
  })()`, &input)
		return input.Count > 1 || input.Ready, err
	})
	if err != nil || input.Count != 1 || !input.Ready {
		return fmt.Errorf("exact empty document input was not proven")
	}
	if !input.Supported {
		return fmt.Errorf("attachment type is not accepted by the current document input")
	}
	for _, file := range files {
		info, err := os.Stat(file.path)
		if err != nil || !os.SameFile(file.info, info) || info.Size() != file.info.Size() || !info.ModTime().Equal(file.info.ModTime()) {
			return fmt.Errorf("attachment changed before assignment")
		}
	}
	var document struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := attachmentExec(ctx, session, "DOM.getDocument", map[string]any{"depth": 0, "pierce": true}, &document); err != nil {
		return err
	}
	var query struct {
		NodeIDs []int `json:"nodeIds"`
	}
	if err := attachmentExec(ctx, session, "DOM.querySelectorAll", map[string]any{"nodeId": document.Root.NodeID, "selector": documentInputSelector}, &query); err != nil {
		return err
	}
	if len(query.NodeIDs) != 1 {
		return fmt.Errorf("document input changed before assignment")
	}
	for i := range evidence {
		evidence[i].AssignmentAttempts = 1
		evidence[i].AssignmentOutcome = "unknown"
	}
	if err := attachmentExec(ctx, session, "DOM.setFileInputFiles", map[string]any{"nodeId": query.NodeIDs[0], "files": paths}, nil); err != nil {
		return err
	}
	for i := range evidence {
		evidence[i].AssignmentOutcome = "confirmed"
	}
	return waitForAttachments(ctx, session, names, evidence, timeout, interval)
}

func attachmentExec(ctx context.Context, session *cdp.PageSession, method string, params any, target any) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	raw, err := session.Exec(ctx, method, encoded)
	if err != nil {
		return fmt.Errorf("attachment %s failed: %w", method, err)
	}
	if target != nil {
		return json.Unmarshal(raw, target)
	}
	return nil
}

type attachmentReadiness struct {
	Ready  bool `json:"ready"`
	Failed bool `json:"failed"`
}

func observeAttachments(ctx context.Context, session *cdp.PageSession, names []string) (attachmentReadiness, error) {
	encoded, _ := json.Marshal(names)
	var observation attachmentReadiness
	err := evaluateInto(ctx, session, `(() => {
   const geminiAttachmentNames = `+string(encoded)+`;
   const visible = e => { const r=e.getBoundingClientRect(), s=getComputedStyle(e); return r.width>0 && r.height>0 && s.display !== 'none' && s.visibility !== 'hidden'; };
   const composers = [...document.querySelectorAll('.text-input-field:has([role=textbox][contenteditable=true])')].filter(visible);
   const composer = composers.length === 1 ? composers[0] : null;
   const chips = composer ? [...composer.querySelectorAll('.attachment-preview-wrapper .file-preview-container')].filter(visible) : [];
   const chipNames = chips.map(e => (e.getAttribute('aria-describedby') || '').split(/\s+/).map(id=>document.getElementById(id)?.textContent?.trim() || '').filter(Boolean).join(' '));
   const namesMatch = chips.length === geminiAttachmentNames.length && geminiAttachmentNames.every(name=>chipNames.filter(value=>value === name).length === 1);
   const processing = composer && [...composer.querySelectorAll('[role=progressbar],mat-progress-spinner')].some(visible);
   const failed = [...document.querySelectorAll('[role=alert]')].some(e=>visible(e) && (e.innerText||'').trim());
   const sends = composer ? [...composer.querySelectorAll('button[aria-label="Send message"]')].filter(visible) : [];
   return {failed,ready:Boolean(composer && namesMatch && !processing && !failed && sends.length === 1 && !sends[0].disabled && sends[0].getAttribute('aria-disabled') !== 'true')};
  })()`, &observation)
	return observation, err
}

func waitForAttachments(ctx context.Context, session *cdp.PageSession, names []string, evidence []InputAttachment, timeout, interval time.Duration) error {
	var observation attachmentReadiness
	_, err := pollUntil(ctx, timeout, interval, func() (bool, error) {
		var err error
		observation, err = observeAttachments(ctx, session, names)
		return observation.Ready || observation.Failed, err
	})
	if err != nil || !observation.Ready || observation.Failed {
		return fmt.Errorf("matching attachments did not finish processing before Send")
	}
	for i := range evidence {
		evidence[i].ProcessingComplete = true
	}
	return nil
}
