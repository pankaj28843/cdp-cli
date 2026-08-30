package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedBrowserEndpointReachableUsesMatchingActivePort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	port := testServerPort(t, server.URL)
	profile := t.TempDir()
	writeActivePort(t, profile, port)

	if !ManagedBrowserEndpointReachable(context.Background(), profile, port) {
		t.Fatalf("ManagedBrowserEndpointReachable = false, want reachable active endpoint")
	}
}

func TestManagedBrowserEndpointReachableRejectsMismatchedActivePort(t *testing.T) {
	profile := t.TempDir()
	writeActivePort(t, profile, "9222")

	if ManagedBrowserEndpointReachable(context.Background(), profile, "9333") {
		t.Fatal("ManagedBrowserEndpointReachable = true for mismatched runtime port")
	}
}

func TestManagedBrowserEndpointReachableRejectsUnreachableEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	port := testServerPort(t, server.URL)
	server.Close()

	profile := t.TempDir()
	writeActivePort(t, profile, port)
	if ManagedBrowserEndpointReachable(context.Background(), profile, port) {
		t.Fatal("ManagedBrowserEndpointReachable = true after endpoint stopped")
	}
}

func TestManagedBrowserEndpointReachableRejectsForeignAttributedPort(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	port := testServerPort(t, server.URL)
	profile := t.TempDir()
	writeActivePort(t, profile, port)

	originalSnapshots := managedProcessSnapshotsForLiveness
	managedProcessSnapshotsForLiveness = func(context.Context) ([]managedProcessSnapshot, error) {
		return []managedProcessSnapshot{{
			CommandLine: "chrome --remote-debugging-port=" + port + " --user-data-dir=/other/profile",
		}}, nil
	}
	t.Cleanup(func() { managedProcessSnapshotsForLiveness = originalSnapshots })

	if ManagedBrowserEndpointReachable(context.Background(), profile, port) {
		t.Fatal("ManagedBrowserEndpointReachable = true for foreign attributed port")
	}
	if called {
		t.Fatal("foreign port attribution still probed the endpoint")
	}
}

func TestManagedBrowserEndpointReachableAcceptsProfileAttributionWithSpaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	port := testServerPort(t, server.URL)
	profile := filepath.Join(t.TempDir(), "profile with spaces")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	writeActivePort(t, profile, port)

	originalSnapshots := managedProcessSnapshotsForLiveness
	managedProcessSnapshotsForLiveness = func(context.Context) ([]managedProcessSnapshot, error) {
		return []managedProcessSnapshot{{
			CommandLine: "chrome --remote-debugging-port=" + port + " --user-data-dir=" + profile,
		}}, nil
	}
	t.Cleanup(func() { managedProcessSnapshotsForLiveness = originalSnapshots })

	if !ManagedBrowserEndpointReachable(context.Background(), profile, port) {
		t.Fatal("ManagedBrowserEndpointReachable = false for matching profile with spaces")
	}
}

func TestManagedBrowserEndpointReachableRejectsMalformedActivePort(t *testing.T) {
	profile := t.TempDir()
	if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte("not-a-port\n"), 0o600); err != nil {
		t.Fatalf("write malformed DevToolsActivePort: %v", err)
	}

	if ManagedBrowserEndpointReachable(context.Background(), profile, "") {
		t.Fatal("ManagedBrowserEndpointReachable = true for malformed active port")
	}
}

func TestManagedBrowserEndpointReachableRejectsCanceledContext(t *testing.T) {
	profile := t.TempDir()
	writeActivePort(t, profile, "9222")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if ManagedBrowserEndpointReachable(ctx, profile, "9222") {
		t.Fatal("ManagedBrowserEndpointReachable = true after cancellation")
	}
}

func testServerPort(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return u.Port()
}

func writeActivePort(t *testing.T, profile, port string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(port+"\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatalf("write DevToolsActivePort: %v", err)
	}
}
