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

func knownViewportPreset(name string) (responsiveViewport, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "desktop":
		return responsiveViewport{Name: "desktop", Width: 1440, Height: 900, DeviceScaleFactor: 1, Mobile: false}, true
	case "laptop":
		return responsiveViewport{Name: "laptop", Width: 1366, Height: 768, DeviceScaleFactor: 1, Mobile: false}, true
	case "tablet":
		return responsiveViewport{Name: "tablet", Width: 834, Height: 1112, DeviceScaleFactor: 2, Mobile: true}, true
	case "mobile":
		return responsiveViewport{Name: "mobile", Width: 390, Height: 844, DeviceScaleFactor: 3, Mobile: true}, true
	case "iphone-12":
		return responsiveViewport{Name: "iphone-12", Width: 390, Height: 844, DeviceScaleFactor: 3, Mobile: true}, true
	default:
		return responsiveViewport{}, false
	}
}

func viewportPreset(name string) (int, int, float64, bool) {
	preset, ok := knownViewportPreset(name)
	if !ok {
		return 0, 0, 0, false
	}
	return preset.Width, preset.Height, preset.DeviceScaleFactor, preset.Mobile
}

func viewportPresetParams(preset responsiveViewport) map[string]any {
	return map[string]any{
		"width":             preset.Width,
		"height":            preset.Height,
		"deviceScaleFactor": preset.DeviceScaleFactor,
		"mobile":            preset.Mobile,
	}
}

func responsiveViewportPresets(raw string) ([]responsiveViewport, error) {
	parts := strings.Split(raw, ",")
	out := []responsiveViewport{}
	for _, part := range parts {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		vp, ok := knownViewportPreset(name)
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
