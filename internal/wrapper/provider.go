package wrapper

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type providerTranslation struct {
	entrypoint               string
	provider                 string
	runtimeCapabilityRefresh bool
}

var (
	claudeTranslation = providerTranslation{
		entrypoint: "ask-claude",
		provider:   "claude",
	}
	chatgptTranslation = providerTranslation{
		entrypoint:               "ask-chatgpt",
		provider:                 "chatgpt",
		runtimeCapabilityRefresh: true,
	}
	geminiTranslation = providerTranslation{
		entrypoint:               "ask-gemini",
		provider:                 "gemini",
		runtimeCapabilityRefresh: true,
	}
	grokTranslation = providerTranslation{
		entrypoint:               "ask-grok",
		provider:                 "grok",
		runtimeCapabilityRefresh: true,
	}
	perplexityTranslation = providerTranslation{
		entrypoint:               "ask-perplexity",
		provider:                 "perplexity",
		runtimeCapabilityRefresh: true,
	}
	tripadvisorTranslation = providerTranslation{
		entrypoint: "ask-tripadvisor",
		provider:   "tripadvisor",
	}
)

func TranslateClaude(args []string) ([]string, error) {
	return translateProvider(claudeTranslation, args)
}

func TranslateChatGPT(args []string) ([]string, error) {
	return translateProvider(chatgptTranslation, args)
}

func TranslateGemini(args []string) ([]string, error) {
	return translateProvider(geminiTranslation, args)
}

func TranslateGrok(args []string) ([]string, error) {
	return translateProvider(grokTranslation, args)
}

func TranslatePerplexity(args []string) ([]string, error) {
	return translateProvider(perplexityTranslation, args)
}

func translateProvider(
	config providerTranslation,
	args []string,
) ([]string, error) {
	var err error
	args, err = removeProviderBrowserMode(config.entrypoint, args)
	if err != nil {
		return nil, err
	}
	prefix := config.prefix()
	if len(args) == 0 {
		return fields(prefix + " --help"), nil
	}
	switch args[0] {
	case "-h", "--help":
		return fields(prefix + " --help"), nil
	case "--version", "version":
		return []string{"version"}, nil
	case "doctor":
		return append(fields(prefix+" doctor"), args[1:]...), nil
	case "auth":
		return translateProviderAuth(config, args[1:])
	case "capabilities":
		return translateProviderCapabilities(config, args[1:])
	case "conversations":
		return translateProviderConversations(config, args[1:])
	case "ask":
		return translateProviderAsk(config, args[1:])
	case "calibration", "calibrate":
		return nil, fmt.Errorf(
			"unsupported %s command %q",
			config.entrypoint,
			args[0],
		)
	case "research":
		if config.provider != "chatgpt" {
			return nil, fmt.Errorf(
				"unsupported %s command %q",
				config.entrypoint,
				args[0],
			)
		}
		return translateChatGPTResearch(config, args[1:])
	default:
		global, remaining, err := extractTimeout(args)
		if err != nil {
			return nil, err
		}
		return append(global, append(fields(prefix), remaining...)...), nil
	}
}

func translateProviderAuth(
	config providerTranslation,
	args []string,
) ([]string, error) {
	prefix := config.prefix()
	if len(args) == 0 {
		return fields(prefix + " auth --help"), nil
	}
	switch args[0] {
	case "-h", "--help":
		return fields(prefix + " auth --help"), nil
	case "status":
		return append(fields(prefix+" doctor"), args[1:]...), nil
	case "refresh":
		if config.provider == "chatgpt" &&
			hasBooleanFlag(args[1:], "--dry-run") {
			return nil, fmt.Errorf(
				"ask-chatgpt auth refresh --dry-run is unsupported; no browser work was performed",
			)
		}
		rest := dropFlagWithValue(args[1:], "--wait-seconds")
		return append(fields(prefix+" auth refresh"), rest...), nil
	default:
		return nil, fmt.Errorf(
			"unsupported %s auth command %q",
			config.entrypoint,
			args[0],
		)
	}
}

func translateProviderCapabilities(
	config providerTranslation,
	args []string,
) ([]string, error) {
	prefix := config.prefix()
	if len(args) == 0 {
		return fields(prefix + " capabilities --help"), nil
	}
	switch args[0] {
	case "-h", "--help":
		return fields(prefix + " capabilities --help"), nil
	case "status":
		return append(fields(prefix+" capabilities"), args[1:]...), nil
	case "refresh":
		if config.provider == "chatgpt" &&
			hasBooleanFlag(args[1:], "--dry-run") {
			return nil, fmt.Errorf(
				"ask-chatgpt capabilities refresh --dry-run is unsupported; no browser work was performed",
			)
		}
		rest := dropBooleanFlag(args[1:], "--dry-run")
		command := prefix + " capabilities"
		if config.runtimeCapabilityRefresh {
			command += " refresh"
		}
		return append(fields(command), rest...), nil
	default:
		return append(fields(prefix+" capabilities"), args...), nil
	}
}

func translateProviderConversations(
	config providerTranslation,
	args []string,
) ([]string, error) {
	prefix := config.prefix()
	if len(args) == 0 {
		return fields(prefix + " conversations --help"), nil
	}
	switch args[0] {
	case "-h", "--help":
		return fields(prefix + " conversations --help"), nil
	case "list", "detail", "await", "delete":
	case "continue", "download-artifact", "download-attachments", "export-research":
		if config.provider != "chatgpt" {
			return nil, fmt.Errorf(
				"unsupported %s conversations command %q",
				config.entrypoint,
				args[0],
			)
		}
	default:
		return nil, fmt.Errorf(
			"unsupported %s conversations command %q",
			config.entrypoint,
			args[0],
		)
	}
	global, remaining, err := extractTimeout(args[1:])
	if err != nil {
		return nil, err
	}
	command := append(
		fields(prefix+" conversations "+args[0]),
		remaining...,
	)
	if config.provider == "chatgpt" &&
		args[0] == "await" &&
		len(global) == 2 &&
		!hasValueFlag(remaining, "--wait") {
		// Legacy users reasonably expect --timeout to bound the provider wait,
		// not only the outer process context. Keep explicit --wait authoritative.
		command = append(command, "--wait", global[1])
		wait, _ := time.ParseDuration(global[1])
		global[1] = (wait + 30*time.Second).String()
	}
	return append(global, command...), nil
}

