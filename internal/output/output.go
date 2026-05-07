package output

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
)

type Options struct {
	JSON    bool
	JQ      string
	Compact bool
}

type Artifact struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type Envelope struct {
	OK                  bool       `json:"ok"`
	Code                string     `json:"code,omitempty"`
	ErrClass            string     `json:"err_class,omitempty"`
	Message             string     `json:"message,omitempty"`
	Data                any        `json:"data,omitempty"`
	Artifacts           []Artifact `json:"artifacts,omitempty"`
	RemediationCommands []string   `json:"remediation_commands,omitempty"`
	HumanRequired       bool       `json:"human_required,omitempty"`
	AgentShouldStop     bool       `json:"agent_should_stop,omitempty"`
	HumanAction         string     `json:"human_action,omitempty"`
	SafeDiagnostics     []string   `json:"safe_diagnostics,omitempty"`
	ResourceBudget      any        `json:"resource_budget,omitempty"`
}

func Render(ctx context.Context, w io.Writer, opts Options, human string, data any) error {
	if opts.JSON || opts.JQ != "" {
		return renderJSON(ctx, w, opts, data)
	}

	if human == "" {
		return nil
	}
	_, err := fmt.Fprintln(w, human)
	return err
}

func renderJSON(ctx context.Context, w io.Writer, opts Options, data any) error {
	var b []byte
	var err error
	if opts.Compact {
		b, err = json.Marshal(data)
	} else {
		b, err = json.MarshalIndent(data, "", "  ")
	}
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	if opts.JQ != "" {
		b, err = applyJQ(ctx, b, opts.JQ)
		if err != nil {
			return err
		}
		if len(b) == 0 || b[len(b)-1] != '\n' {
			b = append(b, '\n')
		}
		_, err = w.Write(b)
		return err
	}

	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func applyJQ(ctx context.Context, input []byte, expr string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "jq", expr)
	cmd.Stdin = bytes.NewReader(input)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("run jq: %s", bytes.TrimSpace(stderr.Bytes()))
		}
		return nil, fmt.Errorf("run jq: %w", err)
	}

	return out, nil
}
