package cli

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

const defaultYouTubeURL = "https://www.youtube.com/"

var youtubeAuthCookieNames = map[string]bool{
	"SAPISID":        true,
	"__Secure-1PSID": true,
	"__Secure-3PSID": true,
}

type youtubeCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	Session  bool    `json:"session"`
}

func (a *app) newWorkflowYouTubeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "youtube", Short: "Run headed YouTube session workflows"}
	cmd.AddCommand(a.newWorkflowYouTubeCookiesCommand())
	return cmd
}

func (a *app) newWorkflowYouTubeCookiesCommand() *cobra.Command {
	var rawURL string
	var outPath string
	var settle time.Duration
	cmd := &cobra.Command{
		Use:   "cookies",
		Short: "Harvest signed-in YouTube cookies into an owner-only Netscape file",
		Example: "  cdp --browser-mode headed workflow youtube cookies --out ~/.local/state/yt-dlp/cookies.txt --json\n" +
			"  cdp --browser-mode headed workflow youtube cookies --settle 5s --json\n" +
			"  cdp schema workflow-youtube-cookies --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateYouTubeURL(rawURL); err != nil {
				return youtubeCookiesUsageError(err.Error())
			}
			if settle < 0 {
				return youtubeCookiesUsageError("--settle must be non-negative")
			}
			if strings.TrimSpace(outPath) == "" {
				var err error
				outPath, err = defaultYouTubeCookieFile()
				if err != nil {
					return youtubeCookiesUsageError(err.Error())
				}
			}
			ctx, cancel := a.commandContextWithDefault(cmd, 2*settle+20*time.Second)
			defer cancel()
			return a.runYouTubeCookiesWorkflow(ctx, rawURL, outPath, settle)
		},
	}
	cmd.Flags().StringVar(&rawURL, "url", defaultYouTubeURL, "HTTPS YouTube URL to refresh before harvesting")
	cmd.Flags().StringVar(&outPath, "out", "", "Netscape cookie output; defaults under XDG_STATE_HOME/yt-dlp")
	cmd.Flags().DurationVar(&settle, "settle", 3*time.Second, "settle time before and after the hard refresh")
	return cmd
}

func (a *app) runYouTubeCookiesWorkflow(ctx context.Context, rawURL, outPath string, settle time.Duration) error {
	client, closeClient, err := a.browserCDPClient(ctx)
	if err != nil {
		return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
	}
	targetID, err := a.createWorkflowPageTarget(ctx, client, rawURL, "youtube-cookies")
	if err != nil {
		_ = closeClient(ctx)
		return err
	}
	cleanupGuard := &renderedExtractCleanupGuard{client: client, targetID: targetID, owned: true}
	session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
	if err != nil {
		return youtubeCookiesWorkflowError("attach YouTube target", err, cleanupGuard.cleanup())
	}
	defer closeRenderedExtractSession(session, nil)
	defer cleanupGuard.cleanup()

	cookies, err := harvestYouTubeCookies(ctx, session, rawURL, settle)
	if err != nil {
		return youtubeCookiesWorkflowError("harvest YouTube cookies", err, cleanupGuard.cleanup())
	}
	content, selected, err := youtubeNetscapeCookieFile(cookies, time.Now())
	if err != nil {
		return youtubeCookiesWorkflowError("validate YouTube cookies", err, cleanupGuard.cleanup())
	}
	if err := writeYouTubeCookieFile(outPath, content); err != nil {
		return youtubeCookiesWorkflowError("write YouTube cookies", err, cleanupGuard.cleanup())
	}
	cleanup := cleanupGuard.cleanup()
	if cleanup.Error != "" {
		return youtubeCookiesWorkflowError("close YouTube target", fmt.Errorf("%s", cleanup.Error), cleanup)
	}
	payload := map[string]any{
		"ok": true, "url": rawURL, "cookie_file": filepath.Clean(outPath),
		"cookie_count": len(selected), "auth_cookie_names": youtubeAuthNames(selected),
		"security": map[string]string{"directory_mode": "0700", "file_mode": "0600"},
		"cleanup":  cleanup,
	}
	return a.render(ctx, fmt.Sprintf("youtube-cookies\t%d\t%s", len(selected), filepath.Clean(outPath)), payload)
}

func harvestYouTubeCookies(ctx context.Context, session *cdp.PageSession, rawURL string, settle time.Duration) ([]youtubeCookie, error) {
	if err := waitYouTubeSettle(ctx, settle); err != nil {
		return nil, err
	}
	if err := execSessionJSON(ctx, session, "Page.reload", map[string]any{"ignoreCache": true}, nil); err != nil {
		return nil, fmt.Errorf("hard refresh: %w", err)
	}
	if err := waitYouTubeSettle(ctx, settle); err != nil {
		return nil, err
	}
	var result struct {
		Cookies []youtubeCookie `json:"cookies"`
	}
	if err := execSessionJSON(ctx, session, "Network.getCookies", map[string]any{"urls": []string{rawURL}}, &result); err != nil {
		return nil, err
	}
	return result.Cookies, nil
}

