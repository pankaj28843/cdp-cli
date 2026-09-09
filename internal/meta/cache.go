package meta

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const alexCacheTTL = 24 * time.Hour

type CacheEntry struct {
	IntegrationID string   `json:"integration_id"`
	Kind          string   `json:"kind"`
	Path          string   `json:"path"`
	Exists        bool     `json:"exists"`
	TTLSeconds    int      `json:"ttl_seconds"`
	RefreshedAt   string   `json:"refreshed_at,omitempty"`
	AgeSeconds    *float64 `json:"age_seconds"`
	Stale         bool     `json:"stale"`
	CourseCount   int      `json:"course_count,omitempty"`
	ChapterCount  int      `json:"chapter_count,omitempty"`
	FileCount     int      `json:"file_count"`
	ByteCount     int64    `json:"byte_count"`
}

type CacheStatusReport struct {
	OK        bool         `json:"ok"`
	StateRoot string       `json:"state_root"`
	Entries   []CacheEntry `json:"entries"`
}

type CacheCleanReport struct {
	OK               bool     `json:"ok"`
	IntegrationID    string   `json:"integration_id"`
	Kind             string   `json:"kind"`
	DryRun           bool     `json:"dry_run"`
	Deleted          []string `json:"deleted"`
	WouldDelete      []string `json:"would_delete"`
	DeletedCount     int      `json:"deleted_count"`
	WouldDeleteCount int      `json:"would_delete_count"`
}

func cacheStatus(now time.Time) (CacheStatusReport, error) {
	root, catalogPath, contentPath, err := alexCachePaths()
	if err != nil {
		return CacheStatusReport{}, err
	}
	catalog, err := catalogCacheEntry(catalogPath, now)
	if err != nil {
		return CacheStatusReport{}, err
	}
	content, err := contentCacheEntry(contentPath, now)
	if err != nil {
		return CacheStatusReport{}, err
	}
	return CacheStatusReport{
		OK:        true,
		StateRoot: displayPath(root),
		Entries:   []CacheEntry{catalog, content},
	}, nil
}

func cleanCache(
	integrationID string,
	kind string,
	dryRun bool,
) (CacheCleanReport, error) {
	if _, err := integrationByID(integrationID); err != nil {
		return CacheCleanReport{}, err
	}
	if integrationID != "ask-alex" {
		return CacheCleanReport{}, fmt.Errorf(
			"cache management is not implemented for %s",
			integrationID,
		)
	}
	_, catalogPath, contentPath, err := alexCachePaths()
	if err != nil {
		return CacheCleanReport{}, err
	}
	var targets []string
	switch kind {
	case "catalog":
		targets = []string{catalogPath}
	case "content":
		targets = []string{contentPath}
	case "all":
		targets = []string{catalogPath, contentPath}
	default:
		return CacheCleanReport{}, fmt.Errorf(
			"cache kind must be catalog, content, or all",
		)
	}
	report := CacheCleanReport{
		OK:            true,
		IntegrationID: integrationID,
		Kind:          kind,
		DryRun:        dryRun,
		Deleted:       []string{},
		WouldDelete:   []string{},
	}
	existing := make([]string, 0, len(targets))
	for _, target := range targets {
		info, statErr := os.Lstat(target)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return CacheCleanReport{}, fmt.Errorf(
				"inspect cache target: %w",
				statErr,
			)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return CacheCleanReport{}, fmt.Errorf(
				"refusing symlink cache target %s",
				displayPath(target),
			)
		}
		existing = append(existing, target)
		report.WouldDelete = append(
			report.WouldDelete,
			displayPath(target),
		)
	}
	report.WouldDeleteCount = len(report.WouldDelete)
	if dryRun {
		return report, nil
	}
	for _, target := range existing {
		info, statErr := os.Lstat(target)
		if statErr != nil {
			return CacheCleanReport{}, fmt.Errorf(
				"recheck cache target: %w",
				statErr,
			)
		}
		if info.IsDir() {
			if err := os.RemoveAll(target); err != nil {
				return CacheCleanReport{}, fmt.Errorf(
					"delete cache directory: %w",
					err,
				)
			}
		} else {
			if !info.Mode().IsRegular() {
				return CacheCleanReport{}, fmt.Errorf(
					"refusing non-regular cache target %s",
					displayPath(target),
				)
			}
			if err := os.Remove(target); err != nil {
				return CacheCleanReport{}, fmt.Errorf(
					"delete cache file: %w",
					err,
				)
			}
		}
		report.Deleted = append(report.Deleted, displayPath(target))
	}
	report.DeletedCount = len(report.Deleted)
	return report, nil
}

