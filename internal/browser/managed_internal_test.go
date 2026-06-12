package browser

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceManagedProfileFromDefaultFailurePreservesActiveProfile(t *testing.T) {
	tests := []struct {
		name    string
		ops     func(dstRoot string) profileReplaceOps
		wantErr string
	}{
		{
			name: "copy failure",
			ops: func(string) profileReplaceOps {
				return profileReplaceOps{
					copyProfileTree: func(string, string) (int, error) {
						return 0, errors.New("copy failed")
					},
				}
			},
			wantErr: "copy failed",
		},
		{
			name: "sanitize failure",
			ops: func(string) profileReplaceOps {
				return profileReplaceOps{
					removeChromeRuntimeArtifacts: func(string) error {
						return errors.New("sanitize failed")
					},
				}
			},
			wantErr: "sanitize failed",
		},
		{
			name: "chmod failure",
			ops: func(string) profileReplaceOps {
				return profileReplaceOps{
					chmod: func(string, os.FileMode) error {
						return errors.New("chmod failed")
					},
				}
			},
			wantErr: "secure managed profile copy",
		},
		{
			name: "backup stage failure",
			ops: func(dstRoot string) profileReplaceOps {
				return profileReplaceOps{
					rename: func(oldpath, newpath string) error {
						if oldpath == dstRoot {
							return errors.New("backup failed")
						}
						return os.Rename(oldpath, newpath)
					},
				}
			},
			wantErr: "stage old managed profile backup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dstRoot, srcRoot := managedProfileReplacementFixture(t)
			if _, err := replaceManagedProfileFromDefault(dstRoot, srcRoot, tt.ops(dstRoot)); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("replace error = %v, want %q", err, tt.wantErr)
			}
			assertManagedProfileCookie(t, dstRoot, "old-cookie-db")
			assertManagedProfileFile(t, dstRoot, filepath.Join("Default", "old-only"), "old-only")
			assertNoManagedProfileTemps(t, filepath.Dir(dstRoot))
		})
	}
}

func TestReplaceManagedProfileFromDefaultPromoteFailureRollsBackActiveProfile(t *testing.T) {
	dstRoot, srcRoot := managedProfileReplacementFixture(t)
	ops := profileReplaceOps{
		rename: func(oldpath, newpath string) error {
			if strings.Contains(filepath.Base(oldpath), ".headless-profile-copy-") {
				return errors.New("promote failed")
			}
			return os.Rename(oldpath, newpath)
		},
	}

	if _, err := replaceManagedProfileFromDefault(dstRoot, srcRoot, ops); err == nil || !strings.Contains(err.Error(), "install managed profile copy") {
		t.Fatalf("replace error = %v, want promote failure", err)
	}
	assertManagedProfileCookie(t, dstRoot, "old-cookie-db")
	assertManagedProfileFile(t, dstRoot, filepath.Join("Default", "old-only"), "old-only")
	assertNoManagedProfileTemps(t, filepath.Dir(dstRoot))
}

func TestReplaceManagedProfileFromDefaultBackupCleanupFailureKeepsPromotedProfile(t *testing.T) {
	dstRoot, srcRoot := managedProfileReplacementFixture(t)
	ops := profileReplaceOps{
		removeAll: func(path string) error {
			if strings.Contains(filepath.Base(path), "-backup-") {
				return errors.New("cleanup failed")
			}
			return removeAllWithRetry(path)
		},
	}

	copied, err := replaceManagedProfileFromDefault(dstRoot, srcRoot, ops)
	if err != nil {
		t.Fatalf("replace returned error: %v", err)
	}
	if copied == 0 {
		t.Fatalf("copied = 0, want copied files")
	}
	assertManagedProfileCookie(t, dstRoot, "new-cookie-db")
	assertManagedProfileMissing(t, dstRoot, filepath.Join("Default", "old-only"))
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(dstRoot), ".headless-profile-backup-*"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want one retained backup after cleanup failure", backups)
	}
}

func managedProfileReplacementFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dstRoot := filepath.Join(root, "headless-profile")
	srcRoot := filepath.Join(root, "chrome")
	writeManagedProfileTestFile(t, srcRoot, "Local State", "local-state")
	writeManagedProfileTestFile(t, srcRoot, filepath.Join("Default", "Cookies"), "new-cookie-db")
	writeManagedProfileTestFile(t, srcRoot, filepath.Join("Default", "Preferences"), "{}")
	writeManagedProfileTestFile(t, dstRoot, filepath.Join("Default", "Cookies"), "old-cookie-db")
	writeManagedProfileTestFile(t, dstRoot, filepath.Join("Default", "old-only"), "old-only")
	return dstRoot, srcRoot
}

func writeManagedProfileTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func assertManagedProfileCookie(t *testing.T, root, want string) {
	t.Helper()
	assertManagedProfileFile(t, root, filepath.Join("Default", "Cookies"), want)
}

func assertManagedProfileFile(t *testing.T, root, rel, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read managed profile %s: %v", rel, err)
	}
	if string(got) != want {
		t.Fatalf("managed profile %s = %q, want %q", rel, got, want)
	}
}

func assertManagedProfileMissing(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
		t.Fatalf("managed profile %s exists or stat failed: %v", rel, err)
	}
}

func assertNoManagedProfileTemps(t *testing.T, parent string) {
	t.Helper()
	for _, pattern := range []string{".headless-profile-copy-*", ".headless-profile-backup-*"} {
		matches, err := filepath.Glob(filepath.Join(parent, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		if len(matches) != 0 {
			t.Fatalf("temporary managed profile paths for %s remain: %v", pattern, matches)
		}
	}
}
