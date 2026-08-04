package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFinalizeCompletedDownloadRequiresVisibleGUIDFile(t *testing.T) {
	downloadDir := t.TempDir()
	begin := downloadWaitEvent{
		GUID:              "missing-guid",
		SuggestedFilename: "report.csv",
	}
	progress := downloadWaitEvent{
		State:    "completed",
		FilePath: filepath.Join(t.TempDir(), "reported-outside-download-dir.csv"),
	}

	finalPath, err := finalizeCompletedDownload(context.Background(), explicitDownloadFinalizeOptions(downloadDir), begin, progress)
	if err == nil {
		t.Fatalf("finalizeCompletedDownload() = %q, nil; want missing GUID file error", finalPath)
	}
	if finalPath != "" {
		t.Errorf("finalizeCompletedDownload() path = %q, want empty path on failure", finalPath)
	}
}

func TestFinalizeCompletedDownloadRetriesDelayedGUIDVisibility(t *testing.T) {
	downloadDir := t.TempDir()
	guid := "delayed-guid"
	sourcePath := filepath.Join(downloadDir, guid)
	writeResult := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		writeResult <- os.WriteFile(sourcePath, []byte("completed bytes"), 0o600)
	}()

	begin := downloadWaitEvent{
		GUID:              guid,
		SuggestedFilename: "delayed-report.csv",
	}
	progress := downloadWaitEvent{State: "completed"}
	finalPath, err := finalizeCompletedDownload(context.Background(), explicitDownloadFinalizeOptions(downloadDir), begin, progress)
	if writeErr := <-writeResult; writeErr != nil {
		t.Fatalf("write delayed GUID fixture: %v", writeErr)
	}
	if err != nil {
		t.Fatalf("finalizeCompletedDownload() error = %v, want delayed GUID visibility to succeed", err)
	}
	wantPath := filepath.Join(downloadDir, "delayed-report.csv")
	if finalPath != wantPath {
		t.Errorf("finalizeCompletedDownload() path = %q, want %q", finalPath, wantPath)
	}
	content, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read retained download: %v", err)
	}
	if string(content) != "completed bytes" {
		t.Errorf("retained download = %q, want completed bytes", content)
	}
}

func TestFinalizeCompletedDownloadBoundsOverlongUnicodeFilename(t *testing.T) {
	downloadDir := t.TempDir()
	guid := "unicode-guid"
	if err := os.WriteFile(filepath.Join(downloadDir, guid), []byte("image bytes"), 0o600); err != nil {
		t.Fatalf("write GUID fixture: %v", err)
	}

	begin := downloadWaitEvent{
		GUID:              guid,
		SuggestedFilename: strings.Repeat("界", 100) + ".png",
	}
	finalPath, err := finalizeCompletedDownload(
		context.Background(),
		explicitDownloadFinalizeOptions(downloadDir),
		begin,
		downloadWaitEvent{State: "completed"},
	)
	if err != nil {
		t.Fatalf("finalizeCompletedDownload() error = %v, want bounded Unicode filename", err)
	}
	filename := filepath.Base(finalPath)
	if len(filename) > 255 {
		t.Errorf("retained filename is %d bytes, want at most 255: %q", len(filename), filename)
	}
	if !utf8.ValidString(filename) {
		t.Errorf("retained filename is not valid UTF-8: %q", filename)
	}
	if filepath.Ext(filename) != ".png" {
		t.Errorf("retained filename extension = %q, want .png", filepath.Ext(filename))
	}
	if info, statErr := os.Lstat(finalPath); statErr != nil {
		t.Fatalf("inspect retained download: %v", statErr)
	} else if !info.Mode().IsRegular() {
		t.Errorf("retained path mode = %v, want regular file", info.Mode())
	}
}

