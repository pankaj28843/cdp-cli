package chatgpt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/pankaj28843/cdp-cli/internal/testsupport"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

func TestAuthStatusHonorsBearerExpiry(t *testing.T) {
	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name          string
		expiry        time.Time
		captureOffset time.Duration
		ready         bool
	}{
		{"expired", now.Add(-time.Minute), 0, false},
		{"expires now", now, 0, false},
		{"expires soon", now.Add(time.Minute), 0, true},
		{"long lived", now.Add(2 * time.Hour), 0, true},
		{"captured six hours ago", now.Add(2 * time.Hour), -6 * time.Hour, true},
		{"future capture", now.Add(2 * time.Hour), 10 * time.Minute, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newReadTestStore(t, now.Add(test.captureOffset))
			template, err := store.LoadTemplate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			template.Headers["authorization"] = "Bearer synthetic." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, test.expiry.Unix()))) + ".synthetic"
			if err := store.SaveTemplate(context.Background(), template); err != nil {
				t.Fatal(err)
			}
			status := store.AuthStatus(context.Background(), now, DefaultAuthTTL)
			if status.Ready != test.ready || status.ExpiresAt != test.expiry.Format(time.RFC3339Nano) || status.Stale != !now.Before(test.expiry) || status.CapturedAt != template.CapturedAt {
				t.Fatalf("auth status = %+v, want ready=%v expiry=%s and unchanged capture time", status, test.ready, test.expiry)
			}
		})
	}
}

func TestAuthStatusPreservesEvidenceLimitsWithoutTokenExpiry(t *testing.T) {
	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	for _, authorization := range []string{
		"Bearer synthetic",
		"Bearer synthetic.!invalid!.synthetic",
		"Bearer synthetic." + base64.RawURLEncoding.EncodeToString([]byte(`{}`)) + ".synthetic",
		"Bearer synthetic." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":"invalid"}`)) + ".synthetic",
	} {
		for _, captureOffset := range []time.Duration{0, -2 * time.Hour, 10 * time.Minute} {
			store := newReadTestStore(t, now.Add(captureOffset))
			template, err := store.LoadTemplate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			template.Headers["authorization"] = authorization
			if err := store.SaveTemplate(context.Background(), template); err != nil {
				t.Fatal(err)
			}
			status := store.AuthStatus(context.Background(), now, DefaultAuthTTL)
			if status.Ready != (captureOffset == 0) || status.ExpiresAt != now.Add(captureOffset+DefaultAuthTTL).Format(time.RFC3339Nano) {
				t.Fatalf("capture offset %s: status = %+v", captureOffset, status)
			}
		}
	}
}

func TestRefreshAuthRequiresNewReadEvidence(t *testing.T) {
	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	for _, evidence := range []string{"missing", "observed", "event burst", "rejected", "foreign session", "missing cookie"} {
		t.Run(evidence, func(t *testing.T) {
			observed := evidence == "observed" || evidence == "event burst"
			capturedAt := now.Add(-2 * time.Hour)
			store := newReadTestStore(t, capturedAt)
			before, err := store.LoadTemplate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			client := newAuthenticatedReadBrowser(func(string, *testsupport.Browser) (any, error) {
				return map[string]any{"signed_in": true, "signed_out": false}, nil
			})
			switch evidence {
			case "event burst":
				burst := make([]cdp.Event, 300)
				for index := range burst {
					burst[index] = cdp.Event{SessionID: "session-owned-1", Method: "Network.dataReceived"}
				}
				client.events = append(burst, client.events...)
			case "missing":
				client.events = nil
			case "rejected":
				client.events[1].Params = json.RawMessage(strings.Replace(string(client.events[1].Params), `"status":200`, `"status":401`, 1))
			case "foreign session":
				for index := range client.events {
					client.events[index].SessionID = "session-user-page"
				}
			case "missing cookie":
				client.events[0].Params = json.RawMessage(strings.Replace(string(client.events[0].Params), `"cookie":`, `"not-cookie":`, 1))
			}
			engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
			if err != nil {
				t.Fatal(err)
			}
			result := RefreshAuth(context.Background(), AuthRefreshConfig{
				BrowserConfig:      BrowserConfig{Client: client, Engine: engine, Journal: journal},
				Store:              store,
				ObservationTimeout: 150 * time.Millisecond,
				Now:                func() time.Time { return now },
			})
			if result.OK != observed || result.Cleanup.State != webagent.CleanupClosed {
				t.Fatalf("refresh = %+v, want success=%v with owned target closed", result, observed)
			}
			if !observed && (result.Error == nil || result.Error.Code != "chatgpt_read_request_not_observed") {
				t.Fatalf("missing request error = %+v", result.Error)
			}
			template, err := store.LoadTemplate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if observed {
				capturedAt = now
			}
			if template.CapturedAt != capturedAt.Format(time.RFC3339Nano) {
				t.Fatalf("capture time = %s, want %s", template.CapturedAt, capturedAt)
			}
			if !observed {
				beforeJSON, _ := json.Marshal(before)
				afterJSON, _ := json.Marshal(template)
				if string(beforeJSON) != string(afterJSON) {
					t.Fatal("unsuccessful refresh changed the stored template")
				}
			}
		})
	}
}

