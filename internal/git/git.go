// Package git wraps the git subprocess behind the proc.Runner interface so that
// all git invocations in perch are unit-testable without spawning real processes.
package git

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/miniature-pug/perch/internal/model"
	"github.com/miniature-pug/perch/internal/proc"
)

// ValidRef validates a git ref name using the essentials of git check-ref-format
// rules. It rejects values that git would interpret as flags or that git itself
// considers malformed.
//
// Rejected: empty string, leading '-' (parsed as a flag by git), any ".."
// sequence, control characters or space, any of the special chars ~ ^ : ? * [ \,
// trailing '/', ".lock" suffix, "@{" sequence, leading '/'.
//
// ValidRef is the security chokepoint for user-supplied branch and
// base-branch values, before callers pass them as positional arguments to
// git worktree add.
func ValidRef(name string) error {
	if name == "" {
		return fmt.Errorf("git: ref name must not be empty")
	}
	// git would parse a leading '-' as a flag.
	if name[0] == '-' {
		return fmt.Errorf("git: ref name %q must not begin with '-'", name)
	}
	// git check-ref-format does not allow a leading '/'.
	if name[0] == '/' {
		return fmt.Errorf("git: ref name %q must not begin with '/'", name)
	}
	// git check-ref-format does not allow a trailing '/'.
	if name[len(name)-1] == '/' {
		return fmt.Errorf("git: ref name %q must not end with '/'", name)
	}
	// git reserves the ".lock" suffix for lock files.
	if strings.HasSuffix(name, ".lock") {
		return fmt.Errorf("git: ref name %q must not end with '.lock'", name)
	}
	// Scan rune by rune for forbidden sequences and characters.
	prev := rune(0)
	for i, r := range name {
		// Control characters (including NUL) and space.
		if r < 0x20 || r == 0x7f || r == ' ' {
			return fmt.Errorf("git: ref name %q contains forbidden character %q", name, r)
		}
		// git check-ref-format forbids these special characters.
		switch r {
		case '~', '^', ':', '?', '*', '[', '\\':
			return fmt.Errorf("git: ref name %q contains forbidden character %q", name, r)
		}
		// ".." sequence.
		if prev == '.' && r == '.' {
			return fmt.Errorf("git: ref name %q contains forbidden sequence '..'", name)
		}
		// "@{" sequence.
		if prev == '@' && r == '{' {
			return fmt.Errorf("git: ref name %q contains forbidden sequence '@{'", name)
		}
		// A dot at position 0 is allowed. For example, ValidRef accepts
		// ".perch" as a component. A lone "." is still rejected, but by the
		// trailing-dot check below, not by the empty-name check above. Real
		// git check-ref-format also forbids a leading dot in a ref
		// component; ValidRef does not enforce that stricter rule here, only
		// the ".." sequence ban.
		_ = i
		prev = r
	}
	// git check-ref-format forbids a trailing '.'.
	if prev == '.' {
		return fmt.Errorf("git: ref name %q must not end with '.'", name)
	}
	return nil
}

// ErrInvalidRef is a sentinel for flag-injection and ref-format errors caught by
// ValidRef. AddWorktree wraps this so callers can use errors.Is.
var ErrInvalidRef = fmt.Errorf("git: invalid ref name")

// Worktree is one parsed record from `git worktree list --porcelain`. It
// captures every attribute git emits so that later milestones (e.g. locked
// worktree removal) need not re-parse the raw output.
type Worktree struct {
	// Path is the value of the "worktree" line (absolute path).
	Path string
	// Head is the value of the "HEAD" line; empty for bare worktrees.
	Head string
	// Branch is the checked-out branch short name (refs/heads/ prefix stripped).
	// Empty for detached or bare worktrees.
	Branch string
	// Bare is true when the "bare" attribute line is present.
	Bare bool
	// Detached is true when the "detached" attribute line is present.
	Detached bool
	// Locked is true when the "locked" attribute line is present. A reason may
	// appear on the same line after a space but is not stored separately.
	Locked bool
	// Prunable is true when the "prunable" attribute line is present.
	Prunable bool
}

