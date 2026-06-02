// Package state manages perch's two plain-JSON stores and frecency ranking.
//
// Two stores live under $XDG_STATE_HOME/perch/ (default ~/.local/state/perch/):
//   - state.json      — session→worktree choice mappings and frecency bookkeeping.
//   - windows/<k>.json — one file per live tmux window; isolating files means
//     concurrent perch instances never clobber each other on launch/kill.
//
// No database, no daemon, no third-party dependencies.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/Miniature-Pug/perch/internal/model"
)

// stateFile is the filename for the session→worktree mapping store.
const stateFile = "state.json"

// WindowsDirName is the subdirectory that holds per-window JSON records.
const WindowsDirName = "windows"

// maxStateFileSize is the maximum number of bytes readLimited will read from
// any state or window file. Files larger than this cap are rejected to prevent
// a malicious or corrupt file from exhausting available memory (V7b).
const maxStateFileSize int64 = 16 << 20 // 16 MiB

// readLimited opens path and reads at most max bytes. If the actual content
// exceeds max, an error is returned so the caller can treat the file as
// malformed rather than risk an OOM.
func readLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only fd; close error is unactionable
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("file exceeds %d-byte limit (%d+ bytes)", max, max+1)
	}
	return data, nil
}

// ── Types ─────────────────────────────────────────────────────────────────────

// Choice constants for Mapping.Choice.
const (
	// ChoiceWorktree indicates perch created (or reused) a dedicated worktree for
	// the session.
	ChoiceWorktree = "worktree"
	// ChoiceNone indicates the user declined worktree creation; the session runs
	// in the repo root.
	ChoiceNone = "none"
)

// Mapping records which worktree decision perch made for a session, so the
// interactive prompt is never shown twice for the same session.
// Choice values: ChoiceWorktree ("worktree") | ChoiceNone ("none").
type Mapping struct {
	Tool   model.Tool `json:"tool"`
	Tree   string     `json:"tree"`
	Choice string     `json:"choice"`
}

// ProjectStat holds the two fields that drive frecency ranking.
// The score itself is computed at query time (§6.3); only these two values persist.
type ProjectStat struct {
	Rank         float64 `json:"rank"`
	LastAccessed int64   `json:"last_accessed"`
}

// State is the top-level document for state.json.
type State struct {
	Mappings map[string]Mapping     `json:"mappings"`
	Projects map[string]ProjectStat `json:"projects"`
}

// ── Path resolver ─────────────────────────────────────────────────────────────

// StateDir returns the canonical base directory for perch state files, following
// the XDG Base Directory spec: $XDG_STATE_HOME/perch, falling back to
// ~/.local/state/perch when XDG_STATE_HOME is unset.
//
// StateDir only resolves the path — it does NOT create the directory.
// Production callers use this; tests inject an explicit base dir instead.
func StateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("state: resolve home dir for default state path: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "perch"), nil
}

// ── state.json ────────────────────────────────────────────────────────────────

// LoadState reads state.json from baseDir. A missing file returns an empty,
// non-nil State (no error). A malformed file returns an error — caller may
// choose to start fresh — but never panics.
func LoadState(baseDir string) (State, error) {
	empty := State{
		Mappings: make(map[string]Mapping),
		Projects: make(map[string]ProjectStat),
	}

	path := filepath.Join(baseDir, stateFile)
	data, err := readLimited(path, maxStateFileSize)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("state: read %s: %w", path, err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return empty, fmt.Errorf("state: parse %s: %w", path, err)
	}
	// Ensure callers always get non-nil maps.
	if s.Mappings == nil {
		s.Mappings = make(map[string]Mapping)
	}
	if s.Projects == nil {
		s.Projects = make(map[string]ProjectStat)
	}
	return s, nil
}

