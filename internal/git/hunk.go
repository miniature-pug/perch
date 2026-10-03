// internal/git/hunk.go
package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/miniature-pug/perch/internal/proc"
)

const (
	// lockRetryInitialBackoff is the wait before the first retry after a
	// mutating git command fails because another git process holds the
	// repository index lock. The wait doubles on each attempt, up to
	// lockRetryMaxBackoff.
	lockRetryInitialBackoff = 50 * time.Millisecond
	lockRetryMaxBackoff     = 800 * time.Millisecond
)

// lockRetryBudget caps the total time gitApplyPatch keeps retrying on index
// lock contention. It is long enough to outlast a typical pre-commit hook
// (git commit holds index.lock while hooks run) and well under uiGitTimeout,
// so a lock that never clears still surfaces an error. Tests shorten it.
var lockRetryBudget = 15 * time.Second

// isIndexLockContention reports whether stderr shows that a concurrent
// process holds the git index lock. This is the one condition worth
// retrying. isIndexLockContention matches the stable fragments git prints
// for lock contention ("Unable to create '<repo>/.git/index.lock': File
// exists.") instead of matching an exit code, because git shares that exit
// code across many unrelated failures.
func isIndexLockContention(stderr string) bool {
	return strings.Contains(stderr, "index.lock") &&
		(strings.Contains(stderr, "Unable to create") || strings.Contains(stderr, "File exists"))
}

// FileDiff carries the per-file summary from ChangedFiles.
// Status: "M" modified, "A" added, "D" deleted, "R" renamed, "?" untracked.
// Path is the raw path relative to the worktree root, never quoted, and can
// be passed straight back to Hunks. OldPath is set only by ChangedFiles, and
// only for a rename: it is the path before the rename.
type FileDiff struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
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
//
// ID is a content hash of the hunk body (its lines, without the @@ header).
// Pass it back to StageHunkChecked, DiscardHunkChecked or UnstageHunkChecked
// so they act on exactly the hunk the user saw.
type Hunk struct {
	File     string     `json:"file"`
	Index    int        `json:"index"`
	ID       string     `json:"id"`
	Header   string     `json:"header"`
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []HunkLine `json:"lines"`
	Staged   bool       `json:"staged"`
}

