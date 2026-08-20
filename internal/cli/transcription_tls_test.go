package cli

import (
	"path/filepath"
	"testing"
)

func TestDemoURLsUseHTTPSAndRequestedCertificateHosts(t *testing.T) {
	if got, want := demoURL("0.0.0.0:28765", true), "https://127.0.0.1:28765/demo.html"; got != want {
		t.Fatalf("demoURL = %q, want %q", got, want)
	}
	got := demoURLs("0.0.0.0:28765", true, []string{"192.168.5.249", "localhost"})
	want := []string{"https://192.168.5.249:28765/demo.html", "https://localhost:28765/demo.html"}
	if len(got) != len(want) {
		t.Fatalf("demoURLs = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("demoURLs = %#v, want %#v", got, want)
		}
	}
	if got, want := preferredDemoURL("0.0.0.0:28765", true, []string{"192.168.5.249", "localhost"}), "https://192.168.5.249:28765/demo.html"; got != want {
		t.Fatalf("preferredDemoURL = %q, want %q", got, want)
	}
}

func TestConfigureTranscriptionTLSCreatesPrivateLANFiles(t *testing.T) {
	files, err := configureTranscriptionTLS(filepath.Join(t.TempDir(), "state"), "", "", true, []string{"example.test"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if files.CertFile == "" || files.KeyFile == "" || files.Reused {
		t.Fatalf("generated TLS files = %+v", files)
	}
	if _, err := configureTranscriptionTLS(t.TempDir(), files.CertFile, "", false, nil, false); err == nil {
		t.Fatal("incomplete manually configured TLS pair was accepted")
	}
}
