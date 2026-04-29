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
	Project     string `json:"project,omitempty"`
}

type File struct {
	Connections []Connection `json:"connections"`
	Selected    string       `json:"selected,omitempty"`
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
	return file, nil
}

func (s Store) Save(ctx context.Context, file File) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	sortConnections(file.Connections)
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
