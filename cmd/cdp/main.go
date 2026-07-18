package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/pankaj28843/cdp-cli/internal/cli"
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
