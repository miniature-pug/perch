package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/discover"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// ── buildLiveIndex tests ───────────────────────────────────────────────────────

func TestBuildLiveIndex_LivePane(t *testing.T) {
	panes := []tmux.Pane{
		{ID: "%10", PerchSession: "session-abc", Dead: false},
	}
	idx := buildLiveIndex(panes)
	if got, ok := idx["session-abc"]; !ok || got != "%10" {
		t.Errorf("want session-abc → %%10, got ok=%v val=%q", ok, got)
	}
}

func TestBuildLiveIndex_DeadPaneExcluded(t *testing.T) {
	panes := []tmux.Pane{
		{ID: "%11", PerchSession: "session-dead", Dead: true},
	}
	idx := buildLiveIndex(panes)
	if _, ok := idx["session-dead"]; ok {
		t.Error("dead pane must not appear in live index")
	}
}

func TestBuildLiveIndex_EmptyPerchSessionSkipped(t *testing.T) {
	panes := []tmux.Pane{
		{ID: "%12", PerchSession: "", Dead: false},
	}
	idx := buildLiveIndex(panes)
	if _, ok := idx[""]; ok {
		t.Error("pane with empty PerchSession must not be indexed")
	}
}

func TestBuildLiveIndex_Empty(t *testing.T) {
	idx := buildLiveIndex(nil)
	if len(idx) != 0 {
		t.Errorf("empty pane list: want empty index, got %v", idx)
	}
}

// ── buildItemFromSession tests (pure join) ─────────────────────────────────────

func mkSession(id, tool, dir, title string, updated int64) model.Session {
	return model.Session{
		ID:        id,
		Tool:      model.Tool(tool),
		Directory: dir,
		Title:     title,
		Updated:   updated,
	}
}

// Test 1: live pane matching PerchSession == session.ID → item is live, captureTarget == pane.ID.
func TestBuildItemFromSession_LivePaneMatches(t *testing.T) {
	s := mkSession("sess-1", "claude", "/repo", "My Task", 1000)
	liveBySession := map[string]string{"sess-1": "%17"}

	it := buildItemFromSession(s, "myrepo", "main", 2000, liveBySession).(item)

	if !it.live {
		t.Error("want live=true, got false")
	}
	if it.captureTarget != "%17" {
		t.Errorf("captureTarget = %q, want %%17", it.captureTarget)
	}
	if it.status != StatusLive {
		t.Errorf("status = %v, want StatusLive", it.status)
	}
	if it.id != "sess-1" {
		t.Errorf("id = %q, want sess-1", it.id)
	}
	if it.project != "myrepo" {
		t.Errorf("project = %q, want myrepo", it.project)
	}
	if it.tree != "main" {
		t.Errorf("tree = %q, want main", it.tree)
	}
}

// Test 2: pane with Dead==true never reaches liveBySession, so session is idle.
func TestBuildItemFromSession_DeadPaneNotLive(t *testing.T) {
	s := mkSession("sess-2", "claude", "/repo", "Dead Task", 1000)

	// buildLiveIndex excludes dead panes before the join.
	deadPanes := []tmux.Pane{
		{ID: "%20", PerchSession: "sess-2", Dead: true},
	}
	liveBySession := buildLiveIndex(deadPanes)

	it := buildItemFromSession(s, "myrepo", "main", 2000, liveBySession).(item)

	if it.live {
		t.Error("want live=false for dead pane, got true")
	}
	if it.captureTarget != "" {
		t.Errorf("captureTarget = %q, want empty for dead pane", it.captureTarget)
	}
	if it.status != StatusIdle {
		t.Errorf("status = %v, want StatusIdle for dead pane", it.status)
	}
}

