// internal/git/hunk_regress_test.go holds regressions for the hunk audit
// findings GFS-1, GFS-2, GFS-4, GFS-5 and GFS-6.
package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "c")
}

// fortyLines returns 40 distinct lines, so git's diff alignment is unambiguous.
func fortyLines() []string {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line " + strconv.Itoa(i+1)
	}
	return lines
}

// GFS-1: the agent edits the file after the user chose a hunk to discard.
// DiscardHunkChecked must revert the hunk the user saw, never the agent's
// new hunk that now sits at the same position.
func TestDiscardHunkChecked_StaleIndexKeepsAgentWork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	d := initRepo(t)
	f := filepath.Join(d, "f.txt")
	lines := fortyLines()
	writeFile(t, f, strings.Join(lines, "\n")+"\n")
	commitAll(t, d)

	lines[30] = "USER-EDIT"
	writeFile(t, f, strings.Join(lines, "\n")+"\n")
	hs, err := git.Hunks(ctx, r, d, "f.txt")
	if err != nil || len(hs) != 1 {
		t.Fatalf("Hunks = %d, %v; want 1 hunk", len(hs), err)
	}
	seen := hs[0]

	// The agent edits line 2; its hunk becomes index 0.
	lines[1] = "AGENT-WORK"
	writeFile(t, f, strings.Join(lines, "\n")+"\n")

	if err := git.DiscardHunkChecked(ctx, r, d, "f.txt", seen.Index, seen.ID); err != nil {
		t.Fatalf("DiscardHunkChecked: %v", err)
	}
	got := readFile(t, f)
	if !strings.Contains(got, "AGENT-WORK") {
		t.Error("the agent's edit was discarded")
	}
	if strings.Contains(got, "USER-EDIT") {
		t.Error("the hunk the user discarded survived")
	}
}

// GFS-1: when the hunk the user saw no longer exists, the checked mutators
// return ErrHunkChanged and change nothing.
func TestDiscardHunkChecked_VanishedHunkIsRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	d := initRepo(t)
	f := filepath.Join(d, "f.txt")
	lines := fortyLines()
	writeFile(t, f, strings.Join(lines, "\n")+"\n")
	commitAll(t, d)

	lines[30] = "USER-EDIT"
	writeFile(t, f, strings.Join(lines, "\n")+"\n")
	hs, _ := git.Hunks(ctx, r, d, "f.txt")
	seen := hs[0]

	lines[30] = "AGENT-REWROTE-IT"
	writeFile(t, f, strings.Join(lines, "\n")+"\n")
	before := readFile(t, f)

	err := git.DiscardHunkChecked(ctx, r, d, "f.txt", seen.Index, seen.ID)
	if !errors.Is(err, git.ErrHunkChanged) {
		t.Fatalf("DiscardHunkChecked err = %v, want ErrHunkChanged", err)
	}
	if readFile(t, f) != before {
		t.Error("file changed despite the refusal")
	}
	if err := git.StageHunkChecked(ctx, r, d, "f.txt", seen.Index, seen.ID); !errors.Is(err, git.ErrHunkChanged) {
		t.Errorf("StageHunkChecked err = %v, want ErrHunkChanged", err)
	}
}

