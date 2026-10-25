package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

// This is the subscription plan limit displayed in ChatGPT Settings / Usage.
// It is shared by Codex, Work, Workspace Agents and ChatGPT for Excel. It is
// deliberately not described as a ChatGPT conversation or API quota.
const chatgptQuotaScope = "shared_codex_work_workspace_agents_excel_weekly"
const chatgptQuotaURL = "https://chatgpt.com/settings/usage?tab=overview"
const chatgptQuotaSchemaVersion = "chatgpt-plan-quota/v1"

var quotaPercentPattern = regexp.MustCompile(`^(\d{1,3})% left$`)
var quotaResetPattern = regexp.MustCompile(`^Resets in (?:(\d+)d )?(?:(\d+)h)?(?:(?: )?(\d+)m)?$`)
var errQuotaAuthRequired = errors.New("Settings tab requires sign-in")

type quotaPageReading struct {
	URL          string  `json:"url"`
	SectionCount int     `json:"section_count"`
	RowCount     int     `json:"row_count"`
	SectionText  string  `json:"section_text"`
	RowText      string  `json:"row_text"`
	TimeOriginMS float64 `json:"time_origin_ms"`
}

type quotaSnapshot struct {
	OK               bool              `json:"ok"`
	SchemaVersion    string            `json:"schema_version"`
	QuotaScope       string            `json:"quota_scope"`
	RemainingPercent int               `json:"remaining_percent"`
	RemainingUnit    string            `json:"remaining_unit"`
	ResetDisplay     string            `json:"reset_display"`
	ResetAt          *time.Time        `json:"reset_at"`
	ResetTimezone    *string           `json:"reset_timezone"`
	ObservedAt       time.Time         `json:"observed_at"`
	Source           string            `json:"source"`
	SourceURL        string            `json:"source_url"`
	TargetID         string            `json:"target_id"`
	TargetCreated    bool              `json:"target_created"`
	Guard            *quotaGuardResult `json:"guard,omitempty"`
}

type quotaGuardState struct {
	SchemaVersion    string    `json:"schema_version"`
	QuotaScope       string    `json:"quota_scope"`
	Phase            string    `json:"phase"`
	ObservedAt       time.Time `json:"observed_at"`
	RemainingPercent int       `json:"remaining_percent"`
	ResetInSeconds   int64     `json:"reset_in_seconds"`
}

type quotaGuardResult struct {
	State                string `json:"state"`
	Phase                string `json:"phase"`
	BeforeResetStopAtPct int    `json:"before_reset_stop_at_percent"`
	AfterResetStopAtPct  int    `json:"after_reset_stop_at_percent"`
	ResetEvidence        string `json:"reset_evidence"`
}

// Only the plan-limit section and its weekly row leave the page. The same
// section's scope sentence is checked before a number is accepted.
const chatgptQuotaExpression = `(() => {
  const sections = [...document.querySelectorAll("section")]
    .filter(s => s.innerText.trim().startsWith("Plan limits\n"));
  const rows = sections.flatMap(s => [...s.querySelectorAll("div")]
    .filter(e => e.innerText.trim().startsWith("Weekly limit\n") &&
      ![...e.children].some(c => c.innerText.trim().startsWith("Weekly limit\n"))));
  return {url: location.href, section_count: sections.length, row_count: rows.length,
    section_text: sections[0]?.innerText.slice(0, 500) ?? "",
    row_text: rows[0]?.innerText.slice(0, 200) ?? "",
    time_origin_ms: performance.timeOrigin};
})()`

func validQuotaURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "chatgpt.com" &&
		u.Path == "/settings/usage" && u.User == nil
}

func quotaAuthURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	return u.Host == "auth.openai.com" ||
		u.Host == "chatgpt.com" && (u.Path == "/auth/login" || u.Path == "/login")
}

