package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
)

func TestProtocolOfficialDiscoveryBypassesDaemon(t *testing.T) {
	original := fetchOfficialProtocol
	t.Cleanup(func() { fetchOfficialProtocol = original })

	called := 0
	fetchOfficialProtocol = func(context.Context) (cdp.Protocol, error) {
		called++
		return cdp.Protocol{
			Version: cdp.ProtocolVersion{Major: "1", Minor: "3"},
			Domains: []cdp.Domain{{Domain: "Page"}, {Domain: "Runtime"}},
			Source:  cdp.OfficialBrowserProtocolURL + "," + cdp.OfficialJSProtocolURL,
		}, nil
	}

	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--state-dir", t.TempDir(), "protocol", "domains", "--official", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitOK {
		t.Fatalf("official protocol domains exit=%d, want %d; stdout=%s stderr=%s", code, ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK          bool                `json:"ok"`
		DomainCount int                 `json:"domain_count"`
		Domains     []cdp.DomainSummary `json:"domains"`
		Source      string              `json:"source"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode official protocol domains: %v; output=%s", err, out.String())
	}
	if called != 1 || !got.OK || got.DomainCount != 2 || got.Source != cdp.OfficialBrowserProtocolURL+","+cdp.OfficialJSProtocolURL {
		t.Fatalf("official protocol domains = %+v, calls=%d", got, called)
	}
}

func TestProtocolOfficialFetchFailureIsTyped(t *testing.T) {
	original := fetchOfficialProtocol
	t.Cleanup(func() { fetchOfficialProtocol = original })
	fetchOfficialProtocol = func(context.Context) (cdp.Protocol, error) {
		return cdp.Protocol{}, errors.New("synthetic network failure")
	}

	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--state-dir", t.TempDir(), "protocol", "metadata", "--official", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitConnection {
		t.Fatalf("official protocol failure exit=%d, want %d; stdout=%s stderr=%s", code, ExitConnection, out.String(), errOut.String())
	}
	var got struct {
		OK                  bool     `json:"ok"`
		Code                string   `json:"code"`
		ErrClass            string   `json:"err_class"`
		Message             string   `json:"message"`
		RemediationCommands []string `json:"remediation_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode official protocol failure: %v; output=%s", err, out.String())
	}
	if got.OK || got.Code != "official_protocol_fetch_failed" || got.ErrClass != "connection" || !strings.Contains(got.Message, "synthetic network failure") || len(got.RemediationCommands) == 0 || !strings.Contains(got.RemediationCommands[0], "--official") {
		t.Fatalf("official protocol failure = %+v", got)
	}
}

func TestProtocolOfficialExecRequiresValidation(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{"--state-dir", t.TempDir(), "protocol", "exec", "Browser.getVersion", "--official", "--json"}, &out, &errOut, BuildInfo{})
	if code != ExitUsage {
		t.Fatalf("official protocol exec exit=%d, want %d; stdout=%s stderr=%s", code, ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode official protocol exec failure: %v; output=%s", err, out.String())
	}
	if got.Code != "official_protocol_requires_validation" || !strings.Contains(got.Message, "--validate") {
		t.Fatalf("official protocol exec failure = %+v", got)
	}
}
