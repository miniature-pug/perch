// internal/registry/registry.go
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	// appName is the application subdirectory name that perch uses under XDG config dirs.
	appName = "perch"
	// workspacesFile is the filename of the persistent workspace registry.
	workspacesFile = "workspaces.json"
	// backupSuffix is appended to workspacesFile for the last-good copy that
	// flush keeps and Load falls back to when the main file is corrupt.
	backupSuffix = ".bak"
	// staleTempAge is how old a leftover temp file must be before Load removes
	// it. The age guard keeps Load from deleting the temp file of a flush that
	// is in flight in another process.
	staleTempAge = time.Minute
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
	RepoPath      string    `json:"repoPath"`     // source repo root
	WorktreePath  string    `json:"worktreePath"` // linked tree; == RepoPath when Worktree==false
	Worktree      bool      `json:"worktree"`     // true=isolated tree, false=in-repo permanent
	Agent         string    `json:"agent"`
	LastSessionID string    `json:"lastSessionID"`
	Title         string    `json:"title"`
	Branch        string    `json:"branch"`
	BaseRef       string    `json:"baseRef,omitempty"` // branch the worktree was created from; "" for old/in-repo records
	LastActive    time.Time `json:"lastActive"`
}

// ErrNotFound is returned by Update when no workspace has the given ID.
var ErrNotFound = errors.New("registry: workspace not found")

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
	// MkdirAll leaves the mode of an existing directory alone. Tighten a
	// directory that an earlier version or the user left group/world
	// accessible. The call is best-effort.
	if fi, err := os.Stat(configDir); err == nil && fi.IsDir() && fi.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(configDir, ConfigDirMode)
	}
	path := filepath.Join(configDir, workspacesFile)
	s := &Store{path: path, items: make(map[string]Workspace)}
	removeStaleTemps(configDir)
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
			fmt.Fprintf(os.Stderr, "registry: %s was corrupt and has been quarantined to %s\n", path, backupPath)
		} else {
			fmt.Fprintf(os.Stderr, "registry: %s was corrupt; quarantine to %s failed (%v)\n", path, backupPath, renameErr)
		}
		// Recover from the last-good copy that flush keeps, if it parses.
		if bak, bakErr := os.ReadFile(path + backupSuffix); bakErr == nil {
			var recovered []Workspace
			if json.Unmarshal(bak, &recovered) == nil {
				for _, w := range recovered {
					s.items[w.ID] = w
				}
				fmt.Fprintf(os.Stderr, "registry: recovered %d workspace(s) from %s\n", len(recovered), path+backupSuffix)
				return s, nil
			}
		}
		fmt.Fprintf(os.Stderr, "registry: starting with an empty workspace list\n")
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
// Upsert replaces the whole record, so it is only safe for creating a record
// or for a caller that owns every field. To change fields of a record that
// other goroutines may also change or remove, use Update.
// If persisting fails, Upsert restores the in-memory state and returns the
// error.
func (s *Store) Upsert(w Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.items[w.ID]
	s.items[w.ID] = w
	if err := s.flush(); err != nil {
		if had {
			s.items[w.ID] = prev
		} else {
			delete(s.items, w.ID)
		}
		return err
	}
	return nil
}

// Update atomically reads, modifies and persists the workspace with the given
// ID. fn receives a pointer to a copy of the current record and runs while
// the store lock is held, so it must be quick and must not call back into the
// Store. Update never creates a record: if the ID is absent (for example,
// because Remove ran first), it returns ErrNotFound and fn is not called.
// If fn returns an error, nothing is changed or persisted and Update returns
// that error. If persisting fails, the in-memory record is rolled back.
// On success Update returns the stored record. fn must not change the ID.
func (s *Store) Update(id string, fn func(*Workspace) error) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.items[id]
	if !ok {
		return Workspace{}, ErrNotFound
	}
	next := prev
	if err := fn(&next); err != nil {
		return Workspace{}, err
	}
	next.ID = id
	s.items[id] = next
	if err := s.flush(); err != nil {
		s.items[id] = prev
		return Workspace{}, err
	}
	return next, nil
}

// Remove deletes the workspace with the given ID and atomically persists.
// A missing ID is not an error. If persisting fails, the in-memory state is
// restored and the error is returned.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.items[id]
	if !had {
		return nil
	}
	delete(s.items, id)
	if err := s.flush(); err != nil {
		s.items[id] = prev
		return err
	}
	return nil
}

// quarantine renames path to a timestamped .corrupt-* backup and returns
// the backup path. The caller logs the outcome. quarantine itself stays
// silent.
func quarantine(path string) (string, error) {
	backupPath := path + ".corrupt-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	return backupPath, os.Rename(path, backupPath)
}

// removeStaleTemps deletes temp files that a crashed flush left in dir.
func removeStaleTemps(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, ".workspaces-*.json.tmp"))
	if err != nil {
		return
	}
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && time.Since(fi.ModTime()) > staleTempAge {
			_ = os.Remove(m)
		}
	}
}

// flush writes all items to disk atomically: temp file, fsync, rename, then
// fsync of the directory. Items are written sorted by ID so the file is
// stable between writes. Before the rename, flush saves the current file as
// workspaces.json.bak (best-effort) so Load can recover from corruption.
// The caller must hold s.mu before calling flush.
func (s *Store) flush() error {
	list := make([]Workspace, 0, len(s.items))
	for _, w := range s.items {
		list = append(list, w)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("registry: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("registry: close temp: %w", err)
	}
	s.saveBackup()
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("registry: rename: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync() // best-effort: not every filesystem supports directory fsync
		_ = d.Close()
	}
	return nil
}

// saveBackup copies the current workspaces.json to workspaces.json.bak if the
// current file parses as a workspace list. A corrupt current file is never
// copied over a good backup. Failures are ignored: the backup is a
// convenience, not a requirement.
func (s *Store) saveBackup() {
	cur, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var probe []Workspace
	if json.Unmarshal(cur, &probe) != nil {
		return
	}
	_ = os.WriteFile(s.path+backupSuffix, cur, 0o600)
}