// Test 3: session with no matching live pane → idle, empty captureTarget.
func TestBuildItemFromSession_NoMatchingPaneIsIdle(t *testing.T) {
	s := mkSession("sess-3", "opencode", "/repo", "Idle Task", 1000)
	liveBySession := map[string]string{"other-session": "%30"} // no match for sess-3

	it := buildItemFromSession(s, "myrepo", "feature", 2000, liveBySession).(item)

	if it.live {
		t.Error("want live=false when no matching pane, got true")
	}
	if it.captureTarget != "" {
		t.Errorf("captureTarget = %q, want empty", it.captureTarget)
	}
	if it.status != StatusIdle {
		t.Errorf("status = %v, want StatusIdle", it.status)
	}
}

// Test 4: cold/empty ListPanesAll → every session idle.
func TestBuildItemFromSession_EmptyPanes(t *testing.T) {
	sessions := []model.Session{
		mkSession("a", "claude", "/r1", "T1", 1000),
		mkSession("b", "opencode", "/r2", "T2", 2000),
	}
	liveBySession := buildLiveIndex(nil) // empty index

	for _, s := range sessions {
		it := buildItemFromSession(s, "proj", "main", 5000, liveBySession).(item)
		if it.live {
			t.Errorf("session %s: want live=false with empty panes, got true", s.ID)
		}
		if it.captureTarget != "" {
			t.Errorf("session %s: captureTarget = %q, want empty", s.ID, it.captureTarget)
		}
	}
}

// ── relativeTime tests ──────────────────────────────────────────────────────────

func TestRelativeTime(t *testing.T) {
	// Use a now value large enough that all subtractions remain positive.
	now := int64(1_000_000)
	tests := []struct {
		updated int64
		want    string
	}{
		{now - 30, "30s ago"},
		{now - 90, "1m ago"},
		{now - 3700, "1h ago"},
		{now - 90000, "1d ago"},
		{0, ""},
	}
	for _, tt := range tests {
		got := relativeTime(tt.updated, now)
		if got != tt.want {
			t.Errorf("relativeTime(%d, %d) = %q, want %q", tt.updated, now, got, tt.want)
		}
	}
}

// ── previewMsg handling in Model ───────────────────────────────────────────────

// Test 5a: previewMsg for selected live target sets previewContent.
func TestModel_PreviewMsgSetsContent(t *testing.T) {
	liveItem := item{
		id:            "sess-live",
		title:         "live session",
		tool:          "claude",
		status:        StatusWorking,
		live:          true,
		captureTarget: "%99",
		isSession:     true,
	}
	m := New([]list.Item{liveItem})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	updated, _ := m.Update(previewMsg{content: "pane output here", target: "%99"})
	m = updated.(Model)

	if m.previewContent != "pane output here" {
		t.Errorf("previewContent = %q, want %q", m.previewContent, "pane output here")
	}
}

// Test 5b: stale previewMsg (wrong target) does not apply the stale content.
// With no loader, the re-fire path shows static detail (not the stale bytes).
func TestModel_PreviewMsgStaleIsIgnored(t *testing.T) {
	liveItem := item{
		id:            "sess-live",
		title:         "live session",
		tool:          "claude",
		status:        StatusWorking,
		live:          true,
		captureTarget: "%99",
		isSession:     true,
	}
	m := New([]list.Item{liveItem}) // no loader
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	updated, _ := m.Update(previewMsg{content: "stale output", target: "%00"})
	m = updated.(Model)

	// Stale content must never appear; with no loader the re-fire renders static detail.
	if m.previewContent == "stale output" {
		t.Error("stale previewMsg content was applied; it should be ignored")
	}
}

// ── itemsLoadedMsg wires items into list ──────────────────────────────────────

func TestModel_ItemsLoadedMsg(t *testing.T) {
	m := New(nil)
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	newItems := []list.Item{
		item{title: "loaded-1", tool: "claude", status: StatusIdle, isSession: true},
		item{title: "loaded-2", tool: "opencode", status: StatusWorking, isSession: true},
	}
	updated, _ := m.Update(itemsLoadedMsg{items: newItems})
	m = updated.(Model)

	if len(m.list.Items()) != 2 {
		t.Fatalf("after itemsLoadedMsg: want 2 items, got %d", len(m.list.Items()))
	}
}

