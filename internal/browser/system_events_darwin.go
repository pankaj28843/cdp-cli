//go:build darwin

package browser

import (
	"context"
	"strings"
	"time"
)

func runSystemEventsScript(ctx context.Context, script, processName string, timeout time.Duration) ([]byte, error) {
	scriptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := runOwnedBrowserCommandWithInput(scriptCtx, "osascript", strings.NewReader(script), "-", processName)
	output := append([]byte(nil), result.stdout...)
	if len(result.stderr) > 0 {
		if len(output) > 0 {
			output = append(output, '\n')
		}
		output = append(output, result.stderr...)
	}
	return output, err
}
