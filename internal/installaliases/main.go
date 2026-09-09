package installaliases

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

var DefaultAliases = []string{
	"ask-alex",
	"ask-chatgpt",
	"ask-claude",
	"ask-gemini",
	"ask-grok",
	"ask-perplexity",
	"ask-tripadvisor",
	"agent-web",
}

func Main(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	flags := flag.NewFlagSet("install-aliases", flag.ContinueOnError)
	flags.SetOutput(stderr)
	binDirectory := flags.String(
		"bin-dir",
		"",
		"existing directory in which to atomically publish aliases",
	)
	source := flags.String(
		"source",
		"",
		"regular executable to publish (defaults to this executable)",
	)
	jsonOutput := flags.Bool("json", false, "emit a JSON install report")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *binDirectory == "" {
		fmt.Fprintln(
			stderr,
			"usage: cdp install-aliases --bin-dir DIR [--source FILE] [--json]",
		)
		return 2
	}
	if *source == "" {
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintln(stderr, "resolve installer executable:", err)
			return 1
		}
		*source = executable
	}
	report, err := Install(*source, *binDirectory, DefaultAliases)
	if *jsonOutput {
		if encodeErr := json.NewEncoder(stdout).Encode(report); encodeErr != nil {
			fmt.Fprintln(stderr, "encode install report:", encodeErr)
			return 1
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "install aliases:", err)
		return 1
	}
	if !*jsonOutput {
		fmt.Fprintf(
			stdout,
			"installed %d aliases in %s\n",
			len(report.Aliases),
			report.BinDirectory,
		)
	}
	return 0
}
