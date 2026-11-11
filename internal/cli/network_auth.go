package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

// httpAuthOrigin accepts only an origin, never userinfo, paths or query data.
func httpAuthOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", fmt.Errorf("HTTP auth requires an explicit http(s) origin without userinfo, path, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if u.Scheme == "http" && host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", fmt.Errorf("HTTP auth requires HTTPS except for loopback fixtures")
		}
	}
	if host == "" {
		return "", fmt.Errorf("HTTP auth origin requires a host")
	}
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("HTTP auth origin requires a valid port")
		}
	}
	if port != "" && !((u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443")) {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}

	return u.Scheme + "://" + host, nil
}
func httpAuthURLOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return ""
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	s, _ := httpAuthOrigin(u.String())
	return s
}

func (a *app) newNetworkAuthCommand() *cobra.Command {
	var origin, userEnv, passEnv, target, scheme string
	var duration time.Duration
	var reload, popupPrearm bool
	var readyFile string
	cmd := &cobra.Command{Use: "auth", Short: "Temporarily answer origin-bound HTTP Basic or opt-in NTLM challenges using named environment variables", Long: "Answer only server challenges for the selected scheme and one explicit origin and target through the daemon. The default scheme is Basic; NTLM requires explicit --scheme ntlm. This flag permits a challenge scheme; it does not force Chromium to select NTLM when a server also offers Negotiate. Rejecting a different scheme cancels authentication rather than requesting a fallback. Chrome performs NTLM negotiation with the environment-provided username and password; integrated authentication or Kerberos is not configured by this command. Credentials are read from named environment variables at each permitted challenge and sent only through local daemon IPC to Chrome. No credentials enter command arguments, output or cdp state. Chrome may cache accepted credentials for its browser session. Other origins, proxy challenges, unsupported schemes and repeated challenges are canceled. The result reports interception actions, not authentication or application health.", Example: "  cdp network auth --origin https://example.test --username-env SITE_USER --password-env SITE_PASSWORD --target <id> --reload --duration 5s --json\n  cdp network auth --scheme ntlm --origin https://example.test --username-env SITE_USER --password-env SITE_PASSWORD --target <id> --reload --duration 5s --json\n  cdp network auth --popup-prearm --origin https://example.test --username-env SITE_USER --password-env SITE_PASSWORD --target <owned-root> --ready-file <private-path> --duration 2m --json\n  cdp schema network-auth --json", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		normalized, err := httpAuthOrigin(origin)
		scheme = strings.ToLower(scheme)
		envName := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
		if err != nil || (scheme != "basic" && scheme != "ntlm") || !envName.MatchString(userEnv) || !envName.MatchString(passEnv) || strings.TrimSpace(target) == "" || duration <= 0 || duration > 5*time.Minute || (reload && popupPrearm) {
			return commandError("usage", "usage", "--origin, --target and valid --username-env/--password-env names are required; --scheme must be basic or ntlm; --duration must be positive and at most 5m; --popup-prearm cannot be combined with --reload", ExitUsage, nil)
		}
		ctx, cancel := a.commandContextWithDefault(cmd, duration+10*time.Second)
		defer cancel()
		client, session, selected, err := a.attachPageEventSessionWithIndex(ctx, target, "", "", 0)
		if err != nil {
			return err
		}
		closeSession := func() bool { return closeHTTPAuthSession(session.Close) }
		defer closeSession()
		// Subscribe to every request so redirected and proxy challenges are canceled too.
		if err = client.CallSession(ctx, session.SessionID, "Fetch.enable", map[string]any{"handleAuthRequests": true, "patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}}}, nil); err != nil {
			return commandError("http_auth_failed", "connection", "enable HTTP auth interception failed", ExitConnection, nil)
		}
		disabled := false
		cleanup := func() bool {
			c, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			return client.CallSession(c, session.SessionID, "Fetch.disable", map[string]any{}, nil) == nil
		}
		defer func() {
			if !disabled {
				cleanup()
			}
		}()
		if reload {
			if client.CallSession(ctx, session.SessionID, "Page.reload", map[string]any{}, nil) != nil {
				return commandError("http_auth_failed", "connection", "reload during HTTP auth interception failed", ExitConnection, nil)
			}
		}
		if popupPrearm {
			supervisor := newHTTPAuthPopupSupervisor(client, selected.TargetID, session.SessionID)
			defer supervisor.cleanup()
			if err := supervisor.enable(ctx); err != nil {
				return commandError("http_auth_failed", "connection", "enable owned-popup supervision failed", ExitConnection, nil)
			}
			removeReady, err := publishCollectorReadiness(readyFile, selected.TargetID, session.SessionID, []string{"Fetch", "Debugger"})
			if err != nil {
				return collectorReadinessError(err)
			}
			defer removeReady()
			actions, collectErr := supervisor.collect(ctx, duration, normalized, scheme, userEnv, passEnv, os.LookupEnv)
			popupClean := supervisor.cleanup()
			disabled = cleanup()
			detached := closeSession()
			report := map[string]any{"ok": collectErr == nil && popupClean && disabled && detached, "target_id": selected.TargetID, "actions": actions, "cleanup_complete": popupClean && disabled && detached, "bounded": true, "popup_prearm": true}
			if collectErr != nil {
				return commandErrorWithData("http_auth_failed", "connection", collectErr.Error(), ExitConnection, nil, report)
			}
			if !popupClean || !disabled || !detached {
				return commandErrorWithData("http_auth_cleanup_failed", "connection", "popup authentication cleanup failed", ExitConnection, nil, report)
			}
			return a.render(ctx, "Owned-popup HTTP auth interception completed", report)
		}
		removeReady, readyErr := publishCollectorReadiness(readyFile, selected.TargetID, session.SessionID, []string{"Fetch"})
		if readyErr != nil {
			return collectorReadinessError(readyErr)
		}
		defer removeReady()
		actions, collectErr := collectHTTPAuth(ctx, client, session.SessionID, duration, normalized, scheme, userEnv, passEnv, os.LookupEnv)
		disabled = cleanup()
		detached := closeSession()
		report := map[string]any{"ok": collectErr == nil && disabled && detached, "target_id": selected.TargetID, "actions": actions, "cleanup_complete": disabled && detached, "bounded": true}
		if collectErr != nil {
			return commandErrorWithData("http_auth_failed", "connection", collectErr.Error(), ExitConnection, nil, report)
		}
		if !disabled || !detached {
			return commandErrorWithData("http_auth_cleanup_failed", "connection", "HTTP auth interception cleanup failed", ExitConnection, nil, report)
		}
		return a.render(ctx, "HTTP auth interception completed", report)
	}}
	cmd.Flags().StringVar(&scheme, "scheme", "basic", "allowed server authentication scheme: basic or explicit ntlm")
	cmd.Flags().StringVar(&origin, "origin", "", "exact HTTPS origin (HTTP allowed only on loopback) allowed to receive credentials")
	cmd.Flags().StringVar(&userEnv, "username-env", "", "name of the environment variable containing the username")
	cmd.Flags().StringVar(&passEnv, "password-env", "", "name of the environment variable containing the password")
	cmd.Flags().StringVar(&target, "target", "", "required exact page target id or unique prefix")
	cmd.Flags().DurationVar(&duration, "duration", 5*time.Second, "bounded interception window (maximum 5m)")
	cmd.Flags().BoolVar(&reload, "reload", false, "reload the selected target after interception is enabled")
	cmd.Flags().BoolVar(&popupPrearm, "popup-prearm", false, "pre-arm one owned popup by pausing only its opener during a one-use window.open handshake; incompatible with --reload")
	cmd.Flags().StringVar(&readyFile, "ready-file", "", "exclusive owner-only readiness artifact after interception and optional popup supervision are enabled")
	return cmd
}

