package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const collectorReadinessSchemaVersion = "cdp-collector-readiness/v1"

var collectorProcessStarted = time.Now()

type collectorReadinessRecord struct {
	SchemaVersion    string   `json:"schema_version"`
	State            string   `json:"state"`
	TargetID         string   `json:"target_id"`
	SessionBound     bool     `json:"session_bound"`
	EnabledDomains   []string `json:"enabled_domains"`
	ReadyMonotonicNS int64    `json:"ready_monotonic_ns"`
	CollectorPID     int      `json:"collector_pid"`
}

func publishCollectorReadiness(path, targetID, sessionID string, domains []string) (func(), error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return func() {}, nil
	}
	if strings.TrimSpace(targetID) == "" || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("readiness requires an exact target and attached session")
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("resolve ready file: %w", err)
	}
	parent := filepath.Dir(absPath)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return nil, fmt.Errorf("inspect ready file parent: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() || !collectorPathOwnedByCurrentUser(parentInfo) || parentInfo.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("ready file parent must be a caller-owned directory without group/world write access")
	}

	domains = append([]string{}, domains...)
	sort.Strings(domains)
	record := collectorReadinessRecord{
		SchemaVersion:    collectorReadinessSchemaVersion,
		State:            "ready",
		TargetID:         targetID,
		SessionBound:     true,
		EnabledDomains:   domains,
		ReadyMonotonicNS: time.Since(collectorProcessStarted).Nanoseconds(),
		CollectorPID:     os.Getpid(),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("marshal readiness: %w", err)
	}
	file, err := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create ready file exclusively: %w", err)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(absPath)
		}
	}()
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write ready file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sync ready file: %w", err)
	}
	createdInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect ready file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close ready file: %w", err)
	}
	remove = false
	return func() {
		current, err := os.Lstat(absPath)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(createdInfo, current) {
			return
		}
		_ = os.Remove(absPath)
	}, nil
}

func collectorReadinessError(err error) error {
	return commandError("collector_readiness_failed", "artifact", fmt.Sprintf("publish collector readiness: %v", err), ExitInternal, []string{"choose a new owner-only --ready-file parent", "cdp pages --json"})
}
