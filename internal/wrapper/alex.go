package wrapper

import (
	"fmt"
	"strings"
)

const alexPrefix = "workflow agent alex"

func TranslateAlex(args []string) ([]string, error) {
	if len(args) == 0 {
		return fields(alexPrefix + " --help"), nil
	}
	switch args[0] {
	case "-h", "--help":
		return fields(alexPrefix + " --help"), nil
	case "--version", "version":
		return []string{"version"}, nil
	case "doctor":
		return translateAlexDirect(alexPrefix+" doctor", args[1:])
	case "auth":
		return translateAlexAuth(args[1:])
	case "capabilities":
		return translateAlexCapabilities(args[1:])
	case "catalog":
		return translateAlexNested("catalog", args[1:])
	case "courses":
		return translateAlexNested("courses", args[1:])
	case "chapters":
		return translateAlexNested("chapters", args[1:])
	case "content":
		return translateAlexNested("content", args[1:])
	case "ask":
		return translateAlexAsk(args[1:])
	default:
		return nil, fmt.Errorf(
			"unsupported ask-alex command %q",
			args[0],
		)
	}
}

func translateAlexCapabilities(args []string) ([]string, error) {
	if len(args) == 0 {
		return fields(alexPrefix + " capabilities --help"), nil
	}
	switch args[0] {
	case "-h", "--help":
		return fields(alexPrefix + " capabilities --help"), nil
	case "status":
		return translateAlexDirect(
			alexPrefix+" capabilities",
			args[1:],
		)
	case "refresh":
		return nil, fmt.Errorf(
			"ask-alex has no volatile model or mode catalog to refresh",
		)
	default:
		return nil, fmt.Errorf(
			"unsupported ask-alex capabilities command %q",
			args[0],
		)
	}
}

func translateAlexAuth(args []string) ([]string, error) {
	if len(args) == 0 {
		return fields(alexPrefix + " auth --help"), nil
	}
	switch args[0] {
	case "status":
		return translateAlexDirect(alexPrefix+" doctor", args[1:])
	case "refresh":
		for _, arg := range args[1:] {
			if arg == "--smoke-e2e" {
				return nil, fmt.Errorf(
					"ask-alex auth refresh --smoke-e2e is unsupported because auth refresh must not submit a provider request",
				)
			}
		}
		return translateAlexDirect(
			alexPrefix+" auth refresh",
			args[1:],
		)
	case "smoke-e2e":
		return nil, fmt.Errorf(
			"ask-alex auth smoke-e2e is unsupported; use an explicit ask with useful context",
		)
	default:
		return nil, fmt.Errorf(
			"unsupported ask-alex auth command %q",
			args[0],
		)
	}
}

func translateAlexNested(
	group string,
	args []string,
) ([]string, error) {
	if len(args) == 0 {
		return fields(alexPrefix + " " + group + " --help"), nil
	}
	allowed := map[string]map[string]bool{
		"catalog":  {"status": true, "refresh": true},
		"courses":  {"list": true},
		"chapters": {"list": true},
		"content":  {"fetch": true},
	}
	if !allowed[group][args[0]] {
		return nil, fmt.Errorf(
			"unsupported ask-alex %s command %q",
			group,
			args[0],
		)
	}
	return translateAlexDirect(
		alexPrefix+" "+group+" "+args[0],
		args[1:],
	)
}

func translateAlexDirect(
	command string,
	args []string,
) ([]string, error) {
	global, remaining, err := extractTimeout(args)
	if err != nil {
		return nil, err
	}
	remaining, err = removeAlexBrowserMode(remaining)
	if err != nil {
		return nil, err
	}
	return append(global, append(fields(command), remaining...)...), nil
}

func translateAlexAsk(args []string) ([]string, error) {
	global, remaining, err := extractTimeout(args)
	if err != nil {
		return nil, err
	}
	remaining, err = removeAlexBrowserMode(remaining)
	if err != nil {
		return nil, err
	}
	promptFlag := ""
	positional := []string{}
	passthrough := []string{}
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
		case arg == "--course" || arg == "--chapter-id":
			if index+1 >= len(remaining) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			passthrough = append(
				passthrough,
				arg,
				remaining[index+1],
			)
			index++
		case strings.HasPrefix(arg, "--course=") ||
			strings.HasPrefix(arg, "--chapter-id="):
			passthrough = append(passthrough, arg)
		case strings.HasPrefix(arg, "-"):
			passthrough = append(passthrough, arg)
		default:
			positional = append(positional, arg)
		}
	}
	if promptFlag != "" && len(positional) > 0 {
		return nil, fmt.Errorf(
			"ask-alex accepts either --prompt or positional prompt text, not both",
		)
	}
	prompt := promptFlag
	if prompt == "" {
		prompt = strings.Join(positional, " ")
	}
	command := append(fields(alexPrefix+" ask"), passthrough...)
	if prompt != "" {
		command = append(command, prompt)
	}
	return append(global, command...), nil
}

func removeAlexBrowserMode(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--browser-mode":
			if index+1 >= len(args) {
				return nil, fmt.Errorf("--browser-mode requires a value")
			}
			index++
			if args[index] != "headed" {
				return nil, fmt.Errorf(
					"ask-alex supports only the headed browser workflow",
				)
			}
		case strings.HasPrefix(arg, "--browser-mode="):
			if strings.TrimPrefix(arg, "--browser-mode=") != "headed" {
				return nil, fmt.Errorf(
					"ask-alex supports only the headed browser workflow",
				)
			}
		default:
			out = append(out, arg)
		}
	}
	return out, nil
}
