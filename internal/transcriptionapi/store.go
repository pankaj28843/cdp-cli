package transcriptionapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const DefaultMaxAudioBytes int64 = 512 * 1024 * 1024

var ErrAudioTooLarge = errors.New("audio exceeds the configured storage limit")

// Store preserves compact JSON records and optionally retains audio. The API
// service uses ephemeral transaction media by default; NewStore is available
// for callers that explicitly want a retryable on-disk audio cache.
type Store struct {
	root          string
	audioRoot     string
	retainAudio   bool
	maxAudioBytes int64
	mu            sync.Mutex
}

func NewStore(root string, maxAudioBytes int64) (*Store, error) {
	return newStore(root, maxAudioBytes, true)
}

// NewEphemeralStore keeps request/result JSON under root but places provider
// input audio in a temporary transaction directory. Callers should invoke
// RemoveAudio when the request finishes; the server does this at its boundary.
func NewEphemeralStore(root string, maxAudioBytes int64) (*Store, error) {
	return newStore(root, maxAudioBytes, false)
}

func newStore(root string, maxAudioBytes int64, retainAudio bool) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("transcription state directory is required")
	}
	if maxAudioBytes <= 0 {
		maxAudioBytes = DefaultMaxAudioBytes
	}
	if err := os.MkdirAll(filepath.Join(root, "requests"), 0o700); err != nil {
		return nil, fmt.Errorf("create transcription state directory: %w", err)
	}
	audioRoot := root
	if !retainAudio {
		var err error
		audioRoot, err = os.MkdirTemp("", "cdp-transcription-audio-*")
		if err != nil {
			return nil, fmt.Errorf("create ephemeral transcription audio directory: %w", err)
		}
	}
	return &Store{root: root, audioRoot: audioRoot, retainAudio: retainAudio, maxAudioBytes: maxAudioBytes}, nil
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) MaxAudioBytes() int64 {
	if s == nil {
		return 0
	}
	return s.maxAudioBytes
}

func NewRequestID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "tr-" + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("tr-%d", time.Now().UnixNano())
}

