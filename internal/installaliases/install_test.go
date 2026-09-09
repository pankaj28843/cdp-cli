package installaliases

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPublishesAndProbesCompleteAliasSet(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "cdp")
	binDirectory := filepath.Join(root, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	sourceBytes := []byte("new-go-binary")
	if err := os.WriteFile(source, sourceBytes, 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(binDirectory, "ask-one"),
		[]byte("old-binary"),
		0o755,
	); err != nil {
		t.Fatalf("write existing alias: %v", err)
	}
	probed := make(map[string]bool)

	report, err := install(
		source,
		binDirectory,
		[]string{"ask-one", "ask-two"},
		installOptions{
			probe: func(path string) error {
				probed[filepath.Base(path)] = true
				return nil
			},
		},
	)

	if err != nil {
		t.Fatalf("install aliases: %v", err)
	}
	if !report.OK || report.RolledBack || len(report.Aliases) != 2 {
		t.Fatalf("install report = %+v", report)
	}
	for _, alias := range report.Aliases {
		path := filepath.Join(binDirectory, alias)
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", alias, readErr)
		}
		info, statErr := os.Lstat(path)
		if statErr != nil {
			t.Fatalf("stat %s: %v", alias, statErr)
		}
		if !bytes.Equal(content, sourceBytes) ||
			info.Mode().Perm()&0o111 == 0 ||
			!probed[alias] {
			t.Fatalf(
				"published alias %s: mode=%o content=%q probed=%t",
				alias,
				info.Mode().Perm(),
				content,
				probed[alias],
			)
		}
	}
	entries, err := os.ReadDir(binDirectory)
	if err != nil {
		t.Fatalf("read bin directory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("bin entries = %d, want 2", len(entries))
	}
}

func TestInstallPublishesDefaultsWithoutDeletingUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "cdp")
	binDirectory := filepath.Join(root, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	sourceBytes := []byte("current-go-binary")
	if err := os.WriteFile(source, sourceBytes, 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sentinel := filepath.Join(binDirectory, "user-owned-tool")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o755); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	report, err := install(
		source,
		binDirectory,
		DefaultAliases,
		installOptions{probe: func(string) error { return nil }},
	)
	if err != nil {
		t.Fatalf("install defaults: %v", err)
	}
	if !report.OK || len(report.Aliases) != len(DefaultAliases) {
		t.Fatalf("install report = %+v", report)
	}
	entries, err := os.ReadDir(binDirectory)
	if err != nil {
		t.Fatalf("read bin directory: %v", err)
	}
	if len(entries) != len(DefaultAliases)+1 {
		t.Fatalf(
			"bin entries = %d, want %d",
			len(entries),
			len(DefaultAliases)+1,
		)
	}
	for _, alias := range DefaultAliases {
		content, readErr := os.ReadFile(
			filepath.Join(binDirectory, alias),
		)
		if readErr != nil {
			t.Fatalf("read default alias %s: %v", alias, readErr)
		}
		if !bytes.Equal(content, sourceBytes) {
			t.Fatalf("default alias %s has unexpected content", alias)
		}
	}
	content, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(content) != "keep" {
		t.Fatalf("sentinel changed: %q", content)
	}
}

func TestInstallRollsBackEveryAliasWhenProbeFails(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "cdp")
	binDirectory := filepath.Join(root, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(source, []byte("new-go-binary"), 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}
	oldBytes := []byte("previous-working-binary")
	if err := os.WriteFile(
		filepath.Join(binDirectory, "ask-one"),
		oldBytes,
		0o755,
	); err != nil {
		t.Fatalf("write existing alias: %v", err)
	}

	report, err := install(
		source,
		binDirectory,
		[]string{"ask-one", "ask-two"},
		installOptions{
			probe: func(string) error {
				return errors.New("probe failed")
			},
		},
	)

	if err == nil {
		t.Fatal("install unexpectedly succeeded")
	}
	if report.OK || !report.RolledBack {
		t.Fatalf("rollback report = %+v", report)
	}
	restored, readErr := os.ReadFile(
		filepath.Join(binDirectory, "ask-one"),
	)
	if readErr != nil {
		t.Fatalf("read restored alias: %v", readErr)
	}
	if !bytes.Equal(restored, oldBytes) {
		t.Fatalf("restored alias = %q, want %q", restored, oldBytes)
	}
	if _, statErr := os.Lstat(
		filepath.Join(binDirectory, "ask-two"),
	); !os.IsNotExist(statErr) {
		t.Fatalf("new alias survived rollback: %v", statErr)
	}
}

func TestInstallRejectsExistingSymlinkBeforePublishing(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "cdp")
	binDirectory := filepath.Join(root, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(source, []byte("new-go-binary"), 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("do-not-change"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(
		outside,
		filepath.Join(binDirectory, "ask-two"),
	); err != nil {
		t.Fatalf("create alias symlink: %v", err)
	}

	report, err := install(
		source,
		binDirectory,
		[]string{"ask-one", "ask-two"},
		installOptions{probe: func(string) error { return nil }},
	)

	if err == nil {
		t.Fatal("symlink install unexpectedly succeeded")
	}
	if report.OK || report.RolledBack {
		t.Fatalf("preflight report = %+v", report)
	}
	if _, statErr := os.Lstat(
		filepath.Join(binDirectory, "ask-one"),
	); !os.IsNotExist(statErr) {
		t.Fatalf("first alias was published before preflight finished: %v", statErr)
	}
	content, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("read outside file: %v", readErr)
	}
	if string(content) != "do-not-change" {
		t.Fatalf("outside file changed: %q", content)
	}
}