// ChangedFiles returns per-file summaries of all uncommitted changes in
// worktree, sorted by path. It costs two git processes:
//
//   - `git status --porcelain=v2 -z --branch --renames` lists the changed
//     paths. The -z form never quotes paths, so names with spaces, leading
//     spaces or non-ASCII bytes come back verbatim, and a rename is one
//     record with the new path plus OldPath.
//   - `git diff HEAD --numstat -z -M` counts lines net against HEAD, so a
//     line staged as X->Y and then edited to Z counts once, not twice. On an
//     unborn HEAD there is nothing to diff against, so ChangedFiles counts
//     the staged and unstaged diffs instead.
//
// Untracked files report 0/0, and an untracked directory is one entry
// ending in "/", as in `git status`.
func ChangedFiles(ctx context.Context, r proc.Runner, worktree string) ([]FileDiff, error) {
	stOut, stErr, err := r.Run(ctx, "git", "-C", worktree, "status", "--porcelain=v2", "-z", "--branch", "--renames")
	if err != nil {
		return nil, fmt.Errorf("git status: %w: %s", err, strings.TrimSpace(string(stErr)))
	}
	files := make(map[string]*FileDiff)
	unborn := false
	recs := strings.Split(string(stOut), "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if rec == "" {
			continue
		}
		var path, oldPath, xy string
		switch rec[0] {
		case '#':
			if rec == "# branch.oid (initial)" {
				unborn = true
			}
			continue
		case '1': // 1 XY sub mH mI mW hH hI path
			f := strings.SplitN(rec, " ", 9)
			if len(f) < 9 {
				continue
			}
			xy, path = f[1], f[8]
		case '2': // 2 XY sub mH mI mW hH hI Xscore path NUL origPath
			f := strings.SplitN(rec, " ", 10)
			if len(f) < 10 {
				continue
			}
			xy, path = f[1], f[9]
			if i+1 < len(recs) {
				oldPath = recs[i+1]
				i++
			}
		case 'u': // u XY sub m1 m2 m3 mW h1 h2 h3 path
			f := strings.SplitN(rec, " ", 11)
			if len(f) < 11 {
				continue
			}
			xy, path = f[1], f[10]
		case '?':
			xy, path = "??", strings.TrimPrefix(rec, "? ")
		default: // '!' ignored, or a record type this parser does not know.
			continue
		}
		files[path] = &FileDiff{Path: path, OldPath: oldPath, Status: statusCode(strings.ReplaceAll(xy, ".", " "))}
	}

	numstats := [][]string{{"diff", "HEAD", "--numstat", "-z", "-M"}}
	if unborn {
		numstats = [][]string{{"diff", "--cached", "--numstat", "-z"}, {"diff", "--numstat", "-z"}}
	}
	for _, extra := range numstats {
		out, errOut, runErr := r.Run(ctx, "git", append([]string{"-C", worktree}, extra...)...)
		if runErr != nil {
			return nil, fmt.Errorf("git %v: %w: %s", extra, runErr, strings.TrimSpace(string(errOut)))
		}
		addNumstatZ(files, string(out))
	}

	result := make([]FileDiff, 0, len(files))
	for _, f := range files {
		result = append(result, *f)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

// addNumstatZ adds the counts from `git diff --numstat -z` output to files.
// Each record is "added<TAB>removed<TAB>path<NUL>", or, for a rename,
// "added<TAB>removed<TAB><NUL>old<NUL>new<NUL>". A binary file reports "-"
// counts, which add 0.
func addNumstatZ(files map[string]*FileDiff, out string) {
	toks := strings.Split(out, "\x00")
	for i := 0; i < len(toks); i++ {
		parts := strings.SplitN(toks[i], "\t", 3)
		if len(parts) < 3 {
			continue
		}
		path, oldPath := parts[2], ""
		if path == "" && i+2 < len(toks) {
			oldPath, path = toks[i+1], toks[i+2]
			i += 2
		}
		a, _ := strconv.Atoi(parts[0])
		d, _ := strconv.Atoi(parts[1])
		f, ok := files[path]
		if !ok {
			f = &FileDiff{Path: path, Status: "M"}
			files[path] = f
		}
		if oldPath != "" && f.OldPath == "" {
			f.OldPath = oldPath
			f.Status = "R"
		}
		f.Added += a
		f.Removed += d
	}
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

// ErrHunkChanged is returned (wrapped) by StageHunkChecked,
// DiscardHunkChecked and UnstageHunkChecked when the hunk the caller saw is
// no longer in the current diff, for example because the agent edited the
// file in the meantime. The caller should re-read the hunks and let the user
// retry. The checked mutators never fall back to a bare position, so a stale
// view can never stage or discard a different change.
var ErrHunkChanged = errors.New("git: hunk changed since it was displayed; refresh and retry")

// fileDiffArgs returns the argv for a single-file `git diff` whose output
// perch can parse and feed back to `git apply`, whatever the user's diff
// configuration says:
//
//   - --no-ext-diff and --no-textconv: diff.external or a textconv
//     attribute would otherwise replace the unified diff with output that
//     is not a patch.
//   - --src-prefix/--dst-prefix: diff.noprefix or diff.mnemonicPrefix would
//     otherwise change the a/ and b/ prefixes that `git apply` strips.
//   - :(literal): file is a path, never a glob or other pathspec magic. A
//     name such as "app/[id]/page.tsx" would otherwise also match other files.
func fileDiffArgs(worktree, file string, cached bool) []string {
	args := []string{"-C", worktree, "diff"}
	if cached {
		args = append(args, "--cached")
	}
	return append(args, "--unified=3", "--no-color", "--no-ext-diff", "--no-textconv",
		"--src-prefix=a/", "--dst-prefix=b/", "--", ":(literal)"+file)
}

// readFileDiff runs the working-tree (cached=false) or index (cached=true)
// diff for file and splits it into hunk blocks.
func readFileDiff(ctx context.Context, r proc.Runner, worktree, file string, cached bool) ([]diffBlock, error) {
	out, errOut, err := r.Run(ctx, "git", fileDiffArgs(worktree, file, cached)...)
	if err != nil {
		label := "git diff"
		if cached {
			label = "git diff --cached"
		}
		return nil, fmt.Errorf("%s %s: %w: %s", label, file, err, strings.TrimSpace(string(errOut)))
	}
	return splitDiff(string(out)), nil
}

func validateHunkFile(file string) error {
	if strings.ContainsAny(file, "\n\r") {
		return fmt.Errorf("git: invalid file path %q", file)
	}
	return nil
}

// Hunks returns unified-diff hunks for file: first the working-tree
// (unstaged) hunks, then the staged hunks, with Staged set. A file that
// appears in neither diff returns an empty slice.
//
// The unstaged diff (index to working tree) and the staged diff (HEAD to
// index) describe disjoint changes, so Hunks reports every hunk of both,
// even when an unstaged and a staged hunk share a range header (an edit to a
// line that is already staged produces exactly that).
//
// NOTE: this function only READS hunks. Hunk.ID identifies a hunk by
// content for the mutators: StageHunkChecked and DiscardHunkChecked act on
// working-tree hunks (Staged==false), UnstageHunkChecked on staged hunks
// (Staged==true). The merged Hunk.Index only breaks ties between identical
// hunks.
func Hunks(ctx context.Context, r proc.Runner, worktree, file string) ([]Hunk, error) {
	wt, err := readFileDiff(ctx, r, worktree, file, false)
	if err != nil {
		return nil, err
	}
	st, err := readFileDiff(ctx, r, worktree, file, true)
	if err != nil {
		return nil, err
	}
	hunks := make([]Hunk, 0, len(wt)+len(st))
	for _, b := range wt {
		hunks = append(hunks, b.hunk(file, len(hunks), false))
	}
	for _, b := range st {
		hunks = append(hunks, b.hunk(file, len(hunks), true))
	}
	return hunks, nil
}

// diffBlock is one "@@" hunk of a raw git diff, kept verbatim so it can be
// fed back to `git apply`.
type diffBlock struct {
	// fileHeader is the "diff --git" section header this hunk belongs to
	// (every line from "diff --git" up to the first "@@").
	fileHeader []string
	// header is the "@@ -a,b +c,d @@ ..." line.
	header string
	// body is every line after header, up to the next hunk or section,
	// including "\ No newline at end of file" markers.
	body []string
}

// id is a content hash of the hunk body. It ignores the header, so it stays
// stable when an edit elsewhere in the file shifts the hunk's line numbers.
func (b diffBlock) id() string {
	sum := sha256.Sum256([]byte(strings.Join(b.body, "\n")))
	return hex.EncodeToString(sum[:16])
}

func (b diffBlock) hunk(file string, index int, staged bool) Hunk {
	h := Hunk{File: file, Index: index, ID: b.id(), Header: b.header, Staged: staged}
	parseHunkHeader(b.header, &h)
	for _, line := range b.body {
		// Inside a hunk the first byte alone classifies a line. A deleted
		// line "-- comment" is emitted as "--- comment", so a prefix check for
		// "---" would drop real content.
		switch {
		case strings.HasPrefix(line, "+"):
			h.Lines = append(h.Lines, HunkLine{Kind: "add", Text: line[1:]})
		case strings.HasPrefix(line, "-"):
			h.Lines = append(h.Lines, HunkLine{Kind: "del", Text: line[1:]})
		case strings.HasPrefix(line, " "):
			h.Lines = append(h.Lines, HunkLine{Kind: "ctx", Text: line[1:]})
		case line == "":
			// diff.suppressBlankEmpty prints an empty context line bare.
			h.Lines = append(h.Lines, HunkLine{Kind: "ctx"})
		}
		// "\ No newline at end of file" is a marker, not a line.
	}
	return h
}

// patch returns a self-contained, git-apply-compatible patch for this
// hunk: its own section header plus the verbatim block. This preserves
// "\ No newline at end of file" markers and the true ---/+++ headers,
// including "+++ /dev/null" for deletions.
func (b diffBlock) patch() string {
	var sb strings.Builder
	for _, l := range b.fileHeader {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	sb.WriteString(b.header)
	sb.WriteByte('\n')
	for _, l := range b.body {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// splitDiff splits raw `git diff` output into hunk blocks. Each block
// carries the header of the "diff --git" section it came from, so a diff
// with several sections (a type change prints a deletion and an addition
// for the same path) never pairs a hunk with the wrong header.
func splitDiff(raw string) []diffBlock {
	lines := strings.Split(raw, "\n")
	// A newline-terminated diff ends in an empty element that is not a line.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var blocks []diffBlock
	var section []string
	inHunks := false // true once the current section reached its first "@@"
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			section = []string{line}
			inHunks = false
		case strings.HasPrefix(line, "@@ "):
			inHunks = true
			blocks = append(blocks, diffBlock{fileHeader: section, header: line})
		case inHunks:
			last := &blocks[len(blocks)-1]
			last.body = append(last.body, line)
		case section != nil:
			section = append(section, line)
		}
	}
	return blocks
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

// StageHunkChecked stages the working-tree hunk whose content hash is id
// (Hunk.ID from Hunks). index is the Hunk.Index the caller saw; it is used
// only to choose between identical hunks. If no current hunk has that
// content, StageHunkChecked returns an error wrapping ErrHunkChanged and
// changes nothing.
func StageHunkChecked(ctx context.Context, r proc.Runner, worktree, file string, index int, id string) error {
	if id == "" {
		return fmt.Errorf("git: StageHunkChecked: empty hunk id")
	}
	return applyWorktreeHunk(ctx, r, worktree, file, index, id, "--cached")
}

// DiscardHunkChecked reverts the working-tree hunk whose content hash is id
// (Hunk.ID from Hunks), with the same matching rules as StageHunkChecked. It
// never reverts a hunk the caller did not see.
func DiscardHunkChecked(ctx context.Context, r proc.Runner, worktree, file string, index int, id string) error {
	if id == "" {
		return fmt.Errorf("git: DiscardHunkChecked: empty hunk id")
	}
	return applyWorktreeHunk(ctx, r, worktree, file, index, id, "--reverse")
}

// UnstageHunkChecked unstages the staged hunk whose content hash is id
// (Hunk.ID from Hunks), with the same matching rules as StageHunkChecked.
// index is the merged Hunk.Index the caller saw.
func UnstageHunkChecked(ctx context.Context, r proc.Runner, worktree, file string, index int, id string) error {
	if id == "" {
		return fmt.Errorf("git: UnstageHunkChecked: empty hunk id")
	}
	if err := validateHunkFile(file); err != nil {
		return err
	}
	st, err := readFileDiff(ctx, r, worktree, file, true)
	if err != nil {
		return err
	}
	b, err := findBlock(st, id, func() int {
		// Only needed to break a tie between identical staged hunks.
		wt, werr := readFileDiff(ctx, r, worktree, file, false)
		if werr != nil {
			return -1
		}
		return index - len(wt)
	})
	if err != nil {
		return err
	}
	return gitApplyPatch(ctx, r, worktree, b.patch(), "--reverse", "--cached")
}

// findBlock returns the block whose content hash is id. When several blocks
// share that content, hint() names the position the caller saw, and that
// block is used only if it matches too. findBlock never falls back to a
// position whose content differs.
func findBlock(blocks []diffBlock, id string, hint func() int) (diffBlock, error) {
	var matches []int
	for i, b := range blocks {
		if b.id() == id {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		return diffBlock{}, fmt.Errorf("git: hunk %s: %w", id, ErrHunkChanged)
	case 1:
		return blocks[matches[0]], nil
	}
	if pos := hint(); pos >= 0 && pos < len(blocks) && blocks[pos].id() == id {
		return blocks[pos], nil
	}
	return diffBlock{}, fmt.Errorf("git: hunk %s is ambiguous: %w", id, ErrHunkChanged)
}

// applyWorktreeHunk re-runs `git diff` for file, selects the working-tree
// hunk whose content hash is id (see findBlock), and pipes its verbatim
// patch to `git apply <flag>`.
func applyWorktreeHunk(ctx context.Context, r proc.Runner, worktree, file string, index int, id, flag string) error {
	if err := validateHunkFile(file); err != nil {
		return err
	}
	blocks, err := readFileDiff(ctx, r, worktree, file, false)
	if err != nil {
		return err
	}
	b, err := findBlock(blocks, id, func() int { return index })
	if err != nil {
		return err
	}
	return gitApplyPatch(ctx, r, worktree, b.patch(), flag)
}

// gitApplyPatch pipes patch into `git apply <flags...> -` through the
// runner. This keeps the call visible to FakeRunner in tests, and it obeys
// the runner's context and timeout controls. flags carries the apply mode:
// "--cached" for stage, "--reverse" for discard, or "--reverse --cached" for
// unstage. Unstage reverses the index entry only and leaves the working
// tree untouched.
//
// The apply takes the repository index lock. When the agent and the UI
// touch the same worktree at the same time, git can fail with "Unable to
// create '.git/index.lock': File exists." The holder usually clears the lock
// quickly, but `git commit` holds it for its whole pre-commit hook run, which
// can take many seconds. Instead of surfacing that as a hard error,
// gitApplyPatch retries with capped exponential backoff, only for lock
// contention, for up to lockRetryBudget in total. Every other failure
// returns immediately. The budget stays well under uiGitTimeout, and
// gitApplyPatch honors ctx cancellation.
func gitApplyPatch(ctx context.Context, r proc.Runner, worktree, patch string, flags ...string) error {
	// git apply <flags...> -. gitApplyPatch builds args once and reuses them across lock retries.
	args := make([]string, 0, len(flags)+3)
	// --whitespace=nowarn: the patch is git's own diff of the user's content.
	// apply.whitespace=error would refuse it, and apply.whitespace=fix would
	// stage content that differs from the working tree (trailing spaces
	// stripped), leaving a phantom unstaged hunk.
	args = append(args, "apply", "--whitespace=nowarn")
	args = append(args, flags...)
	args = append(args, "-")
	label := strings.Join(flags, " ")

	deadline := time.Now().Add(lockRetryBudget)
	backoff := lockRetryInitialBackoff
	for {
		_, errOut, err := r.RunStdin(ctx, worktree, []byte(patch), "git", args...)
		if err == nil {
			return nil
		}
		stderr := strings.TrimSpace(string(errOut))
		if !isIndexLockContention(stderr) || time.Now().Add(backoff).After(deadline) {
			return fmt.Errorf("git apply %s: %w: %s", label, err, stderr)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("git apply %s: %w: %s", label, ctx.Err(), stderr)
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > lockRetryMaxBackoff {
			backoff = lockRetryMaxBackoff
		}
	}
}