func collectHTTPAuth(ctx context.Context, client browserEventClient, session string, duration time.Duration, origin, scheme, userEnv, passEnv string, lookup func(string) (string, bool)) (map[string]int, error) {
	state := newHTTPAuthState(origin, scheme, userEnv, passEnv, lookup)
	counts := state.counts
	eventCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	for {
		event, err := client.ReadSessionEvent(eventCtx, session)
		if err != nil {
			if ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(eventCtx.Err(), context.DeadlineExceeded)) {
				return counts, nil
			}
			return counts, fmt.Errorf("HTTP auth event observation failed")
		}
		if event.SessionID != session {
			continue
		}
		if err := state.handle(eventCtx, client, session, event); err != nil {
			return counts, err
		}
	}
}

// Session teardown uses an independent bounded context even after command expiry.
func closeHTTPAuthSession(close func(context.Context) error) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return close(ctx) == nil
}

type httpAuthState struct {
	origin, scheme, userEnv, passEnv string
	lookup                           func(string) (string, bool)
	counts                           map[string]int
	attempted                        map[string]bool
}

func newHTTPAuthState(origin, scheme, userEnv, passEnv string, lookup func(string) (string, bool)) *httpAuthState {
	return &httpAuthState{origin: origin, scheme: scheme, userEnv: userEnv, passEnv: passEnv, lookup: lookup, counts: map[string]int{"provided": 0, "canceled": 0, "continued": 0}, attempted: map[string]bool{}}
}

