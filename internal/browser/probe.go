package browser

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type ProbeOptions struct {
	BrowserURL  string
	AutoConnect bool
	Channel     string
	UserDataDir string
	ActiveProbe bool
}

type ProbeResult struct {
	State                string   `json:"state"`
	Message              string   `json:"message"`
	ConnectionMode       string   `json:"connection_mode"`
	Channel              string   `json:"channel,omitempty"`
	Browser              string   `json:"browser,omitempty"`
	ProtocolVersion      string   `json:"protocol_version,omitempty"`
	HTTPStatus           int      `json:"http_status,omitempty"`
	WebSocketDebuggerURL bool     `json:"web_socket_debugger_url"`
	RemediationCommands  []string `json:"remediation_commands,omitempty"`
}

type versionResponse struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func autoConnectRemediationCommands() []string {
	return []string{
		"open chrome://inspect/#remote-debugging",
		"cdp daemon status --json",
		"cdp doctor --check daemon --json",
		"cdp doctor --check browser-health --json",
	}
}

func Probe(ctx context.Context, opts ProbeOptions) (ProbeResult, error) {
	if opts.AutoConnect {
		if !opts.ActiveProbe {
			return probeAutoConnectPassive(opts)
		}
		return probeAutoConnect(ctx, opts)
	}
	return probeBrowserURL(ctx, opts.BrowserURL)
}

func ResolveEndpoint(ctx context.Context, opts ProbeOptions) (string, error) {
	if opts.AutoConnect {
		channel := opts.Channel
		if channel == "" {
			channel = "stable"
		}
		port, path, err := activePort(opts.UserDataDir, channel)
		if err != nil {
			return "", fmt.Errorf("resolve auto-connect endpoint: %w", err)
		}
		return fmt.Sprintf("ws://%s%s", net.JoinHostPort("127.0.0.1", port), path), nil
	}
	if strings.TrimSpace(opts.BrowserURL) == "" {
		return "", fmt.Errorf("browser endpoint is not configured")
	}
	version, err := fetchVersion(ctx, opts.BrowserURL)
	if err != nil {
		return "", err
	}
	if version.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("browser endpoint responded without a browser WebSocket URL")
	}
	return version.WebSocketDebuggerURL, nil
}

func ResolveProtocolURL(ctx context.Context, opts ProbeOptions) (string, error) {
	if opts.AutoConnect {
		channel := opts.Channel
		if channel == "" {
			channel = "stable"
		}
		port, _, err := activePort(opts.UserDataDir, channel)
		if err != nil {
			return "", fmt.Errorf("resolve auto-connect protocol endpoint: %w", err)
		}
		return fmt.Sprintf("http://%s/json/protocol", net.JoinHostPort("127.0.0.1", port)), nil
	}
	if strings.TrimSpace(opts.BrowserURL) == "" {
		return "", fmt.Errorf("browser endpoint is not configured")
	}
	return protocolEndpoint(opts.BrowserURL)
}

func probeBrowserURL(ctx context.Context, rawURL string) (ProbeResult, error) {
	if strings.TrimSpace(rawURL) == "" {
		return ProbeResult{
			State:          "not_configured",
			Message:        "browser endpoint is not configured",
			ConnectionMode: "browser_url",
			RemediationCommands: []string{
				"cdp doctor --browser-url <browser-url> --json",
				"CDP_BROWSER_URL=<browser-url> cdp doctor --json",
			},
		}, nil
	}

	versionURL, err := versionEndpoint(rawURL)
	if err != nil {
		return ProbeResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("create browser probe request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ProbeResult{
			State:          "unreachable",
			Message:        "browser endpoint could not be reached",
			ConnectionMode: "browser_url",
			RemediationCommands: []string{
				"cdp doctor --browser-url <browser-url> --json",
				"cdp daemon start --help",
			},
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ProbeResult{
			State:          "listening_not_cdp",
			Message:        "browser endpoint is listening but did not expose CDP HTTP discovery",
			ConnectionMode: "browser_url",
			HTTPStatus:     resp.StatusCode,
			RemediationCommands: []string{
				"cdp doctor --browser-url <browser-url> --json",
				"cdp --help",
			},
		}, nil
	}

	var version versionResponse
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return ProbeResult{
			State:          "invalid_response",
			Message:        "browser endpoint returned non-CDP JSON",
			ConnectionMode: "browser_url",
			HTTPStatus:     resp.StatusCode,
			RemediationCommands: []string{
				"cdp doctor --browser-url <browser-url> --json",
				"cdp daemon start --help",
			},
		}, nil
	}

	if version.WebSocketDebuggerURL == "" {
		return ProbeResult{
			State:           "missing_browser_websocket",
			Message:         "browser endpoint responded without a browser WebSocket URL",
			ConnectionMode:  "browser_url",
			Browser:         version.Browser,
			ProtocolVersion: version.ProtocolVersion,
			HTTPStatus:      resp.StatusCode,
			RemediationCommands: []string{
				"cdp doctor --browser-url <browser-url> --json",
				"cdp protocol metadata --help",
			},
		}, nil
	}

	return ProbeResult{
		State:                "cdp_available",
		Message:              "browser endpoint exposes CDP HTTP discovery",
		ConnectionMode:       "browser_url",
		Browser:              version.Browser,
		ProtocolVersion:      version.ProtocolVersion,
		HTTPStatus:           resp.StatusCode,
		WebSocketDebuggerURL: true,
	}, nil
}

func fetchVersion(ctx context.Context, rawURL string) (versionResponse, error) {
	versionURL, err := versionEndpoint(rawURL)
	if err != nil {
		return versionResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return versionResponse{}, fmt.Errorf("create browser version request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return versionResponse{}, fmt.Errorf("fetch browser version: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return versionResponse{}, fmt.Errorf("fetch browser version: http status %d", resp.StatusCode)
	}
	var version versionResponse
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return versionResponse{}, fmt.Errorf("decode browser version: %w", err)
	}
	return version, nil
}

