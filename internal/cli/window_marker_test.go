package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWindowMarkerCommandTreeAndExamples(t *testing.T) {
	a := &app{out: &bytes.Buffer{}, err: &bytes.Buffer{}, opts: options{profile: "default"}}
	root := a.newRoot()
	browser, _, err := root.Find([]string{"browser"})
	if err != nil || browser == nil {
		t.Fatalf("find browser command: %v", err)
	}
	marker, _, err := browser.Find([]string{"marker"})
	if err != nil || marker == nil {
		t.Fatalf("find marker command: %v", err)
	}
	for _, name := range []string{"enable", "disable", "status"} {
		child, _, findErr := marker.Find([]string{name})
		if findErr != nil || child == nil {
			t.Fatalf("find marker %s: %v", name, findErr)
		}
		if len(commandExamples("cdp browser marker "+name)) == 0 {
			t.Fatalf("marker %s has no command examples", name)
		}
	}
	if len(commandExamples("cdp browser marker")) < 3 {
		t.Fatalf("marker command examples = %+v", commandExamples("cdp browser marker"))
	}
}

func TestWindowMarkerSchemaIsStableAndPrivacySafe(t *testing.T) {
	info, ok := schemaCatalog()["window-marker"]
	if !ok {
		t.Fatal("window-marker schema missing")
	}
	for _, field := range []string{"schema_version", "state", "enabled", "name", "color", "host_id_present", "active_session_count", "state_path"} {
		if !schemaHasField(info, field) {
			t.Fatalf("window-marker schema missing %q: %+v", field, info)
		}
	}
	for _, field := range info.Fields {
		if field.Name == "host_id" {
			t.Fatal("window-marker schema must not expose the randomized host id")
		}
	}
}

func TestWindowMarkerEnableRejectsHeadlessBeforeDaemonAccess(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(context.Background(), []string{
		"--browser-mode", "headless", "browser", "marker", "enable", "--name", "agent", "--json",
	}, &out, &errOut, BuildInfo{})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, ExitUsage, out.String(), errOut.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v; stdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	if envelope["code"] != "invalid_browser_mode" || !strings.Contains(envelope["message"].(string), "headed") {
		t.Fatalf("error envelope = %+v", envelope)
	}
}
