//go:build darwin

package browser

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func runSystemEventsScript(ctx context.Context, script, processName string, timeout time.Duration) ([]byte, error) {
	scriptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(scriptCtx, "osascript", "-", processName)
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil && scriptCtx.Err() != nil && ctx.Err() == nil {
		_ = exec.Command("/usr/bin/pkill", "-x", "System Events").Run()
	}
	return output, err
}
