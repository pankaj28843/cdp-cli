package wrapper

import "fmt"

func TranslateTripadvisor(args []string) ([]string, error) {
	var err error
	args, err = removeProviderBrowserMode(
		tripadvisorTranslation.entrypoint,
		args,
	)
	if err != nil {
		return nil, err
	}
	prefix := tripadvisorTranslation.prefix()
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
		return translateTripadvisorAuth(args[1:])
	case "capabilities":
		return translateTripadvisorCapabilities(args[1:])
	case "conversations":
		return translateTripadvisorConversations(args[1:])
	case "ask":
		return translateProviderAsk(
			tripadvisorTranslation,
			args[1:],
		)
	default:
		return nil, fmt.Errorf(
			"unsupported ask-tripadvisor command %q",
			args[0],
		)
	}
}

func translateTripadvisorCapabilities(args []string) ([]string, error) {
	prefix := tripadvisorTranslation.prefix()
	if len(args) == 0 {
		return fields(prefix + " capabilities --help"), nil
	}
	switch args[0] {
	case "-h", "--help":
		return fields(prefix + " capabilities --help"), nil
	case "status":
		return append(fields(prefix+" capabilities"), args[1:]...), nil
	case "refresh":
		return nil, fmt.Errorf(
			"ask-tripadvisor has no volatile model or mode catalog to refresh",
		)
	default:
		return nil, fmt.Errorf(
			"unsupported ask-tripadvisor capabilities command %q",
			args[0],
		)
	}
}

func translateTripadvisorAuth(args []string) ([]string, error) {
	prefix := tripadvisorTranslation.prefix()
	if len(args) == 0 {
		return fields(prefix + " auth --help"), nil
	}
	switch args[0] {
	case "-h", "--help":
		return fields(prefix + " auth --help"), nil
	case "status":
		return append(fields(prefix+" doctor"), args[1:]...), nil
	case "refresh":
		remaining := dropFlagWithValue(
			args[1:],
			"--wait-seconds",
		)
		global, remaining, err := extractTimeout(remaining)
		if err != nil {
			return nil, err
		}
		command := append(
			fields(prefix+" auth refresh"),
			remaining...,
		)
		return append(global, command...), nil
	default:
		return nil, fmt.Errorf(
			"unsupported ask-tripadvisor auth command %q",
			args[0],
		)
	}
}

func translateTripadvisorConversations(
	args []string,
) ([]string, error) {
	if len(args) == 0 {
		return fields(
			tripadvisorTranslation.prefix() +
				" conversations --help",
		), nil
	}
	switch args[0] {
	case "-h", "--help":
		return fields(
			tripadvisorTranslation.prefix() +
				" conversations --help",
		), nil
	case "list", "detail", "await":
		return translateProviderConversations(
			tripadvisorTranslation,
			args,
		)
	case "delete":
		return nil, fmt.Errorf(
			"ask-tripadvisor conversation deletion is unsupported because exact rendered identity is not proven",
		)
	default:
		return nil, fmt.Errorf(
			"unsupported ask-tripadvisor conversations command %q",
			args[0],
		)
	}
}
