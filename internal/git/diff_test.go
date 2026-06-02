package git

import (
	"context"
	"testing"

	"github.com/Miniature-Pug/perch/internal/proc"
)

func TestDiff_BuildsArgvAndReturnsOutput(t *testing.T) {
	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: []byte("diff --git a/x b/x\n")},
		"git", "-C", "/repo", "diff", "--no-color")

	out, err := Diff(context.Background(), r, "/repo")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if out != "diff --git a/x b/x\n" {
		t.Fatalf("Diff out = %q", out)
	}
	if got := r.Calls[0]; got.Name != "git" ||
		got.Args[0] != "-C" || got.Args[1] != "/repo" ||
		got.Args[2] != "diff" || got.Args[3] != "--no-color" {
		t.Fatalf("unexpected argv: %+v", got)
	}
}

func TestDiffStat_ParsesAddedRemoved(t *testing.T) {
	r := proc.NewFakeRunner()
	// NOTE: the plan included a second r.Respond call for the same argv with
	// human-format output. FakeRunner.Respond is a map write (last-wins), so the
	// second call would silently overwrite the first — the test would still pass
	// but only by accident. The first (wrong) registration has been removed so
	// the test clearly documents the real --numstat format it expects to parse.
	r.Respond(proc.FakeResult{Stdout: []byte("3\t2\tx\n")},
		"git", "-C", "/repo", "diff", "--numstat")

	st, err := DiffStat(context.Background(), r, "/repo")
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	if st.Added != 3 || st.Removed != 2 || st.Files != 1 {
		t.Fatalf("DiffStat = %+v, want {Files:1 Added:3 Removed:2}", st)
	}
}
