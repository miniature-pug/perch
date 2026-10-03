// internal/fs/fs.go
package fs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/fsnotify/fsnotify"

	"github.com/miniature-pug/perch/internal/proc"
	"github.com/miniature-pug/perch/internal/safe"
)

const (
	// gitStatusTimeout sets the deadline for the git subprocess calls in
	// enrichGitStatus: rev-parse and status.
	gitStatusTimeout = 5 * time.Second
	// defaultFileMode is the permission bits that WriteFile applies to new files.
	defaultFileMode = 0o644
	// MaxReadFileBytes caps how much data ReadFile loads into memory.
	// MaxReadFileBytes guards against unbounded reads. A special file such as
	// /dev/zero would otherwise exhaust memory. A huge regular file would
	// overload the editor.
	MaxReadFileBytes = 10 << 20 // 10 MiB
	// binarySniffBytes is how much of a file ReadFile scans for a NUL byte.
	binarySniffBytes = 8 << 10
)

// ErrBinaryFile is returned (wrapped) by ReadFile for content that is not
// UTF-8 text. Such content cannot survive the trip to the editor: the JSON
// bridge replaces invalid UTF-8 with U+FFFD, so saving it back would corrupt
// the file.
var ErrBinaryFile = errors.New("binary or non-UTF-8 file")

