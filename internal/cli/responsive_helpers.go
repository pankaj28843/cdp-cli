package cli

import (
	"fmt"
	"strings"
)

type responsiveViewport struct {
	Name              string
	Width             int
	Height            int
	DeviceScaleFactor float64
	Mobile            bool
}

func responsiveViewportPresets(raw string) ([]responsiveViewport, error) {
	known := map[string]responsiveViewport{
		"desktop": {Name: "desktop", Width: 1440, Height: 900, DeviceScaleFactor: 1, Mobile: false},
		"tablet":  {Name: "tablet", Width: 834, Height: 1112, DeviceScaleFactor: 2, Mobile: true},
		"mobile":  {Name: "mobile", Width: 390, Height: 844, DeviceScaleFactor: 3, Mobile: true},
	}
	parts := strings.Split(raw, ",")
	out := []responsiveViewport{}
	for _, part := range parts {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		vp, ok := known[name]
		if !ok {
			return nil, commandError("invalid_viewport", "usage", fmt.Sprintf("unknown viewport preset %q", name), ExitUsage, []string{"cdp workflow responsive-audit https://example.com --viewports desktop,tablet,mobile --json"})
		}
		out = append(out, vp)
	}
	if len(out) == 0 {
		return nil, commandError("invalid_viewport", "usage", "at least one viewport is required", ExitUsage, []string{"cdp workflow responsive-audit https://example.com --viewports mobile --json"})
	}
	return out, nil
}

func countFailedRequests(requests []networkRequest) int {
	count := 0
	for _, request := range requests {
		if request.Failed || request.Status >= 400 {
			count++
		}
	}
	return count
}

func countConsoleIssues(messages []consoleMessage) int {
	count := 0
	for _, message := range messages {
		level := strings.ToLower(message.Level)
		if level == "error" || level == "warning" || message.Type == "exception" || message.Type == "assert" {
			count++
		}
	}
	return count
}
