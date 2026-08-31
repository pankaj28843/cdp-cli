package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestProtocolDescribePlainPrintsActionableSignature(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"protocol", "describe", "Page.navigate"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("plain protocol describe exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	for _, want := range []string{"command\tPage.navigate", "Parameters:", "url: string (required)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("plain protocol describe missing %q: %s", want, out.String())
		}
	}
}
