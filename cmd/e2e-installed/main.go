package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/pankaj28843/cdp-cli/internal/e2e"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	report, err := e2e.Run(ctx)
	if err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"ok":         false,
			"error_type": "harness",
			"message":    err.Error(),
		})
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "encode E2E report:", err)
		os.Exit(1)
	}
	if !report.OK {
		os.Exit(1)
	}
}