func parseQuotaReading(reading quotaPageReading, now time.Time) (quotaSnapshot, time.Duration, error) {
	if !validQuotaURL(reading.URL) || reading.SectionCount != 1 || reading.RowCount != 1 {
		return quotaSnapshot{}, 0, errors.New("unique ChatGPT Settings / Usage weekly limit was not observed")
	}
	if !strings.Contains(reading.SectionText,
		"Shared across Codex, Work, Workspace Agents, and ChatGPT for Excel. Chat conversations are not included.") {
		return quotaSnapshot{}, 0, errors.New("weekly limit scope changed or was not visible")
	}
	lines := strings.Split(strings.TrimSpace(reading.RowText), "\n")
	if len(lines) != 3 || strings.TrimSpace(lines[0]) != "Weekly limit" {
		return quotaSnapshot{}, 0, errors.New("weekly limit row format changed")
	}
	reset := strings.TrimSpace(lines[1])
	matches := quotaResetPattern.FindStringSubmatch(reset)
	if matches == nil {
		return quotaSnapshot{}, 0, errors.New("weekly reset countdown was not understood")
	}
	var seconds int64
	for i, multiplier := range []int64{86400, 3600, 60} {
		if matches[i+1] == "" {
			continue
		}
		n, err := strconv.ParseInt(matches[i+1], 10, 64)
		if err != nil || n > 365 {
			return quotaSnapshot{}, 0, errors.New("weekly reset countdown was invalid")
		}
		seconds += n * multiplier
	}
	if seconds <= 0 || seconds > int64(8*24*time.Hour/time.Second) {
		return quotaSnapshot{}, 0, errors.New("weekly reset countdown was outside a weekly cycle")
	}
	percent := quotaPercentPattern.FindStringSubmatch(strings.TrimSpace(lines[2]))
	if percent == nil {
		return quotaSnapshot{}, 0, errors.New("weekly remaining percentage was not understood")
	}
	remaining, _ := strconv.Atoi(percent[1])
	if remaining > 100 {
		return quotaSnapshot{}, 0, errors.New("weekly remaining percentage was invalid")
	}
	return quotaSnapshot{
		OK: true, SchemaVersion: chatgptQuotaSchemaVersion, QuotaScope: chatgptQuotaScope,
		RemainingPercent: remaining, RemainingUnit: "percent", ResetDisplay: reset,
		// The page gives a rounded relative countdown, not an absolute reset time.
		ResetAt: nil, ResetTimezone: nil, ObservedAt: now.UTC(),
		Source:    "visible_chatgpt_settings_plan_limits_weekly_row",
		SourceURL: chatgptQuotaURL,
	}, time.Duration(seconds) * time.Second, nil
}

func readQuotaPage(ctx context.Context, session *cdp.PageSession) (quotaPageReading, error) {
	result, err := session.Evaluate(ctx, chatgptQuotaExpression, false)
	if err != nil {
		return quotaPageReading{}, err
	}
	if result.Exception != nil {
		return quotaPageReading{}, errors.New("Settings page evaluation failed")
	}
	var reading quotaPageReading
	if err := json.Unmarshal(result.Object.Value, &reading); err != nil {
		return quotaPageReading{}, errors.New("Settings page returned an invalid weekly limit reading")
	}
	return reading, nil
}

func refreshQuotaPage(ctx context.Context, session *cdp.PageSession) (quotaPageReading, error) {
	before, err := readQuotaPage(ctx, session)
	if err != nil {
		return quotaPageReading{}, err
	}
	if !validQuotaURL(before.URL) {
		return quotaPageReading{}, errors.New("target is not ChatGPT Settings / Usage")
	}
	if err := session.Reload(ctx, false); err != nil {
		return quotaPageReading{}, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		reading, err := readQuotaPage(ctx, session)
		if err == nil && reading.TimeOriginMS > before.TimeOriginMS &&
			reading.SectionCount == 1 && reading.RowCount == 1 {
			return reading, nil
		}
		select {
		case <-ctx.Done():
			return quotaPageReading{}, errors.New("fresh weekly limit was not observed before timeout")
		case <-ticker.C:
		}
	}
}

// A newly created target can still be about:blank when Target.createTarget
// returns. Wait for the exact Settings document before requesting a reload.
func waitQuotaPageReady(ctx context.Context, session *cdp.PageSession) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		reading, err := readQuotaPage(ctx, session)
		if err == nil && validQuotaURL(reading.URL) && reading.SectionCount == 1 && reading.RowCount == 1 {
			return nil
		}
		if err == nil && quotaAuthURL(reading.URL) {
			return errQuotaAuthRequired
		}
		select {
		case <-ctx.Done():
			return errors.New("Settings / Usage did not become ready before timeout")
		case <-ticker.C:
		}
	}
}

// selectQuotaTarget only creates a page for the default selection. An explicit
// target is never replaced. Multiple matching tabs select a stable target ID.
func (a *app) selectQuotaTarget(ctx context.Context, client cdp.CommandClient, targetID string) (cdp.TargetInfo, bool, error) {
	if targetID != "" {
		target, err := a.resolvePageTargetWithClient(ctx, client, targetID, "", "")
		if err != nil {
			return cdp.TargetInfo{}, false, err
		}
		if !validQuotaURL(target.URL) {
			return cdp.TargetInfo{}, false, commandError("chatgpt_quota_wrong_target", "usage", "selected target is not ChatGPT Settings / Usage", ExitUsage, nil)
		}
		return target, false, nil
	}
	targets, err := cdp.ListTargetsWithClient(ctx, client)
	if err != nil {
		return cdp.TargetInfo{}, false, commandError("chatgpt_quota_unknown", "check", "Settings tabs could not be listed", ExitCheckFailed, nil)
	}
	var match cdp.TargetInfo
	for _, target := range targets {
		if target.Type != "page" || !validQuotaURL(target.URL) {
			continue
		}
		if match.TargetID != "" && match.TargetID < target.TargetID {
			continue
		}
		match = target
	}
	if match.TargetID != "" {
		return match, false, nil
	}
	id, err := a.createPageTarget(ctx, client, chatgptQuotaURL)
	if err != nil {
		return cdp.TargetInfo{}, false, err
	}
	return cdp.TargetInfo{TargetID: id, Type: "page", URL: chatgptQuotaURL}, true, nil
}

