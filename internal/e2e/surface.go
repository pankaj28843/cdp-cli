package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"time"
)

const (
	commandTimeout = 90 * time.Second
	interruptGrace = 30 * time.Second
	terminateGrace = 5 * time.Second
	outputLimit    = 4 << 20
)

type SurfaceResult struct {
	Provider   string   `json:"provider"`
	Command    []string `json:"command"`
	ReturnCode int      `json:"returncode"`
	JSONObject bool     `json:"json_object"`
	OK         bool     `json:"ok"`
	ErrorType  string   `json:"error_type,omitempty"`
}

type Report struct {
	OK                   bool            `json:"ok"`
	CommandsOK           bool            `json:"commands_ok"`
	TabSetPreserved      bool            `json:"tab_set_preserved"`
	BeforeTabCount       int             `json:"before_tab_count"`
	AfterTabCount        int             `json:"after_tab_count"`
	LeakedTargetIDs      []string        `json:"leaked_target_ids"`
	DisappearedTargetIDs []string        `json:"disappeared_target_ids"`
	Budget               map[string]any  `json:"budget,omitempty"`
	Results              []SurfaceResult `json:"results"`
}

type commandOutcome struct {
	returnCode int
	payload    map[string]any
}

type commandRunner func(
	context.Context,
	[]string,
	time.Duration,
) commandOutcome

type providerSurface struct {
	id       string
	commands [][]string
}

func Run(ctx context.Context) (Report, error) {
	return run(ctx, runJSONCommand)
}

func run(ctx context.Context, runner commandRunner) (Report, error) {
	beforeIDs, beforeBudget, err := pageIDs(ctx, runner)
	if err != nil {
		return Report{}, err
	}
	results := make([]SurfaceResult, 0)
	for _, provider := range providerSurfaces() {
		for _, command := range provider.commands {
			outcome := runner(ctx, command, commandTimeout)
			ok, _ := outcome.payload["ok"].(bool)
			errorType, _ := outcome.payload["error_type"].(string)
			results = append(results, SurfaceResult{
				Provider:   provider.id,
				Command:    slices.Clone(command[1:]),
				ReturnCode: outcome.returnCode,
				JSONObject: outcome.payload != nil,
				OK:         ok,
				ErrorType:  errorType,
			})
		}
	}
	afterIDs, afterBudget, err := pageIDs(ctx, runner)
	if err != nil {
		return Report{}, err
	}
	leaked := setDifference(afterIDs, beforeIDs)
	disappeared := setDifference(beforeIDs, afterIDs)
	commandsOK := true
	for _, result := range results {
		commandsOK = commandsOK &&
			result.ReturnCode == 0 &&
			result.JSONObject &&
			result.OK
	}
	budget := afterBudget
	if budget == nil {
		budget = beforeBudget
	}
	return Report{
		OK:                   commandsOK && len(leaked) == 0 && len(disappeared) == 0,
		CommandsOK:           commandsOK,
		TabSetPreserved:      len(leaked) == 0 && len(disappeared) == 0,
		BeforeTabCount:       len(beforeIDs),
		AfterTabCount:        len(afterIDs),
		LeakedTargetIDs:      leaked,
		DisappearedTargetIDs: disappeared,
		Budget:               budget,
		Results:              results,
	}, nil
}

func providerSurfaces() []providerSurface {
	return []providerSurface{
		standardSurface("alex"),
		standardSurface("chatgpt"),
		standardSurface("gemini"),
		standardSurface("perplexity"),
		standardSurface("claude"),
		standardSurface("grok"),
		standardSurface("tripadvisor"),
	}
}

func standardSurface(provider string) providerSurface {
	return providerSurface{
		id: provider,
		commands: [][]string{
			{"cdp", "--browser-mode", "headed", "workflow", "agent", provider, "capabilities", "--json"},
		},
	}
}

