package attach

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// makeCandidate builds a live Candidate for testing.
func makeCandidate(project, branch, tool, sessName, winName string) Candidate {
	return Candidate{
		Project:     project,
		Branch:      branch,
		Tool:        tool,
		TmuxSession: sessName,
		TmuxWindow:  winName,
		LiveTarget:  "=" + sessName + ":=" + winName,
		IsLive:      true,
	}
}

// ── Resolve tests ─────────────────────────────────────────────────────────────

func TestResolve_NoQuery_NoCandidates(t *testing.T) {
	res := Resolve("anything", nil)
	if res.Count != 0 {
		t.Errorf("expected 0 matches for empty candidates, got %d", res.Count)
	}
	if res.Matched != nil {
		t.Error("Matched should be nil for 0 matches")
	}
}

func TestResolve_ZeroMatches(t *testing.T) {
	cands := []Candidate{
		makeCandidate("myproject", "main", "claude", "myproject", "main"),
	}
	res := Resolve("zzzzz-nomatch-zzzzz", cands)
	if res.Count != 0 {
		t.Errorf("expected 0 matches, got %d", res.Count)
	}
	if res.Matched != nil {
		t.Error("Matched should be nil for 0 matches")
	}
	if len(res.Ambiguous) != 0 {
		t.Error("Ambiguous should be empty for 0 matches")
	}
}

func TestResolve_OneMatch(t *testing.T) {
	cands := []Candidate{
		makeCandidate("myproject", "main", "claude", "myproject", "main"),
		makeCandidate("otherrepo", "develop", "opencode", "otherrepo", "develop"),
	}
	res := Resolve("myproject", cands)
	if res.Count != 1 {
		t.Fatalf("expected 1 match, got %d", res.Count)
	}
	if res.Matched == nil {
		t.Fatal("Matched should not be nil for 1 match")
	}
	if res.Matched.Project != "myproject" {
		t.Errorf("Matched.Project = %q, want myproject", res.Matched.Project)
	}
	if len(res.Ambiguous) != 0 {
		t.Error("Ambiguous must be empty for a single match")
	}
}

func TestResolve_AmbiguousMatches(t *testing.T) {
	cands := []Candidate{
		makeCandidate("myproject", "main", "claude", "myproject", "main"),
		makeCandidate("myproject", "feat/foo", "claude", "myproject", "feat-foo"),
	}
	// "myproject" matches both.
	res := Resolve("myproject", cands)
	if res.Count < 2 {
		t.Fatalf("expected 2+ matches for ambiguous query, got %d", res.Count)
	}
	if res.Matched != nil {
		t.Error("Matched must be nil for ambiguous result")
	}
	if len(res.Ambiguous) < 2 {
		t.Errorf("Ambiguous should contain >=2 candidates; got %d", len(res.Ambiguous))
	}
}

func TestResolve_MatchByBranch(t *testing.T) {
	cands := []Candidate{
		makeCandidate("myproject", "main", "claude", "myproject", "main"),
		makeCandidate("myproject", "feat/awesome", "claude", "myproject", "feat-awesome"),
	}
	res := Resolve("feat/awesome", cands)
	if res.Count != 1 {
		t.Fatalf("expected 1 match by branch, got %d", res.Count)
	}
	if res.Matched.Branch != "feat/awesome" {
		t.Errorf("Matched.Branch = %q, want feat/awesome", res.Matched.Branch)
	}
}

func TestResolve_MatchByTool(t *testing.T) {
	cands := []Candidate{
		makeCandidate("proj", "main", "claude", "proj", "main"),
		makeCandidate("proj", "main", "opencode", "proj", "main"),
	}
	// "opencode" uniquely identifies the second candidate.
	res := Resolve("opencode", cands)
	if res.Count != 1 {
		t.Fatalf("expected 1 match by tool, got %d", res.Count)
	}
	if res.Matched.Tool != "opencode" {
		t.Errorf("Matched.Tool = %q, want opencode", res.Matched.Tool)
	}
}