// gitRunner runs the git calls in this package. It is the production
// runner; the variable keeps those calls behind the proc.Runner seam.
var gitRunner proc.Runner = proc.ExecRunner{}

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
//
// A symlink to a directory is listed as a directory (IsDir true). The
// caller is responsible for checking that its target is somewhere the user
// may browse before listing it.
func ListDir(absDir string, gitignoreAware bool) ([]Node, error) {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	var rules []ignoreRule
	if gitignoreAware {
		rules = parseIgnoreRules(loadGitignorePatterns(filepath.Join(absDir, ".gitignore")))
	}

	var dirs, files []Node
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(absDir, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			// DirEntry describes the link itself; follow it to list a linked
			// directory as a directory.
			if fi, statErr := os.Stat(full); statErr == nil {
				isDir = fi.IsDir()
			}
		}
		if gitignoreAware && ignored(name, isDir, rules) {
			continue
		}
		n := Node{
			Name:  name,
			Path:  full,
			IsDir: isDir,
		}
		if isDir {
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
// Modified and Untracked fields from the result. A directory node is marked
// when anything under it is modified or untracked. enrichGitStatus is
// best-effort: if git fails, both flags stay false and ListDir still
// returns the listing.
//
// The status is limited to absDir (pathspec "."), so a listing costs a
// scan of that subtree, not of the whole repository. Paths are matched by
// absDir's prefix inside the repository (rev-parse --show-prefix), which
// git computes from the resolved directory, so a symlinked absDir still
// matches. The -z output is never quoted, so names with spaces or
// non-ASCII bytes match too.
func enrichGitStatus(absDir string, nodes []Node) {
	if len(nodes) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitStatusTimeout)
	defer cancel()

	// The prefix is absDir relative to the repo root ("" at the root, else
	// "dir/sub/"). This also acts as the "is a git repo?" check.
	prefixOut, _, err := gitRunner.Run(ctx, "git", "-C", absDir, "rev-parse", "--show-prefix")
	if err != nil {
		return // not a git repo, or git not available
	}
	prefix := strings.TrimSuffix(string(prefixOut), "\n")

	statusOut, _, err := gitRunner.Run(ctx, "git", "-C", absDir, "status", "--porcelain=v2", "-z", "--", ".")
	if err != nil {
		return
	}

	byName := make(map[string]int, len(nodes))
	for i := range nodes {
		byName[nodes[i].Name] = i
	}
	recs := strings.Split(string(statusOut), "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if rec == "" {
			continue
		}
		var p string
		untracked := false
		switch rec[0] {
		case '1': // 1 XY sub mH mI mW hH hI path
			if f := strings.SplitN(rec, " ", 9); len(f) == 9 {
				p = f[8]
			}
		case '2': // 2 XY sub mH mI mW hH hI Xscore path NUL origPath
			if f := strings.SplitN(rec, " ", 10); len(f) == 10 {
				p = f[9]
			}
			i++ // skip origPath
		case 'u': // u XY sub m1 m2 m3 mW h1 h2 h3 path
			if f := strings.SplitN(rec, " ", 11); len(f) == 11 {
				p = f[10]
			}
		case '?':
			p, untracked = strings.TrimPrefix(rec, "? "), true
		}
		rest, ok := strings.CutPrefix(p, prefix)
		if !ok || rest == "" {
			continue
		}
		// Mark the child of absDir that contains this path: the entry
		// itself, or the directory it lives under. An untracked directory
		// is reported as "dir/", which lands on the "dir" node.
		child, _, _ := strings.Cut(rest, "/")
		idx, ok := byName[child]
		if !ok {
			continue
		}
		if untracked {
			nodes[idx].Untracked = true
		} else {
			nodes[idx].Modified = true
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

// ignoreRule is one parsed .gitignore pattern.
type ignoreRule struct {
	// pattern is the doublestar glob, without the "!", leading "/" and
	// trailing "/" markers.
	pattern string
	// negate is true for a "!pattern" line, which re-includes a match.
	negate bool
	// dirOnly is true for a "pattern/" line, which matches directories only.
	dirOnly bool
	// anchored is true when the pattern contains a "/" before its end. git
	// then matches it against the path relative to the .gitignore's
	// directory, instead of against the base name at any depth.
	anchored bool
}

// parseIgnoreRules turns raw .gitignore lines into rules, following the
// gitignore(5) rules for "!", leading and inner "/", and trailing "/".
func parseIgnoreRules(patterns []string) []ignoreRule {
	rules := make([]ignoreRule, 0, len(patterns))
	for _, p := range patterns {
		var r ignoreRule
		if strings.HasPrefix(p, "!") {
			r.negate = true
			p = p[1:]
		} else if strings.HasPrefix(p, `\!`) || strings.HasPrefix(p, `\#`) {
			p = p[1:]
		}
		if strings.HasSuffix(p, "/") {
			r.dirOnly = true
			p = strings.TrimRight(p, "/")
		}
		if strings.Contains(p, "/") {
			r.anchored = true
			p = strings.TrimPrefix(p, "/")
		}
		if p == "" {
			continue
		}
		r.pattern = p
		rules = append(rules, r)
	}
	return rules
}

// ignored reports whether rel, a slash-separated path relative to the
// directory of the .gitignore the rules came from, is ignored. As in git,
// the last matching rule wins, so a later "!pattern" re-includes a path.
func ignored(rel string, isDir bool, rules []ignoreRule) bool {
	base := path.Base(rel)
	result := false
	for _, r := range rules {
		if r.dirOnly && !isDir {
			continue
		}
		subject := base
		if r.anchored {
			subject = rel
		}
		if ok, _ := doublestar.Match(r.pattern, subject); ok {
			result = !r.negate
		}
	}
	return result
}

// ShouldExclude reports whether a directory directly under the watch root
// must not be watched: always ".git", plus anything the root .gitignore
// patterns ignore. name is the directory's path relative to the root.
func ShouldExclude(name string, patterns []string) bool {
	if path.Base(name) == ".git" {
		return true
	}
	return ignored(name, true, parseIgnoreRules(patterns))
}

// Watcher watches a directory tree for filesystem changes.
type Watcher struct {
	fw       *fsnotify.Watcher
	onChange func(string)
	root     string
	rules    []ignoreRule
	// addFailed records that an inotify watch could not be added, so the
	// failure is logged once instead of once per directory.
	addFailed bool
	once      sync.Once
	done      chan struct{}
}

// Watch creates a Watcher for absRoot. Watch calls onChange with the
// absolute path of any changed file or directory. Watch returns an error if
// fsnotify fails to start, or if Watch cannot add the root.
// The watcher is recursive: it watches all subdirectories, except .git and
// any directory that the patterns in absRoot/.gitignore ignore. When a new
// directory appears, the watcher adds it and every directory below it, and
// reports the files already inside them, since those were created before
// any watch could see them (mkdir -p, git checkout, tar x).
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
	w := &Watcher{
		fw:       fw,
		onChange: onChange,
		root:     absRoot,
		rules:    parseIgnoreRules(loadGitignorePatterns(filepath.Join(absRoot, ".gitignore"))),
		done:     make(chan struct{}),
	}
	w.addTree(absRoot, false)
	go w.loop()
	return w, nil
}

// excluded reports whether the directory at absPath must not be watched.
func (w *Watcher) excluded(absPath string) bool {
	if filepath.Base(absPath) == ".git" {
		return true
	}
	rel, err := filepath.Rel(w.root, absPath)
	if err != nil || rel == "." {
		return false
	}
	return ignored(filepath.ToSlash(rel), true, w.rules)
}

// addTree adds a watch for dir and every non-excluded directory below it.
// When reportFiles is true, it also calls onChange for every file it finds.
// Best-effort: an unreadable subdirectory is skipped, and the first failed
// watch is logged (inotify watch limits are the usual cause).
func (w *Watcher) addTree(dir string, reportFiles bool) {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if reportFiles {
				w.onChange(p)
			}
			return nil
		}
		if w.excluded(p) {
			return filepath.SkipDir
		}
		if addErr := w.fw.Add(p); addErr != nil && !w.addFailed {
			w.addFailed = true
			log.Printf("fs.Watcher: cannot watch %s (further failures not logged): %v", p, addErr)
		}
		return nil
	})
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
			// On a create event, watch the new directory and everything
			// already below it.
			if event.Op&fsnotify.Create != 0 {
				info, statErr := os.Lstat(event.Name)
				if statErr == nil && info.IsDir() && !w.excluded(event.Name) {
					w.addTree(event.Name, true)
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
// MaxReadFileBytes, and returns an error wrapping ErrBinaryFile for content
// that is not UTF-8 text (invalid UTF-8, or a NUL byte near the start).
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
	if bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0 || !utf8.Valid(data) {
		return nil, fmt.Errorf("ReadFile: %q: %w", absPath, ErrBinaryFile)
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
//
// When absPath is a symlink, WriteFile writes through it: the link stays a
// link, and its target gets the new content. Renaming over the link itself
// would turn it into a plain file and leave the target stale (for example a
// CLAUDE.md -> AGENTS.md link). A dangling link is an error. The caller is
// responsible for checking that the link's target is somewhere it may
// write (app.WriteFile resolves and validates it).
func WriteFile(absPath string, data []byte) error {
	target := absPath
	if li, err := os.Lstat(absPath); err == nil && li.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(absPath)
		if err != nil {
			return fmt.Errorf("WriteFile: resolve symlink %q: %w", absPath, err)
		}
		target = resolved
	}
	mode := os.FileMode(defaultFileMode)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(target)
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
	// Flush the data before the rename publishes it, so a crash cannot leave
	// an empty file in place of the old content.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
