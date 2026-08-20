//go:build linux

package browser

import "context"

func ensureHeadedChromeWindow(_ context.Context, channel string) (HeadedWindowResult, error) {
	result := unsupportedHeadedWindow(channel)
	result.Platform = "linux"
	result.Adapter = "linux-window-accessibility-placeholder"
	result.NextImplementation = "choose AT-SPI, xdotool/wmctrl, or the desktop environment's supported window API, then implement count-or-create for a headed Chrome window"
	result.Message = "headed Chrome window verification is not implemented for Ubuntu/Linux yet"
	return result, nil
}
