package cli

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// The guide is embedded so a directly copied cdp binary remains useful even
// when its installation does not include the optional readable sidecar.
//
//go:embed guide.md
var embeddedAgentGuide []byte

const guideSchemaVersion = "guide/v1"

func (a *app) newGuideCommand() *cobra.Command {
	var pathOnly bool

	cmd := &cobra.Command{
		Use:   "guide",
		Short: "Print the installed agent guide",
		Long: "Print cdp-cli's public, version-matched operating guide without connecting to a browser. " +
			"Use --path when a file reader should consume the guide instead of stdout.",
		Example: "  cdp guide\n  cdp guide --path\n  cdp guide --json --jq '.path // .content'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if pathOnly {
				path, source, err := readableGuidePath()
				if err != nil {
					return commandError(
						"guide_path_failed",
						"internal",
						fmt.Sprintf("make the bundled guide readable: %v", err),
						ExitInternal,
						[]string{"cdp guide --json", "cdp guide --path"},
					)
				}
				data := map[string]any{
					"ok":             true,
					"schema_version": guideSchemaVersion,
					"mode":           "path",
					"path":           path,
					"bytes":          len(embeddedAgentGuide),
					"source":         source,
				}
				if a.opts.json || a.opts.jq != "" {
					ctx, cancel := a.commandContext(cmd)
					defer cancel()
					return a.render(ctx, "bundled agent guide path", data)
				}
				_, err = fmt.Fprintln(a.out, path)
				return err
			}

			content := string(embeddedAgentGuide)
			data := map[string]any{
				"ok":             true,
				"schema_version": guideSchemaVersion,
				"mode":           "content",
				"bytes":          len(embeddedAgentGuide),
				"content":        content,
				"source":         "embedded",
			}
			if a.opts.json || a.opts.jq != "" {
				ctx, cancel := a.commandContext(cmd)
				defer cancel()
				return a.render(ctx, "bundled agent guide", data)
			}
			_, err := fmt.Fprint(a.out, content)
			return err
		},
	}
	cmd.Flags().BoolVar(&pathOnly, "path", false, "print a readable filesystem path for the version-matched guide")
	return cmd
}

func readableGuidePath() (string, string, error) {
	for _, candidate := range guideSidecarCandidates() {
		content, err := os.ReadFile(candidate)
		if err == nil && bytes.Equal(content, embeddedAgentGuide) {
			return candidate, "installed-sidecar", nil
		}
	}

	file, err := os.CreateTemp("", "cdp-cli-guide-*.md")
	if err != nil {
		return "", "", err
	}
	path := file.Name()
	if _, err := file.Write(embeddedAgentGuide); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", "", err
	}
	return path, "materialized", nil
}

func guideSidecarCandidates() []string {
	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	paths := []string{executable}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil && resolved != executable {
		paths = append(paths, resolved)
	}

	candidates := make([]string, 0, len(paths)*2)
	for _, path := range paths {
		dir := filepath.Dir(path)
		candidates = append(candidates,
			filepath.Clean(filepath.Join(dir, "..", "share", "cdp-cli", "guide.md")),
			filepath.Join(dir, "guide.md"),
		)
	}
	return candidates
}