func TestRetainDownloadWithoutOverwriteRejectsReplacedNonregularSource(t *testing.T) {
	downloadDir := t.TempDir()
	sourcePath := filepath.Join(downloadDir, "replacement-guid")
	if err := os.WriteFile(sourcePath, []byte("original bytes"), 0o600); err != nil {
		t.Fatalf("write original GUID fixture: %v", err)
	}
	if info, err := os.Lstat(sourcePath); err != nil {
		t.Fatalf("inspect original GUID fixture: %v", err)
	} else if !info.Mode().IsRegular() {
		t.Fatalf("original GUID mode = %v, want regular file", info.Mode())
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("remove original GUID fixture: %v", err)
	}
	replacementTarget := filepath.Join(t.TempDir(), "replacement-target")
	if err := os.WriteFile(replacementTarget, []byte("replacement bytes"), 0o600); err != nil {
		t.Fatalf("write replacement target fixture: %v", err)
	}
	if err := os.Symlink(replacementTarget, sourcePath); err != nil {
		t.Skipf("create nonregular replacement fixture: %v", err)
	}

	finalPath, err := retainDownloadWithoutOverwrite(sourcePath, downloadDir, "report.csv")
	if err == nil {
		t.Errorf("retainDownloadWithoutOverwrite() = %q, nil; want replaced nonregular source error", finalPath)
	}
	if finalPath != "" {
		t.Errorf("retainDownloadWithoutOverwrite() path = %q, want empty path on failure", finalPath)
	}
	if info, statErr := os.Lstat(sourcePath); statErr != nil {
		t.Errorf("replacement source was removed: %v", statErr)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("replacement source mode = %v, want symlink left untouched", info.Mode())
	}
	if _, statErr := os.Lstat(filepath.Join(downloadDir, "report.csv")); !os.IsNotExist(statErr) {
		t.Errorf("nonregular retained candidate exists, stat error = %v", statErr)
	}
}

func TestRetainDownloadWithoutOverwriteRejectsSourceReplacedAfterLink(t *testing.T) {
	downloadDir := t.TempDir()
	sourcePath := filepath.Join(downloadDir, "replacement-guid")
	if err := os.WriteFile(sourcePath, []byte("completed bytes"), 0o600); err != nil {
		t.Fatalf("write completed GUID fixture: %v", err)
	}
	expectedSource, err := os.Lstat(sourcePath)
	if err != nil {
		t.Fatalf("inspect completed GUID fixture: %v", err)
	}

	fileOps := localDownloadFileOperations()
	link := fileOps.link
	fileOps.link = func(oldPath, newPath string) error {
		if err := link(oldPath, newPath); err != nil {
			return err
		}
		if err := os.Remove(oldPath); err != nil {
			return err
		}
		return os.WriteFile(oldPath, []byte("replacement bytes"), 0o600)
	}

	finalPath, err := retainDownloadWithoutOverwriteFrom(
		sourcePath,
		downloadDir,
		"report.csv",
		expectedSource,
		fileOps,
	)
	if err == nil {
		t.Errorf("retainDownloadWithoutOverwriteFrom() = %q, nil; want replaced source error", finalPath)
	}
	if finalPath != "" {
		t.Errorf("retainDownloadWithoutOverwriteFrom() path = %q, want empty path on failure", finalPath)
	}
	content, readErr := os.ReadFile(sourcePath)
	if readErr != nil {
		t.Fatalf("read replacement source: %v", readErr)
	}
	if string(content) != "replacement bytes" {
		t.Errorf("replacement source = %q, want replacement bytes", content)
	}
	if _, statErr := os.Lstat(filepath.Join(downloadDir, "report.csv")); !os.IsNotExist(statErr) {
		t.Errorf("retained candidate was not rolled back, stat error = %v", statErr)
	}
}

func explicitDownloadFinalizeOptions(downloadDir string) downloadWaitOptions {
	return downloadWaitOptions{
		DownloadDir:               downloadDir,
		FinalizeSuggestedFilename: true,
	}
}