func applyQuotaGuard(current quotaSnapshot, resetIn time.Duration, previous *quotaGuardState, beforeStop, afterStop int) (quotaGuardState, quotaGuardResult, error) {
	next := quotaGuardState{SchemaVersion: chatgptQuotaSchemaVersion, QuotaScope: chatgptQuotaScope,
		Phase: "before_reset", ObservedAt: current.ObservedAt, RemainingPercent: current.RemainingPercent,
		ResetInSeconds: int64(resetIn / time.Second)}
	result := quotaGuardResult{State: "allow", Phase: next.Phase,
		BeforeResetStopAtPct: beforeStop, AfterResetStopAtPct: afterStop, ResetEvidence: "baseline"}
	if previous != nil {
		if previous.SchemaVersion != chatgptQuotaSchemaVersion || previous.QuotaScope != chatgptQuotaScope ||
			previous.ObservedAt.IsZero() || previous.ObservedAt.After(current.ObservedAt) ||
			previous.ObservedAt.Location() != time.UTC ||
			previous.RemainingPercent < 0 || previous.RemainingPercent > 100 ||
			previous.ResetInSeconds <= 0 || previous.ResetInSeconds > int64(8*24*time.Hour/time.Second) ||
			previous.Phase != "before_reset" && previous.Phase != "after_reset" {
			return next, quotaGuardResult{State: "unknown"}, errors.New("guard state was invalid")
		}
		elapsed := current.ObservedAt.Sub(previous.ObservedAt)
		if elapsed > 8*24*time.Hour {
			return next, quotaGuardResult{State: "unknown"}, errors.New("guard observation gap exceeded one weekly cycle")
		}
		expectedRemaining := time.Duration(previous.ResetInSeconds)*time.Second - elapsed
		jump := resetIn - expectedRemaining
		switch {
		case jump > 6*time.Hour && current.RemainingPercent > previous.RemainingPercent:
			next.Phase = "after_reset"
			result.Phase = next.Phase
			result.ResetEvidence = "countdown_jump_and_remaining_increase_inferred"
		case jump > 6*time.Hour || current.RemainingPercent > previous.RemainingPercent:
			return next, quotaGuardResult{State: "unknown"}, errors.New("weekly reset evidence was ambiguous")
		default:
			next.Phase = previous.Phase
			result.Phase = next.Phase
			result.ResetEvidence = "same_cycle"
		}
	}
	threshold := beforeStop
	if next.Phase == "after_reset" {
		threshold = afterStop
	}
	if current.RemainingPercent <= threshold {
		result.State = "stop"
	}
	return next, result, nil
}

