package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestProtocolHelpOfficialUsesOfficialSource(t *testing.T) {
	original := fetchOfficialProtocol
	t.Cleanup(func() { fetchOfficialProtocol = original })
	fetchOfficialProtocol = func(context.Context) (cdp.Protocol, error) {
		return cdp.Protocol{
			Version: cdp.ProtocolVersion{Major: "1", Minor: "3"},
			Domains: []cdp.Domain{{
				Domain:   "Page",
				Commands: json.RawMessage(`[{"name":"navigate","description":"Navigate"}]`),
			}},
			Source: cdp.OfficialBrowserProtocolURL + "," + cdp.OfficialJSProtocolURL,
		}, nil
	}

	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"help", "Page", "--official", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("official help exit=%d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		Mode   string `json:"mode"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode official help: %v; output=%s", err, out.String())
	}
	if !got.OK || got.Mode != "domain" || got.Source != cdp.OfficialBrowserProtocolURL+","+cdp.OfficialJSProtocolURL {
		t.Fatalf("official help = %+v, want official source", got)
	}
}