// TestResolve_ArgvSafety checks that a query string that looks like a tmux flag
// ("--foo" or "-X") is only used for fuzzy matching, never as a target string.
// The matched candidate's tmux target is derived from TmuxSession/TmuxWindow,
// not from the query itself.
func TestResolve_ArgvSafety(t *testing.T) {
	// A candidate that matches "-X" because its MatchString contains "X".
	cand := makeCandidate("X-project", "main", "claude", "X-project", "main")
	cands := []Candidate{cand}

	res := Resolve("-X", cands)
	if res.Count == 0 {
		// Fuzzy may not match — that's fine, proves safety already (no inject).
		return
	}
	if res.Matched == nil {
		return
	}
	// The tmux target must NOT be "-X" or contain the raw query.
	if res.Matched.LiveTarget == "-X" {
		t.Errorf("LiveTarget must not equal the raw query flag; got %q", res.Matched.LiveTarget)
	}
	// The target must be a properly anchored tmux target (starts with '=').
	if len(res.Matched.LiveTarget) > 0 && res.Matched.LiveTarget[0] != '=' {
		t.Errorf("LiveTarget must start with '=' (anchored tmux target); got %q", res.Matched.LiveTarget)
	}
}

// TestResolve_DashDashFlagQuery ensures a "--foo" query is used only for fuzzy
// matching and the resolved target is always a validated session name.
func TestResolve_DashDashFlagQuery(t *testing.T) {
	// "foo" as project name: "--foo" might fuzzy-match it.
	cand := makeCandidate("foo", "main", "claude", "foo", "main")
	res := Resolve("--foo", []Candidate{cand})

	if res.Count > 0 && res.Matched != nil {
		// The resolved live target must be "=foo:=main", not "--foo".
		want := "=foo:=main"
		if res.Matched.LiveTarget != want {
			t.Errorf("LiveTarget = %q, want %q", res.Matched.LiveTarget, want)
		}
	}
	// Either 0 or 1 match — never panic, never use query as target.
}

// ── MatchString test ──────────────────────────────────────────────────────────

func TestCandidate_MatchString(t *testing.T) {
	c := makeCandidate("proj", "main", "claude", "proj", "main")
	got := c.MatchString()
	want := "proj main claude"
	if got != want {
		t.Errorf("MatchString() = %q, want %q", got, want)
	}
}

// ── buildLiveIndex tests ───────────────────────────────────────────────────────

func TestBuildLiveIndex_Empty(t *testing.T) {
	idx := buildLiveIndex(nil)
	if len(idx) != 0 {
		t.Errorf("expected empty index for nil panes, got %d", len(idx))
	}
}

func TestBuildLiveIndex_SkipsDead(t *testing.T) {
	panes := []tmux.Pane{
		{ID: "%1", PerchSession: "ses_abc", Dead: true},
		{ID: "%2", PerchSession: "ses_xyz", Dead: false},
	}
	idx := buildLiveIndex(panes)
	if _, ok := idx["ses_abc"]; ok {
		t.Error("dead pane should be excluded from live index")
	}
	if _, ok := idx["ses_xyz"]; !ok {
		t.Error("live pane with valid PerchSession should be in index")
	}
}

func TestBuildLiveIndex_SkipsEmptySession(t *testing.T) {
	panes := []tmux.Pane{
		{ID: "%1", PerchSession: "", Dead: false},
	}
	idx := buildLiveIndex(panes)
	if len(idx) != 0 {
		t.Errorf("expected empty index for pane with empty PerchSession, got %d", len(idx))
	}
}

func TestBuildLiveIndex_FirstWriteWins(t *testing.T) {
	panes := []tmux.Pane{
		{ID: "%1", PerchSession: "ses_dup", Dead: false, Session: "first"},
		{ID: "%2", PerchSession: "ses_dup", Dead: false, Session: "second"},
	}
	idx := buildLiveIndex(panes)
	if len(idx) != 1 {
		t.Errorf("expected 1 entry for duplicate PerchSession, got %d", len(idx))
	}
	if idx["ses_dup"].Session != "first" {
		t.Errorf("expected first pane to win; got session=%q", idx["ses_dup"].Session)
	}
}

