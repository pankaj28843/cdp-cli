package transcriptionapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	FixtureManifestSchemaVersion = "cdp-cli-transcription-fixtures/v2"
	DefaultFixtureCount          = 100
	DefaultProbeDurationMS       = 1500
)

// ProbeFixture is checked-in synthetic input used by the service's bounded
// provider health probe. It contains no expected transcript so probe state can
// never become a transcript store.
type ProbeFixture struct {
	ID         string
	Path       string
	FileName   string
	MIMEType   string
	Bytes      int64
	DurationMS int64
}

type fixtureManifest struct {
	SchemaVersion string                 `json:"schema_version"`
	Count         int                    `json:"count"`
	Entries       []fixtureManifestEntry `json:"entries"`
}

type fixtureManifestEntry struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	WebM       string `json:"webm"`
	WebMBytes  int64  `json:"webm_bytes"`
	WebMSHA256 string `json:"webm_sha256"`
}

// LoadFixtureCatalog validates the exact checked-in WebM corpus before the
// service starts probing providers. The loader deliberately rejects a partial
// corpus so a green health endpoint cannot silently mean "only some fixtures
// were exercised".
func LoadFixtureCatalog(root string) ([]ProbeFixture, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("transcription fixture directory is required")
	}
	absoluteRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("resolve transcription fixture directory: %w", err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(absoluteRoot, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read transcription fixture manifest: %w", err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse transcription fixture manifest: %w", err)
	}
	if manifest.SchemaVersion != FixtureManifestSchemaVersion {
		return nil, fmt.Errorf("unsupported transcription fixture manifest schema %q", manifest.SchemaVersion)
	}
	if manifest.Count != DefaultFixtureCount || len(manifest.Entries) != DefaultFixtureCount {
		return nil, fmt.Errorf("transcription fixture corpus must contain exactly %d entries", DefaultFixtureCount)
	}

	fixtures := make([]ProbeFixture, 0, len(manifest.Entries))
	seenIDs := make(map[string]struct{}, len(manifest.Entries))
	seenPaths := make(map[string]struct{}, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		wantID := fmt.Sprintf("fixture-%03d", index+1)
		if entry.ID != wantID || strings.TrimSpace(entry.Text) == "" {
			return nil, fmt.Errorf("invalid transcription fixture entry %d", index+1)
		}
		if _, ok := seenIDs[entry.ID]; ok {
			return nil, fmt.Errorf("duplicate transcription fixture id %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		name := filepath.Base(strings.TrimSpace(entry.WebM))
		if name != entry.WebM || filepath.Ext(name) != ".webm" {
			return nil, fmt.Errorf("transcription fixture %q is not a safe WebM filename", entry.ID)
		}
		if _, ok := seenPaths[name]; ok {
			return nil, fmt.Errorf("duplicate transcription fixture path %q", name)
		}
		seenPaths[name] = struct{}{}
		path := filepath.Join(absoluteRoot, name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxUploadBytes {
			return nil, fmt.Errorf("transcription fixture %q is not a bounded regular file", entry.ID)
		}
		if info.Size() != entry.WebMBytes {
			return nil, fmt.Errorf("transcription fixture %q size changed", entry.ID)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read transcription fixture %q: %w", entry.ID, err)
		}
		if len(data) < 4 || data[0] != 0x1a || data[1] != 0x45 || data[2] != 0xdf || data[3] != 0xa3 {
			return nil, fmt.Errorf("transcription fixture %q is not an EBML WebM file", entry.ID)
		}
		digest := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), entry.WebMSHA256) {
			return nil, fmt.Errorf("transcription fixture %q hash changed", entry.ID)
		}
		fixtures = append(fixtures, ProbeFixture{
			ID:         entry.ID,
			Path:       path,
			FileName:   name,
			MIMEType:   "audio/webm",
			Bytes:      info.Size(),
			DurationMS: DefaultProbeDurationMS,
		})
	}
	return fixtures, nil
}

type fixtureSelector struct {
	mu       sync.Mutex
	fixtures []ProbeFixture
	lastUsed map[string]time.Time
	rng      *rand.Rand
}

func newFixtureSelector(fixtures []ProbeFixture, lastUsed map[string]time.Time, seed int64) *fixtureSelector {
	copyFixtures := append([]ProbeFixture(nil), fixtures...)
	copyLastUsed := make(map[string]time.Time, len(lastUsed))
	for id, at := range lastUsed {
		copyLastUsed[id] = at
	}
	return &fixtureSelector{
		fixtures: copyFixtures,
		lastUsed: copyLastUsed,
		rng:      rand.New(rand.NewSource(seed)),
	}
}

func (s *fixtureSelector) Choose(now time.Time) (ProbeFixture, bool) {
	if s == nil {
		return ProbeFixture{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fixtures) == 0 {
		return ProbeFixture{}, false
	}
	candidates := append([]ProbeFixture(nil), s.fixtures...)
	sort.SliceStable(candidates, func(i, j int) bool {
		left, leftOK := s.lastUsed[candidates[i].ID]
		right, rightOK := s.lastUsed[candidates[j].ID]
		if leftOK != rightOK {
			return !leftOK
		}
		if left.Equal(right) {
			return candidates[i].ID < candidates[j].ID
		}
		return left.Before(right)
	})
	poolSize := len(candidates) / 4
	if poolSize < 1 {
		poolSize = 1
	}
	if poolSize > len(candidates) {
		poolSize = len(candidates)
	}
	candidates = candidates[:poolSize]
	totalWeight := 0.0
	weights := make([]float64, len(candidates))
	for index, candidate := range candidates {
		weight := float64(len(candidates) - index)
		if last, ok := s.lastUsed[candidate.ID]; ok && !last.IsZero() {
			ageMinutes := now.Sub(last).Minutes()
			if ageMinutes > 0 {
				weight += minFloat(ageMinutes/float64(DefaultProbeInterval/time.Minute), 10)
			}
		}
		weights[index] = weight
		totalWeight += weight
	}
	choice := s.rng.Float64() * totalWeight
	selected := candidates[len(candidates)-1]
	for index, weight := range weights {
		choice -= weight
		if choice <= 0 {
			selected = candidates[index]
			break
		}
	}
	s.lastUsed[selected.ID] = now.UTC()
	return selected, true
}

func (s *fixtureSelector) Snapshot() map[string]time.Time {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copyLastUsed := make(map[string]time.Time, len(s.lastUsed))
	for id, at := range s.lastUsed {
		copyLastUsed[id] = at
	}
	return copyLastUsed
}

func minFloat(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}
