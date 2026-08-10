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
	// appName is the application subdirectory name that perch uses under XDG config dirs.
	appName = "perch"
	// workspacesFile is the filename of the persistent workspace registry.
	workspacesFile = "workspaces.json"
	// ConfigDirMode is the permission bits that Load uses when it creates the
	// perch config directory. ConfigDirMode is exported so callers (for
	// example, app) can reuse the same value instead of duplicating it.
	ConfigDirMode = 0o700
)

// Workspace is the persistent record for one perch workspace.
// The JSON tags are frozen. Do not rename them. This struct may gain new
// fields over time.
// A missing field in stored JSON defaults to the Go zero value on load. For
// example, RepoPath is "" for records written before perch added RepoPath.
type Workspace struct {
	ID            string    `json:"id"`
	RepoPath      string    `json:"repoPath"`      // source repo root
	WorktreePath  string    `json:"worktreePath"`  // linked tree; == RepoPath when Worktree==false
	Worktree      bool      `json:"worktree"`      // true=isolated tree, false=in-repo permanent
	Agent         string    `json:"agent"`
	LastSessionID string    `json:"lastSessionID"`
	Title         string    `json:"title"`
	Branch        string    `json:"branch"`
	BaseRef       string    `json:"baseRef,omitempty"` // branch the worktree was created from; "" for old/in-repo records
	LastActive    time.Time `json:"lastActive"`
}

// Store is a thread-safe, file-backed workspace registry.
type Store struct {
	path  string
	mu    sync.Mutex
	items map[string]Workspace
}

// DefaultConfigDir returns the perch config directory. It follows the XDG
// Base Directory spec: $XDG_CONFIG_HOME/perch, or ~/.config/perch if
// XDG_CONFIG_HOME is unset.
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

// Load reads workspaces.json from configDir. A missing file is not an
// error. Load returns an empty store instead. Load creates configDir if it
// does not exist.
func Load(configDir string) (*Store, error) {
	if err := os.MkdirAll(configDir, ConfigDirMode); err != nil {
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
		backupPath, renameErr := quarantine(path)
		if renameErr == nil {
			fmt.Fprintf(os.Stderr, "registry: %s was corrupt and has been quarantined to %s; starting with an empty workspace list\n", path, backupPath)
		} else {
			fmt.Fprintf(os.Stderr, "registry: %s was corrupt; quarantine to %s failed (%v); starting with an empty workspace list\n", path, backupPath, renameErr)
		}
		return s, nil
	}
	for _, w := range items {
		s.items[w.ID] = w
	}
	return s, nil
}

// List returns all workspaces sorted by LastActive descending, with the
// workspace ID as a stable tie-breaker.
// items is a map, so its iteration order is random. Without the ID
// tie-breaker, two records that share a LastActive timestamp would reorder
// at random between refreshes, and the sidebar would flicker.
// SliceStable and the explicit ID comparison together pin one deterministic
// order for equal timestamps.
func (s *Store) List() []Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Workspace, 0, len(s.items))
	for _, w := range s.items {
		out = append(out, w)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastActive.Equal(out[j].LastActive) {
			return out[i].ID < out[j].ID
		}
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

// quarantine renames path to a timestamped .corrupt-* backup and returns
// the backup path. The caller logs the outcome. quarantine itself stays
// silent.
func quarantine(path string) (string, error) {
	backupPath := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
	return backupPath, os.Rename(path, backupPath)
}

// flush writes all items to disk atomically (temp file, then rename).
// The caller must hold s.mu before calling flush.
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
