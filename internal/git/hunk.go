// internal/git/hunk.go
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Miniature-Pug/perch/internal/proc"
)

// FileDiff carries the per-file summary from DiffStat.
// Status: "M" modified, "A" added, "D" deleted, "R" renamed, "?" untracked.
type FileDiff struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Status  string `json:"status"`
}

// HunkLine is one line inside a unified-diff hunk.
// Kind: "ctx" context, "add" addition, "del" deletion.
type HunkLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Hunk is one @@ block from a unified diff for a single file.
type Hunk struct {
	File     string     `json:"file"`
	Index    int        `json:"index"`
	Header   string     `json:"header"`
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []HunkLine `json:"lines"`
}

// DiffStat returns per-file diff summaries for all uncommitted changes in worktree.
func DiffStat(ctx context.Context, r proc.Runner, worktree string) ([]FileDiff, error) {
	stOut, stErr, err := r.Run(ctx, "git", "-C", worktree, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git status: %w: %s", err, strings.TrimSpace(string(stErr)))
	}

	type fileInfo struct {
		added, removed int
		status         string
	}
	files := make(map[string]*fileInfo)

	for _, line := range strings.Split(strings.TrimRight(string(stOut), "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		if i := strings.Index(path, "\x00"); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			continue
		}
		status := statusCode(xy)
		if _, ok := files[path]; !ok {
			files[path] = &fileInfo{status: status}
		} else {
			files[path].status = status
		}
	}

	for _, extraArgs := range [][]string{
		{"diff", "--numstat"},
		{"diff", "--cached", "--numstat"},
	} {
		args := append([]string{"-C", worktree}, extraArgs...)
		out, errOut, runErr := r.Run(ctx, "git", args...)
		if runErr != nil {
			return nil, fmt.Errorf("git %v: %w: %s", extraArgs, runErr, strings.TrimSpace(string(errOut)))
		}
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			path := strings.TrimSpace(parts[2])
			a, _ := strconv.Atoi(parts[0])
			d, _ := strconv.Atoi(parts[1])
			if _, ok := files[path]; !ok {
				files[path] = &fileInfo{status: "M"}
			}
			files[path].added += a
			files[path].removed += d
		}
	}

	out := make([]FileDiff, 0, len(files))
	for path, info := range files {
		out = append(out, FileDiff{
			Path:    path,
			Added:   info.added,
			Removed: info.removed,
			Status:  info.status,
		})
	}
	return out, nil
}

func statusCode(xy string) string {
	if len(xy) < 2 {
		return "?"
	}
	x, y := rune(xy[0]), rune(xy[1])
	switch {
	case x == 'D' || y == 'D':
		return "D"
	case x == 'R' || y == 'R':
		return "R"
	case x == 'A' || y == 'A':
		return "A"
	case x == '?' && y == '?':
		return "?"
	default:
		return "M"
	}
}

// Hunks parses `git diff` unified output for one file and returns []Hunk.
func Hunks(ctx context.Context, r proc.Runner, worktree, file string) ([]Hunk, error) {
	out, errOut, err := r.Run(ctx, "git", "-C", worktree, "diff", "--unified=3", "--no-color", "--", file)
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w: %s", file, err, strings.TrimSpace(string(errOut)))
	}
	return parseUnifiedDiff(file, string(out)), nil
}

func parseUnifiedDiff(file, raw string) []Hunk {
	var hunks []Hunk
	var cur *Hunk
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			h := Hunk{File: file, Index: len(hunks), Header: line}
			parseHunkHeader(line, &h)
			cur = &h
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			cur.Lines = append(cur.Lines, HunkLine{Kind: "add", Text: line[1:]})
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			cur.Lines = append(cur.Lines, HunkLine{Kind: "del", Text: line[1:]})
		case strings.HasPrefix(line, " "):
			cur.Lines = append(cur.Lines, HunkLine{Kind: "ctx", Text: line[1:]})
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks
}

func parseHunkHeader(header string, h *Hunk) {
	inner := strings.TrimPrefix(header, "@@ ")
	if end := strings.Index(inner, " @@"); end > 0 {
		inner = inner[:end]
	}
	parts := strings.Fields(inner)
	if len(parts) >= 2 {
		h.OldStart, h.OldLines = parseRange(parts[0])
		h.NewStart, h.NewLines = parseRange(parts[1])
	}
}