func translateProviderAsk(
	config providerTranslation,
	args []string,
) ([]string, error) {
	global, remaining, err := extractTimeout(args)
	if err != nil {
		return nil, err
	}
	promptFlag := ""
	promptWords := make([]string, 0)
	passthroughFlags := make([]string, 0)
	valueFlags := map[string]bool{
		"--file":             true,
		"--thinking":         true,
		"--reasoning":        true,
		"--intelligence":     true,
		"--minimum-thinking": true,
		"--model":            true,
		"--tool":             true,
	}
	for index := 0; index < len(remaining); index++ {
		arg := remaining[index]
		switch {
		case arg == "--prompt":
			if index+1 >= len(remaining) {
				return nil, fmt.Errorf("--prompt requires a value")
			}
			index++
			promptFlag = remaining[index]
		case strings.HasPrefix(arg, "--prompt="):
			promptFlag = strings.TrimPrefix(arg, "--prompt=")
		case valueFlags[arg]:
			if index+1 >= len(remaining) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			index++
			passthroughFlags = append(
				passthroughFlags,
				arg,
				remaining[index],
			)
		case strings.Contains(arg, "=") &&
			valueFlags[strings.SplitN(arg, "=", 2)[0]]:
			passthroughFlags = append(
				passthroughFlags,
				arg,
			)
		case strings.HasPrefix(arg, "-"):
			promptWords = append(promptWords, arg)
		default:
			promptWords = append(promptWords, arg)
		}
	}
	command := fields(config.prefix() + " ask")
	command = append(command, passthroughFlags...)
	flags := make([]string, 0)
	words := make([]string, 0)
	for _, value := range promptWords {
		if strings.HasPrefix(value, "-") {
			flags = append(flags, value)
		} else {
			words = append(words, value)
		}
	}
	command = append(command, flags...)
	prompt := promptFlag
	if prompt == "" && len(words) > 0 {
		prompt = strings.Join(words, " ")
	}
	if prompt != "" {
		command = append(command, prompt)
	}
	return append(global, command...), nil
}

func removeProviderBrowserMode(entrypoint string, args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--browser-mode" || arg == "--browserMode":
			if index+1 >= len(args) {
				return nil, fmt.Errorf("%s --browser-mode requires a value", entrypoint)
			}
			index++
			if strings.ToLower(strings.TrimSpace(args[index])) != "headed" {
				return nil, fmt.Errorf(
					"%s supports only the headed browser workflow",
					entrypoint,
				)
			}
		case strings.HasPrefix(arg, "--browser-mode=") ||
			strings.HasPrefix(arg, "--browserMode="):
			value := arg[strings.IndexByte(arg, '=')+1:]
			if strings.ToLower(strings.TrimSpace(value)) != "headed" {
				return nil, fmt.Errorf(
					"%s supports only the headed browser workflow",
					entrypoint,
				)
			}
		default:
			out = append(out, arg)
		}
	}
	return out, nil
}

func translateChatGPTResearch(
	config providerTranslation,
	args []string,
) ([]string, error) {
	global, remaining, err := extractTimeout(args)
	if err != nil {
		return nil, err
	}
	flags := make([]string, 0)
	words := make([]string, 0)
	for _, value := range remaining {
		if strings.HasPrefix(value, "-") {
			flags = append(flags, value)
		} else {
			words = append(words, value)
		}
	}
	command := fields(config.prefix() + " research")
	command = append(command, flags...)
	if len(words) > 0 {
		command = append(command, strings.Join(words, " "))
	}
	return append(global, command...), nil
}

func (c providerTranslation) prefix() string {
	return "workflow agent " + c.provider
}

func extractTimeout(args []string) ([]string, []string, error) {
	global := make([]string, 0, 2)
	remaining := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--timeout":
			if index+1 >= len(args) {
				return nil, nil, fmt.Errorf("--timeout requires a value")
			}
			index++
			value, err := normalizeTimeout(args[index])
			if err != nil {
				return nil, nil, err
			}
			global = append(global, "--timeout", value)
		case strings.HasPrefix(arg, "--timeout="):
			value, err := normalizeTimeout(strings.TrimPrefix(arg, "--timeout="))
			if err != nil {
				return nil, nil, err
			}
			global = append(global, "--timeout", value)
		default:
			remaining = append(remaining, arg)
		}
	}
	return global, remaining, nil
}

func normalizeTimeout(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("timeout must not be empty")
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return "", fmt.Errorf("timeout must be greater than zero")
		}
		return time.Duration(seconds * float64(time.Second)).String(), nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return "", fmt.Errorf("invalid timeout %q", value)
	}
	return duration.String(), nil
}

func dropFlagWithValue(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == name {
			if index+1 < len(args) {
				index++
			}
			continue
		}
		if strings.HasPrefix(args[index], name+"=") {
			continue
		}
		out = append(out, args[index])
	}
	return out
}

func dropBooleanFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != name {
			out = append(out, arg)
		}
	}
	return out
}

func hasBooleanFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

func hasValueFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func fields(value string) []string {
	return strings.Fields(value)
}
