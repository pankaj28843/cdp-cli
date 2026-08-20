package browser

import (
	"context"
	"runtime"
)

// HeadedWindowResult is the platform boundary for keeping a normal browser
// window available to headed CDP. Unsupported platforms remain explicit so a
// scheduler can distinguish "not implemented" from "window verified".
type HeadedWindowResult struct {
	Supported          bool   `json:"supported"`
	Platform           string `json:"platform,omitempty"`
	Adapter            string `json:"adapter,omitempty"`
	BrowserApplication string `json:"browser_application,omitempty"`
	WindowsBefore      int    `json:"windows_before,omitempty"`
	WindowsAfter       int    `json:"windows_after,omitempty"`
	WindowReady        bool   `json:"window_ready"`
	Action             string `json:"action"`
	Message            string `json:"message"`
	NextImplementation string `json:"next_implementation,omitempty"`
	Detail             string `json:"detail,omitempty"`
}

func EnsureHeadedChromeWindow(ctx context.Context, channel string) (HeadedWindowResult, error) {
	return ensureHeadedChromeWindow(ctx, channel)
}

func unsupportedHeadedWindow(channel string) HeadedWindowResult {
	return HeadedWindowResult{
		Supported:          false,
		Platform:           runtime.GOOS,
		Adapter:            "unimplemented",
		Action:             "unsupported",
		Message:            "headed Chrome window verification is not implemented on this platform",
		NextImplementation: "add a platform window/accessibility adapter and preserve the shared window_ready plus CDP probe contract for " + channel,
	}
}
