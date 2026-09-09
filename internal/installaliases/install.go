package installaliases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Report struct {
	OK           bool     `json:"ok"`
	SourceSHA256 string   `json:"source_sha256"`
	BinDirectory string   `json:"bin_directory"`
	Aliases      []string `json:"aliases"`
	Probe        string   `json:"probe"`
	RolledBack   bool     `json:"rolled_back"`
}

type installOptions struct {
	probe func(string) error
}

type stagedAlias struct {
	name        string
	destination string
	staged      string
	backup      string
	hadBackup   bool
	published   bool
}

func Install(
	sourcePath string,
	binDirectory string,
	aliases []string,
) (Report, error) {
	return install(sourcePath, binDirectory, aliases, installOptions{
		probe: probeHelp,
	})
}

func install(
	sourcePath string,
	binDirectory string,
	aliases []string,
	options installOptions,
) (Report, error) {
	report := Report{
		Aliases: append([]string(nil), aliases...),
		Probe:   "--help",
	}
	source, sourceMode, sourceHash, err := validateSource(sourcePath)
	if err != nil {
		return report, err
	}
	report.SourceSHA256 = sourceHash
	destinationDirectory, err := validateBinDirectory(binDirectory)
	if err != nil {
		return report, err
	}
	report.BinDirectory = destinationDirectory
	if err := validateAliases(aliases); err != nil {
		return report, err
	}
	stageDirectory, err := os.MkdirTemp(
		destinationDirectory,
		".cdp-cli-install-*",
	)
	if err != nil {
		return report, fmt.Errorf("create alias staging directory: %w", err)
	}
	if err := os.Chmod(stageDirectory, 0o700); err != nil {
		_ = os.RemoveAll(stageDirectory)
		return report, fmt.Errorf("protect alias staging directory: %w", err)
	}
	defer os.RemoveAll(stageDirectory)

	staged := make([]stagedAlias, 0, len(aliases))
	for _, alias := range aliases {
		item, stageErr := stageAlias(
			source,
			sourceMode,
			sourceHash,
			destinationDirectory,
			stageDirectory,
			alias,
		)
		if stageErr != nil {
			return report, stageErr
		}
		staged = append(staged, item)
	}
	if err := syncDirectory(stageDirectory); err != nil {
		return report, fmt.Errorf("sync alias staging directory: %w", err)
	}

	for index := range staged {
		item := &staged[index]
		if err := os.Rename(item.staged, item.destination); err != nil {
			report.RolledBack = true
			rollbackErr := rollback(staged[:index])
			if rollbackErr != nil {
				return report, fmt.Errorf(
					"publish alias %q: %w; rollback: %v",
					item.name,
					err,
					rollbackErr,
				)
			}
			return report, fmt.Errorf("publish alias %q: %w", item.name, err)
		}
		item.published = true
	}
	if err := syncDirectory(destinationDirectory); err != nil {
		report.RolledBack = true
		rollbackErr := rollback(staged)
		if rollbackErr != nil {
			return report, fmt.Errorf(
				"sync published aliases: %w; rollback: %v",
				err,
				rollbackErr,
			)
		}
		return report, fmt.Errorf("sync published aliases: %w", err)
	}
	for _, item := range staged {
		if err := verifyPublished(item.destination, sourceHash); err != nil {
			report.RolledBack = true
			rollbackErr := rollback(staged)
			if rollbackErr != nil {
				return report, fmt.Errorf(
					"verify alias %q: %w; rollback: %v",
					item.name,
					err,
					rollbackErr,
				)
			}
			return report, fmt.Errorf("verify alias %q: %w", item.name, err)
		}
	}
	for _, item := range staged {
		if err := options.probe(item.destination); err != nil {
			report.RolledBack = true
			rollbackErr := rollback(staged)
			if rollbackErr != nil {
				return report, fmt.Errorf(
					"probe alias %q: %w; rollback: %v",
					item.name,
					err,
					rollbackErr,
				)
			}
			return report, fmt.Errorf("probe alias %q: %w", item.name, err)
		}
	}
	report.OK = true
	return report, nil
}