func (s *httpAuthState) handle(ctx context.Context, client browserEventClient, session string, event cdp.Event) error {
	if event.Method != "Fetch.authRequired" && event.Method != "Fetch.requestPaused" {
		return nil
	}
	var p struct {
		RequestID string `json:"requestId"`
		Request   struct {
			URL string `json:"url"`
		} `json:"request"`
		Challenge struct {
			Origin string `json:"origin"`
			Source string `json:"source"`
			Scheme string `json:"scheme"`
		} `json:"authChallenge"`
	}
	if json.Unmarshal(event.Params, &p) != nil || p.RequestID == "" {
		return fmt.Errorf("invalid HTTP auth interception event")
	}
	if event.Method == "Fetch.requestPaused" {
		if client.CallSession(ctx, session, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID}, nil) != nil {
			return fmt.Errorf("continue intercepted request failed")
		}
		s.counts["continued"]++
		return nil
	}
	response := map[string]any{"response": "CancelAuth"}
	action := "canceled"
	challengeOrigin, _ := httpAuthOrigin(p.Challenge.Origin)
	if p.Challenge.Source == "Server" && strings.EqualFold(p.Challenge.Scheme, s.scheme) && challengeOrigin == s.origin && httpAuthURLOrigin(p.Request.URL) == s.origin && !s.attempted[session+":"+p.RequestID] && s.counts["provided"] < 16 {
		s.attempted[session+":"+p.RequestID] = true
		username, userOK := s.lookup(s.userEnv)
		password, passOK := s.lookup(s.passEnv)
		if !userOK || !passOK || username == "" || password == "" {
			_ = client.CallSession(ctx, session, "Fetch.continueWithAuth", map[string]any{"requestId": p.RequestID, "authChallengeResponse": response}, nil)
			return fmt.Errorf("HTTP auth credential environment variable is unset or empty")
		}
		response = map[string]any{"response": "ProvideCredentials", "username": username, "password": password}
		action = "provided"
	}
	err := client.CallSession(ctx, session, "Fetch.continueWithAuth", map[string]any{"requestId": p.RequestID, "authChallengeResponse": response}, nil)
	clear(response)
	if err != nil {
		return fmt.Errorf("answer HTTP auth challenge failed")
	}
	s.counts[action]++
	return nil
}
