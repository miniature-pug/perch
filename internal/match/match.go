// Package match provides **-aware glob matching for config blacklist and
// wildcard rules, backed by doublestar (path globbing that supports ** across
// separators). A malformed pattern is skipped (never matches), never panics.
package match

import (
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// MatchAny reports whether path matches any of the glob patterns. Each pattern
// is tried against the full path and against filepath.Base(path), so both
// path-style ("**/archive/**") and basename-style ("*.tmp") patterns work.
// Patterns that fail to compile are skipped.
func MatchAny(patterns []string, path string) bool {
	base := filepath.Base(path)
	for _, p := range patterns {
		if ok, err := doublestar.Match(p, path); err == nil && ok {
			return true
		}
		if ok, err := doublestar.Match(p, base); err == nil && ok {
			return true
		}
	}
	return false
}