func probeAutoConnect(ctx context.Context, opts ProbeOptions) (ProbeResult, error) {
	channel := opts.Channel
	if channel == "" {
		channel = "stable"
	}
	port, path, err := activePort(opts.UserDataDir, channel)
	if err != nil {
		return ProbeResult{
			State:               "permission_pending",
			Message:             "Chrome auto-connect is not ready; enable remote debugging in Chrome and allow the prompt",
			ConnectionMode:      "auto_connect",
			Channel:             channel,
			RemediationCommands: autoConnectRemediationCommands(),
		}, nil
	}

	status, err := websocketProbe(ctx, port, path)
	if err != nil {
		return ProbeResult{
			State:               "permission_pending",
			Message:             "Chrome auto-connect endpoint exists but did not accept the DevTools WebSocket yet",
			ConnectionMode:      "auto_connect",
			Channel:             channel,
			HTTPStatus:          status,
			RemediationCommands: autoConnectRemediationCommands(),
		}, nil
	}

	return ProbeResult{
		State:                "cdp_available",
		Message:              "Chrome auto-connect DevTools WebSocket is available",
		ConnectionMode:       "auto_connect",
		Channel:              channel,
		HTTPStatus:           status,
		WebSocketDebuggerURL: true,
	}, nil
}

func probeAutoConnectPassive(opts ProbeOptions) (ProbeResult, error) {
	channel := opts.Channel
	if channel == "" {
		channel = "stable"
	}
	_, _, err := activePort(opts.UserDataDir, channel)
	if err != nil {
		return ProbeResult{
			State:               "permission_pending",
			Message:             "Chrome auto-connect is not ready; enable remote debugging in Chrome and allow the prompt",
			ConnectionMode:      "auto_connect",
			Channel:             channel,
			RemediationCommands: autoConnectRemediationCommands(),
		}, nil
	}
	return ProbeResult{
		State:          "active_probe_skipped",
		Message:        "Chrome auto-connect state exists; active browser probing was skipped to avoid a remote-debugging prompt",
		ConnectionMode: "auto_connect",
		Channel:        channel,
		RemediationCommands: []string{
			"cdp daemon status --json",
			"cdp doctor --check daemon --json",
			"cdp doctor --check browser-health --json",
			"cdp daemon logs --tail 50 --json",
		},
	}, nil
}

func activePort(userDataDir, channel string) (string, string, error) {
	dir := userDataDir
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = defaultUserDataDir(channel)
		if err != nil {
			return "", "", err
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if err != nil {
		return "", "", fmt.Errorf("read DevToolsActivePort: %w", err)
	}
	lines := strings.Fields(string(b))
	if len(lines) < 2 {
		return "", "", fmt.Errorf("parse DevToolsActivePort: missing port or path")
	}
	if _, err := net.LookupPort("tcp", lines[0]); err != nil {
		return "", "", fmt.Errorf("parse DevToolsActivePort port: %w", err)
	}
	if !strings.HasPrefix(lines[1], "/") {
		return "", "", fmt.Errorf("parse DevToolsActivePort path: path must start with /")
	}
	return lines[0], lines[1], nil
}

func defaultUserDataDir(channel string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	switch runtime.GOOS {
	case "linux":
		base := os.Getenv("CHROME_CONFIG_HOME")
		if base == "" {
			base = os.Getenv("XDG_CONFIG_HOME")
		}
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		switch channel {
		case "stable", "":
			return filepath.Join(base, "google-chrome"), nil
		case "beta":
			return filepath.Join(base, "google-chrome-beta"), nil
		case "canary":
			return filepath.Join(base, "google-chrome-canary"), nil
		case "dev":
			return filepath.Join(base, "google-chrome-unstable"), nil
		}
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support", "Google")
		switch channel {
		case "stable", "":
			return filepath.Join(base, "Chrome"), nil
		case "beta":
			return filepath.Join(base, "Chrome Beta"), nil
		case "canary":
			return filepath.Join(base, "Chrome Canary"), nil
		case "dev":
			return filepath.Join(base, "Chrome Dev"), nil
		}
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		switch channel {
		case "stable", "":
			return filepath.Join(base, "Google", "Chrome", "User Data"), nil
		case "beta":
			return filepath.Join(base, "Google", "Chrome Beta", "User Data"), nil
		case "canary":
			return filepath.Join(base, "Google", "Chrome SxS", "User Data"), nil
		case "dev":
			return filepath.Join(base, "Google", "Chrome Dev", "User Data"), nil
		}
	}
	return "", fmt.Errorf("unsupported Chrome channel %q", channel)
}

func websocketProbe(ctx context.Context, port, path string) (int, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return 0, fmt.Errorf("set websocket probe deadline: %w", err)
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return 0, fmt.Errorf("generate websocket key: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	_, err = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: localhost:%s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, port, key)
	if err != nil {
		return 0, fmt.Errorf("write websocket handshake: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, fmt.Errorf("read websocket handshake: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return resp.StatusCode, fmt.Errorf("websocket handshake status = %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func versionEndpoint(rawURL string) (string, error) {
	return discoveryEndpoint(rawURL, "/json/version")
}

func protocolEndpoint(rawURL string) (string, error) {
	return discoveryEndpoint(rawURL, "/json/protocol")
}

func discoveryEndpoint(rawURL, path string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse browser url: %w", err)
	}
	if u.Scheme == "" {
		u.Scheme = "http"
	}
	if u.Host == "" {
		return "", fmt.Errorf("parse browser url: missing host")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