// ── FormatAmbiguous ───────────────────────────────────────────────────────────

func TestFormatAmbiguous(t *testing.T) {
	cands := []Candidate{
		makeCandidate("proj-a", "main", "claude", "proj-a", "main"),
		makeCandidate("proj-b", "feat", "opencode", "proj-b", "feat"),
	}
	got := FormatAmbiguous(cands)
	if got == "" {
		t.Error("FormatAmbiguous must not return empty string")
	}
	for _, c := range cands {
		if !contains(got, c.Project) {
			t.Errorf("FormatAmbiguous output missing project %q", c.Project)
		}
		if !contains(got, c.Tool) {
			t.Errorf("FormatAmbiguous output missing tool %q", c.Tool)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub)))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── buildCandidate tests ───────────────────────────────────────────────────────

func TestBuildCandidate_LiveSession(t *testing.T) {
	pane := tmux.Pane{ID: "%1", Session: "proj", Window: "main"}
	liveBySession := map[string]tmux.Pane{"ses_abc": pane}

	s := model.Session{ID: "ses_abc", Tool: model.ToolClaude}
	c := buildCandidate(s, "proj", "main", "proj", "main", liveBySession)

	if !c.IsLive {
		t.Error("expected candidate to be live")
	}
	if c.LiveTarget == "" {
		t.Error("expected non-empty LiveTarget for live candidate")
	}
	if c.Project != "proj" {
		t.Errorf("Project = %q, want proj", c.Project)
	}
	if c.Tool != "claude" {
		t.Errorf("Tool = %q, want claude", c.Tool)
	}
}

func TestBuildCandidate_IdleSession(t *testing.T) {
	liveBySession := map[string]tmux.Pane{} // empty: no live panes

	s := model.Session{ID: "ses_xyz", Tool: model.ToolOpencode}
	c := buildCandidate(s, "proj", "feat", "proj", "feat", liveBySession)

	if c.IsLive {
		t.Error("expected candidate to be idle (not live)")
	}
	if c.LiveTarget != "" {
		t.Errorf("expected empty LiveTarget for idle candidate, got %q", c.LiveTarget)
	}
}

// ── Gather tests ───────────────────────────────────────────────────────────────

// TestGather_NoLiveSessions verifies that Gather returns no candidates when
// there are no live tmux panes, even if projects are discovered.
func TestGather_NoLiveSessions(t *testing.T) {
	// Sandbox HOME so Claude.ListSessions sees no sessions.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	root := t.TempDir() // empty dir → no git repos → no candidates

	r := proc.NewFakeRunner()
	// list-panes -a → no panes
	r.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F",
		"#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}")

	deps := Deps{
		Tmux:    tmux.Tmux{Runner: r, Bin: "tmux"},
		Runner:  r,
		Claude:  agent.Claude{}, // HOME→temp: no sessions
		Root:    root,
		BaseDir: t.TempDir(),
		Now:     time.Now().Unix(),
	}

	cands, err := Gather(context.Background(), deps)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("expected 0 candidates with no live panes, got %d", len(cands))
	}
}

// TestGather_InvalidRoot verifies that Gather returns an error for a
// non-existent root directory.
func TestGather_InvalidRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	r := proc.NewFakeRunner()
	deps := Deps{
		Tmux:    tmux.Tmux{Runner: r, Bin: "tmux"},
		Runner:  r,
		Claude:  agent.Claude{},
		Root:    "/this-does-not-exist-at-all-perch-test",
		BaseDir: t.TempDir(),
		Now:     time.Now().Unix(),
	}
	_, err := Gather(context.Background(), deps)
	if err == nil {
		t.Error("expected error for non-existent root, got nil")
	}
}

