package cli

import (
	"context"
	"testing"
	"time"
)

func TestContextMutexHonorsCancellationWhileProviderRepairIsBusy(t *testing.T) {
	var mutex contextMutex
	if err := mutex.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer mutex.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := mutex.Lock(ctx)
	if err == nil {
		t.Fatal("Lock returned nil while the mutex was held")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("canceled lock took %v, want bounded cancellation", elapsed)
	}
}