func pageIDs(
	ctx context.Context,
	runner commandRunner,
) (map[string]struct{}, map[string]any, error) {
	outcome := runner(ctx, []string{
		"cdp",
		"--browser-mode",
		"headed",
		"pages",
		"--limit",
		"0",
		"--compact",
		"--json",
	}, commandTimeout)
	ok, _ := outcome.payload["ok"].(bool)
	if outcome.returnCode != 0 || !ok {
		return nil, nil, fmt.Errorf(
			"headed CDP pages health check failed",
		)
	}
	rawPages, valid := outcome.payload["pages"].([]any)
	if !valid {
		return nil, nil, fmt.Errorf(
			"headed CDP pages response omitted pages",
		)
	}
	if len(rawPages) == 0 {
		return nil, nil, fmt.Errorf(
			"headed CDP pages health check found no open tabs",
		)
	}
	ids := make(map[string]struct{}, len(rawPages))
	for _, raw := range rawPages {
		page, valid := raw.(map[string]any)
		if !valid {
			continue
		}
		id, _ := page["id"].(string)
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	budget, _ := outcome.payload["budget"].(map[string]any)
	return ids, budget, nil
}

func setDifference(
	left map[string]struct{},
	right map[string]struct{},
) []string {
	values := make([]string, 0)
	for value := range left {
		if _, exists := right[value]; !exists {
			values = append(values, value)
		}
	}
	slices.Sort(values)
	return values
}

func runJSONCommand(
	ctx context.Context,
	command []string,
	timeout time.Duration,
) commandOutcome {
	process := exec.Command(command[0], command[1:]...)
	configureOwnedProcess(process)
	var stdout, stderr boundedBuffer
	stdout.limit = outputLimit
	stderr.limit = outputLimit
	process.Stdout = &stdout
	process.Stderr = &stderr
	if err := process.Start(); err != nil {
		return commandOutcome{
			returnCode: 127,
			payload: failurePayload(
				"command-unavailable",
				"command could not start",
			),
		}
	}
	waited := make(chan error, 1)
	go func() {
		waited <- process.Wait()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-timer.C:
		stopOwnedProcess(process, waited)
		return commandOutcome{
			returnCode: 124,
			payload: failurePayload(
				"timeout",
				fmt.Sprintf(
					"command exceeded %s harness deadline",
					timeout,
				),
			),
		}
	case <-ctx.Done():
		stopOwnedProcess(process, waited)
		return commandOutcome{
			returnCode: 130,
			payload:    failurePayload("interrupted", "surface verification interrupted"),
		}
	}
	code := 0
	if process.ProcessState != nil {
		code = process.ProcessState.ExitCode()
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if stdout.truncated || decoder.Decode(&payload) != nil ||
		decoder.Decode(new(any)) != io.EOF {
		return commandOutcome{
			returnCode: code,
			payload: failurePayload(
				"invalid-json",
				"command did not return one bounded JSON object",
			),
		}
	}
	if waitErr != nil && code == 0 {
		code = 1
	}
	return commandOutcome{returnCode: code, payload: payload}
}

func stopOwnedProcess(
	process *exec.Cmd,
	waited <-chan error,
) {
	signalOwnedProcess(process, interruptSignal())
	timer := time.NewTimer(interruptGrace)
	select {
	case <-waited:
		timer.Stop()
		return
	case <-timer.C:
	}
	signalOwnedProcess(process, terminateSignal())
	timer = time.NewTimer(terminateGrace)
	select {
	case <-waited:
		timer.Stop()
		return
	case <-timer.C:
	}
	signalOwnedProcess(process, killSignal())
	<-waited
}

func failurePayload(errorType string, message string) map[string]any {
	return map[string]any{
		"ok":         false,
		"error_type": errorType,
		"message":    message,
	}
}

type boundedBuffer struct {
	data      bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := buffer.limit - buffer.data.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
			buffer.truncated = true
		}
		_, _ = buffer.data.Write(value)
	} else if original > 0 {
		buffer.truncated = true
	}
	return original, nil
}

func (buffer *boundedBuffer) Bytes() []byte {
	return buffer.data.Bytes()
}