// GFS-1 + GFS-2: StageHunkChecked and UnstageHunkChecked act on the hunk
// identified by ID, and the round trip restores the original state.
func TestStageUnstageHunkChecked_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	d := initRepo(t)
	f := filepath.Join(d, "f.txt")
	lines := fortyLines()
	writeFile(t, f, strings.Join(lines, "\n")+"\n")
	commitAll(t, d)
	lines[2], lines[30] = "A", "B"
	writeFile(t, f, strings.Join(lines, "\n")+"\n")

	hs, _ := git.Hunks(ctx, r, d, "f.txt")
	if len(hs) != 2 {
		t.Fatalf("want 2 hunks, got %d", len(hs))
	}
	if err := git.StageHunkChecked(ctx, r, d, "f.txt", hs[1].Index, hs[1].ID); err != nil {
		t.Fatalf("StageHunkChecked: %v", err)
	}
	hs, _ = git.Hunks(ctx, r, d, "f.txt")
	var staged *git.Hunk
	for i := range hs {
		if hs[i].Staged {
			staged = &hs[i]
		}
	}
	if staged == nil || !hunkTouches(*staged, "B") {
		t.Fatalf("expected the B hunk staged, got %+v", hs)
	}
	if err := git.UnstageHunkChecked(ctx, r, d, "f.txt", staged.Index, staged.ID); err != nil {
		t.Fatalf("UnstageHunkChecked: %v", err)
	}
	hs, _ = git.Hunks(ctx, r, d, "f.txt")
	for _, h := range hs {
		if h.Staged {
			t.Errorf("hunk still staged after UnstageHunkChecked: %+v", h)
		}
	}
}

// GFS-2: an unstaged hunk with the same range header as a staged hunk must
// not hide the staged one, and UnstageHunk must reach it.
func TestHunks_SameHeaderStagedAndUnstaged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	d := initRepo(t)
	f := filepath.Join(d, "f.txt")
	base := "1\n2\n3\n4\nX\n6\n7\n8\n9\n"
	writeFile(t, f, base)
	commitAll(t, d)
	writeFile(t, f, strings.Replace(base, "X", "Y", 1))
	runGit(t, d, "add", "f.txt")
	writeFile(t, f, strings.Replace(base, "X", "Z", 1))

	hs, err := git.Hunks(ctx, r, d, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 2 || hs[0].Staged || !hs[1].Staged {
		t.Fatalf("want [unstaged Y->Z, staged X->Y], got %+v", hs)
	}
	if hs[0].Header != hs[1].Header {
		t.Fatalf("test premise: headers should match, got %q vs %q", hs[0].Header, hs[1].Header)
	}
	if err := unstageAt(ctx, r, d, "f.txt", 1); err != nil {
		t.Fatalf("UnstageHunk: %v", err)
	}
	hs, _ = git.Hunks(ctx, r, d, "f.txt")
	if len(hs) != 1 || hs[0].Staged {
		t.Fatalf("after unstage want one unstaged hunk, got %+v", hs)
	}
	if got := readFile(t, f); !strings.Contains(got, "Z") {
		t.Errorf("working tree changed by unstage: %q", got)
	}
}

// GFS-4: user diff config (noprefix, external diff, textconv) must not break
// Hunks or StageHunk.
func TestHunks_IgnoresUserDiffConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	d := initRepo(t)
	f := filepath.Join(d, "sub", "f.txt")
	writeFile(t, f, "1\n2\n3\n")
	commitAll(t, d)
	runGit(t, d, "config", "diff.noprefix", "true")
	runGit(t, d, "config", "diff.external", "echo")
	runGit(t, d, "config", "diff.upper.textconv", "tr a-z A-Z")
	writeFile(t, filepath.Join(d, ".gitattributes"), "*.txt diff=upper\n")
	writeFile(t, f, "1\n2b\n3\n")

	hs, err := git.Hunks(ctx, r, d, "sub/f.txt")
	if err != nil || len(hs) != 1 {
		t.Fatalf("Hunks = %d, %v; want 1 hunk", len(hs), err)
	}
	if err := git.StageHunkChecked(ctx, r, d, "sub/f.txt", 0, hs[0].ID); err != nil {
		t.Fatalf("StageHunkChecked with diff.noprefix/external/textconv: %v", err)
	}
	hs, _ = git.Hunks(ctx, r, d, "sub/f.txt")
	if len(hs) != 1 || !hs[0].Staged {
		t.Fatalf("want the hunk staged, got %+v", hs)
	}
}