func parseRange(s string) (int, int) {
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimPrefix(s, "+")
	parts := strings.SplitN(s, ",", 2)
	start, _ := strconv.Atoi(parts[0])
	if len(parts) == 1 {
		return start, 1
	}
	lines, _ := strconv.Atoi(parts[1])
	return start, lines
}

// StageHunk applies hunk `index` (0-based, relative to the CURRENT `git diff`
// output for file) to the git index via `git apply --cached`. Staging shifts the
// remaining hunks, so callers MUST re-derive hunks (re-call Hunks) after each
// StageHunk/DiscardHunk before staging another.
func StageHunk(ctx context.Context, r proc.Runner, worktree, file string, index int) error {
	return applyHunkByIndex(ctx, r, worktree, file, index, "--cached")
}

// DiscardHunk reverses hunk `index` in the worktree via `git apply --reverse`.
// The same re-derive-after-each-call precondition as StageHunk applies.
func DiscardHunk(ctx context.Context, r proc.Runner, worktree, file string, index int) error {
	return applyHunkByIndex(ctx, r, worktree, file, index, "--reverse")
}

// applyHunkByIndex re-runs `git diff` for file, slices out the index-th hunk's
// verbatim patch text (file header + that @@ block, preserving newline markers
// and real ---/+++ headers), and pipes it to `git apply <flag>`.
func applyHunkByIndex(ctx context.Context, r proc.Runner, worktree, file string, index int, flag string) error {
	if strings.ContainsAny(file, "\n\r") {
		return fmt.Errorf("git: invalid file path %q", file)
	}
	out, errOut, err := r.Run(ctx, "git", "-C", worktree, "diff", "--unified=3", "--no-color", "--", file)
	if err != nil {
		return fmt.Errorf("git diff %s: %w: %s", file, err, strings.TrimSpace(string(errOut)))
	}
	patch, err := singleHunkPatch(string(out), index)
	if err != nil {
		return err
	}
	return gitApplyPatch(ctx, worktree, patch, flag)
}

// singleHunkPatch builds a self-contained, git-apply-compatible patch for hunk
// `index` (0-based) from the raw `git diff` output of a single file: the file
// header (every line before the first "@@ ") plus the index-th "@@…@@" block,
// copied verbatim so that "\ No newline at end of file" markers and the true
// ---/+++ headers (including "+++ /dev/null" for deletions) are preserved.
func singleHunkPatch(raw string, index int) (string, error) {
	lines := strings.Split(raw, "\n")
	firstHunk := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "@@ ") {
			firstHunk = i
			break
		}
	}
	if firstHunk < 0 {
		return "", fmt.Errorf("git: no hunks in diff (requested hunk %d)", index)
	}
	var starts []int
	for i := firstHunk; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "@@ ") {
			starts = append(starts, i)
		}
	}
	if index < 0 || index >= len(starts) {
		return "", fmt.Errorf("git: hunk index %d out of range (%d hunks)", index, len(starts))
	}
	start := starts[index]
	end := len(lines)
	if index+1 < len(starts) {
		end = starts[index+1]
	}
	var sb strings.Builder
	writeLine := func(s string) {
		sb.WriteString(s)
		sb.WriteByte('\n')
	}
	for _, h := range lines[:firstHunk] {
		writeLine(h)
	}
	for i := start; i < end; i++ {
		// strings.Split on a trailing-newline-terminated diff yields a final ""
		// element; don't emit it as a spurious blank line at EOF.
		if i == len(lines)-1 && lines[i] == "" {
			continue
		}
		writeLine(lines[i])
	}
	return sb.String(), nil
}

// gitApplyPatch pipes patch into `git apply <flag>` with worktree as cwd.
func gitApplyPatch(ctx context.Context, worktree, patch, flag string) error {
	cmd := exec.CommandContext(ctx, "git", "apply", flag, "-")
	cmd.Dir = worktree
	cmd.Stdin = bytes.NewBufferString(patch)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git apply %s: %w: %s", flag, err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}
