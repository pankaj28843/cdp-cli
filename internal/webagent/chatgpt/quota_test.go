package chatgpt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReadQuotaUsesObservedGETAndValidatesWeeklyWindow(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	store := newReadTestStore(t, now)
	reset := now.Add(6*24*time.Hour + 18*time.Hour)
	responseBody := fmt.Sprintf(`{"account_id":"synthetic-account","rate_limit":{"primary_window":{"used_percent":5,"limit_window_seconds":604800,"reset_after_seconds":583200,"reset_at":%d},"secondary_window":null}}`, reset.Unix())
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != Origin+UsagePath ||
			request.Header.Get("Authorization") != "Bearer test-only" ||
			request.Header.Get("Cookie") != "__Secure-next-auth.session-token=test-only" {
			t.Fatalf("unexpected authenticated quota request: %s %s", request.Method, request.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(responseBody))}, nil
	})}
	reading, failure := ReadQuota(context.Background(), ReadConfig{Store: store, HTTPClient: client, Now: func() time.Time { return now }})
	if failure != nil || reading.RemainingPercent != 95 || reading.ResetAfterSeconds != 583200 || !reading.ResetAt.Equal(reset) {
		t.Fatalf("reading=%+v failure=%+v", reading, failure)
	}
	responseBody = strings.Replace(responseBody, `"limit_window_seconds":604800`, `"limit_window_seconds":3600`, 1)
	if _, failure = ReadQuota(context.Background(), ReadConfig{Store: store, HTTPClient: client, Now: func() time.Time { return now }}); failure == nil || failure.Code != "chatgpt_quota_invalid_response" {
		t.Fatalf("nonweekly window accepted: %+v", failure)
	}
	responseBody = fmt.Sprintf(`{"account_id":"synthetic-account","rate_limit":{"primary_window":{"used_percent":50,"limit_window_seconds":3600},"secondary_window":{"used_percent":5,"limit_window_seconds":604800,"reset_after_seconds":583200,"reset_at":%d}}}`, reset.Unix())
	if reading, failure = ReadQuota(context.Background(), ReadConfig{Store: store, HTTPClient: client, Now: func() time.Time { return now }}); failure != nil || reading.RemainingPercent != 95 {
		t.Fatalf("unique secondary weekly window was not selected: %+v %+v", reading, failure)
	}
	responseBody = fmt.Sprintf(`{"account_id":"synthetic-account","rate_limit":{"primary_window":{"used_percent":5,"limit_window_seconds":604800,"reset_after_seconds":583200,"reset_at":%d},"secondary_window":{"used_percent":5,"limit_window_seconds":604800,"reset_after_seconds":583200,"reset_at":%d}}}`, reset.Unix(), reset.Unix())
	if _, failure = ReadQuota(context.Background(), ReadConfig{Store: store, HTTPClient: client, Now: func() time.Time { return now }}); failure == nil || failure.Code != "chatgpt_quota_invalid_response" {
		t.Fatalf("ambiguous weekly windows accepted: %+v", failure)
	}
	responseBody = fmt.Sprintf(`{"account_id":"synthetic-account","rate_limit":{"primary_window":{"used_percent":5,"limit_window_seconds":604800,"reset_after_seconds":583200,"reset_at":%d}}}`, reset.Unix())
	template, err := store.LoadTemplate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	template.Headers["chatgpt-account-id"] = "different-account"
	if err := store.SaveTemplate(context.Background(), template); err != nil {
		t.Fatal(err)
	}
	if _, failure = ReadQuota(context.Background(), ReadConfig{Store: store, HTTPClient: client, Now: func() time.Time { return now }}); failure == nil || failure.Code != "chatgpt_quota_invalid_response" {
		t.Fatalf("cross-account quota was accepted: %+v", failure)
	}
}

func TestReadQuotaPreservesTypedAuthenticationFailure(t *testing.T) {
	now := time.Now().UTC()
	store := newReadTestStore(t, now)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	_, failure := ReadQuota(context.Background(), ReadConfig{Store: store, HTTPClient: client, Now: func() time.Time { return now }})
	if failure == nil || failure.Code != "chatgpt_browser_context_required" || failure.Class != "auth" {
		t.Fatalf("401 lost typed auth boundary: %+v", failure)
	}
}
