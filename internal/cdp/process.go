package cdp

import (
	"context"
)

// BrowserProcess is the privacy-safe process telemetry exposed by
// SystemInfo.getProcessInfo. CDP reports cumulative CPU time, but does not
// expose command lines, profile paths, or a stable target-to-process key.
type BrowserProcess struct {
	Type    string  `json:"type"`
	ID      int     `json:"id"`
	CPUTime float64 `json:"cpu_time"`
}

// BrowserProcessInfo is an optional browser-level process snapshot. It is
// intentionally separate from target metadata because Chrome does not expose
// a reliable mapping between the two through CDP.
type BrowserProcessInfo struct {
	ProcessCount         int              `json:"process_count"`
	RendererProcessCount int              `json:"renderer_process_count"`
	TypeCounts           map[string]int   `json:"type_counts"`
	Processes            []BrowserProcess `json:"processes"`
}

func CollectProcessInfo(ctx context.Context, client CommandClient) (BrowserProcessInfo, error) {
	var result struct {
		ProcessInfo []struct {
			Type    string  `json:"type"`
			ID      int     `json:"id"`
			CPUTime float64 `json:"cpuTime"`
		} `json:"processInfo"`
	}
	if err := client.Call(ctx, "SystemInfo.getProcessInfo", map[string]any{}, &result); err != nil {
		return BrowserProcessInfo{}, err
	}
	info := BrowserProcessInfo{
		ProcessCount: len(result.ProcessInfo),
		TypeCounts:   map[string]int{},
		Processes:    make([]BrowserProcess, 0, len(result.ProcessInfo)),
	}
	for _, process := range result.ProcessInfo {
		info.TypeCounts[process.Type]++
		if process.Type == "renderer" {
			info.RendererProcessCount++
		}
		info.Processes = append(info.Processes, BrowserProcess{
			Type:    process.Type,
			ID:      process.ID,
			CPUTime: process.CPUTime,
		})
	}
	return info, nil
}
