// internal/fs/fs.go
package fs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/fsnotify/fsnotify"

	"github.com/miniature-pug/perch/internal/safe"
)

const (
	// gitStatusTimeout is the deadline for git subprocess calls (rev-parse + status --porcelain).
	gitStatusTimeout = 5 * time.Second
	// gitPorcelainMinLen is the minimum valid line length in git status --porcelain output.
	gitPorcelainMinLen = 4
	// defaultFileMode is the permission bits applied to new files written by WriteFile.
	defaultFileMode = 0o644
	// MaxReadFileBytes caps how much ReadFile will load into memory. It guards
	// against unbounded reads: a special file like /dev/zero would otherwise
	// exhaust memory, and a huge regular file would balloon the editor.
	MaxReadFileBytes = 10 << 20 // 10 MiB
)

// Node is one entry in a directory listing.
// JSON tags are frozen — do not rename.
type Node struct {
	Name      string `json:"name"`
	Path      string `json:"path"` // absolute
	IsDir     bool   `json:"isDir"`
	Modified  bool   `json:"modified"`
	Untracked bool   `json:"untracked"`
}

// ListDir returns the immediate children of absDir sorted dirs-first, then
// files ascending by name. When gitignoreAware is true, entries matching
// any pattern in absDir/.gitignore are excluded (single-level patterns only;
// no recursive gitignore walk).
func ListDir(absDir string, gitignoreAware bool) ([]Node, error) {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	var patterns []string
	if gitignoreAware {
		patterns = loadGitignorePatterns(filepath.Join(absDir, ".gitignore"))
	}

	var dirs, files []Node
	for _, e := range entries {
		name := e.Name()
		if gitignoreAware && matchesAny(name, patterns) {
			continue
		}
		n := Node{
			Name:  name,
			Path:  filepath.Join(absDir, name),
			IsDir: e.IsDir(),
		}
		if e.IsDir() {
			dirs = append(dirs, n)
		} else {
			files = append(files, n)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	result := append(dirs, files...)
	enrichGitStatus(absDir, result)
	return result, nil
}

// enrichGitStatus queries git status for absDir and marks each node's Modified
// and Untracked fields accordingly. It is best-effort: any git failure leaves
// both flags false and the listing is returned normally.
func enrichGitStatus(absDir string, nodes []Node) {
	ctx, cancel := context.WithTimeout(context.Background(), gitStatusTimeout)
	defer cancel()

	// Determine the repo root. This also acts as the "is a git repo?" check.
	rootCmd := exec.CommandContext(ctx, "git", "-C", absDir, "rev-parse", "--show-toplevel")
	rootOut, err := rootCmd.Output()
	if err != nil {
		return // not a git repo or git not available
	}
	repoRoot := strings.TrimSpace(string(rootOut))

	// Run git status --porcelain from the repo root so paths are always
	// relative to repoRoot (no ambiguity about cwd vs repo root).
	statusCmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "status", "--porcelain")
	statusOut, err := statusCmd.Output()
	if err != nil {
		return
	}

	// Build a set of relative paths that are modified or untracked.
	type gitEntry struct{ modified, untracked bool }
	entries := make(map[string]gitEntry)
	sc := bufio.NewScanner(strings.NewReader(string(statusOut)))
	for sc.Scan() {
		line := sc.Text()
		if len(line) < gitPorcelainMinLen {
			continue
		}
		xy := line[0:2]   // two status chars
		rel := line[3:]   // path relative to repo root
		e := entries[rel] // zero-value if not present
		if xy == "??" {
			e.untracked = true
		} else {
			e.modified = true
		}
		entries[rel] = e
	}

	// Match each node against the porcelain entries.
	for i := range nodes {
		rel, err := filepath.Rel(repoRoot, nodes[i].Path)
		if err != nil {
			continue
		}
		if e, ok := entries[rel]; ok {
			nodes[i].Modified = e.modified
			nodes[i].Untracked = e.untracked
			continue
		}
		// Untracked directories are emitted with a trailing slash in porcelain.
		if nodes[i].IsDir {
			if e, ok := entries[rel+"/"]; ok {
				nodes[i].Modified = e.modified
				nodes[i].Untracked = e.untracked
			}
		}
	}
}

// loadGitignorePatterns reads pattern lines from a .gitignore file.
// Blank lines and comments (#) are ignored.
func loadGitignorePatterns(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var patterns []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// matchesAny reports whether name matches any gitignore pattern using
// doublestar glob matching. Trailing-slash patterns (dir patterns) match
// the name without the slash.
func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.TrimSuffix(p, "/")
		if ok, _ := doublestar.Match(p, name); ok {
			return true
		}
	}
	return false
}

// ShouldExclude reports whether a directory base name must not be watched:
// always ".git", plus anything matching the root .gitignore patterns.
func ShouldExclude(name string, patterns []string) bool {
	if name == ".git" {
		return true
	}
	return matchesAny(name, patterns)
}

