//go:build !linux && !darwin

package daemon

import "context"

func listDaemonHoldProcesses(context.Context) ([]daemonHoldProcess, error) {
	return nil, nil
}

func readDaemonHoldEnvironment(context.Context, int) (map[string]string, error) {
	return nil, nil
}
