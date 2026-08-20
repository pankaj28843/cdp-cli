package transcriptionservice

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	selfSignedTLSValidity = 365 * 24 * time.Hour
	selfSignedTLSDirName  = "tls"
	selfSignedTLSCertName = "transcription.crt"
	selfSignedTLSKeyName  = "transcription.key"
)

// TLSFiles describes the certificate and key used by the transcription API.
// Hosts is the normalized set of DNS names and IP addresses in the generated
// certificate. It is returned so callers can show URLs that are usable from a
// phone or another machine on the LAN.
type TLSFiles struct {
	CertFile string
	KeyFile  string
	Hosts    []string
	Reused   bool
}

// SelfSignedTLSDirectory returns the state-directory location used by the
// one-command self-signed HTTPS setup.
func SelfSignedTLSDirectory(stateDir string) string {
	return filepath.Join(stateDir, selfSignedTLSDirName)
}

// DefaultTLSHosts returns useful local names for a self-signed certificate.
// Callers should add a LAN address explicitly when they want a stable,
// readable command and certificate that is easy to install on a phone.
func DefaultTLSHosts() []string {
	hosts := []string{"localhost", "127.0.0.1"}
	if hostname, err := os.Hostname(); err == nil {
		hosts = append(hosts, hostname)
	}
	if interfaces, err := net.Interfaces(); err == nil {
		for _, networkInterface := range interfaces {
			addresses, addressErr := networkInterface.Addrs()
			if addressErr != nil {
				continue
			}
			for _, address := range addresses {
				host, _, splitErr := net.ParseCIDR(address.String())
				if splitErr == nil && host != nil && !host.IsUnspecified() {
					hosts = append(hosts, host.String())
				}
			}
		}
	}
	normalizedHosts, err := normalizeTLSHosts(hosts)
	if err != nil {
		return []string{"localhost", "127.0.0.1"}
	}
	return normalizedHosts
}

