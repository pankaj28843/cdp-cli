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

func TestRemoveConnection(t *testing.T) {
	file := state.File{
		Selected: "local",
		Connections: []state.Connection{
			{Name: "default", Mode: "auto_connect"},
			{Name: "local", Mode: "browser_url"},
		},
		PageSelections: []state.PageSelection{
			{Connection: "local", TargetID: "page-1"},
		},
	}
	got, ok := state.RemoveConnection(file, "local")
	if !ok || len(got.Connections) != 1 || got.Connections[0].Name != "default" || got.Selected != "default" || len(got.PageSelections) != 0 {
		t.Fatalf("RemoveConnection() = %+v ok=%v, want default selected", got, ok)
	}
}

func TestPruneMissingProjects(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "repo")
	file := state.File{
		Selected: "missing",
		Connections: []state.Connection{
			{Name: "keep", Mode: "browser_url", Project: existing},
			{Name: "missing", Mode: "browser_url", Project: filepath.Join(existing, "missing")},
		},
	}
	got, removed := state.PruneMissingProjects(file, func(path string) bool {
		return path == existing
	})
	if len(removed) != 1 || removed[0].Name != "missing" || len(got.Connections) != 1 || got.Selected != "keep" {
		t.Fatalf("PruneMissingProjects() = %+v removed=%+v, want missing removed and keep selected", got, removed)
	}
}

func TestConnectionByName(t *testing.T) {
	file := state.File{Connections: []state.Connection{
		{Name: "default", Mode: "auto_connect"},
		{Name: "local", Mode: "browser_url"},
	}}
	got, ok := state.ConnectionByName(file, "local")
	if !ok || got.Name != "local" || got.Mode != "browser_url" {
		t.Fatalf("ConnectionByName() = %+v ok=%v, want local browser_url", got, ok)
	}
}

func TestProjectConnectionUsesLongestPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(root, "app")
	file := state.File{Connections: []state.Connection{
		{Name: "root", Mode: "browser_url", Project: root},
		{Name: "nested", Mode: "browser_url", Project: nested},
	}}
	got, ok := state.ProjectConnection(file, filepath.Join(nested, "cmd"))
	if !ok || got.Name != "nested" {
		t.Fatalf("ProjectConnection() = %+v ok=%v, want nested", got, ok)
	}
}

func TestProjectConnectionMatchesSymlinkedTempPaths(t *testing.T) {
	realRoot := t.TempDir()
	symlinkRoot := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatalf("Symlink returned error: %v", err)
	}
	project := filepath.Join(symlinkRoot, "repo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	file := state.File{Connections: []state.Connection{{Name: "project", Mode: "browser_url", Project: project}}}
	got, ok := state.ProjectConnection(file, filepath.Join(realRoot, "repo"))
	if !ok || got.Name != "project" {
		t.Fatalf("ProjectConnection() = %+v ok=%v, want symlinked project", got, ok)
	}
}

func TestPageSelectionForConnection(t *testing.T) {
	file := state.UpsertPageSelection(state.File{}, state.PageSelection{
		Connection: "local",
		TargetID:   "page-1",
		URL:        "https://example.test/app",
		Title:      "Example App",
		SelectedAt: "2026-04-29T00:00:00Z",
	})
	file = state.UpsertPageSelection(file, state.PageSelection{
		Connection: "local",
		TargetID:   "page-2",
		SelectedAt: "2026-04-29T00:01:00Z",
	})
	got, ok := state.PageSelectionForConnection(file, "local")
	if !ok || got.TargetID != "page-2" || len(file.PageSelections) != 1 {
		t.Fatalf("PageSelectionForConnection() = %+v ok=%v selections=%+v, want updated page-2", got, ok, file.PageSelections)
	}
}
