//go:build linux

package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxDaemonHoldCommandBytes = 16 << 10

func listDaemonHoldProcesses(ctx context.Context) ([]daemonHoldProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("read process table")
	}
	processes := make([]daemonHoldProcess, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return processes, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		args, err := readLinuxProcessArgs(ctx, pid)
		if err != nil || len(args) != 3 || args[1] != "daemon" || args[2] != "hold" {
			continue
		}
		parentPID, err := readLinuxProcessParentPID(ctx, pid)
		if err != nil {
			continue
		}
		processes = append(processes, daemonHoldProcess{
			PID:        pid,
			ParentPID:  parentPID,
			Executable: args[0],
			Args:       args,
		})
	}
	return processes, nil
}

func readDaemonHoldEnvironment(ctx context.Context, pid int) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := readLinuxProcessFile(ctx, pid, "environ", maxDaemonHoldEnvironmentBytes)
	if err != nil {
		return nil, err
	}
	return parseDaemonHoldEnvironment(raw), nil
}

func readLinuxProcessArgs(ctx context.Context, pid int) ([]string, error) {
	raw, err := readLinuxProcessFile(ctx, pid, "cmdline", maxDaemonHoldCommandBytes)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(raw), "\x00")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts, nil
}

func readLinuxProcessParentPID(ctx context.Context, pid int) (int, error) {
	raw, err := readLinuxProcessFile(ctx, pid, "stat", maxDaemonHoldCommandBytes)
	if err != nil {
		return 0, err
	}
	closeParen := strings.LastIndexByte(string(raw), ')')
	if closeParen < 0 {
		return 0, fmt.Errorf("malformed process state")
	}
	fields := strings.Fields(string(raw)[closeParen+1:])
	if len(fields) < 2 {
		return 0, fmt.Errorf("process parent is unavailable")
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil || parentPID <= 0 {
		return 0, fmt.Errorf("process parent is unavailable")
	}
	return parentPID, nil
}

func readLinuxProcessFile(ctx context.Context, pid int, name string, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), name))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > int(maxBytes) {
		return nil, fmt.Errorf("process metadata exceeded bound")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}
