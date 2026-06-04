// internal/registry/registry.go
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	// appName is the application subdirectory name used under XDG config dirs.
	appName = "perch"
	// workspacesFile is the filename of the persistent workspace registry.
	workspacesFile = "workspaces.json"
	// configDirMode is the permission bits used when creating the perch config directory.
	configDirMode = 0o700
)

// Workspace is the persistent record for one perch workspace.
// JSON tags are frozen — do not rename. New fields may be added.
// Missing fields in stored JSON default to the Go zero value on load
// (e.g. Model=="" for records written before model plumbing was added).
type Workspace struct {
	ID            string    `json:"id"`
	WorktreePath  string    `json:"worktreePath"`
	Agent         string    `json:"agent"`
	LastSessionID string    `json:"lastSessionID"`
	Model         string    `json:"model,omitempty"`
	Title         string    `json:"title"`
	Branch        string    `json:"branch"`
	LastActive    time.Time `json:"lastActive"`
}

// Store is a thread-safe, file-backed workspace registry.
type Store struct {
	path  string
	mu    sync.Mutex
	items map[string]Workspace
}

// DefaultConfigDir returns the perch config directory following the XDG Base
// Directory spec: $XDG_CONFIG_HOME/perch, falling back to ~/.config/perch.
func DefaultConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".config", appName)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, appName)
}

// Load reads workspaces.json from configDir. A missing file is not an error
// and returns an empty store. configDir is created if it does not exist.
func Load(configDir string) (*Store, error) {
	if err := os.MkdirAll(configDir, configDirMode); err != nil {
		return nil, fmt.Errorf("registry: mkdir %s: %w", configDir, err)
	}
	path := filepath.Join(configDir, workspacesFile)
	s := &Store{path: path, items: make(map[string]Workspace)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("registry: read %s: %w", path, err)
	}
	var items []Workspace
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("registry: parse %s: %w", path, err)
	}
	for _, w := range items {
		s.items[w.ID] = w
	}
	return s, nil
}

// List returns all workspaces sorted by LastActive descending.
func (s *Store) List() []Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Workspace, 0, len(s.items))
	for _, w := range s.items {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastActive.After(out[j].LastActive)
	})
	return out
}

// Get returns the workspace with the given ID, or false if not found.
func (s *Store) Get(id string) (Workspace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.items[id]
	return w, ok
}

// Upsert inserts or replaces the workspace and atomically persists the store.
func (s *Store) Upsert(w Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[w.ID] = w
	return s.flush()
}

// Remove deletes the workspace with the given ID and atomically persists.
// A missing ID is not an error.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return s.flush()
}

// flush writes all items to disk atomically (temp file + rename).
// Must be called with s.mu held.
func (s *Store) flush() error {
	list := make([]Workspace, 0, len(s.items))
	for _, w := range s.items {
		list = append(list, w)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("registry: marshal: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".workspaces-*.json.tmp")
	if err != nil {
		return fmt.Errorf("registry: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("registry: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("registry: close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("registry: rename: %w", err)
	}
	return nil
}
