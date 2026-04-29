package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestVersionJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"version", "--json"}, &out, &errOut, cli.BuildInfo{
		Version: "test",
		Commit:  "abc",
		Date:    "now",
	})
	if code != 0 {
		t.Fatalf("Execute exit code = %d, want 0; stderr=%s", code, errOut.String())
	}

	var got cli.BuildInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("version output is invalid JSON: %v", err)
	}
	if got.Version != "test" {
		t.Fatalf("Version = %q, want %q", got.Version, "test")
	}
}

func TestDescribeJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"describe", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != 0 {
		t.Fatalf("Execute exit code = %d, want 0; stderr=%s", code, errOut.String())
	}

	if !strings.Contains(out.String(), `"commands"`) {
		t.Fatalf("describe output = %s, want command metadata", out.String())
	}
}

func TestPlannedCommandJSONError(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"pages", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitNotImplemented {
		t.Fatalf("Execute exit code = %d, want %d", code, cli.ExitNotImplemented)
	}

	var got struct {
		OK       bool   `json:"ok"`
		Code     string `json:"code"`
		ErrClass string `json:"err_class"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("error output is invalid JSON: %v", err)
	}
	if got.OK || got.Code != "not_implemented" || got.ErrClass != "not_implemented" {
		t.Fatalf("error envelope = %+v, want not_implemented", got)
	}
}

func TestDaemonStatusJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"daemon", "status", "--state-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Daemon struct {
			State          string `json:"state"`
			ConnectionMode string `json:"connection_mode"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("daemon status output is invalid JSON: %v", err)
	}
	if !got.OK || got.Daemon.State != "not_running" || got.Daemon.ConnectionMode != "browser_url" {
		t.Fatalf("daemon status = %+v, want not_running browser_url", got)
	}
}

func TestConnectionMemoryJSON(t *testing.T) {
	stateDir := t.TempDir()
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"connection", "add", "default", "--auto-connect", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(stateDir, "connections.json")); err != nil {
		t.Fatalf("connections.json was not written: %v", err)
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"connection", "current", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection current exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK         bool `json:"ok"`
		Connection struct {
			Name        string `json:"name"`
			Mode        string `json:"mode"`
			AutoConnect bool   `json:"auto_connect"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("connection current output is invalid JSON: %v", err)
	}
	if !got.OK || got.Connection.Name != "default" || got.Connection.Mode != "auto_connect" || !got.Connection.AutoConnect {
		t.Fatalf("connection current = %+v, want default auto_connect", got)
	}
}

func TestDoctorUsesSelectedConnection(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	stateDir := t.TempDir()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"connection", "add", "local", "--browser-url", server.URL, "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("connection add exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = cli.Execute(context.Background(), []string{"doctor", "--state-dir", stateDir, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("doctor exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name           string `json:"name"`
			ConnectionMode string `json:"connection_mode"`
			Details        struct {
				State string `json:"state"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.ConnectionMode != "browser_url" || check.Details.State != "listening_not_cdp" {
				t.Fatalf("browser check = %+v, want selected browser_url state", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", got.Checks)
}

func TestExplainErrorJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"explain-error", "not_implemented", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Error struct {
			Code     string `json:"code"`
			ExitCode int    `json:"exit_code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("explain-error output is invalid JSON: %v", err)
	}
	if !got.OK || got.Error.Code != "not_implemented" || got.Error.ExitCode != cli.ExitNotImplemented {
		t.Fatalf("explain-error = %+v, want not_implemented metadata", got)
	}
}

func TestExitCodesJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"exit-codes", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK        bool `json:"ok"`
		ExitCodes []struct {
			Code int    `json:"code"`
			Name string `json:"name"`
		} `json:"exit_codes"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("exit-codes output is invalid JSON: %v", err)
	}
	if !got.OK || len(got.ExitCodes) < 2 || got.ExitCodes[0].Code != cli.ExitOK {
		t.Fatalf("exit-codes = %+v, want ok plus error rows", got)
	}
}

func TestSchemaJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"schema", "error-envelope", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Schema struct {
			Name   string `json:"name"`
			Fields []struct {
				Name     string `json:"name"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("schema output is invalid JSON: %v", err)
	}
	if !got.OK || got.Schema.Name != "error-envelope" || len(got.Schema.Fields) == 0 {
		t.Fatalf("schema = %+v, want error-envelope fields", got)
	}
}

func TestDescribeCommandJSON(t *testing.T) {
	var out, errOut bytes.Buffer

	code := cli.Execute(context.Background(), []string{"describe", "--command", "daemon status", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK       bool `json:"ok"`
		Commands struct {
			Name     string   `json:"name"`
			Use      string   `json:"use"`
			Examples []string `json:"examples"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("describe output is invalid JSON: %v", err)
	}
	if !got.OK || got.Commands.Name != "status" || !strings.Contains(got.Commands.Use, "daemon status") || len(got.Commands.Examples) == 0 {
		t.Fatalf("describe --command = %+v, want daemon status command", got)
	}
}

func TestDoctorBrowserURLWarnsForNonCDPEndpoint(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--browser-url", server.URL, "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Details struct {
				State      string `json:"state"`
				HTTPStatus int    `json:"http_status"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.Status != "warn" || check.Details.State != "listening_not_cdp" || check.Details.HTTPStatus != http.StatusNotFound {
				t.Fatalf("browser check = %+v, want listening_not_cdp warning", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", got.Checks)
}

func TestDoctorAutoConnectReportsPermissionFlow(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "--auto-connect", "--user-data-dir", t.TempDir(), "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("Execute exit code = %d, want %d; stderr=%s", code, cli.ExitOK, errOut.String())
	}

	var got struct {
		Checks []struct {
			Name               string `json:"name"`
			Status             string `json:"status"`
			ConnectionMode     string `json:"connection_mode"`
			RequiresUserAllow  bool   `json:"requires_user_allow"`
			DefaultProfileFlow bool   `json:"default_profile_flow"`
			Details            struct {
				State string `json:"state"`
			} `json:"details"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("doctor output is invalid JSON: %v", err)
	}
	for _, check := range got.Checks {
		if check.Name == "browser_debug_endpoint" {
			if check.Status != "pending" || check.ConnectionMode != "auto_connect" || !check.RequiresUserAllow || !check.DefaultProfileFlow || check.Details.State != "permission_pending" {
				t.Fatalf("browser check = %+v, want auto_connect pending permission flow", check)
			}
			return
		}
	}
	t.Fatalf("doctor checks = %+v, want browser_debug_endpoint", got.Checks)
}
