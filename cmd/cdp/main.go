package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/pankaj28843/cdp-cli/internal/cli"
	"github.com/pankaj28843/cdp-cli/internal/installaliases"
	"github.com/pankaj28843/cdp-cli/internal/meta"
	"github.com/pankaj28843/cdp-cli/internal/wrapper"
)

var (
	version      = "dev"
	commit       = "unknown"
	date         = "unknown"
	dirty        = "false"
	managedBuild = "false"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if code, handled := executeCompatibility(ctx, os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}

	if code, handled := cli.ExecuteInternal(ctx, os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}

	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, cli.BuildInfo{
		Version:    version,
		Commit:     commit,
		Date:       date,
		Dirty:      dirty == "true",
		Verified:   managedBuild == "true",
		Provenance: map[bool]string{true: "managed", false: "unverified"}[managedBuild == "true"],
	})
	os.Exit(code)
}

func executeCompatibility(ctx context.Context, args []string, stdout, stderr io.Writer) (int, bool) {
	name := filepath.Base(os.Args[0])
	if name == "agent-web" {
		return meta.MainContext(ctx, args, stdout, stderr), true
	}
	if isProviderAlias(name) {
		if err := wrapper.Exec(name, args); err != nil {
			fmt.Fprintln(stderr, err)
			return 127, true
		}
		return 0, true
	}
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case "meta":
		return meta.MainContext(ctx, args[1:], stdout, stderr), true
	case "install-aliases":
		return installaliases.Main(args[1:], stdout, stderr), true
	case "agent-web":
		return meta.MainContext(ctx, args[1:], stdout, stderr), true
	}
	if isProviderAlias(args[0]) {
		if err := wrapper.Exec(args[0], args[1:]); err != nil {
			fmt.Fprintln(stderr, err)
			return 127, true
		}
		return 0, true
	}
	return 0, false
}

func isProviderAlias(name string) bool {
	switch name {
	case "ask-alex", "ask-chatgpt", "ask-claude", "ask-gemini", "ask-grok", "ask-perplexity", "ask-tripadvisor":
		return true
	default:
		return false
	}
}
