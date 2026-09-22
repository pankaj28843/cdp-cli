package browser

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/availability"
)

func TestActiveAutoConnectProbeDoesNotRequestApprovalWithoutDesktop(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "must not dial", http.StatusForbidden)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(fmt.Sprintf("%s\n/devtools/browser/synthetic\n", port)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"screen_locked", "lid_closed", "desktop_state_unknown"} {
		got, err := probeAutoConnectWithDesktop(context.Background(), ProbeOptions{AutoConnect: true, ActiveProbe: true, UserDataDir: profile}, func(context.Context) availability.Result { return availability.Result{Reason: reason} })
		if err != nil || got.State != "permission_pending" || got.WebSocketDebuggerURL || requests.Load() != 0 {
			t.Fatalf("probe=%+v err=%v requests=%d", got, err, requests.Load())
		}
	}
}
