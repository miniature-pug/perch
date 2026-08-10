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
	// gitStatusTimeout sets the deadline for git subprocess calls: rev-parse and status --porcelain.
	gitStatusTimeout = 5 * time.Second
	// gitPorcelainMinLen is the minimum valid line length in git status --porcelain output.
	gitPorcelainMinLen = 4
	// defaultFileMode is the permission bits that WriteFile applies to new files.
	defaultFileMode = 0o644
	// MaxReadFileBytes caps how much data ReadFile loads into memory.
	// MaxReadFileBytes guards against unbounded reads. A special file such as
	// /dev/zero would otherwise exhaust memory. A huge regular file would
	// overload the editor.
	MaxReadFileBytes = 10 << 20 // 10 MiB
)

// Node is one entry in a directory listing.
// JSON tags are frozen. Do not rename them.
type Node struct {
	Name      string `json:"name"`
	Path      string `json:"path"` // absolute
	IsDir     bool   `json:"isDir"`
	Modified  bool   `json:"modified"`
	Untracked bool   `json:"untracked"`
}

// ListDir returns the immediate children of absDir, sorted with directories
// first and then files, both in ascending name order. When gitignoreAware is
// true, ListDir excludes entries that match a pattern in absDir/.gitignore.
// This filter covers single-level patterns only; ListDir does not walk
// nested .gitignore files.
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

// enrichGitStatus queries git status for absDir. It marks each node's
// Modified and Untracked fields from the result. enrichGitStatus is
// best-effort: if git fails, both flags stay false and ListDir still
// returns the listing.
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
		// git emits untracked directories with a trailing slash in porcelain output.
		if nodes[i].IsDir {
			if e, ok := entries[rel+"/"]; ok {
				nodes[i].Modified = e.modified
				nodes[i].Untracked = e.untracked
			}
		}
	}
}

// loadGitignorePatterns reads pattern lines from a .gitignore file.
// loadGitignorePatterns ignores blank lines and comment lines (#).
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

// Watch creates a Watcher for absRoot. Watch calls onChange with the
// absolute path of any changed file or directory. Watch returns an error if
// fsnotify fails to start, or if Watch cannot add the root.
// The watcher is recursive: it watches all subdirectories, except .git and
// any directory that matches a pattern in absRoot/.gitignore.
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
	// Walk the subdirectories and add them. Best-effort: skip any error on a single subdirectory.
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
		// The root is already added. Adding it again is safe.
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

// Close stops the watcher. Close is safe to call more than once.
func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		close(w.done)
		err = w.fw.Close()
	})
	return err
}

// ReadFile reads and returns the contents of absPath. ReadFile rejects any
// file that is not a regular file: a device, FIFO, socket, or character
// device. A FIFO or socket would otherwise block forever, and a device such
// as /dev/zero would read without end. ReadFile also enforces
// MaxReadFileBytes.
//
// The Lstat guard on the final path component rejects a symlink whose
// target is a special file. io.LimitReader gives a second size check in
// case the file grows between the stat call and the read.
func ReadFile(absPath string) ([]byte, error) {
	// Use Lstat so the mode check catches a symlink to a special file instead
	// of following it. A symlink to a regular file falls through to os.Open
	// below, which resolves it normally.
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

	// Re-check the opened file's type. This guards against a race: something
	// could replace the path with a special file between the Stat call and
	// the Open call.
	if fi, statErr := f.Stat(); statErr == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("ReadFile: %q is not a regular file", absPath)
	}

	// Read at most MaxReadFileBytes+1 bytes. This way, ReadFile still rejects
	// a file that grows past the cap after the size check, instead of
	// silently truncating it.
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

// revealRunner is the active runner. A nil value means RevealInFiles uses
// the default exec runner.
var revealRunner RevealRunner

// SetRevealRunner replaces the runner that RevealInFiles uses. Pass nil to
// restore the default runner, xdg-open via os/exec. Use SetRevealRunner in
// tests only.
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

// WriteFile writes data to absPath atomically, using a temporary file and a
// rename. If absPath already exists, WriteFile preserves its permission
// bits. A new file gets mode 0o644.
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
