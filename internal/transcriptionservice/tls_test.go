package transcriptionservice

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSelfSignedTLSIncludesRequestedHostsAndReusesFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "tls")
	requestedHosts := []string{"192.0.2.44", "Example.test", "localhost"}
	files, err := EnsureSelfSignedTLS(directory, requestedHosts, false)
	if err != nil {
		t.Fatal(err)
	}
	if files.Reused {
		t.Fatal("first certificate creation unexpectedly reported reuse")
	}
	for _, path := range []string{files.CertFile, files.KeyFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
	certificateInfo, err := os.Stat(files.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := certificateInfo.Mode().Perm(); got != 0o644 {
		t.Fatalf("certificate mode = %o, want 644", got)
	}
	keyInfo, err := os.Stat(files.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := keyInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("key mode = %o, want 600", got)
	}

	keyPair, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range requestedHosts {
		if err := certificate.VerifyHostname(host); err != nil {
			t.Fatalf("certificate does not cover %q: %v", host, err)
		}
	}

	reused, err := EnsureSelfSignedTLS(directory, requestedHosts, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reused.Reused {
		t.Fatal("second certificate creation did not report reuse")
	}
	if reused.CertFile != files.CertFile || reused.KeyFile != files.KeyFile {
		t.Fatalf("reused files = %+v, want %+v", reused, files)
	}
}

func TestEnsureSelfSignedTLSRequiresMatchingFilesAndValidHosts(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "tls")
	if _, err := EnsureSelfSignedTLS(directory, []string{"example.test:28765"}, false); err == nil {
		t.Fatal("TLS generation accepted a host with a port")
	}
	if _, err := EnsureSelfSignedTLS(directory, nil, false); err == nil {
		t.Fatal("TLS generation accepted an empty host list")
	}

	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, selfSignedTLSCertName), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSelfSignedTLS(directory, []string{"example.test"}, false); err == nil {
		t.Fatal("TLS generation accepted an incomplete existing pair")
	}
}
