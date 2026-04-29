package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DirName = ".cdp-cli"

type Store struct {
	Dir string
}

type Connection struct {
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	BrowserURL  string `json:"browser_url,omitempty"`
	AutoConnect bool   `json:"auto_connect"`
	Channel     string `json:"channel,omitempty"`
	UserDataDir string `json:"user_data_dir,omitempty"`
	Project     string `json:"project,omitempty"`
}

type PageSelection struct {
	Connection string `json:"connection"`
	TargetID   string `json:"target_id"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	SelectedAt string `json:"selected_at"`
}

type File struct {
	Connections    []Connection    `json:"connections"`
	Selected       string          `json:"selected,omitempty"`
	PageSelections []PageSelection `json:"page_selections,omitempty"`
}

func NewStore(dir string) (Store, error) {
	if strings.TrimSpace(dir) != "" {
		return Store{Dir: dir}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, fmt.Errorf("resolve home directory: %w", err)
	}
	return Store{Dir: filepath.Join(home, DirName)}, nil
}

func (s Store) Path() string {
	return filepath.Join(s.Dir, "connections.json")
}

func (s Store) Load(ctx context.Context) (File, error) {
	select {
	case <-ctx.Done():
		return File{}, ctx.Err()
	default:
	}

	b, err := os.ReadFile(s.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, nil
		}
		return File{}, fmt.Errorf("read connection state: %w", err)
	}

	var file File
	if err := json.Unmarshal(b, &file); err != nil {
		return File{}, fmt.Errorf("parse connection state: %w", err)
	}
	sortConnections(file.Connections)
	sortPageSelections(file.PageSelections)
	return file, nil
}

func (s Store) Save(ctx context.Context, file File) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	sortConnections(file.Connections)
	sortPageSelections(file.PageSelections)
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal connection state: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(s.Path(), b, 0o600); err != nil {
		return fmt.Errorf("write connection state: %w", err)
	}
	return nil
}

func UpsertConnection(file File, conn Connection) File {
	for i, existing := range file.Connections {
		if existing.Name == conn.Name {
			file.Connections[i] = conn
			sortConnections(file.Connections)
			return file
		}
	}
	file.Connections = append(file.Connections, conn)
	sortConnections(file.Connections)
	return file
}

func SelectConnection(file File, name string) (File, bool) {
	for _, conn := range file.Connections {
		if conn.Name == name {
			file.Selected = name
			return file, true
		}
	}
	return file, false
}

func RemoveConnection(file File, name string) (File, bool) {
	removed := false
	conns := file.Connections[:0]
	for _, conn := range file.Connections {
		if conn.Name == name {
			removed = true
			continue
		}
		conns = append(conns, conn)
	}
	if !removed {
		return file, false
	}
	file.Connections = conns
	file = RemovePageSelection(file, name)
	if file.Selected == name {
		file.Selected = ""
		if len(file.Connections) == 1 {
			file.Selected = file.Connections[0].Name
		}
	}
	sortConnections(file.Connections)
	return file, true
}

func PruneMissingProjects(file File, exists func(string) bool) (File, []Connection) {
	var removed []Connection
	conns := file.Connections[:0]
	for _, conn := range file.Connections {
		if conn.Project != "" && !exists(conn.Project) {
			removed = append(removed, conn)
			continue
		}
		conns = append(conns, conn)
	}
	if len(removed) == 0 {
		return file, nil
	}
	file.Connections = conns
	for _, conn := range removed {
		if file.Selected == conn.Name {
			file.Selected = ""
			break
		}
	}
	if file.Selected == "" && len(file.Connections) == 1 {
		file.Selected = file.Connections[0].Name
	}
	sortConnections(file.Connections)
	return file, removed
}

func CurrentConnection(file File) (Connection, bool) {
	if file.Selected != "" {
		for _, conn := range file.Connections {
			if conn.Name == file.Selected {
				return conn, true
			}
		}
	}
	if len(file.Connections) == 1 {
		return file.Connections[0], true
	}
	return Connection{}, false
}

func ProjectConnection(file File, cwd string) (Connection, bool) {
	cwd = filepath.Clean(cwd)
	var best Connection
	bestLen := -1
	for _, conn := range file.Connections {
		if conn.Project == "" {
			continue
		}
		project := filepath.Clean(conn.Project)
		if cwd != project && !strings.HasPrefix(cwd, project+string(os.PathSeparator)) {
			continue
		}
		if len(project) > bestLen {
			best = conn
			bestLen = len(project)
		}
	}
	return best, bestLen >= 0
}

func UpsertPageSelection(file File, selection PageSelection) File {
	for i, existing := range file.PageSelections {
		if existing.Connection == selection.Connection {
			file.PageSelections[i] = selection
			sortPageSelections(file.PageSelections)
			return file
		}
	}
	file.PageSelections = append(file.PageSelections, selection)
	sortPageSelections(file.PageSelections)
	return file
}

func PageSelectionForConnection(file File, connection string) (PageSelection, bool) {
	for _, selection := range file.PageSelections {
		if selection.Connection == connection {
			return selection, true
		}
	}
	return PageSelection{}, false
}

func RemovePageSelection(file File, connection string) File {
	filtered := file.PageSelections[:0]
	for _, selection := range file.PageSelections {
		if selection.Connection == connection {
			continue
		}
		filtered = append(filtered, selection)
	}
	file.PageSelections = filtered
	return file
}

func ConnectionByName(file File, name string) (Connection, bool) {
	for _, conn := range file.Connections {
		if conn.Name == name {
			return conn, true
		}
	}
	return Connection{}, false
}

func sortConnections(conns []Connection) {
	sort.Slice(conns, func(i, j int) bool {
		return conns[i].Name < conns[j].Name
	})
}

func sortPageSelections(selections []PageSelection) {
	sort.Slice(selections, func(i, j int) bool {
		return selections[i].Connection < selections[j].Connection
	})
}