// ParsePorcelain parses the output of `git worktree list --porcelain` into a
// slice of Worktree records. It is a pure function with no I/O.
//
// Blank lines separate records. Each record starts with a "worktree" line,
// followed by attribute lines. ParsePorcelain silently skips unknown
// attribute lines. ParsePorcelain tolerates CRLF line endings and ignores
// leading and trailing blank lines.
//
// ParsePorcelain returns an error only when a known attribute line appears
// before any "worktree" line. That input is structurally broken.
// ParsePorcelain handles all other malformed conditions defensively,
// including unknown lines and extra blank lines.
func ParsePorcelain(raw []byte) ([]Worktree, error) {
	// known attribute keywords (everything except "worktree" itself).
	isKnownAttr := map[string]bool{
		"HEAD":     true,
		"branch":   true,
		"bare":     true,
		"detached": true,
		"locked":   true,
		"prunable": true,
	}

	var results []Worktree
	var cur *Worktree // nil = not inside a record yet

	lines := bytes.Split(raw, []byte("\n"))
	for _, rawLine := range lines {
		// Normalize: strip \r for CRLF input.
		line := strings.TrimRight(string(rawLine), "\r")

		if line == "" {
			// Blank line = record separator. Flush current record if any.
			if cur != nil {
				results = append(results, *cur)
				cur = nil
			}
			continue
		}

		// Split into keyword and remainder (e.g. "branch refs/heads/main" → "branch", "refs/heads/main").
		fields := strings.SplitN(line, " ", 2)
		keyword := fields[0]

		if keyword == "worktree" {
			// Start a new record. Flush any in-progress record first.
			if cur != nil {
				results = append(results, *cur)
			}
			path := ""
			if len(fields) == 2 {
				path = fields[1]
			}
			cur = &Worktree{Path: path}
			continue
		}

		// For all other keywords, check whether the keyword is a known
		// attribute that appears before any worktree line. That input is
		// structurally broken.
		if cur == nil {
			if isKnownAttr[keyword] {
				return nil, fmt.Errorf("git: ParsePorcelain: attribute %q before any worktree line", keyword)
			}
			// Skip an unknown line before the first worktree line, defensively.
			continue
		}

		switch keyword {
		case "HEAD":
			if len(fields) == 2 {
				cur.Head = fields[1]
			}
		case "branch":
			if len(fields) == 2 {
				cur.Branch = strings.TrimPrefix(fields[1], "refs/heads/")
			}
		case "bare":
			cur.Bare = true
		case "detached":
			cur.Detached = true
		case "locked":
			cur.Locked = true
			// A reason may follow on the same line. ParsePorcelain records only the bool.
		case "prunable":
			cur.Prunable = true
			// A reason may follow on the same line. ParsePorcelain records only the bool.
		default:
			// Unknown attribute: skip silently (forward-compatible).
		}
	}

	// Flush the final record (git's last record has no trailing blank line).
	if cur != nil {
		results = append(results, *cur)
	}

	return results, nil
}

// ListWorktrees invokes `git -C <repoRoot> worktree list --porcelain` through r
// and parses the output with ParsePorcelain. ListWorktrees includes the
// repoRoot argument in every error message, for debugging.
//
// On a runner error, ListWorktrees includes stderr, if any, in the returned
// error. On a parse error, ListWorktrees wraps the error with context.
func ListWorktrees(ctx context.Context, r proc.Runner, repoRoot string) ([]Worktree, error) {
	stdout, stderr, err := r.Run(ctx, "git", "-C", repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		if len(stderr) > 0 {
			return nil, fmt.Errorf("git: worktree list %s: %w (stderr: %s)", repoRoot, err, bytes.TrimSpace(stderr))
		}
		return nil, fmt.Errorf("git: worktree list %s: %w", repoRoot, err)
	}

	wts, err := ParsePorcelain(stdout)
	if err != nil {
		return nil, fmt.Errorf("git: parse worktree list %s: %w", repoRoot, err)
	}
	return wts, nil
}

// firstNonBare returns the index of the first non-bare entry in wts, or -1
// if none exists. This index is the canonical main worktree position.
//
// Rule: the main worktree is the first non-bare entry. Git always lists it
// first in normal repositories. In a bare repository with attached
// worktrees, the bare entry comes first, and the main working checkout is
// the next non-bare entry.
func firstNonBare(wts []Worktree) int {
	for i := range wts {
		if !wts[i].Bare {
			return i
		}
	}
	return -1
}

// MainWorktree returns the canonical main worktree: the first non-bare entry
// in wts. For a bare-only repository (no working checkout), ok is false.
//
// Git always places the main worktree first in the porcelain output,
// regardless of which worktree directory the caller ran the command from.
// So index 0 is normally the main checkout. The non-bare check makes
// MainWorktree safe for the rare case of a bare repository with linked
// working worktrees.
func MainWorktree(wts []Worktree) (Worktree, bool) {
	i := firstNonBare(wts)
	if i < 0 {
		return Worktree{}, false
	}
	return wts[i], true
}

// ToTrees maps a slice of Worktree records to []model.Tree for the given
// project. Bare entries have no working checkout, so ToTrees skips them
// entirely.
//
// IsMain is true for the first non-bare entry (the main checkout). All other
// non-bare entries are linked worktrees with IsMain == false. ToTrees sets
// the Project pointer on every returned Tree.
func ToTrees(wts []Worktree, project *model.Project) []model.Tree {
	mainIdx := firstNonBare(wts)
	var trees []model.Tree
	for i, wt := range wts {
		if wt.Bare {
			// Bare worktrees have no working directory. Skip them.
			continue
		}
		trees = append(trees, model.Tree{
			Path:    wt.Path,
			Branch:  wt.Branch,
			IsMain:  i == mainIdx,
			Project: project,
		})
	}
	return trees
}
