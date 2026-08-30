package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestDaemonKeepaliveUsesManagedEndpointAfterLauncherPIDExited(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	stateDir := shortCLIStateDir(t)
	t.Setenv("CDP_DAEMON_BROWSER_MODE", "headless")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Hold(ctx, stateDir, fakeWebSocketEndpoint(t, server.URL), "browser_url", 30*time.Second)
	}()
	waitForDaemonRuntimeForMode(t, ctx, stateDir, "headless")
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("daemon hold returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("daemon hold did not stop")
		}
	})

	runtimeState, ok, err := daemon.LoadRuntimeForMode(context.Background(), stateDir, "headless")
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeForMode headless ok=%v err=%v, want runtime", ok, err)
	}
	endpoint := fakeWebSocketEndpoint(t, server.URL)
	port := writeManagedActivePortForEndpoint(t, stateDir, endpoint)
	deadPID := exitedProcessPID(t)
	runtimeState.ManagedProfilePath = browser.ManagedProfileDir(stateDir)
	runtimeState.ProfileSeedStrategy = browser.ProfileSeedStrategyManaged
	runtimeState.ChromePID = deadPID
	runtimeState.ChromePort = port
	runtimeState.ManagedBrowser = &browser.ManagedStatus{
		BrowserMode:         "headless",
		ChromePID:           deadPID,
		UserDataDir:         browser.ManagedProfileDir(stateDir),
		DebuggingPort:       port,
		ProfileSeedStrategy: browser.ProfileSeedStrategyManaged,
	}
	if err := daemon.SaveRuntimeForMode(context.Background(), stateDir, "headless", runtimeState); err != nil {
		t.Fatalf("SaveRuntimeForMode returned error: %v", err)
	}

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{
		"--browser-mode", "headless", "daemon", "keepalive", "--state-dir", stateDir, "--json",
	}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("daemon keepalive exit code = %d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		State  string `json:"state"`
		Action string `json:"action"`
		Health struct {
			Result               string `json:"result"`
			ManagedBrowserHealth struct {
				State          string `json:"state"`
				Running        bool   `json:"running"`
				LivenessSource string `json:"liveness_source"`
			} `json:"managed_browser_health"`
		} `json:"health"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon keepalive output is invalid JSON: %v", err)
	}
	if !got.OK || got.State != "healthy" || got.Action != "none" || got.Health.Result != "target_list_ok" {
		t.Fatalf("daemon keepalive = %+v, want healthy/no repair", got)
	}
	if got.Health.ManagedBrowserHealth.State != "running" || !got.Health.ManagedBrowserHealth.Running || got.Health.ManagedBrowserHealth.LivenessSource != "debugging_endpoint" {
		t.Fatalf("managed browser health = %+v, want endpoint-backed liveness", got.Health.ManagedBrowserHealth)
	}
}