// apply.whitespace must not refuse (=error) or rewrite (=fix) a staged hunk:
// the index must get exactly the working-tree content.
func TestStageHunk_IgnoresApplyWhitespaceConfig(t *testing.T) {
	for _, mode := range []string{"error", "fix"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ctx := context.Background()
			r := proc.ExecRunner{}
			d := initRepo(t)
			f := filepath.Join(d, "f.txt")
			writeFile(t, f, "1\n2\n3\n")
			commitAll(t, d)
			runGit(t, d, "config", "apply.whitespace", mode)
			writeFile(t, f, "1\n2 trailing   \n3\n")

			hs, err := git.Hunks(ctx, r, d, "f.txt")
			if err != nil || len(hs) != 1 {
				t.Fatalf("Hunks = %d, %v", len(hs), err)
			}
			if err := git.StageHunkChecked(ctx, r, d, "f.txt", 0, hs[0].ID); err != nil {
				t.Fatalf("StageHunkChecked with apply.whitespace=%s: %v", mode, err)
			}
			hs, _ = git.Hunks(ctx, r, d, "f.txt")
			if len(hs) != 1 || !hs[0].Staged {
				t.Fatalf("want exactly the staged hunk and no phantom unstaged one, got %+v", hs)
			}
		})
	}
}

// GFS-5: a file name with glob characters is a literal path, not a pattern.
func TestHunks_GlobCharactersAreLiteral(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	d := initRepo(t)
	writeFile(t, filepath.Join(d, "a[bc].txt"), "1\n2\n3\n")
	writeFile(t, filepath.Join(d, "ab.txt"), "x\ny\nz\n")
	commitAll(t, d)
	writeFile(t, filepath.Join(d, "a[bc].txt"), "1\n2b\n3\n")
	writeFile(t, filepath.Join(d, "ab.txt"), "x\ny2\nz\n")

	hs, err := git.Hunks(ctx, r, d, "a[bc].txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 1 || !hunkTouches(hs[0], "2b") {
		t.Fatalf("Hunks(a[bc].txt) = %+v; want only its own hunk", hs)
	}
	if err := stageAt(ctx, r, d, "a[bc].txt", 1); err == nil {
		t.Error("StageHunk(index 1) should be out of range for a one-hunk file")
	}
}

// GFS-6: content lines that start with "--" or "++" are real lines.
func TestHunks_KeepsDashDashAndPlusPlusLines(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	d := initRepo(t)
	f := filepath.Join(d, "q.sql")
	writeFile(t, f, "select 1;\n-- comment\nselect 2;\n")
	commitAll(t, d)
	writeFile(t, f, "select 1;\n++ added\nselect 2;\n")

	hs, err := git.Hunks(ctx, proc.ExecRunner{}, d, "q.sql")
	if err != nil || len(hs) != 1 {
		t.Fatalf("Hunks = %d, %v", len(hs), err)
	}
	var kinds []string
	for _, l := range hs[0].Lines {
		kinds = append(kinds, l.Kind+":"+l.Text)
	}
	want := "ctx:select 1;|del:-- comment|add:++ added|ctx:select 2;"
	if got := strings.Join(kinds, "|"); got != want {
		t.Errorf("lines = %s\nwant   %s", got, want)
	}
}

// Hunk.ID depends only on the hunk body, so it survives a line shift above.
func TestHunks_IDStableAcrossLineShift(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	d := initRepo(t)
	f := filepath.Join(d, "f.txt")
	lines := fortyLines()
	writeFile(t, f, strings.Join(lines, "\n")+"\n")
	commitAll(t, d)
	lines[30] = "EDIT"
	writeFile(t, f, strings.Join(lines, "\n")+"\n")
	before, _ := git.Hunks(ctx, r, d, "f.txt")

	shifted := append([]string{"new first line"}, lines...)
	writeFile(t, f, strings.Join(shifted, "\n")+"\n")
	after, _ := git.Hunks(ctx, r, d, "f.txt")
	if len(before) != 1 || len(after) != 2 {
		t.Fatalf("hunks before=%d after=%d", len(before), len(after))
	}
	if after[1].ID != before[0].ID || after[1].Header == before[0].Header {
		t.Errorf("ID should survive a line shift: before %s %q, after %s %q",
			before[0].ID, before[0].Header, after[1].ID, after[1].Header)
	}
}
