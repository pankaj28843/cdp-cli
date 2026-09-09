//go:build unix

package wrapper

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func Exec(name string, args []string) error {
	var translated []string
	var err error
	switch strings.TrimSpace(name) {
	case "ask-alex":
		translated, err = TranslateAlex(args)
	case "ask-chatgpt":
		translated, err = TranslateChatGPT(args)
	case "ask-claude":
		translated, err = TranslateClaude(args)
	case "ask-gemini":
		translated, err = TranslateGemini(args)
	case "ask-grok":
		translated, err = TranslateGrok(args)
	case "ask-perplexity":
		translated, err = TranslatePerplexity(args)
	case "ask-tripadvisor":
		translated, err = TranslateTripadvisor(args)
	default:
		return fmt.Errorf("unsupported agent CLI entrypoint %q", name)
	}
	if err != nil {
		return err
	}
	binary := strings.TrimSpace(os.Getenv("CDP_COMPAT_CDP_BIN"))
	if binary == "" {
		binary, err = exec.LookPath("cdp")
		if err != nil {
			return fmt.Errorf("find installed cdp: %w", err)
		}
	}
	return syscall.Exec(binary, append([]string{binary}, translated...), os.Environ())
}
