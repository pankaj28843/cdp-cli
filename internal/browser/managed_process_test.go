package browser

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

func TestManagedProcessSnapshotsReportsContextCancellation(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process snapshots are only implemented on Unix")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := managedProcessSnapshots(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("managedProcessSnapshots error = %v, want context.Canceled", err)
	}
}