// SaveState writes s to state.json inside baseDir atomically (temp file in the
// same dir + os.Rename). baseDir is created if absent.
//
// A lock is not used in v1: mappings are only written on the interactive prompt
// (low contention) and projects tolerate a rare lost increment.
func SaveState(baseDir string, s State) error {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return fmt.Errorf("state: mkdir %s: %w", baseDir, err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("state: marshal state: %w", err)
	}
	target := filepath.Join(baseDir, stateFile)
	return writeFileAtomic(target, data)
}

// LookupMapping returns the Mapping for sessionID and whether it was present.
// A nil Mappings map is treated as empty and returns the zero Mapping and false.
func LookupMapping(s State, sessionID string) (Mapping, bool) {
	if s.Mappings == nil {
		return Mapping{}, false
	}
	m, ok := s.Mappings[sessionID]
	return m, ok
}

// SetMapping stores m under sessionID in s.Mappings, lazily initialising the
// map if it is nil. The caller is responsible for calling SaveState afterward
// to persist the change; this function only mutates the in-memory State.
func SetMapping(s *State, sessionID string, m Mapping) {
	if s.Mappings == nil {
		s.Mappings = make(map[string]Mapping)
	}
	s.Mappings[sessionID] = m
}

// ── windows/<paneKey>.json ────────────────────────────────────────────────────

// EncodePaneKey percent-encodes a tmux pane id so it is filesystem-safe.
// Example: "%17" → "%2517". DecodePaneKey is the inverse.
func EncodePaneKey(paneKey string) string {
	return url.PathEscape(paneKey)
}

// DecodePaneKey reverses EncodePaneKey. Returns an error if the encoded string
// contains an invalid percent sequence.
func DecodePaneKey(name string) (string, error) {
	// url.PathUnescape preserves '+' (unlike QueryUnescape which maps it to space).
	decoded, err := url.PathUnescape(name)
	if err != nil {
		return "", fmt.Errorf("state: decode pane key %q: %w", name, err)
	}
	return decoded, nil
}

// windowsDir returns the windows/ subdirectory under baseDir.
func windowsDir(baseDir string) string {
	return filepath.Join(baseDir, WindowsDirName)
}

// SaveWindow atomically writes w to windows/<encoded paneKey>.json, creating the
// windows/ directory if absent.
func SaveWindow(baseDir string, w model.Window) error {
	dir := windowsDir(baseDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("state: mkdir %s: %w", dir, err)
	}
	data, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("state: marshal window %s: %w", w.PaneKey, err)
	}
	target := filepath.Join(dir, EncodePaneKey(w.PaneKey)+".json")
	return writeFileAtomic(target, data)
}

// LoadWindows reads all windows/*.json files and returns the decoded records.
// Individual malformed or unreadable files are skipped (logged) so that a
// single corrupt file never blocks resurrect. A missing windows/ directory
// returns an empty slice, not an error.
func LoadWindows(baseDir string) ([]model.Window, error) {
	dir := windowsDir(baseDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []model.Window{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: read dir %s: %w", dir, err)
	}

	var windows []model.Window
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := readLimited(path, maxStateFileSize)
		if err != nil {
			log.Printf("state: skip unreadable window file %s: %v", path, err)
			continue
		}
		var w model.Window
		if err := json.Unmarshal(data, &w); err != nil {
			log.Printf("state: skip malformed window file %s: %v", path, err)
			continue
		}
		windows = append(windows, w)
	}
	return windows, nil
}

// RemoveWindow removes the window record for paneKey on clean teardown.
// Removing a non-existent record is not an error.
func RemoveWindow(baseDir, paneKey string) error {
	path := filepath.Join(windowsDir(baseDir), EncodePaneKey(paneKey)+".json")
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("state: remove window %s: %w", paneKey, err)
	}
	return nil
}

// ── Frecency (zoxide algorithm) ───────────────────────────────────────────────

// maxAge is the ceiling for total frecency weight before aging is applied.
const maxAge = 10_000.0

