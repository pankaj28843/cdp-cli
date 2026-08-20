package cli

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/pankaj28843/cdp-cli/internal/transcriptionservice"
)

func configureTranscriptionTLS(stateDir, certFile, keyFile string, selfSigned bool, hosts []string, regenerate bool) (transcriptionservice.TLSFiles, error) {
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	if selfSigned {
		if certFile != "" || keyFile != "" {
			return transcriptionservice.TLSFiles{}, fmt.Errorf("--tls-self-signed cannot be combined with --tls-cert or --tls-key")
		}
		if len(hosts) == 0 {
			hosts = transcriptionservice.DefaultTLSHosts()
		}
		files, err := transcriptionservice.EnsureSelfSignedTLS(transcriptionservice.SelfSignedTLSDirectory(stateDir), hosts, regenerate)
		if err != nil {
			return transcriptionservice.TLSFiles{}, err
		}
		return files, nil
	}
	if regenerate {
		return transcriptionservice.TLSFiles{}, fmt.Errorf("--tls-regenerate requires --tls-self-signed")
	}
	if (certFile == "") != (keyFile == "") {
		return transcriptionservice.TLSFiles{}, fmt.Errorf("--tls-cert and --tls-key must be provided together")
	}
	return transcriptionservice.TLSFiles{CertFile: certFile, KeyFile: keyFile}, nil
}

func demoURL(address string, tlsEnabled bool) string {
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	address = strings.TrimSpace(address)
	if address == "0.0.0.0:28765" || address == "[::]:28765" {
		address = "127.0.0.1:28765"
	}
	if strings.HasPrefix(address, ":") {
		return scheme + "://127.0.0.1" + address + "/demo.html"
	}
	return scheme + "://" + address + "/demo.html"
}

func demoURLs(address string, tlsEnabled bool, hosts []string) []string {
	if len(hosts) == 0 {
		return []string{demoURL(address, tlsEnabled)}
	}
	_, port := demoHostPort(address)
	seen := make(map[string]struct{}, len(hosts))
	urls := make([]string, 0, len(hosts))
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" || host == "0.0.0.0" || host == "::" {
			continue
		}
		addressHost := host
		if net.ParseIP(host) != nil && strings.Contains(host, ":") {
			addressHost = "[" + host + "]"
		}
		if port != "" {
			addressHost = net.JoinHostPort(host, port)
		}
		candidate := scheme + "://" + addressHost + "/demo.html"
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		urls = append(urls, candidate)
	}
	if len(urls) == 0 {
		return []string{demoURL(address, tlsEnabled)}
	}
	return urls
}

func preferredDemoURL(address string, tlsEnabled bool, hosts []string) string {
	urls := demoURLs(address, tlsEnabled, hosts)
	if len(urls) > 0 {
		return urls[0]
	}
	return demoURL(address, tlsEnabled)
}

func demoHostPort(address string) (string, string) {
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, ":") {
		return "127.0.0.1", strings.TrimPrefix(address, ":")
	}
	host, port, err := net.SplitHostPort(address)
	if err == nil {
		return host, port
	}
	return address, ""
}

func transcriptionServiceTLSConfigured(platform transcriptionservice.Platform, paths transcriptionservice.Paths) bool {
	path := paths.Environment
	if platform == transcriptionservice.PlatformMacOS {
		path = paths.LaunchAgent
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if platform == transcriptionservice.PlatformLinux {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "CDP_TRANSCRIPTION_TLS_CERT=") {
				continue
			}
			value := strings.Trim(strings.TrimPrefix(line, "CDP_TRANSCRIPTION_TLS_CERT="), `"`)
			return strings.TrimSpace(value) != ""
		}
		return false
	}
	text := string(data)
	marker := "<key>CDP_TRANSCRIPTION_TLS_CERT</key>"
	markerIndex := strings.Index(text, marker)
	if markerIndex < 0 {
		return false
	}
	valueStart := strings.Index(text[markerIndex+len(marker):], "<string>")
	if valueStart < 0 {
		return false
	}
	valueStart += markerIndex + len(marker) + len("<string>")
	valueEnd := strings.Index(text[valueStart:], "</string>")
	if valueEnd < 0 {
		return false
	}
	return strings.TrimSpace(text[valueStart:valueStart+valueEnd]) != ""
}
