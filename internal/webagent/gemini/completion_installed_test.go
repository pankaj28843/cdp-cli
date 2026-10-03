package gemini

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/testsupport"
)

func TestGeminiCompletionInstalled(t *testing.T) {
	binary := os.Getenv("CDP_GEMINI_COMPLETION_INSTALLED_BINARY")
	if binary == "" {
		t.Skip("set CDP_GEMINI_COMPLETION_INSTALLED_BINARY to the installed cdp executable")
	}
	browser := testsupport.NewInstalledBrowser(t, binary)
	const id = "abcdefghijklmnop"
	expression, err := conversationDetailExpression(id)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture supplies the provider route while executing the actual observer.
	expression = `(location => ` + expression + `)({origin:'https://gemini.google.com',pathname:'/app/` + id + `'})`
	const answer = `<model-response style="display:block"><message-content style="display:block"><div class="markdown" aria-busy="false">Synthetic answer</div></message-content><button aria-label="Copy">Copy</button></model-response>`
	cases := []struct {
		name, body string
		ready      bool
	}{
		{"completed", answer, true},
		{"busy without Stop", strings.Replace(answer, `aria-busy="false"`, `aria-busy="true"`, 1), false},
		{"completion signal absent", strings.Replace(answer, ` aria-busy="false"`, "", 1), false},
		{"Copy absent", strings.Replace(answer, `<button aria-label="Copy">Copy</button>`, "", 1), false},
		{"Copy hidden", strings.Replace(answer, `<button `, `<button hidden `, 1), false},
		{"Copy disabled", strings.Replace(answer, `<button `, `<button disabled `, 1), false},
		{"Copy ambiguous", strings.Replace(answer, `</model-response>`, `<button aria-label="Copy">Copy</button></model-response>`, 1), false},
		{"visible Stop", answer + `<button aria-label="Stop response">Stop</button>`, false},
		{"hidden Stop", answer + `<button hidden aria-label="Stop response">Stop</button>`, true},
		{"last response still busy", answer + strings.Replace(answer, `aria-busy="false"`, `aria-busy="true"`, 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			browser.Eval(t, fmt.Sprintf("document.body.innerHTML=%s; true", body), nil)
			var observation detailObservation
			browser.Eval(t, expression, &observation)
			if observation.CompletionReady != tc.ready {
				t.Fatalf("completion_ready=%v want=%v", observation.CompletionReady, tc.ready)
			}
			var completion answerCompletion
			if completion.observe(observation, id, nil) {
				t.Fatal("one observation certified terminal")
			}
			if completion.observe(observation, id, nil) != tc.ready {
				t.Fatal("confirmed observer contract failed")
			}
		})
	}
	t.Run("exact route required", func(t *testing.T) {
		body, _ := json.Marshal(answer)
		browser.Eval(t, fmt.Sprintf("document.body.innerHTML=%s; true", body), nil)
		var observation detailObservation
		browser.Eval(t, strings.Replace(expression, "pathname:'/app/"+id+"'", "pathname:'/app/ponmlkjihgfedcba'", 1), &observation)
		var completion answerCompletion
		if observation.RouteMatches || completion.observe(observation, id, nil) || completion.observe(observation, id, nil) {
			t.Fatal("wrong conversation certified terminal")
		}
	})
	t.Run("answer growth invalidates confirmation", func(t *testing.T) {
		body, _ := json.Marshal(answer)
		browser.Eval(t, fmt.Sprintf("document.body.innerHTML=%s; true", body), nil)
		var completion answerCompletion
		var observation detailObservation
		browser.Eval(t, expression, &observation)
		if completion.observe(observation, id, nil) {
			t.Fatal("initial snapshot certified terminal")
		}
		browser.Eval(t, `document.querySelector('.markdown').innerText='Synthetic answer with final suffix'; true`, nil)
		browser.Eval(t, expression, &observation)
		if completion.observe(observation, id, nil) {
			t.Fatal("growing snapshot certified terminal")
		}
		browser.Eval(t, expression, &observation)
		if !completion.observe(observation, id, nil) || observation.Text != "Synthetic answer with final suffix" {
			t.Fatal("stable full snapshot not returned")
		}
	})
}
