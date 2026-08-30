package artifacts

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCappedWriterConcurrentStreamsRemainHardBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create managed log: %v", err)
	}
	defer file.Close()

	const maxBytes = int64(1024)
	writer := &cappedWriter{writer: file, remaining: maxBytes}
	chunk := []byte("0123456789")
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 1000 {
				if _, err := writer.Write(chunk); err != nil {
					t.Errorf("capped writer write: %v", err)
					return
				}
			}
		}()
	}
	group.Wait()
	if err := file.Close(); err != nil {
		t.Fatalf("close managed log: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat managed log: %v", err)
	}
	if info.Size() > maxBytes || writer.inputBytes != 80000 || writer.droppedBytes != writer.inputBytes-maxBytes {
		t.Fatalf("capped writer size=%d input=%d dropped=%d, want hard cap and exact counters", info.Size(), writer.inputBytes, writer.droppedBytes)
	}
}