func loadQuotaGuard(path string) (*quotaGuardState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state quotaGuardState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveQuotaGuard(path string, state quotaGuardState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".quota-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (a *app) newWorkflowAgentChatGPTQuotaCommand() *cobra.Command {
	var targetID, guardPath string
	var beforeStop, afterStop int
	var establishBeforeReset bool
	cmd := &cobra.Command{
		Use: "quota", Short: "Read the weekly subscription plan limit from a Settings tab",
		Long: "Reuse one ChatGPT Settings / Usage tab or open and keep one if none exists, then reload and read its visible weekly plan limit. " +
			"This is the shared Codex, Work, Workspace Agents, and ChatGPT for Excel allowance; chat conversations and API quota are excluded. " +
			"The page shows only a rounded relative reset countdown, so reset_at and reset_timezone remain null. " +
			"Optional --guard-state stores a private baseline for this session. An absent state requires explicit --establish-before-reset-baseline: stop at or below the before-reset threshold, " +
			"then stop at or below the after-reset threshold after an inferred countdown jump and percentage increase. " +
			"Unknown readings and ambiguous reset evidence fail closed. The command does not send a provider request or close a tab.",
		Example: "  cdp --browser-mode headed workflow agent chatgpt quota --json\n" +
			"  cdp --browser-mode headed workflow agent chatgpt quota --guard-state ~/.local/state/cdp-cli/my-session-quota.json --establish-before-reset-baseline --before-reset-stop-at 1 --after-reset-stop-at 90 --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if beforeStop < 0 || beforeStop > 100 || afterStop < 0 || afterStop > 100 {
				return commandError("chatgpt_quota_threshold_invalid", "usage", "quota thresholds must be between 0 and 100 percent", ExitUsage, nil)
			}
			if !a.selectHeadedProviderRuntime() {
				return commandError("chatgpt_quota_headed_required", "usage", "ChatGPT Settings quota requires the headed browser", ExitUsage, nil)
			}
			ctx, cancel := a.browserCommandContext(cmd)
			defer cancel()
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError("chatgpt_quota_browser_unavailable", "connection", "headed browser is unavailable", ExitConnection, nil)
			}
			defer closeClient(context.Background())
			target, created, err := a.selectQuotaTarget(ctx, client, targetID)
			if err != nil {
				return err
			}
			targetData := map[string]any{"target_id": target.TargetID, "target_created": created, "tab_preserved": true}
			// Chrome can suspend a background Settings tab. Activate only this
			// selected tab so its visible row hydrates before reload.
			if err := cdp.ActivateTargetWithClient(ctx, client, target.TargetID); err != nil {
				return commandErrorWithData("chatgpt_quota_unknown", "check", "Settings tab could not be activated", ExitCheckFailed, nil, targetData)
			}
			session, err := cdp.AttachToTargetWithClient(ctx, client, target.TargetID, nil)
			if err != nil {
				return commandErrorWithData("chatgpt_quota_unknown", "check", "Settings tab could not be attached", ExitCheckFailed, nil, targetData)
			}
			defer session.Close(context.Background())
			if err := waitQuotaPageReady(ctx, session); err != nil {
				if errors.Is(err, errQuotaAuthRequired) {
					return commandErrorWithData("chatgpt_quota_auth_required", "check", err.Error(), ExitCheckFailed, nil, targetData)
				}
				return commandErrorWithData("chatgpt_quota_unknown", "check", err.Error(), ExitCheckFailed, nil, targetData)
			}
			reading, err := refreshQuotaPage(ctx, session)
			if err != nil {
				return commandErrorWithData("chatgpt_quota_unknown", "check", err.Error(), ExitCheckFailed, nil, targetData)
			}
			snapshot, resetIn, err := parseQuotaReading(reading, time.Now())
			if err != nil {
				return commandErrorWithData("chatgpt_quota_unknown", "check", err.Error(), ExitCheckFailed, nil, targetData)
			}
			snapshot.TargetID = target.TargetID
			snapshot.TargetCreated = created
			if guardPath != "" {
				previous, err := loadQuotaGuard(guardPath)
				if err != nil {
					return commandErrorWithData("chatgpt_quota_guard_unknown", "check", "private quota guard state could not be read", ExitCheckFailed, nil, snapshot)
				}
				if previous == nil && !establishBeforeReset {
					return commandErrorWithData("chatgpt_quota_guard_unknown", "check", "missing guard baseline: explicitly establish a before-reset baseline", ExitCheckFailed, nil, snapshot)
				}
				next, decision, err := applyQuotaGuard(snapshot, resetIn, previous, beforeStop, afterStop)
				if err != nil {
					return commandErrorWithData("chatgpt_quota_guard_unknown", "check", err.Error(), ExitCheckFailed, nil, snapshot)
				}
				snapshot.Guard = &decision
				if err := saveQuotaGuard(guardPath, next); err != nil {
					return commandErrorWithData("chatgpt_quota_guard_unknown", "check", "private quota guard state could not be saved", ExitCheckFailed, nil, snapshot)
				}
				if decision.State == "stop" {
					return commandErrorWithData("chatgpt_quota_guard_stop", "check", "weekly subscription quota reached the configured stop threshold", ExitCheckFailed, nil, snapshot)
				}
			}
			return a.render(ctx, fmt.Sprintf("weekly subscription plan limit: %d%% left; %s", snapshot.RemainingPercent, snapshot.ResetDisplay), snapshot)
		},
	}
	cmd.Flags().StringVar(&targetID, "target", "", "exact existing Settings / Usage page target ID; never replaced")
	cmd.Flags().StringVar(&guardPath, "guard-state", "", "private per-session JSON guard state path; absent means read-only snapshot")
	cmd.Flags().BoolVar(&establishBeforeReset, "establish-before-reset-baseline", false, "explicitly establish a before-reset phase when private guard state is absent")
	cmd.Flags().IntVar(&beforeStop, "before-reset-stop-at", 1, "stop at or below this remaining percentage before a reset (with --guard-state)")
	cmd.Flags().IntVar(&afterStop, "after-reset-stop-at", 90, "stop at or below this remaining percentage after a reset (with --guard-state)")
	return cmd
}