// makeGitWorktreeRepo creates a minimal git repo under root/reponame and
// returns the repo path. The repo has a real .git directory with HEAD so that
// git.ListWorktrees can discover it via "git worktree list".
func makeGitWorktreeRepo(t *testing.T, root, repoName string) string {
	t.Helper()
	repoDir := filepath.Join(root, repoName)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", repoDir, err)
	}
	gitDir := filepath.Join(repoDir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n\trepositoryformatversion = 0\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return repoDir
}

// makeClaudeHome creates a minimal Claude home directory with one session
// rooted at repoPath. Returns the Claude Home path.
func makeClaudeHome(t *testing.T, repoPath string) (claudeHome string, sessionID string) {
	t.Helper()
	claudeHome = t.TempDir()
	sessionID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	// Slug is derived by encoding the path with '-' as separator.
	// Claude uses the path with leading '/' replaced: /home/user/repo → -home-user-repo.
	slug := encodePath(repoPath)

	slugDir := filepath.Join(claudeHome, "projects", slug)
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatalf("mkdir slugdir: %v", err)
	}

	// Write a minimal JSONL transcript.
	jsonl := `{"type":"user","sessionId":"` + sessionID + `","cwd":"` + repoPath + `","gitBranch":"main","message":{"role":"user","content":"hi"},"timestamp":"2026-05-30T06:55:12.501Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(slugDir, sessionID+".jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return claudeHome, sessionID
}

// encodePath converts an absolute path to a Claude slug: replace leading '/'
// with '-' and every subsequent '/' with '-'.
func encodePath(p string) string {
	result := make([]byte, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == '/' || p[i] == os.PathSeparator {
			result[i] = '-'
		} else {
			result[i] = p[i]
		}
	}
	return string(result)
}

// paneFormatStr is the pane format expected by ListPanesAll.
const paneFormatStr = "#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}\x1f#{@perch_pane_status}"

// TestGather_LiveSession verifies that Gather returns a live candidate when a
// Claude session has a matching live pane (PerchSession == sessionID).
func TestGather_LiveSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	// Set up a temp filesystem root with one git repo.
	root := t.TempDir()
	repoDir := makeGitWorktreeRepo(t, root, "myrepo")

	// Set up a Claude home with one session rooted at repoDir.
	claudeHome, sessionID := makeClaudeHome(t, repoDir)

	r := proc.NewFakeRunner()

	// git worktree list --porcelain → main worktree only.
	worktreeOut := "worktree " + repoDir + "\nHEAD abc1234\nbranch refs/heads/main\n\n"
	r.Respond(proc.FakeResult{Stdout: []byte(worktreeOut)},
		"git", "-C", repoDir, "worktree", "list", "--porcelain")

	// list-panes -a → one live pane with the session ID.
	paneOut := "%1\x1f1234\x1fclaude\x1f0\x1f" + repoDir + "\x1fmyrepo\x1fmain\x1f" + sessionID + "\x1f\n"
	r.Respond(proc.FakeResult{Stdout: []byte(paneOut)},
		"tmux", "list-panes", "-a", "-F", paneFormatStr)

	deps := Deps{
		Tmux:    tmux.Tmux{Runner: r, Bin: "tmux"},
		Runner:  r,
		Claude:  agent.Claude{Home: claudeHome},
		Root:    root,
		BaseDir: t.TempDir(),
		Now:     time.Now().Unix(),
	}

	cands, err := Gather(context.Background(), deps)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 live candidate, got %d", len(cands))
	}
	c := cands[0]
	if c.Project != "myrepo" {
		t.Errorf("Project = %q, want myrepo", c.Project)
	}
	if c.Branch != "main" {
		t.Errorf("Branch = %q, want main", c.Branch)
	}
	if c.Tool != "claude" {
		t.Errorf("Tool = %q, want claude", c.Tool)
	}
	if !c.IsLive {
		t.Error("expected candidate to be live")
	}
	if c.LiveTarget == "" {
		t.Error("expected non-empty LiveTarget")
	}
}

// TestGather_IdleSession verifies that Gather returns no candidates when the
// Claude session has no matching live pane (session is known but idle).
func TestGather_IdleSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	root := t.TempDir()
	repoDir := makeGitWorktreeRepo(t, root, "idlerepo")
	claudeHome, _ := makeClaudeHome(t, repoDir)

	r := proc.NewFakeRunner()

	// git worktree list --porcelain
	worktreeOut := "worktree " + repoDir + "\nHEAD abc1234\nbranch refs/heads/main\n\n"
	r.Respond(proc.FakeResult{Stdout: []byte(worktreeOut)},
		"git", "-C", repoDir, "worktree", "list", "--porcelain")

	// list-panes -a → no panes (empty server)
	r.Respond(proc.FakeResult{Stdout: []byte("")},
		"tmux", "list-panes", "-a", "-F", paneFormatStr)

	deps := Deps{
		Tmux:    tmux.Tmux{Runner: r, Bin: "tmux"},
		Runner:  r,
		Claude:  agent.Claude{Home: claudeHome},
		Root:    root,
		BaseDir: t.TempDir(),
		Now:     time.Now().Unix(),
	}

	cands, err := Gather(context.Background(), deps)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("expected 0 candidates for idle session, got %d", len(cands))
	}
}

// TestGather_DedupByWindow verifies that two Claude sessions in the same project
// tree produce only one candidate when they resolve to the same tmux window.
func TestGather_DedupByWindow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	root := t.TempDir()
	repoDir := makeGitWorktreeRepo(t, root, "duperepo")

	// Create TWO sessions for the same repo.
	claudeHome := t.TempDir()
	sessIDA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	sessIDB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	slug := encodePath(repoDir)
	slugDir := filepath.Join(claudeHome, "projects", slug)
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatalf("mkdir slugdir: %v", err)
	}

	writeTranscript := func(sessID string) {
		t.Helper()
		jsonl := `{"type":"user","sessionId":"` + sessID + `","cwd":"` + repoDir + `","gitBranch":"main","message":{"role":"user","content":"hi"},"timestamp":"2026-05-30T06:55:12.501Z"}` + "\n"
		if err := os.WriteFile(filepath.Join(slugDir, sessID+".jsonl"), []byte(jsonl), 0o644); err != nil {
			t.Fatalf("write transcript: %v", err)
		}
	}
	writeTranscript(sessIDA)
	writeTranscript(sessIDB)

	r := proc.NewFakeRunner()

	// git worktree list --porcelain → main worktree only.
	worktreeOut := "worktree " + repoDir + "\nHEAD abc1234\nbranch refs/heads/main\n\n"
	r.Respond(proc.FakeResult{Stdout: []byte(worktreeOut)},
		"git", "-C", repoDir, "worktree", "list", "--porcelain")

	// Two live panes: each associated with one session, but both in the same window.
	paneOut := "%1\x1f1234\x1fclaude\x1f0\x1f" + repoDir + "\x1fduperepo\x1fmain\x1f" + sessIDA + "\x1f\n" +
		"%2\x1f1235\x1fclaude\x1f0\x1f" + repoDir + "\x1fduperepo\x1fmain\x1f" + sessIDB + "\x1f\n"
	r.Respond(proc.FakeResult{Stdout: []byte(paneOut)},
		"tmux", "list-panes", "-a", "-F", paneFormatStr)

	deps := Deps{
		Tmux:    tmux.Tmux{Runner: r, Bin: "tmux"},
		Runner:  r,
		Claude:  agent.Claude{Home: claudeHome},
		Root:    root,
		BaseDir: t.TempDir(),
		Now:     time.Now().Unix(),
	}

	cands, err := Gather(context.Background(), deps)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	// Both sessions map to the same TmuxSession+TmuxWindow → deduped to 1.
	if len(cands) != 1 {
		t.Errorf("expected 1 candidate after dedup, got %d", len(cands))
	}
}
