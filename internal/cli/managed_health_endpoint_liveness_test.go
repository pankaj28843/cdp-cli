package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/browser"
	"github.com/pankaj28843/cdp-cli/internal/daemon"
)

func TestManagedRuntimeProcessCheckUsesManagedEndpointAfterLauncherExit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	port := endpointTestServerPort(t, server.URL)
	profile := t.TempDir()
	writeEndpointActivePort(t, profile, port)

	originalRunning := managedRuntimeProcessRunning
	originalEndpoint := managedRuntimeEndpointReachable
	managedRuntimeProcessRunning = func(context.Context, int) (bool, error) {
		return false, nil
	}
	managedRuntimeEndpointReachable = func(ctx context.Context, userDataDir, expectedPort string) bool {
		return browser.ManagedBrowserEndpointReachable(ctx, userDataDir, expectedPort)
	}
	t.Cleanup(func() {
		managedRuntimeProcessRunning = originalRunning
		managedRuntimeEndpointReachable = originalEndpoint
	})

	result, detail := managedRuntimeProcessCheck(context.Background(), &daemon.Runtime{
		BrowserMode:         "headless",
		ChromePID:           12345,
		ChromePort:          port,
		ManagedProfilePath:  profile,
		ProfileSeedStrategy: "managed",
	})
	if !result || detail == nil || detail["running"] != true || detail["state"] != "running" || detail["liveness_source"] != "debugging_endpoint" {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v, want endpoint-backed running detail", result, detail)
	}
}

func TestManagedRuntimeProcessCheckDoesNotRescueIdentityMismatchWithEndpoint(t *testing.T) {
	endpointCalled := false
	originalRunning := managedRuntimeProcessRunning
	originalEndpoint := managedRuntimeEndpointReachable
	managedRuntimeProcessRunning = func(context.Context, int) (bool, error) {
		return true, nil
	}
	managedRuntimeEndpointReachable = func(context.Context, string, string) bool {
		endpointCalled = true
		return true
	}
	t.Cleanup(func() {
		managedRuntimeProcessRunning = originalRunning
		managedRuntimeEndpointReachable = originalEndpoint
	})

	result, detail := managedRuntimeProcessCheck(context.Background(), &daemon.Runtime{
		BrowserMode:            "headless",
		ChromePID:              os.Getpid(),
		ChromePort:             "9222",
		ManagedProfilePath:     t.TempDir(),
		ChromeProcessStartTime: "proc:not-the-live-process",
	})
	if result || detail == nil || detail["running"] != false || detail["state"] != "process_identity_mismatch" || endpointCalled {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v endpointCalled=%v, want identity mismatch without endpoint rescue", result, detail, endpointCalled)
	}
}

func TestManagedRuntimeProcessCheckPreservesCancellationDuringEndpointFallback(t *testing.T) {
	originalRunning := managedRuntimeProcessRunning
	originalEndpoint := managedRuntimeEndpointReachable
	managedRuntimeProcessRunning = func(context.Context, int) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() {
		managedRuntimeProcessRunning = originalRunning
		managedRuntimeEndpointReachable = originalEndpoint
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	managedRuntimeEndpointReachable = func(ctx context.Context, _, _ string) bool {
		cancel()
		<-ctx.Done()
		return false
	}
	result, detail := managedRuntimeProcessCheck(ctx, &daemon.Runtime{
		BrowserMode:        "headless",
		ChromePID:          12345,
		ChromePort:         "9222",
		ManagedProfilePath: t.TempDir(),
	})
	if result || detail == nil || detail["running"] != false || detail["state"] != "process_check_canceled" {
		t.Fatalf("managedRuntimeProcessCheck = result=%v detail=%v, want canceled fallback detail", result, detail)
	}
}

func endpointTestServerPort(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return u.Port()
}

func writeEndpointActivePort(t *testing.T, profile, port string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(port+"\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatalf("write DevToolsActivePort: %v", err)
	}
}