func (s *Store) PersistAudio(
	ctx context.Context,
	requestID string,
	fileName string,
	mimeType string,
	source io.Reader,
) (AudioAsset, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return AudioAsset{}, fmt.Errorf("transcription store is not configured")
	}
	if err := contextErr(ctx); err != nil {
		return AudioAsset{}, err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return AudioAsset{}, fmt.Errorf("request id is required")
	}
	if source == nil {
		return AudioAsset{}, fmt.Errorf("audio source is required")
	}
	ext := safeAudioExtension(fileName)
	directory := s.audioDirectory(requestID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return AudioAsset{}, fmt.Errorf("create request directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".audio-*"+ext)
	if err != nil {
		return AudioAsset{}, fmt.Errorf("create temporary audio file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	defer cleanup()

	limited := io.LimitReader(source, MaxUploadBytes+1)
	bytesWritten, copyErr := io.Copy(temporary, limited)
	if copyErr != nil {
		return AudioAsset{}, fmt.Errorf("persist audio: %w", copyErr)
	}
	if bytesWritten <= 0 {
		return AudioAsset{}, fmt.Errorf("audio file is empty")
	}
	if bytesWritten > MaxUploadBytes {
		return AudioAsset{}, ErrAudioTooLarge
	}
	if err := temporary.Sync(); err != nil {
		return AudioAsset{}, fmt.Errorf("flush persisted audio: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return AudioAsset{}, fmt.Errorf("close persisted audio: %w", err)
	}
	path := filepath.Join(directory, "audio"+ext)
	if err := os.Rename(temporaryPath, path); err != nil {
		return AudioAsset{}, fmt.Errorf("finalize persisted audio: %w", err)
	}
	asset := AudioAsset{
		FileName:      filepath.Base(fileName),
		MIMEType:      strings.TrimSpace(mimeType),
		Bytes:         bytesWritten,
		PersistedPath: path,
		Ephemeral:     !s.retainAudio,
	}
	if s.retainAudio {
		if err := s.PruneAudioExcept(ctx, path); err != nil {
			return AudioAsset{}, err
		}
	}
	return asset, nil
}

// RemoveAudio removes transaction media while preserving its JSON record. It
// is a no-op for the explicit durable-cache store.
func (s *Store) RemoveAudio(ctx context.Context, requestID string) error {
	if s == nil || s.retainAudio {
		return nil
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("request id is required")
	}
	if err := os.RemoveAll(s.audioDirectory(requestID)); err != nil {
		return fmt.Errorf("remove ephemeral transcription audio: %w", err)
	}
	return nil
}

// Close removes the temporary audio root used by an ephemeral store. Durable
// stores are intentionally left untouched because their records and cache are
// user-owned state.
func (s *Store) Close() error {
	if s == nil || s.retainAudio || strings.TrimSpace(s.audioRoot) == "" {
		return nil
	}
	if err := os.RemoveAll(s.audioRoot); err != nil {
		return fmt.Errorf("remove ephemeral transcription audio root: %w", err)
	}
	return nil
}

func (s *Store) audioDirectory(requestID string) string {
	if s.retainAudio {
		return filepath.Join(s.root, "requests", filepath.Base(strings.TrimSpace(requestID)))
	}
	return filepath.Join(s.audioRoot, filepath.Base(strings.TrimSpace(requestID)))
}

// AppendAudio persists a realtime PCM stream incrementally. It uses the same
// transaction directory and record lifecycle as completed-file uploads. The
// default API store removes that media when the WebSocket ends.
func (s *Store) AppendAudio(
	ctx context.Context,
	requestID string,
	fileName string,
	mimeType string,
	chunk []byte,
) (AudioAsset, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return AudioAsset{}, fmt.Errorf("transcription store is not configured")
	}
	if err := contextErr(ctx); err != nil {
		return AudioAsset{}, err
	}
	if len(chunk) == 0 {
		return AudioAsset{}, fmt.Errorf("audio chunk is empty")
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return AudioAsset{}, fmt.Errorf("request id is required")
	}
	directory := s.audioDirectory(requestID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return AudioAsset{}, fmt.Errorf("create realtime request directory: %w", err)
	}
	path := filepath.Join(directory, "audio.pcm")
	s.mu.Lock()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.mu.Unlock()
		return AudioAsset{}, fmt.Errorf("open realtime audio: %w", err)
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		s.mu.Unlock()
		return AudioAsset{}, fmt.Errorf("stat realtime audio: %w", statErr)
	}
	if info.Size()+int64(len(chunk)) > MaxRealtimeAudioBytes {
		_ = file.Close()
		s.mu.Unlock()
		return AudioAsset{}, ErrAudioTooLarge
	}
	if _, err := file.Write(chunk); err != nil {
		_ = file.Close()
		s.mu.Unlock()
		return AudioAsset{}, fmt.Errorf("append realtime audio: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		s.mu.Unlock()
		return AudioAsset{}, fmt.Errorf("flush realtime audio: %w", err)
	}
	if err := file.Close(); err != nil {
		s.mu.Unlock()
		return AudioAsset{}, fmt.Errorf("close realtime audio: %w", err)
	}
	asset := AudioAsset{
		FileName:      filepath.Base(fileName),
		MIMEType:      strings.TrimSpace(mimeType),
		Bytes:         info.Size() + int64(len(chunk)),
		PersistedPath: path,
		Ephemeral:     !s.retainAudio,
	}
	s.mu.Unlock()
	if s.retainAudio {
		if err := s.PruneAudioExcept(ctx, path); err != nil {
			return AudioAsset{}, err
		}
	}
	return asset, nil
}

func (s *Store) SaveRecord(ctx context.Context, record RequestRecord) error {
	if s == nil {
		return fmt.Errorf("transcription store is not configured")
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate transcription record: %w", err)
	}
	return s.atomicJSON(ctx, filepath.Join(s.requestDirectory(record.RequestID), "record.json"), record)
}

func (s *Store) SaveResult(ctx context.Context, record RequestRecord, result Result) error {
	if s == nil {
		return fmt.Errorf("transcription store is not configured")
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate transcription record: %w", err)
	}
	return s.atomicJSON(ctx, filepath.Join(s.requestDirectory(record.RequestID), "result.json"), struct {
		Record RequestRecord `json:"record"`
		Result Result        `json:"result"`
	}{Record: record, Result: result})
}

func (s *Store) LoadRecord(ctx context.Context, requestID string) (RequestRecord, error) {
	var record RequestRecord
	if err := s.readJSON(ctx, filepath.Join(s.requestDirectory(requestID), "record.json"), &record); err != nil {
		return RequestRecord{}, err
	}
	return record, nil
}

func (s *Store) requestDirectory(requestID string) string {
	return filepath.Join(s.root, "requests", filepath.Base(strings.TrimSpace(requestID)))
}

func (s *Store) atomicJSON(ctx context.Context, path string, value any) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create transcription record directory: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transcription record: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".record-*")
	if err != nil {
		return fmt.Errorf("create temporary transcription record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("protect transcription record: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write transcription record: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush transcription record: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close transcription record: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("finalize transcription record: %w", err)
	}
	return nil
}

func (s *Store) readJSON(ctx context.Context, path string, target any) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse transcription record: %w", err)
	}
	return nil
}

func (s *Store) PruneAudioExcept(ctx context.Context, protectedPath string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	files, total, err := s.audioFiles()
	if err != nil {
		return err
	}
	if total <= s.maxAudioBytes {
		return nil
	}
	protectedPath, _ = filepath.Abs(protectedPath)
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})
	for _, file := range files {
		if total <= s.maxAudioBytes {
			break
		}
		absolute, _ := filepath.Abs(file.path)
		if absolute == protectedPath {
			continue
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("prune cached audio %q: %w", filepath.Base(file.path), err)
		}
		total -= file.size
	}
	return nil
}

type audioFile struct {
	path    string
	size    int64
	modTime time.Time
}

func (s *Store) audioFiles() ([]audioFile, int64, error) {
	root := filepath.Join(s.root, "requests")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, 0, fmt.Errorf("list transcription requests: %w", err)
	}
	files := make([]audioFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		matches, globErr := filepath.Glob(filepath.Join(root, entry.Name(), "audio.*"))
		if globErr != nil {
			return nil, 0, fmt.Errorf("find cached audio: %w", globErr)
		}
		for _, path := range matches {
			info, statErr := os.Stat(path)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					continue
				}
				return nil, 0, fmt.Errorf("stat cached audio: %w", statErr)
			}
			files = append(files, audioFile{path: path, size: info.Size(), modTime: info.ModTime()})
			total += info.Size()
		}
	}
	return files, total, nil
}

func safeAudioExtension(fileName string) string {
	ext := strings.ToLower(filepath.Ext(filepath.Base(strings.TrimSpace(fileName))))
	switch ext {
	case ".mp3", ".mp4", ".mpeg", ".mpga", ".m4a", ".wav", ".webm":
		return ext
	default:
		return ".audio"
	}
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
