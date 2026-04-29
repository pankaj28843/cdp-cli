package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type LockMetadata struct {
	Name      string `json:"name"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
	Phase     string `json:"phase,omitempty"`
}

type LockHandle struct {
	Path     string
	Metadata LockMetadata
}

func AcquireLock(ctx context.Context, stateDir, name string, timeout, staleAfter time.Duration, metadata LockMetadata) (LockHandle, bool, LockMetadata, error) {
	if strings.TrimSpace(stateDir) == "" {
		return LockHandle{}, false, LockMetadata{}, fmt.Errorf("state directory is required for lock")
	}
	name = sanitizeLockName(name)
	path := filepath.Join(stateDir, "locks", name+".lock")
	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return LockHandle{}, false, LockMetadata{}, ctx.Err()
		default:
		}

		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return LockHandle{}, false, LockMetadata{}, fmt.Errorf("create lock directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if metadata.Name == "" {
				metadata.Name = name
			}
			if metadata.PID == 0 {
				metadata.PID = os.Getpid()
			}
			if metadata.StartedAt == "" {
				metadata.StartedAt = time.Now().UTC().Format(time.RFC3339)
			}
			if err := json.NewEncoder(file).Encode(metadata); err != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return LockHandle{}, false, LockMetadata{}, fmt.Errorf("write lock metadata: %w", err)
			}
			if err := file.Close(); err != nil {
				_ = os.Remove(path)
				return LockHandle{}, false, LockMetadata{}, fmt.Errorf("close lock metadata: %w", err)
			}
			return LockHandle{Path: path, Metadata: metadata}, true, LockMetadata{}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return LockHandle{}, false, LockMetadata{}, fmt.Errorf("create lock file: %w", err)
		}

		existing, stale := readLockMetadata(path, staleAfter)
		if stale {
			if removeErr := os.Remove(path); removeErr == nil || os.IsNotExist(removeErr) {
				continue
			}
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return LockHandle{Path: path}, false, existing, nil
		}

		sleep := 100 * time.Millisecond
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if sleep <= 0 {
			return LockHandle{Path: path}, false, existing, nil
		}
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return LockHandle{}, false, LockMetadata{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (h LockHandle) Update(ctx context.Context, phase string) error {
	if strings.TrimSpace(h.Path) == "" {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	h.Metadata.Phase = phase
	b, err := json.MarshalIndent(h.Metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lock metadata: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(h.Path, b, 0o600); err != nil {
		return fmt.Errorf("write lock metadata: %w", err)
	}
	return nil
}

func (h LockHandle) Release() error {
	if strings.TrimSpace(h.Path) == "" {
		return nil
	}
	if err := os.Remove(h.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release lock: %w", err)
	}
	return nil
}

func readLockMetadata(path string, staleAfter time.Duration) (LockMetadata, bool) {
	info, statErr := os.Stat(path)
	var metadata LockMetadata
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &metadata)
	}
	if staleAfter <= 0 {
		return metadata, false
	}
	var started time.Time
	if metadata.StartedAt != "" {
		started, _ = time.Parse(time.RFC3339, metadata.StartedAt)
	}
	if started.IsZero() && statErr == nil {
		started = info.ModTime()
	}
	return metadata, !started.IsZero() && time.Since(started) > staleAfter
}

func sanitizeLockName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "default"
	}
	return out
}