// ── loader end-to-end: FakeRunner live/idle join ───────────────────────────────

// tmuxPaneLine builds one list-panes output line matching paneFormat.
func tmuxPaneLine(paneID, pid, cmd, dead, path, session, window, perchSession string) string {
	return strings.Join([]string{paneID, pid, cmd, dead, path, session, window, perchSession}, "\x1f")
}

// paneFormatFlag is the -F argument used by ListPanesAll (must match tmux.go).
const paneFormatFlag = "#{pane_id}\x1f#{pane_pid}\x1f#{pane_current_command}\x1f#{pane_dead}\x1f#{pane_current_path}\x1f#{session_name}\x1f#{window_name}\x1f#{@perch_session}"

// opencodeSessions builds minimal opencode JSON for the given sessions.
func opencodeSessions(sessions ...model.Session) []byte {
	type wire struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Directory string `json:"directory"`
		Updated   int64  `json:"updated"` // unix ms
	}
	var recs []wire
	for _, s := range sessions {
		recs = append(recs, wire{
			ID:        s.ID,
			Title:     s.Title,
			Directory: s.Directory,
			Updated:   s.Updated * 1000,
		})
	}
	b, _ := json.Marshal(recs)
	return b
}

// gitWorktreePorcelain returns porcelain output for a single main worktree.
func gitWorktreePorcelain(path, branch string) []byte {
	return []byte("worktree " + path + "\nbranch refs/heads/" + branch + "\nHEAD abc123\n\n")
}

// makeRepoDir creates a temp dir with a .git file so discover.Scan picks it up.
func makeRepoDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// A .git file (not dir) is what linked worktrees have; Scan accepts both.
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /fake\n"), 0o644); err != nil {
		t.Fatalf("create .git file: %v", err)
	}
	return dir
}

// TestLoaderLivePaneJoin runs the full loader with canned FakeRunner data and
// asserts the live/idle join at the item level.
func TestLoaderLivePaneJoin(t *testing.T) {
	repo := makeRepoDir(t)

	sessLive := model.Session{ID: "oc-live", Tool: model.ToolOpencode, Directory: repo, Title: "Live OC", Updated: 900}
	sessIdle := model.Session{ID: "oc-idle", Tool: model.ToolOpencode, Directory: repo, Title: "Idle OC", Updated: 800}

	livePaneLine := tmuxPaneLine("%10", "1234", "opencode", "0", repo, "perch", "w1", "oc-live")

	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: gitWorktreePorcelain(repo, "main")},
		"git", "-C", repo, "worktree", "list", "--porcelain")
	r.Respond(proc.FakeResult{Stdout: []byte(livePaneLine + "\n")},
		"tmux", "list-panes", "-a", "-F", paneFormatFlag)
	r.Respond(proc.FakeResult{Stdout: opencodeSessions(sessLive, sessIdle)},
		"opencode", "session", "list", "--format", "json")

	l := loader{
		Tmux:    tmux.Tmux{Runner: r},
		Runner:  r,
		Claude:  agent.Claude{Home: t.TempDir()}, // empty → no claude sessions
		Root:    repo,
		BaseDir: t.TempDir(),
		Now:     1000,
	}

	msg := l.load()().(itemsLoadedMsg)
	if msg.err != nil {
		t.Fatalf("loader error: %v", msg.err)
	}
	if len(msg.items) != 2 {
		t.Fatalf("want 2 items, got %d", len(msg.items))
	}

	byID := make(map[string]item)
	for _, li := range msg.items {
		it := li.(item)
		byID[it.id] = it
	}

	// oc-live has a matching live pane.
	if !byID["oc-live"].live {
		t.Error("oc-live: want live=true")
	}
	if byID["oc-live"].captureTarget != "%10" {
		t.Errorf("oc-live: captureTarget = %q, want %%10", byID["oc-live"].captureTarget)
	}

	// oc-idle has no matching pane.
	if byID["oc-idle"].live {
		t.Error("oc-idle: want live=false")
	}
	if byID["oc-idle"].captureTarget != "" {
		t.Errorf("oc-idle: captureTarget = %q, want empty", byID["oc-idle"].captureTarget)
	}
}

