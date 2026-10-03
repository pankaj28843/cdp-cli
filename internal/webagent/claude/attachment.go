package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

const documentInputSelector = `input#chat-input-file-upload-onpage[data-testid="file-upload"]`

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

type attachmentPreparationError struct {
	stage string
	err   error
}

func (e *attachmentPreparationError) Error() string { return e.stage + ": " + e.err.Error() }
func (e *attachmentPreparationError) Unwrap() error { return e.err }

// Assign once on the fresh owned Ask target, under its input lease.
func prepareAttachments(ctx context.Context, session *cdp.PageSession, files []localAttachment, evidence []InputAttachment, interval time.Duration) (err error) {
	stage := "input_readiness"
	defer func() {
		if err != nil {
			err = &attachmentPreparationError{stage: stage, err: err}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var input struct {
		Ready    bool `json:"ready"`
		Occupied bool `json:"occupied"`
	}
	for {
		err := evaluateInto(ctx, session, `(() => {
   const claudeAttachmentInput = [...document.querySelectorAll('input#chat-input-file-upload-onpage[data-testid="file-upload"]')];
   const e=claudeAttachmentInput.length===1 ? claudeAttachmentInput[0] : null;
   const editors=[...document.querySelectorAll('[contenteditable="true"][aria-label="Write your prompt to Claude"]')].filter(e=>e.getBoundingClientRect().width>0);
   let root=editors.length===1?editors[0].parentElement:null;
   while(root && root.tagName!=='BODY' && !root.querySelector('input#chat-input-file-upload-onpage[data-testid="file-upload"]'))root=root.parentElement;
   const scoped=root && root.tagName!=='BODY';
   const occupied=Boolean(e && (e.files.length>0 || (scoped && [...root.querySelectorAll('[data-testid="file-thumbnail"]')].some(e=>e.getBoundingClientRect().width>0))));
   return {occupied,ready:Boolean(scoped && e && !e.disabled && e.multiple && !occupied)};
  })()`, &input)
		if err != nil {
			return err
		}
		if input.Occupied {
			stage = "existing_attachments"
			return fmt.Errorf("Claude composer already contains attachments")
		}
		if input.Ready {
			break
		}
		if err := waitSelectionPoll(ctx, interval); err != nil {
			return err
		}
	}
	stage = "file_validation"
	paths := make([]string, len(files))
	for i, file := range files {
		info, err := os.Stat(file.path)
		if err != nil || !os.SameFile(file.info, info) || info.Size() != file.info.Size() || !info.ModTime().Equal(file.info.ModTime()) {
			return fmt.Errorf("attachment changed before assignment")
		}
		paths[i] = file.path
	}
	stage = "document_query"
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
	stage = "assignment"
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
	stage = "processing"
	for {
		state, err := observeAttachments(ctx, session, evidence)
		if err != nil {
			return err
		}
		if state.Failed {
			return fmt.Errorf("Claude rejected an attachment")
		}
		if state.Ready {
			for i := range evidence {
				evidence[i].ProcessingComplete = true
			}
			return nil
		}
		if err := waitSelectionPoll(ctx, interval); err != nil {
			return err
		}
	}
}

type attachmentReadiness struct {
	Ready     bool `json:"ready"`
	Failed    bool `json:"failed"`
	SendReady bool `json:"send_ready"`
}

func observeAttachments(ctx context.Context, session *cdp.PageSession, evidence []InputAttachment) (attachmentReadiness, error) {
	names := make([]string, len(evidence))
	for i := range evidence {
		names[i] = evidence[i].Name
	}
	encoded, _ := json.Marshal(names)
	var state attachmentReadiness
	err := evaluateInto(ctx, session, `(() => {
  const claudeAttachmentNames = `+string(encoded)+`;
  const visible=e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return r.width>0&&r.height>0&&s.display!=='none'&&s.visibility!=='hidden'};
  const editors=[...document.querySelectorAll('[contenteditable="true"][aria-label="Write your prompt to Claude"]')].filter(visible);
  let root=editors.length===1?editors[0].parentElement:null;
  while(root && root.tagName!=='BODY' && !root.querySelector('input#chat-input-file-upload-onpage[data-testid="file-upload"]'))root=root.parentElement;
  if(!root || root.tagName==='BODY')return {ready:false,failed:false};
  const chips=[...root.querySelectorAll('[data-testid="file-thumbnail"]')].filter(visible);
  const chipNames=chips.map(e=>[...e.querySelectorAll('button[aria-label]')].map(b=>b.getAttribute('aria-label')).filter(n=>n.startsWith('Remove ')).map(n=>n.substring(7))).flat();
  const matches=chips.length===claudeAttachmentNames.length && chipNames.length===chips.length && claudeAttachmentNames.every(n=>chipNames.filter(v=>v===n).length===1);
  const processing=[...root.querySelectorAll('[role=progressbar],[aria-busy="true"],.animate-spin')].some(visible);
  const failed=[...document.querySelectorAll('[role=alert]')].some(e=>visible(e)&&(e.innerText||'').trim());
  const sends=[...root.querySelectorAll('button[data-testid="chat-input-send"][aria-label="Send message"]')].filter(visible);
  return {failed,ready:Boolean(matches && !processing && !failed),send_ready:Boolean(sends.length===1 && !sends[0].disabled && sends[0].getAttribute('aria-disabled')!=='true')};
 })()`, &state)
	return state, err
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