func waitYouTubeSettle(ctx context.Context, duration time.Duration) error {
	if duration == 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func youtubeNetscapeCookieFile(cookies []youtubeCookie, now time.Time) (string, []youtubeCookie, error) {
	selected := currentYouTubeCookies(cookies, now)
	if len(selected) == 0 {
		return "", nil, fmt.Errorf("the headed Chrome profile exposed no current YouTube cookies")
	}
	if len(youtubeAuthNames(selected)) == 0 {
		return "", nil, fmt.Errorf("the headed Chrome profile does not appear to be signed in to YouTube")
	}
	lines := []string{"# Netscape HTTP Cookie File", "# Generated from headed Chrome by cdp workflow youtube cookies.", "# Keep this file private.", ""}
	for _, cookie := range selected {
		line, err := youtubeNetscapeCookieLine(cookie)
		if err != nil {
			return "", nil, err
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n", selected, nil
}

func currentYouTubeCookies(cookies []youtubeCookie, now time.Time) []youtubeCookie {
	selected := make([]youtubeCookie, 0, len(cookies))
	for _, cookie := range cookies {
		if !isYouTubeDomain(cookie.Domain) || (!cookie.Session && cookie.Expires > 0 && cookie.Expires <= float64(now.Unix())) {
			continue
		}
		selected = append(selected, cookie)
	}
	sort.Slice(selected, func(i, j int) bool {
		left, right := selected[i], selected[j]
		return left.Domain+"\x00"+left.Path+"\x00"+left.Name < right.Domain+"\x00"+right.Path+"\x00"+right.Name
	})
	return selected
}

func youtubeNetscapeCookieLine(cookie youtubeCookie) (string, error) {
	domain, err := youtubeCookieField(cookie.Domain, "domain")
	if err != nil {
		return "", err
	}
	name, err := youtubeCookieField(cookie.Name, "name")
	if err != nil || domain == "" || name == "" {
		return "", fmt.Errorf("YouTube cookie domain and name must not be empty or contain control characters")
	}
	path, err := youtubeCookieField(cookie.Path, "path")
	if err != nil {
		return "", err
	}
	if path == "" {
		path = "/"
	}
	value, err := youtubeCookieField(cookie.Value, "value")
	if err != nil {
		return "", err
	}
	includeSubdomains := netscapeBool(strings.HasPrefix(domain, "."))
	if cookie.HTTPOnly {
		domain = "#HttpOnly_" + domain
	}
	expires := int64(0)
	if !cookie.Session && cookie.Expires > 0 && cookie.Expires <= math.MaxInt64 {
		expires = int64(cookie.Expires)
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%s\t%s", domain, includeSubdomains, path, netscapeBool(cookie.Secure), expires, name, value), nil
}

func netscapeBool(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}

func youtubeCookieField(value, label string) (string, error) {
	if strings.ContainsAny(value, "\t\r\n") {
		return "", fmt.Errorf("YouTube cookie %s contains unsupported control characters", label)
	}
	return value, nil
}

func youtubeAuthNames(cookies []youtubeCookie) []string {
	names := []string{}
	for _, cookie := range cookies {
		if youtubeAuthCookieNames[cookie.Name] {
			names = append(names, cookie.Name)
		}
	}
	sort.Strings(names)
	return names
}

func writeYouTubeCookieFile(path, content string) error {
	path = filepath.Clean(path)
	directory := filepath.Dir(path)
	if err := ensureOwnerOnlyYouTubeDirectory(directory); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".cookies.txt.*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func ensureOwnerOnlyYouTubeDirectory(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		return os.Chmod(path, 0o700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("cookie output directory %s must be an owner-only directory", path)
	}
	return nil
}

func defaultYouTubeCookieFile() (string, error) {
	root := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		root = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(root, "yt-dlp", "cookies.txt"), nil
}

func validateYouTubeURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || !isYouTubeDomain(parsed.Hostname()) {
		return fmt.Errorf("--url must be an HTTPS youtube.com URL")
	}
	return nil
}

func isYouTubeDomain(domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
	return domain == "youtube.com" || strings.HasSuffix(domain, ".youtube.com")
}

func youtubeCookiesUsageError(message string) error {
	return commandError("usage", "usage", message, ExitUsage, []string{"cdp --browser-mode headed workflow youtube cookies --json"})
}

func youtubeCookiesWorkflowError(action string, err error, cleanup renderedExtractCleanupResult) error {
	return commandErrorWithData(
		"youtube_cookies_failed", "check_failed", fmt.Sprintf("%s: %v", action, err), ExitCheckFailed,
		[]string{"cdp --browser-mode headed pages --json", "cdp --browser-mode headed workflow youtube cookies --json"},
		map[string]any{"cleanup": cleanup},
	)
}
