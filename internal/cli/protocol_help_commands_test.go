package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestProtocolHelpListsDomainsJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"help", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("help domains exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		Mode    string `json:"mode"`
		Source  string `json:"source"`
		Domains []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode help domains: %v; output=%s", err, out.String())
	}
	if !got.OK || got.Mode != "domains" || got.Source == "" || len(got.Domains) != 3 || got.Domains[0].Name != "Page" || got.Domains[0].Description != "Page domain" {
		t.Fatalf("help domains = %+v, want live domain descriptions", got)
	}
}

func TestProtocolHelpDomainPlain(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"help", "Page"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("help domain exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	for _, want := range []string{"Domain: Page", "Commands:", "Page.navigate", "Page.captureScreenshot", "Capture page pixels"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help domain missing %q: %s", want, out.String())
		}
	}
	if errOut.Len() != 0 {
		t.Fatalf("help domain stderr=%q, want empty", errOut.String())
	}
}

func TestProtocolHelpEntityJSON(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"help", "Page.captureScreenshot", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("help entity exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK     bool   `json:"ok"`
		Mode   string `json:"mode"`
		Query  string `json:"query"`
		Source string `json:"source"`
		Entity struct {
			Kind   string `json:"kind"`
			Path   string `json:"path"`
			Schema struct {
				Name string `json:"name"`
			} `json:"schema"`
		} `json:"entity"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode help entity: %v; output=%s", err, out.String())
	}
	if !got.OK || got.Mode != "entity" || got.Query != "Page.captureScreenshot" || got.Source == "" || got.Entity.Kind != "command" || got.Entity.Path != "Page.captureScreenshot" || got.Entity.Schema.Name != "captureScreenshot" {
		t.Fatalf("help entity = %+v, want command schema", got)
	}
}

func TestProtocolHelpEntityPlainSignature(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"help", "Page.captureScreenshot"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("help signature exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	for _, want := range []string{"Page.captureScreenshot", "Capture page pixels", "Parameters:", "format: string (optional)", "quality: integer (optional)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help signature missing %q: %s", want, out.String())
		}
	}
}

func TestProtocolHelpUnknownEntityIsTyped(t *testing.T) {
	server := newFakeCDPServer(t, nil)
	defer server.Close()
	startFakeDaemon(t, server, "browser_url")

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"help", "Page.missing", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitUsage {
		t.Fatalf("unknown help exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitUsage, out.String(), errOut.String())
	}
	var got struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode unknown help: %v; output=%s", err, out.String())
	}
	if got.OK || got.Code != "unknown_protocol_entity" {
		t.Fatalf("unknown help = %+v, want typed usage error", got)
	}
}

func TestProtocolHelpNoBrowserFallsBackToRootHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--state-dir", t.TempDir(), "help"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("no-browser help exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "Usage:") || !strings.Contains(out.String(), "Discover CDP domains, commands, events, and signatures") {
		t.Fatalf("no-browser help = %q, want root command help", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("no-browser help stderr=%q, want empty", errOut.String())
	}
}

func TestProtocolHelpRetainsCobraCommandHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"--state-dir", t.TempDir(), "help", "pages"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("command help exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "Usage:") || !strings.Contains(out.String(), "List open pages and tabs") {
		t.Fatalf("command help = %q, want pages usage", out.String())
	}
}

func TestProtocolHelpSchema(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"schema", "protocol-help", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("help schema exit=%d, want %d; stdout=%s stderr=%s", code, cli.ExitOK, out.String(), errOut.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Schema struct {
			Name   string `json:"name"`
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode help schema: %v; output=%s", err, out.String())
	}
	fieldNames := make(map[string]bool, len(got.Schema.Fields))
	for _, field := range got.Schema.Fields {
		fieldNames[field.Name] = true
	}
	if !got.OK || got.Schema.Name != "protocol-help" || !fieldNames["mode"] || !fieldNames["source"] || !fieldNames["entity"] {
		t.Fatalf("help schema = %+v, want protocol-help fields", got)
	}
}