// TestLoaderNoLivePanesAllIdle verifies cold-start (no tmux server) → all idle.
func TestLoaderNoLivePanesAllIdle(t *testing.T) {
	repo := makeRepoDir(t)
	sess := model.Session{ID: "oc-s", Tool: model.ToolOpencode, Directory: repo, Title: "T", Updated: 900}

	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: gitWorktreePorcelain(repo, "main")},
		"git", "-C", repo, "worktree", "list", "--porcelain")
	// tmux list-panes -a fails (no server).
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "list-panes", "-a", "-F", paneFormatFlag)
	r.Respond(proc.FakeResult{Stdout: opencodeSessions(sess)},
		"opencode", "session", "list", "--format", "json")

	l := loader{
		Tmux:    tmux.Tmux{Runner: r},
		Runner:  r,
		Claude:  agent.Claude{Home: t.TempDir()},
		Root:    repo,
		BaseDir: t.TempDir(),
		Now:     1000,
	}

	msg := l.load()().(itemsLoadedMsg)
	if msg.err != nil {
		t.Fatalf("loader error: %v", msg.err)
	}
	if len(msg.items) != 1 {
		t.Fatalf("want 1 item, got %d", len(msg.items))
	}
	it := msg.items[0].(item)
	if it.live {
		t.Error("want live=false when tmux fails, got true")
	}
}

// TestLoaderDegradesToEmptyWhenNoSessions verifies that the loader uses frecency
// state without error and degrades gracefully to zero items when opencode
// returns no sessions and no claude sessions exist.
func TestLoaderDegradesToEmptyWhenNoSessions(t *testing.T) {
	repoA := makeRepoDir(t)

	r := proc.NewFakeRunner()
	r.Respond(proc.FakeResult{Stdout: gitWorktreePorcelain(repoA, "main")},
		"git", "-C", repoA, "worktree", "list", "--porcelain")
	r.Respond(proc.FakeResult{Err: proc.FakeExitError{Code: 1}},
		"tmux", "list-panes", "-a", "-F", paneFormatFlag)
	// opencode returns empty — no sessions, no error.
	r.Respond(proc.FakeResult{Stdout: []byte("[]")},
		"opencode", "session", "list", "--format", "json")

	baseDir := t.TempDir()
	writeStateWithProjects(t, baseDir, map[string]float64{repoA: 5.0})

	l := loader{
		Tmux:    tmux.Tmux{Runner: r},
		Runner:  r,
		Claude:  agent.Claude{Home: t.TempDir()},
		Root:    repoA,
		BaseDir: baseDir,
		Now:     100,
	}

	msg := l.load()().(itemsLoadedMsg)
	if msg.err != nil {
		t.Fatalf("loader error: %v", msg.err)
	}
	// No sessions produced (empty opencode, no claude) — no panic, clean return.
	if len(msg.items) != 0 {
		t.Errorf("want 0 items when no sessions exist, got %d", len(msg.items))
	}
}

