package state_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/state"
)

func TestStoreRoundTrip(t *testing.T) {
	store, err := state.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	file := state.UpsertConnection(state.File{}, state.Connection{
		Name:       "local",
		Mode:       "browser_url",
		BrowserURL: "http://example",
	})
	file, ok := state.SelectConnection(file, "local")
	if !ok {
		t.Fatal("SelectConnection returned false")
	}
	if err := store.Save(context.Background(), file); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, "connections.json")); err != nil {
		t.Fatalf("connections.json was not written: %v", err)
	}

	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	current, ok := state.CurrentConnection(got)
	if !ok || current.Name != "local" || current.Mode != "browser_url" {
		t.Fatalf("CurrentConnection() = %+v, %v; want local browser_url", current, ok)
	}
}

func TestSelectMissingConnection(t *testing.T) {
	_, ok := state.SelectConnection(state.File{}, "missing")
	if ok {
		t.Fatal("SelectConnection returned true for missing connection")
	}
}
