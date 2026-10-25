package chatgpt

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

// UsagePath was observed as the Settings / Usage plan-limit request.
const UsagePath = "/backend-api/wham/usage"

type QuotaReading struct {
	UsedPercent       int
	RemainingPercent  int
	ResetAfterSeconds int64
	ResetAt           time.Time
	ObservedAt        time.Time
	AccountID         string
}

type QuotaFailure struct {
	Code    string
	Class   string
	Message string
}

// ReadQuota performs one authenticated, same-origin GET with the existing
// owner-only request template. It never opens a browser or falls back to UI.
func ReadQuota(ctx context.Context, config ReadConfig) (QuotaReading, *QuotaFailure) {
	template, failure := loadFreshReadTemplate(ctx, config)
	if failure != nil {
		return QuotaReading{}, quotaFailure(failure)
	}
	request, err := newChatGPTRequest(ctx, template, Origin+UsagePath, UsagePath)
	if err != nil {
		return QuotaReading{}, &QuotaFailure{"chatgpt_quota_request_invalid", "internal", "ChatGPT usage request could not be prepared"}
	}
	response, failure := doChatGPTRequest(config, request)
	if failure != nil {
		return QuotaReading{}, quotaFailure(failure)
	}
	defer response.Body.Close()
	var payload struct {
		AccountID string `json:"account_id"`
		RateLimit struct {
			PrimaryWindow   *quotaWindow `json:"primary_window"`
			SecondaryWindow *quotaWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := decodeBoundedJSON(response.Body, &payload); err != nil {
		return QuotaReading{}, &QuotaFailure{"chatgpt_quota_invalid_response", "provider", "ChatGPT usage response was incomplete or invalid"}
	}
	var weekly []*quotaWindow
	for _, window := range []*quotaWindow{payload.RateLimit.PrimaryWindow, payload.RateLimit.SecondaryWindow} {
		if window == nil {
			continue
		}
		length, err := window.LimitWindowSeconds.Int64()
		if err != nil {
			return QuotaReading{}, &QuotaFailure{"chatgpt_quota_invalid_response", "provider", "ChatGPT usage window length was invalid"}
		}
		if length == 7*24*60*60 {
			weekly = append(weekly, window)
		}
	}
	if len(weekly) != 1 {
		return QuotaReading{}, &QuotaFailure{"chatgpt_quota_invalid_response", "provider", "ChatGPT weekly usage window was missing or ambiguous"}
	}
	window := weekly[0]
	used, usedErr := strconv.Atoi(window.UsedPercent.String())
	remaining, remainingErr := window.ResetAfterSeconds.Int64()
	resetUnix, resetErr := window.ResetAt.Int64()
	observed := nowForRead(config).UTC()
	reset := time.Unix(resetUnix, 0).UTC()
	var capturedAccountID string
	for name, value := range template.Headers {
		if strings.EqualFold(name, "chatgpt-account-id") {
			capturedAccountID = value
		}
	}
	if payload.AccountID == "" || capturedAccountID != "" && capturedAccountID != payload.AccountID ||
		usedErr != nil || used < 0 || used > 100 ||
		remainingErr != nil || remaining <= 0 || remaining > 7*24*60*60 ||
		resetErr != nil || math.Abs(reset.Sub(observed.Add(time.Duration(remaining)*time.Second)).Seconds()) > 120 {
		return QuotaReading{}, &QuotaFailure{"chatgpt_quota_invalid_response", "provider", "ChatGPT weekly usage fields were missing or inconsistent"}
	}
	return QuotaReading{UsedPercent: used, RemainingPercent: 100 - used,
		ResetAfterSeconds: remaining, ResetAt: reset, ObservedAt: observed,
		AccountID: payload.AccountID}, nil
}

type quotaWindow struct {
	UsedPercent        json.Number `json:"used_percent"`
	LimitWindowSeconds json.Number `json:"limit_window_seconds"`
	ResetAfterSeconds  json.Number `json:"reset_after_seconds"`
	ResetAt            json.Number `json:"reset_at"`
}

func quotaFailure(failure *readFailure) *QuotaFailure {
	if failure == nil {
		return nil
	}
	return &QuotaFailure{Code: failure.code, Class: failure.errClass, Message: failure.message}
}
