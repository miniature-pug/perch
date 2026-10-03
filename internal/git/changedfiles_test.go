package git_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miniature-pug/perch/internal/git"
	"github.com/miniature-pug/perch/internal/proc"
)

// GFS-3: renames, quoted paths and leading spaces each produce exactly one
// entry, with a raw path that Hunks accepts.
func TestChangedFiles_RenamesAndUnusualPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	r := proc.ExecRunner{}
	d := initRepo(t)
	writeFile(t, filepath.Join(d, "old.txt"), "a\nb\nc\n")
	writeFile(t, filepath.Join(d, "sp ace.txt"), "x\n")
	writeFile(t, filepath.Join(d, "é.txt"), "x\n")
	writeFile(t, filepath.Join(d, " lead.txt"), "x\n")
	commitAll(t, d)
	runGit(t, d, "mv", "old.txt", "new.txt")
	writeFile(t, filepath.Join(d, "sp ace.txt"), "y\n")
	writeFile(t, filepath.Join(d, "é.txt"), "y\n")
	writeFile(t, filepath.Join(d, " lead.txt"), "y\n")
	writeFile(t, filepath.Join(d, "untracked.txt"), "u\n")

	fds, err := git.ChangedFiles(ctx, r, d)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fds {
		got = append(got, f.Status+" "+f.Path+"<-"+f.OldPath)
	}
	want := []string{
		"M  lead.txt<-",
		"R new.txt<-old.txt",
		"M sp ace.txt<-",
		"? untracked.txt<-",
		"M é.txt<-",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("ChangedFiles =\n  %q\nwant\n  %q", got, want)
	}
	for _, f := range fds {
		if f.Status != "M" {
			continue
		}
		if f.Added != 1 || f.Removed != 1 {
			t.Errorf("%q: +%d -%d, want +1 -1", f.Path, f.Added, f.Removed)
		}
		hs, err := git.Hunks(ctx, r, d, f.Path)
		if err != nil || len(hs) != 1 {
			t.Errorf("Hunks(%q) = %d, %v; want 1 hunk", f.Path, len(hs), err)
		}
	}
}

// GFS-15: a line staged as X->Y and then edited to Z counts once, net
// against HEAD.
func TestChangedFiles_NetCountsAgainstHead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	d := initRepo(t)
	f := filepath.Join(d, "f.txt")
	base := "1\n2\nX\n4\n"
	writeFile(t, f, base)
	commitAll(t, d)
	writeFile(t, f, strings.Replace(base, "X", "Y", 1))
	runGit(t, d, "add", "f.txt")
	writeFile(t, f, strings.Replace(base, "X", "Z", 1))

	fds, err := git.ChangedFiles(ctx, proc.ExecRunner{}, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(fds) != 1 || fds[0].Added != 1 || fds[0].Removed != 1 {
		t.Fatalf("ChangedFiles = %+v, want one file +1 -1", fds)
	}
}

// An unborn HEAD has nothing to diff against; staged files still count.
func TestChangedFiles_UnbornHead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := t.TempDir()
	runGit(t, d, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(d, "a.txt"), "1\n2\n")
	runGit(t, d, "add", "a.txt")

	fds, err := git.ChangedFiles(context.Background(), proc.ExecRunner{}, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(fds) != 1 || fds[0].Path != "a.txt" || fds[0].Status != "A" || fds[0].Added != 2 {
		t.Fatalf("ChangedFiles = %+v, want a.txt A +2", fds)
	}
}

// The parser handles every porcelain v2 record shape without a real repo.
func TestChangedFiles_ParsesPorcelainV2(t *testing.T) {
	r := proc.NewFakeRunner()
	status := "# branch.oid abc\x00# branch.head main\x00" +
		"1 .M N... 100644 100644 100644 h1 h2 dir/a b.txt\x00" +
		"2 R. N... 100644 100644 100644 h1 h2 R100 to.txt\x00from.txt\x00" +
		"u UU N... 100644 100644 100644 100644 h1 h2 h3 conflict.txt\x00" +
		"? new dir/\x00" +
		"! ignored.log\x00"
	r.Respond(proc.FakeResult{Stdout: []byte(status)},
		"git", "-C", "/wt", "status", "--porcelain=v2", "-z", "--branch", "--renames")
	numstat := "3\t1\tdir/a b.txt\x00" + "0\t0\t\x00from.txt\x00to.txt\x00" + "-\t-\tconflict.txt\x00"
	r.Respond(proc.FakeResult{Stdout: []byte(numstat)},
		"git", "-C", "/wt", "diff", "HEAD", "--numstat", "-z", "-M")

	fds, err := git.ChangedFiles(context.Background(), r, "/wt")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fds {
		got = append(got, f.Status+"|"+f.Path+"|"+f.OldPath)
	}
	want := []string{"M|conflict.txt|", "M|dir/a b.txt|", "?|new dir/|", "R|to.txt|from.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if fds[1].Added != 3 || fds[1].Removed != 1 {
		t.Errorf("dir/a b.txt counts = +%d -%d, want +3 -1", fds[1].Added, fds[1].Removed)
	}
}
