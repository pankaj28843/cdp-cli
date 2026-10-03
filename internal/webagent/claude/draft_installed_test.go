package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/testsupport"
)

func TestClaudeDraftInstalled(t *testing.T) {
	binary := os.Getenv("CDP_CLAUDE_DRAFT_INSTALLED_BINARY")
	if binary == "" {
		t.Skip("set CDP_CLAUDE_DRAFT_INSTALLED_BINARY to the installed cdp executable")
	}
	browser := testsupport.NewInstalledBrowser(t, binary)
	const input = `<input id="chat-input-file-upload-onpage" data-testid="file-upload" type="file" multiple>`
	const editor = `<div contenteditable="true" aria-label="Write your prompt to Claude">%s</div>`
	const chip = `<div data-testid="file-thumbnail">Synthetic file</div>`
	cases := []struct {
		name, body  string
		text, files int
	}{
		{"empty", `<section>` + input + fmt.Sprintf(editor, "") + `</section>`, 0, 0},
		{"text draft", `<section>` + input + fmt.Sprintf(editor, "Synthetic draft") + `</section>`, 15, 0},
		{"whitespace", `<section>` + input + fmt.Sprintf(editor, "  ") + `</section>`, 0, 0},
		{"attachment draft", `<section>` + input + fmt.Sprintf(editor, "") + chip + `</section>`, 0, 1},
		{"both", `<section>` + input + fmt.Sprintf(editor, "Synthetic draft") + chip + `</section>`, 15, 1},
		{"hidden preview", `<section>` + input + fmt.Sprintf(editor, "") + `<div hidden data-testid="file-thumbnail">Synthetic file</div></section>`, 0, 0},
		{"history outside composer", chip + `<p>Synthetic history</p><section>` + input + fmt.Sprintf(editor, "") + `</section>`, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			browser.Eval(t, fmt.Sprintf("document.body.innerHTML=%s; true", body), nil)
			var observation composerObservation
			browser.Eval(t, composerExpression, &observation)
			if !observation.Ready || observation.DraftTextCharacters != tc.text || observation.DraftAttachmentCount != tc.files {
				t.Fatalf("draft observer=%+v want text=%d files=%d", observation, tc.text, tc.files)
			}
		})
	}
	t.Run("selected files without previews", func(t *testing.T) {
		body, _ := json.Marshal(`<section>` + input + fmt.Sprintf(editor, "") + `</section>`)
		browser.Eval(t, fmt.Sprintf("document.body.innerHTML=%s; true", body), nil)
		browser.Eval(t, `(() => { const files = new DataTransfer(); files.items.add(new File(['Synthetic'], 'synthetic.txt')); document.querySelector('input[type=file]').files = files.files; return true; })()`, nil)
		var observation composerObservation
		browser.Eval(t, composerExpression, &observation)
		if observation.DraftAttachmentCount != 1 {
			t.Fatalf("selected files were missed: %+v", observation)
		}
	})
}