// EnsureSelfSignedTLS creates or reuses an ECDSA self-signed server
// certificate. Existing files are never replaced unless regenerate is true.
// The certificate is intended for private-LAN dogfooding; a certificate from
// a trusted CA remains the right choice for a production or shared service.
func EnsureSelfSignedTLS(directory string, hosts []string, regenerate bool) (TLSFiles, error) {
	normalizedHosts, err := normalizeTLSHosts(hosts)
	if err != nil {
		return TLSFiles{}, err
	}
	if len(normalizedHosts) == 0 {
		return TLSFiles{}, fmt.Errorf("at least one TLS host is required")
	}
	if strings.TrimSpace(directory) == "" {
		return TLSFiles{}, fmt.Errorf("self-signed TLS directory is required")
	}
	directory = filepath.Clean(directory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return TLSFiles{}, fmt.Errorf("create self-signed TLS directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return TLSFiles{}, fmt.Errorf("set self-signed TLS directory mode: %w", err)
	}

	files := TLSFiles{
		CertFile: filepath.Join(directory, selfSignedTLSCertName),
		KeyFile:  filepath.Join(directory, selfSignedTLSKeyName),
		Hosts:    normalizedHosts,
	}
	certInfo, certErr := os.Stat(files.CertFile)
	keyInfo, keyErr := os.Stat(files.KeyFile)
	certExists := certErr == nil
	keyExists := keyErr == nil
	if certErr != nil && !os.IsNotExist(certErr) {
		return TLSFiles{}, fmt.Errorf("inspect self-signed TLS certificate: %w", certErr)
	}
	if keyErr != nil && !os.IsNotExist(keyErr) {
		return TLSFiles{}, fmt.Errorf("inspect self-signed TLS key: %w", keyErr)
	}
	if certExists && keyExists && !regenerate {
		if err := validateTLSFiles(files.CertFile, files.KeyFile, normalizedHosts); err != nil {
			return TLSFiles{}, fmt.Errorf("existing self-signed TLS files are not usable: %w; rerun with --tls-regenerate to replace them", err)
		}
		files.Reused = true
		return files, nil
	}
	if (certExists || keyExists) && !regenerate {
		return TLSFiles{}, fmt.Errorf("self-signed TLS certificate and key must exist together; rerun with --tls-regenerate to replace them")
	}
	if certExists && certInfo.Mode().Perm()&0o777 != 0o644 {
		// The generated certificate is public within the local machine. Fixing
		// its mode makes a reused directory predictable without touching the key.
		if err := os.Chmod(files.CertFile, 0o644); err != nil {
			return TLSFiles{}, fmt.Errorf("set self-signed TLS certificate mode: %w", err)
		}
	}
	if keyExists && keyInfo.Mode().Perm()&0o777 != 0o600 {
		if err := os.Chmod(files.KeyFile, 0o600); err != nil {
			return TLSFiles{}, fmt.Errorf("set self-signed TLS key mode: %w", err)
		}
	}

	certificatePEM, keyPEM, err := generateSelfSignedTLS(normalizedHosts)
	if err != nil {
		return TLSFiles{}, err
	}
	if err := writeTLSFileAtomic(files.CertFile, certificatePEM, 0o644); err != nil {
		return TLSFiles{}, fmt.Errorf("write self-signed TLS certificate: %w", err)
	}
	if err := writeTLSFileAtomic(files.KeyFile, keyPEM, 0o600); err != nil {
		return TLSFiles{}, fmt.Errorf("write self-signed TLS key: %w", err)
	}
	if err := validateTLSFiles(files.CertFile, files.KeyFile, normalizedHosts); err != nil {
		return TLSFiles{}, fmt.Errorf("validate generated self-signed TLS files: %w", err)
	}
	return files, nil
}

func generateSelfSignedTLS(hosts []string) ([]byte, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate self-signed TLS key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("generate self-signed TLS serial number: %w", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{CommonName: "cdp transcription API (self-signed)"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(selfSignedTLSValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsTLSHosts(hosts),
		IPAddresses:           ipTLSHosts(hosts),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create self-signed TLS certificate: %w", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKeyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal self-signed TLS key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyDER})
	return certificatePEM, keyPEM, nil
}

func validateTLSFiles(certFile, keyFile string, hosts []string) error {
	keyPair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load certificate and key: %w", err)
	}
	if len(keyPair.Certificate) == 0 {
		return fmt.Errorf("certificate chain is empty")
	}
	certificate, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse certificate: %w", err)
	}
	for _, host := range hosts {
		if err := certificate.VerifyHostname(host); err != nil {
			return fmt.Errorf("certificate does not cover %q: %w", host, err)
		}
	}
	return nil
}

func normalizeTLSHosts(hosts []string) ([]string, error) {
	seen := make(map[string]struct{}, len(hosts))
	result := make([]string, 0, len(hosts))
	for _, rawHost := range hosts {
		host := strings.TrimSpace(rawHost)
		if host == "" {
			continue
		}
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		}
		if strings.Contains(host, "://") {
			return nil, fmt.Errorf("TLS host %q must be a hostname or IP address, not a URL", rawHost)
		}
		if parsedIP := net.ParseIP(host); parsedIP != nil {
			host = parsedIP.String()
		} else if err := validateTLSHostname(host); err != nil {
			return nil, fmt.Errorf("invalid TLS host %q: %w", rawHost, err)
		} else {
			host = strings.ToLower(host)
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		result = append(result, host)
	}
	// Preserve caller order so the first URL in CLI output remains useful,
	// usually the explicitly requested LAN IP.
	return result, nil
}

func validateTLSHostname(host string) error {
	if len(host) > 253 || strings.ContainsAny(host, " /\\\t\r\n") {
		return fmt.Errorf("hostname is too long or contains whitespace")
	}
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return fmt.Errorf("hostname is empty or has an invalid boundary")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("hostname label %q is invalid", label)
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return fmt.Errorf("hostname contains unsupported character %q", character)
		}
	}
	return nil
}

func dnsTLSHosts(hosts []string) []string {
	result := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if net.ParseIP(host) == nil {
			result = append(result, host)
		}
	}
	return result
}

func ipTLSHosts(hosts []string) []net.IP {
	result := make([]net.IP, 0, len(hosts))
	for _, host := range hosts {
		if parsedIP := net.ParseIP(host); parsedIP != nil {
			result = append(result, parsedIP)
		}
	}
	return result
}

func writeTLSFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".tls-tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if removeErr := os.Remove(temporaryPath); err == nil && removeErr != nil && !os.IsNotExist(removeErr) {
			err = removeErr
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}
