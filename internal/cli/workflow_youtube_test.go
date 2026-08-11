package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestYouTubeNetscapeCookieFileFiltersAndPreservesSecurity(t *testing.T) {
	cookies := []youtubeCookie{
		{Domain: ".youtube.com", Name: "SAPISID", Value: "signed-in", Path: "/", Secure: true, HTTPOnly: true, Expires: 2_000_000_000},
		{Domain: ".youtube.com", Name: "PREF", Value: "setting", Path: "/", Secure: true, Session: true},
		{Domain: ".example.com", Name: "ignore", Value: "secret", Path: "/", Expires: 2_000_000_000},
	}

	content, selected, err := youtubeNetscapeCookieFile(cookies, time.Unix(1_900_000_000, 0))
	if err != nil {
		t.Fatalf("youtubeNetscapeCookieFile: %v", err)
	}
	if len(selected) != 2 || !strings.Contains(content, "#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t2000000000\tSAPISID\tsigned-in") {
		t.Fatalf("selected=%+v content=%q, want current YouTube cookies", selected, content)
	}
	if strings.Contains(content, "example.com") {
		t.Fatalf("content=%q, non-YouTube cookies must not be exported", content)
	}
}

func TestYouTubeNetscapeCookieFileRequiresSignedInProfile(t *testing.T) {
	_, _, err := youtubeNetscapeCookieFile([]youtubeCookie{{Domain: ".youtube.com", Name: "PREF", Value: "setting", Session: true}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "signed in") {
		t.Fatalf("error=%v, want signed-in profile error", err)
	}
}

func TestWriteYouTubeCookieFileUsesOwnerOnlyModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "yt-dlp", "cookies.txt")
	if err := writeYouTubeCookieFile(path, "cookie data\n"); err != nil {
		t.Fatalf("writeYouTubeCookieFile: %v", err)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("directory=%o file=%o, want 0700/0600", dirInfo.Mode().Perm(), fileInfo.Mode().Perm())
	}
}

func TestValidateYouTubeURLRejectsLookalikeHost(t *testing.T) {
	if err := validateYouTubeURL("https://youtube.com.evil.example/watch?v=x"); err == nil {
		t.Fatal("validateYouTubeURL accepted a lookalike host")
	}
	if err := validateYouTubeURL("https://www.youtube.com/watch?v=x"); err != nil {
		t.Fatalf("validateYouTubeURL rejected YouTube: %v", err)
	}
}