// A reload cancels the page's in-flight read. The native read can need eight
// seconds, so dividing a twelve-second total into three stages loses it.
func TestRefreshAuthWaitsForSlowNativeReadBeforeReload(t *testing.T) {
	client := &slowAuthReadBrowser{
		authenticatedReadBrowser: newAuthenticatedReadBrowser(func(string, *testsupport.Browser) (any, error) {
			return map[string]any{"signed_in": true, "signed_out": false}, nil
		}),
	}
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	store := newReadTestStore(t, time.Now().Add(-2*time.Hour))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result := RefreshAuth(ctx, AuthRefreshConfig{
		BrowserConfig: BrowserConfig{Client: client, Engine: engine, Journal: journal},
		Store:         store,
	})
	if !result.OK || result.Cleanup.State != webagent.CleanupClosed {
		t.Fatalf("slow native read refresh = %+v", result)
	}
	_, _, reloads, _, _, _, _ := client.Snapshot()
	if len(reloads) != 0 {
		t.Fatalf("interrupted the native read with %d reloads", len(reloads))
	}
	template, err := store.LoadTemplate(ctx)
	if err != nil || template.Source != "headed-cdp-observed-read-request" {
		t.Fatalf("native read template source = %q, error = %v", template.Source, err)
	}
}

type slowAuthReadBrowser struct {
	*authenticatedReadBrowser
	readyAt time.Time
}

func TestRefreshAuthCapturesCookiesAfterNativeRead(t *testing.T) {
	client := &rotatingAuthReadBrowser{newAuthenticatedReadBrowser(func(string, *testsupport.Browser) (any, error) {
		return map[string]any{"signed_in": true, "signed_out": false}, nil
	})}
	engine, journal, err := testsupport.NewRuntime(t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	store := newReadTestStore(t, time.Now().Add(-2*time.Hour))
	result := RefreshAuth(context.Background(), AuthRefreshConfig{
		BrowserConfig:      BrowserConfig{Client: client, Engine: engine, Journal: journal},
		Store:              store,
		ObservationTimeout: time.Second,
	})
	if !result.OK || result.Cleanup.State != webagent.CleanupClosed {
		t.Fatalf("rotating session refresh = %+v", result)
	}
	template, err := store.LoadTemplate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if template.Cookies["__Secure-next-auth.session-token"] != "after-native-read" {
		t.Fatal("refresh saved the session cookie from before the native read")
	}
	if template.CookieHeader != "__Secure-next-auth.session-token=test-only" {
		t.Fatal("refresh changed the literal observed Cookie header")
	}
	if transcriptionCookieHeader(template) != "__Secure-next-auth.session-token=after-native-read" {
		t.Fatal("direct replay did not use the final session cookie")
	}
}

type rotatingAuthReadBrowser struct{ *authenticatedReadBrowser }

func (b *rotatingAuthReadBrowser) CallSession(ctx context.Context, sessionID, method string, params, result any) error {
	if method == "Network.getCookies" && len(b.events) == 0 {
		return json.Unmarshal([]byte(`{"cookies":[{"name":"__Secure-next-auth.session-token","value":"after-native-read"}]}`), result)
	}
	return b.authenticatedReadBrowser.CallSession(ctx, sessionID, method, params, result)
}

func (b *slowAuthReadBrowser) CallSession(ctx context.Context, sessionID, method string, params, result any) error {
	if method == "Page.navigate" || method == "Page.reload" {
		b.readyAt = time.Now().Add(8200 * time.Millisecond)
	}
	return b.authenticatedReadBrowser.CallSession(ctx, sessionID, method, params, result)
}

func (b *slowAuthReadBrowser) ReadEvent(ctx context.Context) (cdp.Event, error) {
	timer := time.NewTimer(time.Until(b.readyAt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return cdp.Event{}, ctx.Err()
	case <-timer.C:
		return b.authenticatedReadBrowser.ReadEvent(ctx)
	}
}
