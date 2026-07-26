package browserflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/artifacts"
)

var (
	ErrRunExists   = errors.New("browserflow recovery run already exists")
	ErrRunNotFound = errors.New("browserflow recovery run was not found")
)

type Journal interface {
	Create(context.Context, Record) error
	Save(context.Context, Record) error
	Load(context.Context, string) (Record, error)
}

type FileJournal struct {
	dir string
}

func NewFileJournal(stateDir string) (*FileJournal, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil, fmt.Errorf("state directory is required")
	}
	return &FileJournal{dir: filepath.Join(stateDir, "webagent", "recovery")}, nil
}

func (j *FileJournal) Path(runID string) (string, error) {
	if j == nil || j.dir == "" {
		return "", fmt.Errorf("file journal is not configured")
	}
	if err := validateIdentity("run_id", runID, 128); err != nil {
		return "", err
	}
	return filepath.Join(j.dir, runID+".json"), nil
}

func (j *FileJournal) Create(ctx context.Context, record Record) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate new recovery record: %w", err)
	}
	path, err := j.Path(record.RunID)
	if err != nil {
		return err
	}
	return artifacts.WithOwnerOnlyFileLock(ctx, path+".lock", func() error {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%w: %s", ErrRunExists, record.RunID)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect recovery record %s: %w", record.RunID, err)
		}
		return writeRecoveryRecord(path, record)
	})
}

func (j *FileJournal) Save(ctx context.Context, record Record) error {
	path, err := j.Path(record.RunID)
	if err != nil {
		return err
	}
	return artifacts.WithOwnerOnlyFileLock(ctx, path+".lock", func() error {
		before, err := readRecoveryRecord(path)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: %s", ErrRunNotFound, record.RunID)
			}
			return err
		}
		if err := validateRecordTransition(before, record); err != nil {
			return fmt.Errorf("validate recovery transition: %w", err)
		}
		return writeRecoveryRecord(path, record)
	})
}

func (j *FileJournal) Load(ctx context.Context, runID string) (Record, error) {
	select {
	case <-ctx.Done():
		return Record{}, ctx.Err()
	default:
	}
	path, err := j.Path(runID)
	if err != nil {
		return Record{}, err
	}
	record, err := readRecoveryRecord(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
		}
		return Record{}, err
	}
	if err := record.Validate(); err != nil {
		return Record{}, fmt.Errorf("validate recovery record %s: %w", runID, err)
	}
	return record, nil
}

func writeRecoveryRecord(path string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recovery record %s: %w", record.RunID, err)
	}
	data = append(data, '\n')
	if err := artifacts.WriteOwnerOnlyFileAtomic(path, data); err != nil {
		return fmt.Errorf("write recovery record %s: %w", record.RunID, err)
	}
	return nil
}

func readRecoveryRecord(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("parse recovery record %s: %w", path, err)
	}
	return record, nil
}
