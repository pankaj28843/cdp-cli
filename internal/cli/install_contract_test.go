package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakeInstallIsBinaryOnlyAndPreservesServiceState(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	command := exec.Command("make", "-n", "install", "PREFIX=/tmp/cdp-install-contract")
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n install: %v\n%s", err, output)
	}
	plan := string(output)
	for _, forbidden := range []string{
		"systemctl", "launchctl", "crontab", "cron install", "service install",
		"daemon start", "daemon restart", "daemon keepalive",
	} {
		if strings.Contains(plan, forbidden) {
			t.Fatalf("make install planned forbidden lifecycle mutation %q:\n%s", forbidden, plan)
		}
	}
	for _, required := range []string{"bin/cdp", "/bin/cdp", "guide.md"} {
		if !strings.Contains(plan, required) {
			t.Fatalf("make install plan missing %q:\n%s", required, plan)
		}
	}
}
