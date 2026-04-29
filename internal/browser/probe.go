package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type ProbeResult struct {
	State                string   `json:"state"`
	Message              string   `json:"message"`
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

func Probe(ctx context.Context, rawURL string) (ProbeResult, error) {
	if strings.TrimSpace(rawURL) == "" {
		return ProbeResult{
			State:   "not_configured",
			Message: "browser endpoint is not configured",
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
			State:   "unreachable",
			Message: "browser endpoint could not be reached",
			RemediationCommands: []string{
				"cdp doctor --browser-url <browser-url> --json",
				"cdp daemon start --help",
			},
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ProbeResult{
			State:      "listening_not_cdp",
			Message:    "browser endpoint is listening but did not expose CDP HTTP discovery",
			HTTPStatus: resp.StatusCode,
			RemediationCommands: []string{
				"cdp doctor --browser-url <browser-url> --json",
				"cdp mcp claude print-config --help",
			},
		}, nil
	}

	var version versionResponse
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return ProbeResult{
			State:      "invalid_response",
			Message:    "browser endpoint returned non-CDP JSON",
			HTTPStatus: resp.StatusCode,
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
		Browser:              version.Browser,
		ProtocolVersion:      version.ProtocolVersion,
		HTTPStatus:           resp.StatusCode,
		WebSocketDebuggerURL: true,
	}, nil
}

func versionEndpoint(rawURL string) (string, error) {
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
	u.Path = strings.TrimRight(u.Path, "/") + "/json/version"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
