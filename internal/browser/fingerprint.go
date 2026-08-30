package browser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const managedFingerprintMaxBytes = 64 << 10

var fingerprintLanguagePattern = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`)

type managedFingerprintProfile struct {
	UserAgent string `json:"userAgent"`
	Platform  string `json:"platform"`
	Vendor    string `json:"vendor"`
	Language  string `json:"language"`
	Timezone  string `json:"timezone"`
	Viewport  struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"viewport"`
}

func loadManagedFingerprintProfile(path string) (*managedFingerprintProfile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fingerprintFileError("open", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fingerprintFileError("inspect", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("managed fingerprint profile must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, managedFingerprintMaxBytes+1))
	if err != nil {
		return nil, fingerprintFileError("read", err)
	}
	if len(data) > managedFingerprintMaxBytes {
		return nil, fmt.Errorf("managed fingerprint profile exceeds %d bytes", managedFingerprintMaxBytes)
	}

	var profile managedFingerprintProfile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profile); err != nil {
		return nil, fingerprintDecodeError(err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := profile.validate(); err != nil {
		return nil, fmt.Errorf("validate managed fingerprint profile: %w", err)
	}
	return &profile, nil
}

func fingerprintDecodeError(err error) error {
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.EOF):
		return fmt.Errorf("decode managed fingerprint profile: empty JSON input")
	case errors.As(err, &syntaxError):
		return fmt.Errorf("decode managed fingerprint profile: malformed JSON")
	case errors.As(err, &typeError):
		return fmt.Errorf("decode managed fingerprint profile: invalid field type")
	case strings.Contains(err.Error(), "unknown field"):
		return fmt.Errorf("decode managed fingerprint profile: unknown field")
	default:
		return fmt.Errorf("decode managed fingerprint profile: invalid JSON object")
	}
}

func fingerprintFileError(operation string, err error) error {
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("%s managed fingerprint profile: file does not exist", operation)
	case os.IsPermission(err):
		return fmt.Errorf("%s managed fingerprint profile: permission denied", operation)
	default:
		return fmt.Errorf("%s managed fingerprint profile: file operation failed", operation)
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("managed fingerprint profile must contain a single JSON object")
	}
	return nil
}

func (p managedFingerprintProfile) validate() error {
	for _, field := range []struct {
		name  string
		value string
		max   int
	}{
		{"userAgent", p.UserAgent, 4096},
		{"platform", p.Platform, 256},
		{"vendor", p.Vendor, 256},
		{"language", p.Language, 64},
		{"timezone", p.Timezone, 128},
	} {
		if err := validateFingerprintText(field.name, field.value, field.max); err != nil {
			return err
		}
	}
	if !fingerprintLanguagePattern.MatchString(p.Language) {
		return fmt.Errorf("language must be a BCP-47-style language tag")
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return fmt.Errorf("timezone must name an available IANA location")
	}
	if p.Viewport.Width < 1 || p.Viewport.Width > 10000 {
		return fmt.Errorf("viewport.width must be between 1 and 10000")
	}
	if p.Viewport.Height < 1 || p.Viewport.Height > 10000 {
		return fmt.Errorf("viewport.height must be between 1 and 10000")
	}
	return nil
}

func validateFingerprintText(name, value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > max {
		return fmt.Errorf("%s exceeds %d bytes", name, max)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func (p managedFingerprintProfile) chromeArgs() []string {
	return []string{
		"--user-agent=" + p.UserAgent,
		"--window-size=" + strconv.Itoa(p.Viewport.Width) + "," + strconv.Itoa(p.Viewport.Height),
		"--lang=" + p.Language,
		"--accept-lang=" + p.Language,
	}
}

func (p managedFingerprintProfile) childEnvironment(base []string) []string {
	environment := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if !strings.HasPrefix(entry, "TZ=") {
			environment = append(environment, entry)
		}
	}
	return append(environment, "TZ="+p.Timezone)
}