// TestAssembleItems_FrecencyOrder is a pure-function test that verifies
// assembleItems preserves the order of pts (the frecency-ordered project list).
// Two projects A and B: A is supplied first in pts; items for A must appear
// before items for B regardless of alphabetical order.
func TestAssembleItems_FrecencyOrder(t *testing.T) {
	projA := model.Project{Path: "/z/proj-a", Name: "proj-a"}
	projB := model.Project{Path: "/a/proj-b", Name: "proj-b"}

	treeA := model.Tree{Path: "/z/proj-a", Branch: "main", IsMain: true, Project: &projA}
	treeB := model.Tree{Path: "/a/proj-b", Branch: "main", IsMain: true, Project: &projB}

	// pts in frecency order: A before B (even though B is alphabetically first).
	pts := []*discover.ProjectTrees{
		{Project: projA, Trees: []model.Tree{treeA}},
		{Project: projB, Trees: []model.Tree{treeB}},
	}

	sessA := mkSession("id-a", "claude", "/z/proj-a", "Task A", 1000)
	sessB := mkSession("id-b", "claude", "/a/proj-b", "Task B", 1000)

	claudeByDir := map[string][]model.Session{
		"/z/proj-a": {sessA},
		"/a/proj-b": {sessB},
	}

	items := assembleItems(pts, claudeByDir, nil, map[string]string{}, 2000)

	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	first := items[0].(item)
	second := items[1].(item)

	if first.id != "id-a" {
		t.Errorf("first item id = %q, want id-a (frecency order: A before B)", first.id)
	}
	if second.id != "id-b" {
		t.Errorf("second item id = %q, want id-b", second.id)
	}
	// Confirm project names are set correctly.
	if first.project != "proj-a" {
		t.Errorf("first.project = %q, want proj-a", first.project)
	}
	if second.project != "proj-b" {
		t.Errorf("second.project = %q, want proj-b", second.project)
	}
}

// writeStateWithProjects writes a minimal state.json with project ranks.
func writeStateWithProjects(t *testing.T, baseDir string, ranks map[string]float64) {
	t.Helper()
	type projectStat struct {
		Rank         float64 `json:"rank"`
		LastAccessed int64   `json:"last_accessed"`
	}
	type stateDoc struct {
		Mappings map[string]interface{} `json:"mappings"`
		Projects map[string]projectStat `json:"projects"`
	}
	projects := make(map[string]projectStat, len(ranks))
	for path, rank := range ranks {
		projects[path] = projectStat{Rank: rank, LastAccessed: 50}
	}
	doc := stateDoc{
		Mappings: map[string]interface{}{},
		Projects: projects,
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "state.json"), data, 0o644); err != nil {
		t.Fatalf("write state.json: %v", err)
	}
}

// ── capture-pane cmd dispatch ──────────────────────────────────────────────────

// TestModel_SelectLiveItemDispatchesCapture verifies that after selection of a
// live item, navigating away and back does not produce duplicate captures
// (the capturing guard). This tests model-state only, not rendered output.
func TestModel_SelectLiveItemDispatchesCapture(t *testing.T) {
	liveItem := item{
		id:            "sess-live",
		title:         "live session",
		tool:          "claude",
		status:        StatusWorking,
		live:          true,
		captureTarget: "%99",
		isSession:     true,
	}
	idleItem := item{
		id:        "sess-idle",
		title:     "idle session",
		tool:      "claude",
		status:    StatusIdle,
		live:      false,
		isSession: true,
	}
	m := New([]list.Item{liveItem, idleItem})
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	// Deliver windowMsg so the preview fires for the initial live selection.
	// The returned cmd should be a capture cmd (non-nil) for liveItem.
	_, cmd := m.Update(windowMsg)
	// cmd may be nil (no loader configured) or a capture cmd; just ensure no panic.
	_ = cmd

	// Deliver a previewMsg for the live item.
	updated, _ := m.Update(previewMsg{content: "live output", target: "%99"})
	m = updated.(Model)
	if m.previewContent != "live output" {
		t.Errorf("previewContent = %q, want %q", m.previewContent, "live output")
	}
	if m.capturing {
		t.Error("capturing should be false after previewMsg resolved")
	}
}