// Watcher watches a directory tree for filesystem changes.
type Watcher struct {
	fw       *fsnotify.Watcher
	onChange func(string)
	patterns []string
	once     sync.Once
	done     chan struct{}
}

// Watch creates a Watcher for absRoot. onChange is called with the absolute
// path of any changed file or directory. Watch returns an error if fsnotify
// cannot be initialised or the root cannot be added.
// The watcher is recursive: all subdirectories are watched, excluding .git and
// any directory matching a pattern in absRoot/.gitignore.
func Watch(absRoot string, onChange func(absPath string)) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	// Root add is fatal.
	if err := fw.Add(absRoot); err != nil {
		_ = fw.Close()
		return nil, err
	}
	patterns := loadGitignorePatterns(filepath.Join(absRoot, ".gitignore"))
	// Walk subdirectories and add them (best-effort; errors on individual subdirs are skipped).
	_ = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if ShouldExclude(d.Name(), patterns) {
			return filepath.SkipDir
		}
		// Root is already added; re-adding is idempotent.
		_ = fw.Add(path)
		return nil
	})
	w := &Watcher{fw: fw, onChange: onChange, patterns: patterns, done: make(chan struct{})}
	go w.loop()
	return w, nil
}

func (w *Watcher) loop() {
	defer safe.Recover("fs-watcher")
	for {
		select {
		case event, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.onChange(event.Name)
			// On a create event, watch newly-created subdirectories.
			if event.Op&fsnotify.Create != 0 {
				info, statErr := os.Stat(event.Name)
				if statErr == nil && info.IsDir() && !ShouldExclude(filepath.Base(event.Name), w.patterns) {
					_ = w.fw.Add(event.Name)
				}
			}
		case werr, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			log.Printf("fs.Watcher: fsnotify error: %v", werr)
		case <-w.done:
			return
		}
	}
}

// Close stops the watcher. Idempotent.
func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		close(w.done)
		err = w.fw.Close()
	})
	return err
}

// ReadFile reads and returns the contents of absPath. It rejects anything that
// is not a regular file (device, FIFO, socket, char device — a FIFO or socket
// would otherwise block forever, a device like /dev/zero would read without
// end) and enforces MaxReadFileBytes. The Lstat guard on the final component
// rejects a symlink whose target is a special file; io.LimitReader is a belt to
// the size cap's suspenders in case the file grows between stat and read.
func ReadFile(absPath string) ([]byte, error) {
	// Lstat so a symlink to a special file is caught by the mode check rather
	// than followed. A symlink to a regular file falls through to os.Open below,
	// which resolves it normally.
	li, err := os.Lstat(absPath)
	if err != nil {
		return nil, err
	}
	info := li
	if li.Mode()&os.ModeSymlink != 0 {
		// Resolve the symlink target to size-check and type-check the real file.
		if info, err = os.Stat(absPath); err != nil {
			return nil, err
		}
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("ReadFile: %q is not a regular file", absPath)
	}
	if info.Size() > MaxReadFileBytes {
		return nil, fmt.Errorf("ReadFile: %q is too large to open (max %d MiB)", absPath, MaxReadFileBytes>>20)
	}

	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// Re-check the opened file's type: guards against a race where the path was
	// swapped for a special file between Stat and Open.
	if fi, statErr := f.Stat(); statErr == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("ReadFile: %q is not a regular file", absPath)
	}

	// Read at most MaxReadFileBytes+1 so a file that grew past the cap after the
	// size check is still rejected rather than silently truncated.
	data, err := io.ReadAll(io.LimitReader(f, MaxReadFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxReadFileBytes {
		return nil, fmt.Errorf("ReadFile: %q is too large to open (max %d MiB)", absPath, MaxReadFileBytes>>20)
	}
	return data, nil
}

// RevealRunner is the seam for RevealInFiles so tests can inject a fake
// without launching xdg-open.
type RevealRunner interface {
	Run(name string, args ...string) error
}

// execRevealRunner uses os/exec to run the command.
type execRevealRunner struct{}

func (execRevealRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

// revealRunner is the active runner; nil means use the default exec runner.
var revealRunner RevealRunner

// SetRevealRunner replaces the runner used by RevealInFiles. Pass nil to
// restore the default (xdg-open via os/exec). For tests only.
func SetRevealRunner(r RevealRunner) {
	revealRunner = r
}

// RevealInFiles opens the directory containing absPath in the OS file manager
// via xdg-open. The injectable runner seam allows tests to assert the command
// without launching anything.
func RevealInFiles(absPath string) error {
	dir := filepath.Dir(absPath)
	r := revealRunner
	if r == nil {
		r = execRevealRunner{}
	}
	return r.Run("xdg-open", dir)
}

// WriteFile writes data to absPath atomically using a temp file + rename.
// If absPath already exists its permission bits are preserved; new files
// get mode 0o644.
func WriteFile(absPath string, data []byte) error {
	mode := os.FileMode(defaultFileMode)
	if info, err := os.Stat(absPath); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(absPath)
	tmp, err := os.CreateTemp(dir, ".write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, absPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
