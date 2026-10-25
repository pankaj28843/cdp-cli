package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/webagent/chatgpt"
	"github.com/spf13/cobra"
)

// This is the subscription plan limit displayed in ChatGPT Settings / Usage.
// It is shared by Codex, Work, Workspace Agents and ChatGPT for Excel. It is
// deliberately not described as a ChatGPT conversation or API quota.
const chatgptQuotaScope = "shared_codex_work_workspace_agents_excel_weekly"
const chatgptQuotaSchemaVersion = "chatgpt-plan-quota/v1"

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
	CacheHit         bool              `json:"cache_hit"`
	CacheAgeMS       int64             `json:"cache_age_ms"`
	CacheTTLSeconds  int               `json:"cache_ttl_seconds"`
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

func validQuotaURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "chatgpt.com" &&
		u.Path == "/settings/usage" && u.User == nil
}

func quotaResetDisplay(resetIn time.Duration) string {
	seconds := int64(resetIn / time.Second)
	days := seconds / 86400
	hours := seconds % 86400 / 3600
	minutes := seconds % 3600 / 60
	if days > 0 {
		return fmt.Sprintf("Resets in %dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("Resets in %dh %dm", hours, minutes)
	}
	return fmt.Sprintf("Resets in %dm", minutes)
}

func (a *app) validateQuotaTarget(ctx context.Context, client cdp.CommandClient, targetID string) (string, error) {
	target, err := a.resolvePageTargetWithClient(ctx, client, targetID, "", "")
	if err != nil {
		return "", err
	}
	if !validQuotaURL(target.URL) {
		return "", commandError("chatgpt_quota_wrong_target", "usage", "selected target is not ChatGPT Settings / Usage", ExitUsage, nil)
	}
	return target.TargetID, nil
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
	var establishBeforeReset, refresh bool
	cmd := &cobra.Command{
		Use: "quota", Short: "Read the weekly subscription plan limit from ChatGPT",
		Long: "Read the observed ChatGPT Usage API by direct authenticated GET, using owner-only browser-captured auth evidence. " +
			"Successful readings are cached privately for at most two minutes; --refresh forces a direct provider read. " +
			"This is the shared Codex, Work, Workspace Agents, and ChatGPT for Excel allowance; chat conversations and API quota are excluded. " +
			"The provider returns a Unix-seconds reset_at; reset_timezone is UTC and reset_display is derived from its relative countdown. " +
			"Optional --guard-state stores a private baseline for this session. An absent state requires explicit --establish-before-reset-baseline: stop at or below the before-reset threshold, " +
			"then stop at or below the after-reset threshold after an inferred countdown jump and percentage increase. " +
			"Unknown readings and ambiguous reset evidence fail closed. A normal read does not open, activate, or reload a tab. " +
			"If auth evidence is stale or rejected, run chatgpt auth refresh explicitly, then retry.",
		Example: "  cdp --browser-mode headed workflow agent chatgpt quota --json\n" +
			"  cdp --browser-mode headed workflow agent chatgpt quota --refresh --json\n" +
			"  cdp --browser-mode headed workflow agent chatgpt quota --guard-state ~/.local/state/cdp-cli/my-session-quota.json --establish-before-reset-baseline --before-reset-stop-at 1 --after-reset-stop-at 90 --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if beforeStop < 0 || beforeStop > 100 || afterStop < 0 || afterStop > 100 {
				return commandError("chatgpt_quota_threshold_invalid", "usage", "quota thresholds must be between 0 and 100 percent", ExitUsage, nil)
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 30*time.Second)
			defer cancel()
			if targetID != "" {
				client, closeClient, err := a.browserCDPClient(ctx)
				if err != nil {
					return commandError("chatgpt_quota_browser_unavailable", "connection", "explicit target could not be checked", ExitConnection, nil)
				}
				defer closeClient(context.Background())
				validatedTargetID, err := a.validateQuotaTarget(ctx, client, targetID)
				if err != nil {
					return err
				}
				targetID = validatedTargetID
			}
			stateStore, err := a.stateStore()
			if err != nil {
				return commandError("chatgpt_quota_state_unavailable", "internal", "private quota state is unavailable", ExitInternal, nil)
			}
			authStore, err := chatgpt.NewStore(stateStore.Dir)
			if err != nil {
				return commandError("chatgpt_quota_state_unavailable", "internal", "private ChatGPT auth state is unavailable", ExitInternal, nil)
			}
			lock, err := acquireQuotaCacheLock(ctx, stateStore.Dir)
			if err != nil {
				return commandError("chatgpt_quota_cache_busy", "timeout", "private quota cache is busy", ExitTimeout, nil)
			}
			defer lock.Release()
			template, authStatus, err := authStore.LoadTemplateStatus(ctx, time.Now(), chatgpt.DefaultAuthTTL)
			if err != nil || !authStatus.Ready {
				return commandError("chatgpt_quota_auth_"+authStatus.State, "auth", "ChatGPT read auth is unavailable; run chatgpt auth refresh", ExitCheckFailed, []string{"cdp --browser-mode headed workflow agent chatgpt auth refresh --json"})
			}
			scope, err := quotaCacheScope(a, cmd, stateStore.Dir, template)
			if err != nil {
				return commandError("chatgpt_quota_cache_unknown", "internal", "private quota cache scope is unavailable", ExitInternal, nil)
			}
			var snapshot quotaSnapshot
			var resetIn time.Duration
			var previous *quotaGuardState
			if guardPath != "" {
				previous, err = loadQuotaGuard(guardPath)
				if err != nil {
					return commandError("chatgpt_quota_guard_unknown", "check", "private quota guard state could not be read", ExitCheckFailed, nil)
				}
			}
			cachePath := quotaCachePath(stateStore.Dir)
			if refresh {
				if err := os.Remove(cachePath); err != nil && !errors.Is(err, os.ErrNotExist) {
					return commandError("chatgpt_quota_cache_unknown", "internal", "private quota cache could not be invalidated", ExitInternal, nil)
				}
			} else {
				snapshot, resetIn, _ = loadQuotaCache(cachePath, scope, time.Now().UTC())
				if cachedQuotaBehindGuard(snapshot, previous) {
					snapshot = quotaSnapshot{}
				}
			}
			if !snapshot.OK {
				reading, failure := chatgpt.ReadQuota(ctx, chatgpt.ReadConfig{Store: authStore, BuildCommit: a.build.Commit})
				if failure != nil {
					return commandError(failure.Code, failure.Class, failure.Message, ExitCheckFailed, []string{"cdp --browser-mode headed workflow agent chatgpt auth refresh --json"})
				}
				currentTemplate, currentStatus, err := authStore.LoadTemplateStatus(ctx, time.Now(), chatgpt.DefaultAuthTTL)
				if err != nil || !currentStatus.Ready {
					return commandError("chatgpt_quota_auth_changed", "auth", "ChatGPT auth changed during quota read", ExitCheckFailed, nil)
				}
				currentScope, err := quotaCacheScope(a, cmd, stateStore.Dir, currentTemplate)
				if err != nil || currentScope != scope {
					return commandError("chatgpt_quota_auth_changed", "auth", "ChatGPT auth changed during quota read", ExitCheckFailed, nil)
				}
				resetIn = time.Duration(reading.ResetAfterSeconds) * time.Second
				resetDisplay := quotaResetDisplay(resetIn)
				zone := "UTC"
				resetAt := reading.ResetAt
				snapshot = quotaSnapshot{OK: true, SchemaVersion: chatgptQuotaSchemaVersion, QuotaScope: chatgptQuotaScope,
					RemainingPercent: reading.RemainingPercent, RemainingUnit: "percent", ResetDisplay: resetDisplay,
					ResetAt: &resetAt, ResetTimezone: &zone, ObservedAt: reading.ObservedAt,
					Source:    "direct_chatgpt_usage_api_weekly_window",
					SourceURL: chatgpt.Origin + chatgpt.UsagePath, CacheTTLSeconds: 120}
				if err := saveQuotaCache(cachePath, scope, snapshot, resetIn); err != nil {
					return commandErrorWithData("chatgpt_quota_cache_unknown", "internal", "private quota cache could not be saved", ExitInternal, nil, snapshot)
				}
			}
			snapshot.TargetID = targetID
			if guardPath != "" {
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
	cmd.Flags().StringVar(&targetID, "target", "", "optional exact Settings tab identity check; never replaced")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "bypass the two-minute cache and require a fresh direct provider GET")
	cmd.Flags().StringVar(&guardPath, "guard-state", "", "private per-session JSON guard state path; absent means read-only snapshot")
	cmd.Flags().BoolVar(&establishBeforeReset, "establish-before-reset-baseline", false, "explicitly establish a before-reset phase when private guard state is absent")
	cmd.Flags().IntVar(&beforeStop, "before-reset-stop-at", 1, "stop at or below this remaining percentage before a reset (with --guard-state)")
	cmd.Flags().IntVar(&afterStop, "after-reset-stop-at", 90, "stop at or below this remaining percentage after a reset (with --guard-state)")
	return cmd
}

func cachedQuotaBehindGuard(snapshot quotaSnapshot, previous *quotaGuardState) bool {
	return snapshot.OK && previous != nil && previous.ObservedAt.After(snapshot.ObservedAt)
}