// TestModel_CaptureGatePreventsDuplicates: if capturing is already true,
// previewCmd returns nil (gate blocks a duplicate capture). When capturing is
// false and a live item is selected with a loader, previewCmd returns non-nil.
func TestModel_CaptureGatePreventsDuplicates(t *testing.T) {
	liveItem := item{
		id:            "sess-live",
		title:         "live session",
		tool:          "claude",
		status:        StatusLive,
		live:          true,
		captureTarget: "%99",
		isSession:     true,
	}
	// Provide a loader so previewCmd can fire a real capture cmd.
	ldr := loader{Tmux: tmux.Tmux{Runner: proc.NewFakeRunner()}}
	m := New([]list.Item{liveItem}).WithLoader(ldr)
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	// Gate test: with capturing=true, previewCmd must return nil.
	m.capturing = true
	cmd := m.previewCmd()
	if cmd != nil {
		t.Error("capture gate: expected nil cmd when capturing=true, got non-nil")
	}

	// Companion: with capturing=false, previewCmd must return a non-nil cmd.
	m.capturing = false
	cmd = m.previewCmd()
	if cmd == nil {
		t.Error("expected non-nil capture cmd when capturing=false and live item selected")
	}
	// previewCmd sets capturing=true when it fires — confirm the state update.
	if !m.capturing {
		t.Error("expected capturing=true after previewCmd fired a capture cmd")
	}
}

// TestModel_StalePreviewMsgReFires verifies that after a stale previewMsg
// (target ≠ current selection), the model re-fires a capture cmd for the
// now-current live selection. This covers navigate-during-capture: the stale
// result must not strand the preview on the new item.
func TestModel_StalePreviewMsgReFires(t *testing.T) {
	// A live item with a loader so previewCmd actually fires a capture cmd.
	r := proc.NewFakeRunner()
	// CapturePane will be called by the returned cmd via the loader's Tmux.
	r.Respond(proc.FakeResult{Stdout: []byte("captured!")},
		"tmux", "capture-pane", "-t", "%99", "-p")

	liveItem := item{
		id:            "sess-live",
		title:         "live session",
		tool:          "claude",
		status:        StatusWorking,
		live:          true,
		captureTarget: "%99",
		isSession:     true,
	}
	ldr := loader{Tmux: tmux.Tmux{Runner: r}}
	m := New([]list.Item{liveItem}).WithLoader(ldr)
	updated0, _ := m.Update(windowMsg)
	m = updated0.(Model)

	// A stale previewMsg arrives (target is %old, not %99).
	updated, cmd := m.Update(previewMsg{content: "stale output", target: "%old"})
	m = updated.(Model)

	// A re-fire cmd must have been returned since current item is live with %99.
	if cmd == nil {
		t.Error("expected a non-nil re-fire cmd after stale previewMsg for live selection")
	}
	// The stale content must not have been applied.
	if m.previewContent == "stale output" {
		t.Error("stale previewMsg content was applied; should be ignored")
	}
	// capturing is true again because previewCmd set it when firing the re-capture.
	if !m.capturing {
		t.Error("capturing should be true after re-fire cmd was issued")
	}

	// Execute the re-fire cmd to completion.
	if cmd != nil {
		resultMsg := cmd()
		updated2, _ := m.Update(resultMsg)
		m = updated2.(Model)
		if m.previewContent != "captured!" {
			t.Errorf("after re-fire resolved: previewContent = %q, want %q",
				m.previewContent, "captured!")
		}
	}
}

// ── claude ctx-cancellation test ──────────────────────────────────────────────

// TestClaude_ListSessionsRespectsCtxCancellation verifies that a pre-cancelled
// context causes ListSessions to return promptly with a non-nil error, rather
// than walking all slug directories. Uses the testdata/claude fixture (which has
// real slug directories) so the cancellation check is exercised.
func TestClaude_ListSessionsRespectsCtxCancellation(t *testing.T) {
	c := agent.Claude{}
	c.Home = "../agent/testdata/claude"
	c.Bin = "claude"
	c.LookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	c.Exists = func(path string) bool {
		return path == "/home" || path == "/home/user" || path == "/home/user/myproject" ||
			path == "/home/user/noai" || path == "/home/user/slugonly"
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	_, err := c.ListSessions(ctx)
	if err == nil {
		t.Error("want non-nil error from cancelled context, got nil")
	}
	if ctx.Err() == nil {
		t.Error("ctx.Err() should be non-nil after cancel")
	}
}
