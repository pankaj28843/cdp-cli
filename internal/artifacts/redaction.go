package artifacts

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const (
	ModeNone    = "none"
	ModeSafe    = "safe"
	ModeHeaders = "headers"

	Redacted = "<redacted>"
)

var localPathPattern = regexp.MustCompile(`(?i)(file:///[^\s"'<>]+|/[Uu]sers/[^\s"'<>]+|/` + `home/[^\s"'<>]+|[A-Z]:\\Users\\[^\s"'<>]+)`)

type SafetyMetadata struct {
	RedactionMode     string   `json:"redaction_mode"`
	Classification    string   `json:"classification"`
	Shareable         bool     `json:"shareable"`
	UnsafeOptIn       bool     `json:"unsafe_opt_in,omitempty"`
	LocalOnlyWarning  string   `json:"local_only_warning,omitempty"`
	ChangedFields     []string `json:"changed_fields,omitempty"`
	ChangedFieldCount int      `json:"changed_field_count"`
}

type Redactor struct {
	mode    string
	changed map[string]struct{}
}

func NewRedactor(mode string) *Redactor {
	mode = NormalizeMode(mode)
	return &Redactor{mode: mode, changed: map[string]struct{}{}}
}

func NormalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeNone:
		return ModeNone
	case ModeSafe:
		return ModeSafe
	case ModeHeaders:
		return ModeHeaders
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func (r *Redactor) Mode() string {
	if r == nil {
		return ModeNone
	}
	return r.mode
}

func (r *Redactor) HeaderMap(headers map[string]any, field string) map[string]any {
	if len(headers) == 0 || r == nil || r.mode == ModeNone {
		return headers
	}
	out := map[string]any{}
	for key, value := range headers {
		childField := joinField(field, key)
		if r.mode == ModeHeaders || SensitiveName(key) || SensitiveValue(value) {
			out[key] = Redacted
			r.record(childField)
			continue
		}
		out[key] = value
	}
	return out
}

func (r *Redactor) URL(rawURL, field string) string {
	if rawURL == "" || r == nil || r.mode != ModeSafe {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return redactLocalPaths(rawURL)
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		if SensitiveName(key) {
			query.Set(key, Redacted)
			changed = true
			r.record(joinField(field, key))
		}
	}
	if changed {
		parsed.RawQuery = query.Encode()
	}
	redacted := redactLocalPaths(parsed.String())
	if redacted != parsed.String() {
		r.record(field)
	}
	return redacted
}

func (r *Redactor) BodyText(text, field string) string {
	if text == "" || r == nil || r.mode == ModeNone {
		return text
	}
	if r.mode == ModeHeaders {
		r.record(field)
		return Redacted
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err == nil {
		redacted := r.redactJSONValue(decoded, field)
		return marshalCompact(redacted)
	}
	if SensitiveValue(text) {
		r.record(field)
		return Redacted
	}
	values, err := url.ParseQuery(text)
	if err == nil && len(values) > 0 {
		changed := false
		for key := range values {
			if SensitiveName(key) {
				values.Set(key, Redacted)
				changed = true
				r.record(joinField(field, key))
			}
		}
		if changed {
			return values.Encode()
		}
	}
	redacted := redactLocalPaths(text)
	if redacted != text {
		r.record(field)
		return redacted
	}
	return text
}

func (r *Redactor) Metadata(writesArtifact bool, localOnlyWarning string) SafetyMetadata {
	mode := ModeNone
	if r != nil {
		mode = r.mode
	}
	fields := r.ChangedFields()
	meta := SafetyMetadata{
		RedactionMode:     mode,
		Classification:    "local_only",
		Shareable:         false,
		ChangedFields:     fields,
		ChangedFieldCount: len(fields),
	}
	if mode == ModeSafe || mode == ModeHeaders {
		meta.Classification = "public_safe"
		meta.Shareable = true
		return meta
	}
	if writesArtifact {
		meta.Classification = "unsafe_opt_in"
		meta.UnsafeOptIn = true
		meta.LocalOnlyWarning = strings.TrimSpace(localOnlyWarning)
	}
	return meta
}

func (r *Redactor) ChangedFields() []string {
	if r == nil || len(r.changed) == 0 {
		return nil
	}
	fields := make([]string, 0, len(r.changed))
	for field := range r.changed {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func (r *Redactor) RecordChanged(field string) {
	r.record(field)
}

func (r *Redactor) redactJSONValue(value any, field string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range typed {
			childField := joinField(field, key)
			if SensitiveName(key) {
				out[key] = Redacted
				r.record(childField)
				continue
			}
			out[key] = r.redactJSONValue(child, childField)
		}
		return out
	case []any:
		for i := range typed {
			typed[i] = r.redactJSONValue(typed[i], field)
		}
		return typed
	case string:
		if SensitiveValue(typed) {
			r.record(field)
			return Redacted
		}
		if looksLikeURL(typed) {
			return r.URL(typed, field)
		}
		redacted := redactLocalPaths(typed)
		if redacted != typed {
			r.record(field)
			return redacted
		}
		return typed
	default:
		return value
	}
}

func (r *Redactor) record(field string) {
	if r == nil {
		return
	}
	field = strings.TrimSpace(field)
	if field == "" {
		field = "value"
	}
	r.changed[field] = struct{}{}
}

func SensitiveName(name string) bool {
	lower := strings.ToLower(name)
	if lower == "auth" || lower == "credential" || lower == "credentials" {
		return true
	}
	for _, needle := range []string{"authorization", "cookie", "csrf", "xsrf", "token", "secret", "password", "session", "api-key", "apikey", "client-transaction-id"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func looksLikeURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") || strings.HasPrefix(lower, "file://")
}

func SensitiveValue(value any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "bearer ") || strings.Contains(lower, "basic ") || strings.Contains(lower, "api_key=") || strings.Contains(lower, "apikey=")
}

type ScanResult struct {
	BytesScanned int      `json:"bytes_scanned"`
	Truncated    bool     `json:"truncated"`
	Findings     []string `json:"findings,omitempty"`
}

func ScanBytes(content []byte, needles []string, maxBytes int) ScanResult {
	scan := content
	truncated := false
	if maxBytes > 0 && len(scan) > maxBytes {
		scan = scan[:maxBytes]
		truncated = true
	}
	haystack := strings.ToLower(string(scan))
	found := map[string]struct{}{}
	for _, needle := range needles {
		needle = strings.TrimSpace(needle)
		if needle == "" {
			continue
		}
		if strings.Contains(haystack, strings.ToLower(needle)) {
			found[needle] = struct{}{}
		}
	}
	findings := make([]string, 0, len(found))
	for finding := range found {
		findings = append(findings, finding)
	}
	sort.Strings(findings)
	return ScanResult{BytesScanned: len(scan), Truncated: truncated, Findings: findings}
}

func marshalCompact(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return Redacted
	}
	return string(b)
}

func redactLocalPaths(text string) string {
	return localPathPattern.ReplaceAllString(text, Redacted)
}

func joinField(parent, child string) string {
	parent = strings.TrimSpace(parent)
	child = strings.TrimSpace(child)
	if parent == "" {
		return child
	}
	if child == "" {
		return parent
	}
	return parent + "." + child
}
