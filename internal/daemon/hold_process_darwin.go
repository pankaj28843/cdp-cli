//go:build darwin

package daemon

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"syscall"

	"github.com/pankaj28843/cdp-cli/internal/processgroup"
)

func listDaemonHoldProcesses(ctx context.Context) ([]daemonHoldProcess, error) {
	output, err := runDaemonHoldProcessTable(ctx, "-axo", "pid=,ppid=,command=")
	if err != nil {
		return nil, err
	}
	processes := make([]daemonHoldProcess, 0)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 5 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parentPID, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil || pid <= 0 || pid == syscall.Getpid() {
			continue
		}
		args := fields[2:]
		if len(args) != 3 || args[1] != "daemon" || args[2] != "hold" {
			continue
		}
		processes = append(processes, daemonHoldProcess{
			PID:        pid,
			ParentPID:  parentPID,
			Executable: args[0],
			Args:       append([]string(nil), args...),
		})
	}
	return processes, nil
}

func readDaemonHoldEnvironment(ctx context.Context, pid int) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := syscall.Sysctl("kern.procargs2." + strconv.Itoa(pid))
	if err == nil {
		if len(raw) > maxDaemonHoldEnvironmentBytes {
			return nil, fmt.Errorf("process environment exceeded bound")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return parseDaemonHoldEnvironment([]byte(raw)), nil
	}

	// macOS does not expose kern.procargs2.<pid> through the shell sysctl OID
	// even though ps can still provide the owner-visible process environment.
	// Keep this fallback bounded and parse only the allowlisted CDP keys; the
	// complete ps line never leaves this function.
	psRaw, psErr := runDaemonHoldProcessTable(ctx, "eww", "-p", strconv.Itoa(pid), "-o", "command=")
	if psErr != nil {
		return nil, psErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return parseDaemonHoldEnvironmentText(psRaw), nil
}

func runDaemonHoldProcessTable(ctx context.Context, args ...string) ([]byte, error) {
	output := &daemonHoldProcessTableOutput{}
	if err := processgroup.Run(ctx, "ps", args, output, io.Discard); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("run process table probe")
	}
	if output.truncated {
		return nil, fmt.Errorf("process table output exceeded bound")
	}
	return output.data, nil
}