func alexCachePaths() (string, string, string, error) {
	root, err := cdpStateRoot()
	if err != nil {
		return "", "", "", err
	}
	resolvedRoot := root
	if _, statErr := os.Stat(root); statErr == nil {
		resolvedRoot, err = filepath.EvalSymlinks(root)
		if err != nil {
			return "", "", "", fmt.Errorf(
				"resolve cdp state root: %w",
				err,
			)
		}
		resolvedRoot, err = absoluteClean(resolvedRoot)
		if err != nil {
			return "", "", "", err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", "", "", fmt.Errorf(
			"inspect cdp state root: %w",
			statErr,
		)
	}
	alexRoot := filepath.Join(resolvedRoot, "webagent", "alex")
	if _, statErr := os.Stat(alexRoot); statErr == nil {
		resolvedAlex, resolveErr := filepath.EvalSymlinks(alexRoot)
		if resolveErr != nil {
			return "", "", "", fmt.Errorf(
				"resolve Ask Alex state root: %w",
				resolveErr,
			)
		}
		relative, relativeErr := filepath.Rel(resolvedRoot, resolvedAlex)
		if relativeErr != nil ||
			relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", "", "", fmt.Errorf(
				"refusing Ask Alex state outside the cdp state root",
			)
		}
		alexRoot = resolvedAlex
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", "", "", fmt.Errorf(
			"inspect Ask Alex state root: %w",
			statErr,
		)
	}
	return resolvedRoot,
		filepath.Join(alexRoot, "catalog.json"),
		filepath.Join(alexRoot, "content"),
		nil
}

func catalogCacheEntry(path string, now time.Time) (CacheEntry, error) {
	entry := CacheEntry{
		IntegrationID: "ask-alex",
		Kind:          "catalog",
		Path:          displayPath(path),
		TTLSeconds:    int(alexCacheTTL.Seconds()),
		Stale:         true,
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return entry, nil
	}
	if err != nil {
		return CacheEntry{}, fmt.Errorf("inspect catalog cache: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return CacheEntry{}, fmt.Errorf(
			"catalog cache is not a regular non-symlink file",
		)
	}
	entry.Exists = true
	entry.FileCount = 1
	entry.ByteCount = info.Size()
	var payload struct {
		RefreshedAt string                       `json:"refreshed_at"`
		Courses     map[string]json.RawMessage   `json:"courses"`
		Chapters    map[string][]json.RawMessage `json:"chapters"`
	}
	file, err := os.Open(path)
	if err != nil {
		return CacheEntry{}, fmt.Errorf("open catalog cache: %w", err)
	}
	decodeErr := json.NewDecoder(file).Decode(&payload)
	closeErr := file.Close()
	if decodeErr != nil {
		return CacheEntry{}, fmt.Errorf("parse catalog cache: %w", decodeErr)
	}
	if closeErr != nil {
		return CacheEntry{}, fmt.Errorf("close catalog cache: %w", closeErr)
	}
	entry.RefreshedAt = payload.RefreshedAt
	entry.CourseCount = len(payload.Courses)
	for _, chapters := range payload.Chapters {
		entry.ChapterCount += len(chapters)
	}
	refreshed, parseErr := time.Parse(time.RFC3339Nano, payload.RefreshedAt)
	if parseErr == nil {
		age := max(0, now.Sub(refreshed).Seconds())
		entry.AgeSeconds = &age
		entry.Stale = now.Sub(refreshed) > alexCacheTTL
	}
	return entry, nil
}

func contentCacheEntry(path string, now time.Time) (CacheEntry, error) {
	entry := CacheEntry{
		IntegrationID: "ask-alex",
		Kind:          "content",
		Path:          displayPath(path),
		TTLSeconds:    int(alexCacheTTL.Seconds()),
		Stale:         true,
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return entry, nil
	}
	if err != nil {
		return CacheEntry{}, fmt.Errorf("inspect content cache: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return CacheEntry{}, fmt.Errorf(
			"content cache is not a real directory",
		)
	}
	entry.Exists = true
	var newest time.Time
	walkErr := filepath.WalkDir(path, func(
		itemPath string,
		item fs.DirEntry,
		walkError error,
	) error {
		if walkError != nil {
			return walkError
		}
		if item.IsDir() || item.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, infoErr := item.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		entry.FileCount++
		entry.ByteCount += info.Size()
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if walkErr != nil {
		return CacheEntry{}, fmt.Errorf("inspect content cache: %w", walkErr)
	}
	if !newest.IsZero() {
		entry.RefreshedAt = newest.UTC().Format(time.RFC3339Nano)
		age := max(0, now.Sub(newest).Seconds())
		entry.AgeSeconds = &age
		entry.Stale = now.Sub(newest) > alexCacheTTL
	}
	return entry, nil
}
