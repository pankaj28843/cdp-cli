package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/cli"
)

func TestDescribeKeepaliveExposesFingerprintProfile(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), []string{"describe", "--command", "daemon keepalive", "--json"}, &out, &errOut, cli.BuildInfo{})
	if code != cli.ExitOK {
		t.Fatalf("describe exit code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var got struct {
		Commands struct {
			Flags []struct {
				Name string `json:"name"`
			} `json:"flags"`
			Examples []string `json:"examples"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode describe output: %v", err)
	}
	foundFlag := false
	for _, flag := range got.Commands.Flags {
		foundFlag = foundFlag || flag.Name == "fingerprint-profile"
	}
	if !foundFlag || !hasExampleContaining(got.Commands.Examples, "--fingerprint-profile") {
		t.Fatalf("keepalive describe = %+v, want fingerprint flag and example", got.Commands)
	}
}
