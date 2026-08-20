//go:build !darwin && !linux

package browser

import "context"

func ensureHeadedChromeWindow(_ context.Context, channel string) (HeadedWindowResult, error) {
	return unsupportedHeadedWindow(channel), nil
}
