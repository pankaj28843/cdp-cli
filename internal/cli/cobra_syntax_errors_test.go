package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCobraSyntaxErrorsUseUsageExitAndPreserveRequestedJSON(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantJSON   bool
		wantOutput string
	}{
		{
			name:       "plain unknown command",
			args:       []string{"definitely-not-a-command"},
			wantOutput: `unknown command "definitely-not-a-command" for "cdp"`,
		},
		{
			name:     "json unknown command",
			args:     []string{"--json", "definitely-not-a-command"},
			wantJSON: true,
		},
		{
			name:       "plain unknown flag",
			args:       []string{"pages", "--definitely-not-a-flag"},
			wantOutput: "unknown flag: --definitely-not-a-flag",
		},
		{
			name:     "json unknown flag",
			args:     []string{"--json", "pages", "--definitely-not-a-flag"},
			wantJSON: true,
		},
		{
			name:       "plain invalid arity",
			args:       []string{"open"},
			wantOutput: "accepts 1 arg(s), received 0",
		},
		{
			name:     "json invalid arity",
			args:     []string{"--json", "open"},
			wantJSON: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Execute(context.Background(), test.args, &out, &errOut, BuildInfo{})
			if code != ExitUsage {
				t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, ExitUsage, out.String(), errOut.String())
			}
			if test.wantJSON {
				var envelope struct {
					OK       bool   `json:"ok"`
					Code     string `json:"code"`
					ErrClass string `json:"err_class"`
					Message  string `json:"message"`
				}
				if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
					t.Fatalf("stdout is not JSON: %v; stdout=%s stderr=%s", err, out.String(), errOut.String())
				}
				if envelope.OK || envelope.Code != "usage" || envelope.ErrClass != "usage" || envelope.Message == "" {
					t.Fatalf("error envelope = %+v, want typed usage error", envelope)
				}
				if errOut.Len() != 0 {
					t.Fatalf("stderr=%q, want empty for JSON", errOut.String())
				}
				return
			}
			if !strings.Contains(errOut.String(), test.wantOutput) {
				t.Fatalf("stderr=%q, want substring %q", errOut.String(), test.wantOutput)
			}
			if out.Len() != 0 {
				t.Fatalf("stdout=%q, want empty for plain output", out.String())
			}
		})
	}
}

func TestCobraSyntaxClassifierDoesNotRelabelRunErrors(t *testing.T) {
	a := &app{out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	root := a.newRoot()
	root.AddCommand(&cobra.Command{
		Use: "synthetic-internal",
		RunE: func(*cobra.Command, []string) error {
			return errors.New("synthetic internal failure")
		},
	})
	root.SetArgs([]string{"synthetic-internal"})

	err := root.ExecuteContext(context.Background())
	if err == nil || err.Error() != "synthetic internal failure" {
		t.Fatalf("ExecuteContext error = %v, want untouched internal error", err)
	}
	if got := exitCode(err); got != ExitInternal {
		t.Fatalf("internal error exit code = %d, want %d", got, ExitInternal)
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		t.Fatalf("internal error unexpectedly became CommandError: %+v", commandErr)
	}
}

func TestRootWithoutCommandStillPrintsHelpBeforePreflight(t *testing.T) {
	var out, errOut bytes.Buffer
	a := &app{out: &out, err: &errOut}
	root := a.newRoot()
	a.opts.maxTabs = -1
	root.SetArgs([]string{})
	root.SetOut(&out)
	root.SetErr(&errOut)

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("root ExecuteContext error = %v, want help success", err)
	}
	if !strings.Contains(out.String(), "Usage:\n  cdp [command]") {
		t.Fatalf("root help = %q, want command usage", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("root help stderr = %q, want empty", errOut.String())
	}
}
