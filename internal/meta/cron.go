package meta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	cronBeginMarker     = "# cdp-cli maintenance BEGIN"
	cronEndMarker       = "# cdp-cli maintenance END"
	cronTimeout         = 15 * time.Second
	maintenanceLogLimit = 1 << 20
)

type CronUpdateReport struct {
	OK           bool   `json:"ok"`
	State        string `json:"state"`
	DryRun       bool   `json:"dry_run"`
	Installed    bool   `json:"installed"`
	Changed      bool   `json:"changed"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
	ManagedBlock string `json:"managed_block"`
	Error        string `json:"error,omitempty"`
}

type tickLogRecord struct {
	Timestamp    string            `json:"timestamp"`
	OK           bool              `json:"ok"`
	State        string            `json:"state"`
	Count        int               `json:"count"`
	Integrations []tickIntegration `json:"integrations"`
}

type tickIntegration struct {
	ID               string   `json:"id"`
	OK               bool     `json:"ok"`
	State            string   `json:"state"`
	FailedAction     string   `json:"failed_action_id,omitempty"`
	CompletedActions []string `json:"completed_actions"`
}

func cronBlock(state SchedulerState) (string, error) {
	invocation, executableDirectory, err := agentWebInvocation()
	if err != nil {
		return "", err
	}
	pathEntries := []string{
		executableDirectory,
		filepath.Join(mustHomeDirectory(), ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	}
	pathEntries = uniqueStrings(pathEntries)
	statePath, err := schedulerPath()
	if err != nil {
		return "", err
	}
	lines := []string{
		cronBeginMarker,
		"# state: " + displayPath(statePath),
		"PATH=" + strings.Join(pathEntries, ":"),
	}
	enabled := false
	for _, integration := range registry {
		enabled = enabled || state.Enrollments[integration.ID].Enabled
	}
	if enabled {
		quoted := make([]string, 0, len(invocation)+4)
		for _, value := range invocation {
			quoted = append(quoted, shellQuote(value))
		}
		quoted = append(
			quoted,
			"maintenance",
			"cron",
			"tick",
		)
		lines = append(
			lines,
			"* * * * * "+strings.Join(quoted, " ")+" >/dev/null 2>&1",
		)
	}
	lines = append(lines, cronEndMarker)
	return strings.Join(lines, "\n") + "\n", nil
}

func agentWebInvocation() ([]string, string, error) {
	if configured := strings.TrimSpace(
		os.Getenv("CDP_META_BIN"),
	); configured != "" {
		path, err := filepath.Abs(configured)
		if err != nil {
			return nil, "", err
		}
		return metaInvocation(path), filepath.Dir(path), nil
	}
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
		if err == nil {
			name := filepath.Base(executable)
			if name == "agent-web" || name == "cdp" {
				return metaInvocation(executable), filepath.Dir(executable), nil
			}
			sibling := filepath.Join(filepath.Dir(executable), "cdp")
			if info, statErr := os.Lstat(sibling); statErr == nil &&
				info.Mode().IsRegular() &&
				info.Mode().Perm()&0o111 != 0 {
				return []string{sibling, "meta"}, filepath.Dir(sibling), nil
			}
		}
	}
	path, err := exec.LookPath("cdp")
	if err != nil {
		return nil, "", fmt.Errorf(
			"cdp executable is unavailable; run make install",
		)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	return []string{path, "meta"}, filepath.Dir(path), nil
}

func metaInvocation(path string) []string {
	if filepath.Base(path) == "agent-web" {
		return []string{path}
	}
	return []string{path, "meta"}
}

func mustHomeDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/nonexistent"
	}
	return home
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func installCron(
	state SchedulerState,
	dryRun bool,
) (CronUpdateReport, error) {
	block, err := cronBlock(state)
	if err != nil {
		return CronUpdateReport{}, err
	}
	current, err := readCrontab()
	if err != nil {
		return CronUpdateReport{}, err
	}
	updated, err := replaceManagedBlock(current, block)
	if err != nil {
		return CronUpdateReport{}, err
	}
	report := CronUpdateReport{
		OK:           true,
		State:        "preview",
		DryRun:       dryRun,
		Changed:      current != updated,
		BeforeSHA256: textSHA256(current),
		AfterSHA256:  textSHA256(updated),
		ManagedBlock: block,
	}
	if dryRun {
		return report, nil
	}
	if err := writeCrontab(updated); err != nil {
		report.OK = false
		report.State = "error"
		report.Error = err.Error()
		return report, nil
	}
	report.State = "installed"
	report.Installed = true
	return report, nil
}

func uninstallCron(dryRun bool) (CronUpdateReport, error) {
	current, err := readCrontab()
	if err != nil {
		return CronUpdateReport{}, err
	}
	updated, err := removeManagedBlock(current)
	if err != nil {
		return CronUpdateReport{}, err
	}
	report := CronUpdateReport{
		OK:           true,
		State:        "preview",
		DryRun:       dryRun,
		Changed:      current != updated,
		BeforeSHA256: textSHA256(current),
		AfterSHA256:  textSHA256(updated),
		ManagedBlock: "",
	}
	if dryRun {
		return report, nil
	}
	if err := writeCrontab(updated); err != nil {
		report.OK = false
		report.State = "error"
		report.Error = err.Error()
		return report, nil
	}
	report.State = "uninstalled"
	return report, nil
}

func replaceManagedBlock(current string, block string) (string, error) {
	without, err := removeManagedBlock(current)
	if err != nil {
		return "", err
	}
	without = strings.TrimSpace(without)
	block = strings.TrimSpace(block)
	if without == "" {
		return block + "\n", nil
	}
	return without + "\n\n" + block + "\n", nil
}

func removeManagedBlock(current string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(current, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	inside := false
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case cronBeginMarker:
			if inside {
				return "", fmt.Errorf("managed cron block has a nested begin marker")
			}
			inside = true
		case cronEndMarker:
			if !inside {
				return "", fmt.Errorf("managed cron block has an unmatched end marker")
			}
			inside = false
		default:
			if !inside {
				kept = append(kept, line)
			}
		}
	}
	if inside {
		return "", fmt.Errorf("managed cron block has no end marker")
	}
	rendered := strings.TrimSpace(strings.Join(kept, "\n"))
	if rendered == "" {
		return "", nil
	}
	return rendered + "\n", nil
}

func readCrontab() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cronTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "crontab", "-l")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("crontab read timed out")
	}
	if err == nil {
		return stdout.String(), nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		text := strings.ToLower(stderr.String())
		if strings.Contains(text, "no crontab") {
			return "", nil
		}
	}
	return "", fmt.Errorf(
		"read crontab: %s",
		boundedTail(strings.TrimSpace(stderr.String()), commandErrorLimit),
	)
}

func writeCrontab(content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cronTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "crontab", "-")
	command.Stdin = strings.NewReader(content)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		observed, readErr := readCrontab()
		if readErr == nil && observed == content {
			return nil
		}
		return fmt.Errorf(
			"crontab write outcome is unknown and the requested table was not observed",
		)
	}
	if err != nil {
		return fmt.Errorf(
			"write crontab: %s",
			boundedTail(strings.TrimSpace(stderr.String()), commandErrorLimit),
		)
	}
	observed, err := readCrontab()
	if err != nil {
		return fmt.Errorf("verify crontab write: %w", err)
	}
	if observed != content {
		return fmt.Errorf("post-write crontab differs from the requested table")
	}
	return nil
}

func textSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func appendTickLog(report RunReport, runError error) error {
	record := tickLogRecord{
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
		OK:           runError == nil && report.OK,
		State:        report.State,
		Count:        report.Count,
		Integrations: []tickIntegration{},
	}
	if runError != nil {
		record.State = "runner-error"
	}
	for _, result := range report.Results {
		item := tickIntegration{
			ID:               result.IntegrationID,
			OK:               result.OK,
			State:            result.State,
			FailedAction:     result.FailedAction,
			CompletedActions: []string{},
		}
		for _, action := range result.Actions {
			item.CompletedActions = append(
				item.CompletedActions,
				action.ActionID,
			)
		}
		record.Integrations = append(record.Integrations, item)
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	path, err := logPath()
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, statErr := os.Stat(path); statErr == nil &&
		info.Size()+int64(len(line)) > maintenanceLogLimit {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(line); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