// Frecency time-bucket boundaries (seconds). Used by FrecencyScore to classify
// how recently a project was accessed.
const (
	frecencyHour = 3_600
	frecencyDay  = 86_400
	frecencyWeek = 604_800
)

// Frecency score multipliers applied per time bucket.
const (
	frecencyMul1h  = 4.0
	frecencyMul1d  = 2.0
	frecencyMul1w  = 0.5
	frecencyMulOld = 0.25
)

// frecencyAgeFactor is the factor applied to all ranks during aging when total
// weight exceeds maxAge. Derived from zoxide's algorithm (§6.3).
const frecencyAgeFactor = 0.9

// rankBumpDelta is the increment added to a project's rank on each access.
const rankBumpDelta = 1.0

// FrecencyScore computes the frecency score for a project given its rank and
// last-access time. The score is computed at query time; only rank and
// last_accessed are persisted (§6.3).
func FrecencyScore(rank float64, lastAccessed, now int64) float64 {
	switch d := now - lastAccessed; {
	case d < frecencyHour:
		return rank * frecencyMul1h
	case d < frecencyDay:
		return rank * frecencyMul1d
	case d < frecencyWeek:
		return rank * frecencyMul1w
	default:
		return rank * frecencyMulOld
	}
}

// BumpProject increments the rank of path by 1.0 and sets last_accessed to now.
// If path is not yet in the map, it is inserted with rank 1.0.
// Callers should invoke AgeProjects after every bump to bound total weight.
func BumpProject(projects map[string]ProjectStat, path string, now int64) {
	cur := projects[path]
	cur.Rank = max(cur.Rank+rankBumpDelta, 0.0) // max(...,0) mirrors zoxide's bump (§6.3); floor is defensive against a corrupted negative rank read from disk.
	cur.LastAccessed = now
	projects[path] = cur
}

// AgeProjects bounds total frecency weight. If the sum of all ranks exceeds
// maxAge, each rank is multiplied by 0.9*maxAge/total, then entries with
// rank < 1.0 (strict) are evicted.
func AgeProjects(projects map[string]ProjectStat) {
	total := 0.0
	for _, stat := range projects {
		total += stat.Rank
	}
	if total <= maxAge {
		return
	}
	factor := frecencyAgeFactor * maxAge / total
	for k, stat := range projects {
		stat.Rank *= factor
		if stat.Rank < 1.0 {
			delete(projects, k)
		} else {
			projects[k] = stat
		}
	}
}

// SortedPaths returns project paths sorted by descending FrecencyScore.
// Ties are broken alphabetically for determinism.
// An empty map returns an empty slice (cold start, no special branch needed).
func SortedPaths(projects map[string]ProjectStat, now int64) []string {
	paths := make([]string, 0, len(projects))
	for k := range projects {
		paths = append(paths, k)
	}
	sort.Slice(paths, func(i, j int) bool {
		si := FrecencyScore(projects[paths[i]].Rank, projects[paths[i]].LastAccessed, now)
		sj := FrecencyScore(projects[paths[j]].Rank, projects[paths[j]].LastAccessed, now)
		if si != sj {
			return si > sj // descending score
		}
		return paths[i] < paths[j] // alphabetical tiebreak
	})
	return paths
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// writeFileAtomic writes data to target via a temp file in the same directory,
// then renames atomically. Temp files use a .tmp suffix so they are never
// mistaken for valid .json records by LoadWindows.
func writeFileAtomic(target string, data []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "*.tmp")
	if err != nil {
		return fmt.Errorf("state: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			// Best-effort cleanup; error is unactionable in a defer.
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		// Close before the defer runs to release the fd; ignore close error
		// here because the write error is what matters.
		_ = tmp.Close()
		return fmt.Errorf("state: write temp file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("state: close temp file %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("state: rename %s → %s: %w", tmpName, target, err)
	}
	ok = true
	return nil
}
