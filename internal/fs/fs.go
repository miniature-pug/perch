// internal/fs/fs.go
package fs

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Node is one entry in a directory listing.
// JSON tags are frozen — do not rename.
type Node struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // absolute
	IsDir bool   `json:"isDir"`
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
	return append(dirs, files...), nil
}

// loadGitignorePatterns reads pattern lines from a .gitignore file.
// Blank lines and comments (#) are ignored.
func loadGitignorePatterns(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
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

// ReadFile reads and returns the contents of absPath.
func ReadFile(absPath string) ([]byte, error) {
	return os.ReadFile(absPath)
}

// WriteFile writes data to absPath atomically using a temp file + rename.
// If absPath already exists its permission bits are preserved; new files
// get mode 0o644.
func WriteFile(absPath string, data []byte) error {
	mode := os.FileMode(0o644)
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