func validateSource(path string) (string, os.FileMode, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", 0, "", fmt.Errorf("resolve source binary: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", 0, "", fmt.Errorf("inspect source binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, "", fmt.Errorf(
			"source binary must be a regular non-symlink file",
		)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", 0, "", fmt.Errorf("source binary must be executable")
	}
	hash, err := fileSHA256(absolute)
	if err != nil {
		return "", 0, "", err
	}
	return absolute, info.Mode().Perm(), hash, nil
}

func validateBinDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve bin directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve bin directory symlinks: %w", err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect bin directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf(
			"bin directory must resolve to a real directory",
		)
	}
	return resolved, nil
}

func validateAliases(aliases []string) error {
	if len(aliases) == 0 {
		return fmt.Errorf("at least one alias is required")
	}
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		if alias == "" ||
			alias == "." ||
			alias == ".." ||
			filepath.Base(alias) != alias ||
			strings.ContainsAny(alias, `/\`) {
			return fmt.Errorf("alias %q is not a plain filename", alias)
		}
		if _, found := seen[alias]; found {
			return fmt.Errorf("alias %q is duplicated", alias)
		}
		seen[alias] = struct{}{}
	}
	return nil
}

func stageAlias(
	source string,
	sourceMode os.FileMode,
	sourceHash string,
	binDirectory string,
	stageDirectory string,
	alias string,
) (stagedAlias, error) {
	item := stagedAlias{
		name:        alias,
		destination: filepath.Join(binDirectory, alias),
		staged:      filepath.Join(stageDirectory, "new-"+alias),
		backup:      filepath.Join(stageDirectory, "old-"+alias),
	}
	if info, err := os.Lstat(item.destination); err == nil {
		if !info.Mode().IsRegular() ||
			info.Mode()&os.ModeSymlink != 0 {
			return stagedAlias{}, fmt.Errorf(
				"existing alias %q must be a regular non-symlink file",
				alias,
			)
		}
		if err := copyFile(
			item.destination,
			item.backup,
			info.Mode().Perm(),
		); err != nil {
			return stagedAlias{}, fmt.Errorf(
				"back up alias %q: %w",
				alias,
				err,
			)
		}
		item.hadBackup = true
	} else if !os.IsNotExist(err) {
		return stagedAlias{}, fmt.Errorf(
			"inspect existing alias %q: %w",
			alias,
			err,
		)
	}
	mode := sourceMode.Perm()
	if mode&0o111 == 0 {
		mode = 0o755
	}
	if err := copyFile(source, item.staged, mode); err != nil {
		return stagedAlias{}, fmt.Errorf("stage alias %q: %w", alias, err)
	}
	if err := verifyPublished(item.staged, sourceHash); err != nil {
		return stagedAlias{}, fmt.Errorf(
			"verify staged alias %q: %w",
			alias,
			err,
		)
	}
	return item, nil
}

func copyFile(source string, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		mode.Perm(),
	)
	if err != nil {
		return err
	}
	copied := false
	defer func() {
		_ = output.Close()
		if !copied {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	copied = true
	return nil
}

func verifyPublished(path string, sourceHash string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() ||
		info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("installed alias is not a regular executable")
	}
	hash, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if hash != sourceHash {
		return fmt.Errorf("installed alias hash does not match source")
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func probeHelp(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--help")
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("help probe timed out")
		}
		return err
	}
	return nil
}

func rollback(items []stagedAlias) error {
	var failures []string
	for index := len(items) - 1; index >= 0; index-- {
		item := items[index]
		if !item.published {
			continue
		}
		if item.hadBackup {
			if err := os.Rename(item.backup, item.destination); err != nil {
				failures = append(failures, item.name)
			}
			continue
		}
		if err := os.Remove(item.destination); err != nil &&
			!os.IsNotExist(err) {
			failures = append(failures, item.name)
		}
	}
	if len(items) > 0 {
		_ = syncDirectory(filepath.Dir(items[0].destination))
	}
	if len(failures) > 0 {
		return fmt.Errorf(
			"could not restore aliases: %s",
			strings.Join(failures, ", "),
		)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
