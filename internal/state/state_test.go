package state_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/state"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ── §20.3 frecency score boundary tests ───────────────────────────────────────

func TestFrecencyScore(t *testing.T) {
	const rank = 10.0
	cases := []struct {
		name string
		d    int64 // now - lastAccessed
		want float64
	}{
		{"d=3599 within hour", 3599, rank * 4.0},
		{"d=3600 within day", 3600, rank * 2.0},
		{"d=86399 within day", 86399, rank * 2.0},
		{"d=86400 within week", 86400, rank * 0.5},
		{"d=604799 within week", 604799, rank * 0.5},
		{"d=604800 older", 604800, rank * 0.25},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := int64(1_000_000)
			lastAccessed := now - tc.d
			got := state.FrecencyScore(rank, lastAccessed, now)
			if got != tc.want {
				t.Errorf("FrecencyScore(%v, d=%v) = %v; want %v", rank, tc.d, got, tc.want)
			}
		})
	}
}

// ── aging tests ───────────────────────────────────────────────────────────────

func TestAgeProjects_BelowThreshold(t *testing.T) {
	// total = 9999.0 → no multiply, no eviction.
	projects := map[string]state.ProjectStat{
		"/a": {Rank: 5000.0, LastAccessed: 0},
		"/b": {Rank: 4999.0, LastAccessed: 0},
	}
	state.AgeProjects(projects)
	if projects["/a"].Rank != 5000.0 {
		t.Errorf("/a rank = %v; want 5000.0 (no aging below threshold)", projects["/a"].Rank)
	}
	if projects["/b"].Rank != 4999.0 {
		t.Errorf("/b rank = %v; want 4999.0 (no aging below threshold)", projects["/b"].Rank)
	}
	if len(projects) != 2 {
		t.Errorf("len = %d; want 2 (no eviction)", len(projects))
	}
}

func TestAgeProjects_AboveThreshold(t *testing.T) {
	// total = 10001.0 → each rank *= 0.9*10000/10001 ≈ 0.8999.
	// With factor ≈ 0.8999: /big survives (≈8998.6), /one and /half both drop
	// below 1.0 and are evicted. All assertions are unconditional.
	const maxAge = 10_000.0
	projects := map[string]state.ProjectStat{
		"/big":  {Rank: 9999.5, LastAccessed: 0},
		"/one":  {Rank: 1.0, LastAccessed: 0},
		"/half": {Rank: 0.5, LastAccessed: 0},
	}
	// total = 9999.5 + 1.0 + 0.5 = 10001.0
	total := 9999.5 + 1.0 + 0.5
	factor := 0.9 * maxAge / total
	t.Logf("factor = %v (0.9*%.1f/%.1f); /one aged to %v, /half aged to %v — both evicted",
		factor, maxAge, total, 1.0*factor, 0.5*factor)

	state.AgeProjects(projects)

	bigWant := 9999.5 * factor
	if v, ok := projects["/big"]; !ok {
		t.Error("/big was evicted; should survive")
	} else if v.Rank != bigWant {
		t.Errorf("/big rank = %v; want %v", v.Rank, bigWant)
	}

	// /one aged rank ≈ 0.8999 < 1.0 → evicted.
	if _, ok := projects["/one"]; ok {
		t.Error("/one should have been evicted (rank after aging < 1.0)")
	}

	// /half aged rank ≈ 0.4499 < 1.0 → evicted.
	if _, ok := projects["/half"]; ok {
		t.Error("/half should have been evicted (rank after aging < 1.0)")
	}

	if len(projects) != 1 {
		t.Errorf("len = %d; want 1 (only /big survives)", len(projects))
	}
}

func TestAgeProjects_LargeRankSurvives(t *testing.T) {
	// Constants chosen so that after aging, /survivor's rank stays deterministically ≥ 1.0.
	// total = 20001.0, factor = 0.9*10000/20001 ≈ 0.4499.
	// /survivor rank=10000 * 0.4499 ≈ 4499.8 — well above 1.0.
	// /small rank=1.0 * 0.4499 ≈ 0.4499 — evicted.
	const maxAge = 10_000.0
	projects := map[string]state.ProjectStat{
		"/survivor": {Rank: 10000.0, LastAccessed: 0},
		"/small":    {Rank: 10001.0, LastAccessed: 0},
	}
	// total = 10000 + 10001 = 20001
	total := 10000.0 + 10001.0
	factor := 0.9 * maxAge / total
	survivorWant := 10000.0 * factor
	t.Logf("factor = %v; /survivor aged to %v (survives)", factor, survivorWant)

	state.AgeProjects(projects)

	if v, ok := projects["/survivor"]; !ok {
		t.Error("/survivor was evicted; should survive")
	} else if v.Rank != survivorWant {
		t.Errorf("/survivor rank = %v; want %v", v.Rank, survivorWant)
	}

	// /small: 10001 * factor ≈ 4500.3 — also survives (both large ranks stay above 1.0).
	smallWant := 10001.0 * factor
	if v, ok := projects["/small"]; !ok {
		t.Error("/small was evicted; should survive")
	} else if v.Rank != smallWant {
		t.Errorf("/small rank = %v; want %v", v.Rank, smallWant)
	}
}

func TestAgeProjects_ExactThreshold(t *testing.T) {
	// total == maxAge exactly → NOT > maxAge, so no aging.
	projects := map[string]state.ProjectStat{
		"/a": {Rank: 5000.0},
		"/b": {Rank: 5000.0},
	}
	state.AgeProjects(projects)
	if projects["/a"].Rank != 5000.0 {
		t.Errorf("/a rank = %v; want 5000.0 (no aging at threshold)", projects["/a"].Rank)
	}
}

// ── bump tests ────────────────────────────────────────────────────────────────

func TestBumpProject(t *testing.T) {
	projects := map[string]state.ProjectStat{
		"/repo": {Rank: 5.0, LastAccessed: 100},
	}
	now := int64(999)
	state.BumpProject(projects, "/repo", now)

	got := projects["/repo"]
	if got.Rank != 6.0 {
		t.Errorf("rank = %v; want 6.0 (5+1)", got.Rank)
	}
	if got.LastAccessed != now {
		t.Errorf("last_accessed = %v; want %v", got.LastAccessed, now)
	}
}

func TestBumpProject_NewEntry(t *testing.T) {
	projects := map[string]state.ProjectStat{}
	now := int64(42)
	state.BumpProject(projects, "/new", now)

	got := projects["/new"]
	if got.Rank != 1.0 {
		t.Errorf("rank = %v; want 1.0 (0+1)", got.Rank)
	}
	if got.LastAccessed != now {
		t.Errorf("last_accessed = %v; want %v", got.LastAccessed, now)
	}
}

// ── ordering helper tests ─────────────────────────────────────────────────────

func TestSortedPaths_ColdStart(t *testing.T) {
	// empty → alphabetical fallback.
	projects := map[string]state.ProjectStat{}
	paths := state.SortedPaths(projects, 0)
	if len(paths) != 0 {
		t.Errorf("empty projects → %v; want []", paths)
	}
}

func TestSortedPaths_AlphabeticalFallback(t *testing.T) {
	// All entries with identical score → alphabetical order.
	now := int64(1_000_000)
	// All last_accessed is far enough in the past that all get the same multiplier.
	la := now - 700_000 // older → * 0.25 for all
	projects := map[string]state.ProjectStat{
		"/z/repo": {Rank: 10.0, LastAccessed: la},
		"/a/repo": {Rank: 10.0, LastAccessed: la},
		"/m/repo": {Rank: 10.0, LastAccessed: la},
	}
	paths := state.SortedPaths(projects, now)
	want := []string{"/a/repo", "/m/repo", "/z/repo"}
	if !equalSlice(paths, want) {
		t.Errorf("paths = %v; want %v (alphabetical tiebreak)", paths, want)
	}
}

func TestSortedPaths_ScoreOrder(t *testing.T) {
	now := int64(1_000_000)
	projects := map[string]state.ProjectStat{
		"/low":  {Rank: 1.0, LastAccessed: now - 700_000}, // 1 * 0.25 = 0.25
		"/high": {Rank: 10.0, LastAccessed: now - 1_000},  // 10 * 4.0 = 40
		"/mid":  {Rank: 5.0, LastAccessed: now - 90_000},  // 5 * 0.5 = 2.5
	}
	paths := state.SortedPaths(projects, now)
	want := []string{"/high", "/mid", "/low"}
	if !equalSlice(paths, want) {
		t.Errorf("paths = %v; want %v (descending score)", paths, want)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── StateDir tests ────────────────────────────────────────────────────────────

func TestStateDir_XDGSet(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	got, err := state.StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	want := filepath.Join(tmp, "perch")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestStateDir_XDGUnset(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	got, err := state.StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".local", "state", "perch")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// ── state.json save/load round-trip ──────────────────────────────────────────

func TestLoadState_Missing(t *testing.T) {
	tmp := t.TempDir()
	s, err := state.LoadState(tmp)
	if err != nil {
		t.Fatalf("LoadState on missing file: %v", err)
	}
	if s.Mappings == nil {
		t.Error("Mappings is nil; want non-nil empty map")
	}
	if s.Projects == nil {
		t.Error("Projects is nil; want non-nil empty map")
	}
	if len(s.Mappings) != 0 {
		t.Errorf("Mappings len = %d; want 0", len(s.Mappings))
	}
	if len(s.Projects) != 0 {
		t.Errorf("Projects len = %d; want 0", len(s.Projects))
	}
}

func TestLoadState_Malformed(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "state.json"), `{not valid json`)
	_, err := state.LoadState(tmp)
	if err == nil {
		t.Fatal("LoadState on malformed JSON: expected error, got nil")
	}
}

func TestSaveLoadState_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	s := state.State{
		Mappings: map[string]state.Mapping{
			"ses_abc123": {Tool: model.ToolClaude, Tree: "/home/me/repo", Choice: "worktree"},
			"ses_xyz789": {Tool: model.ToolOpencode, Tree: "/home/me/other", Choice: "none"},
		},
		Projects: map[string]state.ProjectStat{
			"/home/me/repo":  {Rank: 12.3, LastAccessed: 1748476800},
			"/home/me/other": {Rank: 5.0, LastAccessed: 1000},
		},
	}

	if err := state.SaveState(tmp, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := state.LoadState(tmp)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// Verify mappings.
	if len(loaded.Mappings) != 2 {
		t.Fatalf("Mappings len = %d; want 2", len(loaded.Mappings))
	}
	m := loaded.Mappings["ses_abc123"]
	if m.Tool != model.ToolClaude || m.Tree != "/home/me/repo" || m.Choice != "worktree" {
		t.Errorf("Mappings[ses_abc123] = %+v; want {claude, /home/me/repo, worktree}", m)
	}
	m2 := loaded.Mappings["ses_xyz789"]
	if m2.Tool != model.ToolOpencode || m2.Tree != "/home/me/other" || m2.Choice != "none" {
		t.Errorf("Mappings[ses_xyz789] = %+v; want {opencode, /home/me/other, none}", m2)
	}

	// Verify projects.
	if len(loaded.Projects) != 2 {
		t.Fatalf("Projects len = %d; want 2", len(loaded.Projects))
	}
	p := loaded.Projects["/home/me/repo"]
	if p.Rank != 12.3 || p.LastAccessed != 1748476800 {
		t.Errorf("Projects[/home/me/repo] = %+v; want {12.3, 1748476800}", p)
	}
}

func TestSaveState_AtomicNoLeftoverTemp(t *testing.T) {
	tmp := t.TempDir()
	s := state.State{
		Mappings: map[string]state.Mapping{},
		Projects: map[string]state.ProjectStat{},
	}
	if err := state.SaveState(tmp, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Errorf("unexpected file in state dir: %q (leftover temp?)", e.Name())
		}
	}
}

func TestSaveState_CreatesBaseDir(t *testing.T) {
	// baseDir does not exist yet — SaveState must create it.
	tmp := t.TempDir()
	baseDir := filepath.Join(tmp, "sub", "perch")
	s := state.State{
		Mappings: map[string]state.Mapping{},
		Projects: map[string]state.ProjectStat{},
	}
	if err := state.SaveState(baseDir, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "state.json")); err != nil {
		t.Fatalf("state.json not created: %v", err)
	}
}

// ── JSON tag wire format ──────────────────────────────────────────────────────

func TestStateJSON_WireTags(t *testing.T) {
	// Verify the JSON tags match the spec exactly.
	tmp := t.TempDir()
	s := state.State{
		Mappings: map[string]state.Mapping{
			"id1": {Tool: model.ToolClaude, Tree: "/t", Choice: "worktree"},
		},
		Projects: map[string]state.ProjectStat{
			"/t": {Rank: 1.5, LastAccessed: 42},
		},
	}
	if err := state.SaveState(tmp, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmp, "state.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["mappings"]; !ok {
		t.Error("top-level key 'mappings' not found")
	}
	if _, ok := raw["projects"]; !ok {
		t.Error("top-level key 'projects' not found")
	}

	var mappings map[string]json.RawMessage
	if err := json.Unmarshal(raw["mappings"], &mappings); err != nil {
		t.Fatalf("unmarshal mappings: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(mappings["id1"], &m); err != nil {
		t.Fatalf("unmarshal mapping entry: %v", err)
	}
	for _, tag := range []string{"tool", "tree", "choice"} {
		if _, ok := m[tag]; !ok {
			t.Errorf("Mapping missing JSON tag %q", tag)
		}
	}

	var projects map[string]json.RawMessage
	if err := json.Unmarshal(raw["projects"], &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(projects["/t"], &p); err != nil {
		t.Fatalf("unmarshal project stat: %v", err)
	}
	for _, tag := range []string{"rank", "last_accessed"} {
		if _, ok := p[tag]; !ok {
			t.Errorf("ProjectStat missing JSON tag %q", tag)
		}
	}
}

// ── EncodePaneKey / DecodePaneKey tests ───────────────────────────────────────

func TestEncodePaneKey_RoundTrip(t *testing.T) {
	cases := []string{"%17", "%0", "%123"}
	for _, k := range cases {
		t.Run(k, func(t *testing.T) {
			encoded := state.EncodePaneKey(k)
			decoded, err := state.DecodePaneKey(encoded)
			if err != nil {
				t.Fatalf("DecodePaneKey(%q): %v", encoded, err)
			}
			if decoded != k {
				t.Errorf("round-trip: got %q; want %q", decoded, k)
			}
		})
	}
}

func TestEncodePaneKey_SpecExample(t *testing.T) {
	// Spec: "%17" → "%2517" (the leading % is percent-encoded to %25).
	encoded := state.EncodePaneKey("%17")
	if encoded != "%2517" {
		t.Errorf("EncodePaneKey(%%17) = %q; want %%2517", encoded)
	}
}

func TestDecodePaneKey_Invalid(t *testing.T) {
	// A percent escape that's invalid should return an error.
	_, err := state.DecodePaneKey("%ZZ")
	if err == nil {
		t.Error("DecodePaneKey(%ZZ): expected error, got nil")
	}
}

// ── Window save/load tests ────────────────────────────────────────────────────

func sampleWindow(paneKey string) model.Window {
	return model.Window{
		PaneKey:     paneKey,
		Tool:        model.ToolClaude,
		SessionID:   "ses_abc",
		Tree:        "/home/me/repo",
		TmuxSession: "main",
		TmuxWindow:  "editor",
		BootID:      "1748476800",
		Updated:     1748476800,
	}
}

func TestSaveLoadWindow_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	w := sampleWindow("%17")

	if err := state.SaveWindow(tmp, w); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	windows, err := state.LoadWindows(tmp)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("len = %d; want 1", len(windows))
	}

	got := windows[0]
	if got.PaneKey != w.PaneKey {
		t.Errorf("PaneKey = %q; want %q", got.PaneKey, w.PaneKey)
	}
	if got.Tool != w.Tool {
		t.Errorf("Tool = %q; want %q", got.Tool, w.Tool)
	}
	if got.SessionID != w.SessionID {
		t.Errorf("SessionID = %q; want %q", got.SessionID, w.SessionID)
	}
	if got.Tree != w.Tree {
		t.Errorf("Tree = %q; want %q", got.Tree, w.Tree)
	}
	if got.TmuxSession != w.TmuxSession {
		t.Errorf("TmuxSession = %q; want %q", got.TmuxSession, w.TmuxSession)
	}
	if got.TmuxWindow != w.TmuxWindow {
		t.Errorf("TmuxWindow = %q; want %q", got.TmuxWindow, w.TmuxWindow)
	}
	if got.BootID != w.BootID {
		t.Errorf("BootID = %q; want %q", got.BootID, w.BootID)
	}
	if got.Updated != w.Updated {
		t.Errorf("Updated = %d; want %d", got.Updated, w.Updated)
	}
}

func TestLoadWindows_MultipleWindows(t *testing.T) {
	tmp := t.TempDir()
	keys := []string{"%0", "%17", "%42"}
	for _, k := range keys {
		if err := state.SaveWindow(tmp, sampleWindow(k)); err != nil {
			t.Fatalf("SaveWindow(%s): %v", k, err)
		}
	}

	windows, err := state.LoadWindows(tmp)
	if err != nil {
		t.Fatalf("LoadWindows: %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("len = %d; want 3", len(windows))
	}
	// Verify all keys are present.
	found := map[string]bool{}
	for _, w := range windows {
		found[w.PaneKey] = true
	}
	for _, k := range keys {
		if !found[k] {
			t.Errorf("missing window with PaneKey %q", k)
		}
	}
}

func TestRemoveWindow(t *testing.T) {
	tmp := t.TempDir()
	w := sampleWindow("%17")

	if err := state.SaveWindow(tmp, w); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}
	if err := state.RemoveWindow(tmp, "%17"); err != nil {
		t.Fatalf("RemoveWindow: %v", err)
	}

	windows, err := state.LoadWindows(tmp)
	if err != nil {
		t.Fatalf("LoadWindows after remove: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("len = %d; want 0 after remove", len(windows))
	}
}

func TestRemoveWindow_NonExistent(t *testing.T) {
	tmp := t.TempDir()
	// RemoveWindow on a non-existent key is not an error.
	if err := state.RemoveWindow(tmp, "%99"); err != nil {
		t.Errorf("RemoveWindow non-existent: %v; want nil", err)
	}
}

func TestLoadWindows_MissingDir(t *testing.T) {
	tmp := t.TempDir()
	// No windows/ subdir — should return empty slice, not error.
	windows, err := state.LoadWindows(tmp)
	if err != nil {
		t.Fatalf("LoadWindows missing dir: %v; want nil error", err)
	}
	if len(windows) != 0 {
		t.Errorf("len = %d; want 0", len(windows))
	}
}

func TestLoadWindows_SkipMalformed(t *testing.T) {
	tmp := t.TempDir()
	// Write one valid and one malformed window.
	if err := state.SaveWindow(tmp, sampleWindow("%5")); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}
	// Write a malformed JSON file directly into windows/.
	badPath := filepath.Join(tmp, "windows", "bad.json")
	writeFile(t, badPath, `{not valid json`)

	windows, err := state.LoadWindows(tmp)
	if err != nil {
		t.Fatalf("LoadWindows with malformed file: %v; want nil error", err)
	}
	if len(windows) != 1 {
		t.Fatalf("len = %d; want 1 (malformed skipped)", len(windows))
	}
	if windows[0].PaneKey != "%5" {
		t.Errorf("PaneKey = %q; want %%5", windows[0].PaneKey)
	}
}

func TestLoadWindows_NoLeftoverTemp(t *testing.T) {
	tmp := t.TempDir()
	if err := state.SaveWindow(tmp, sampleWindow("%3")); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(tmp, "windows"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			t.Errorf("unexpected non-json file in windows/: %q (leftover temp?)", e.Name())
		}
	}
}

// ── JSON tags for Window (wire contract) ─────────────────────────────────────

func TestWindowJSON_WireTags(t *testing.T) {
	tmp := t.TempDir()
	w := sampleWindow("%7")
	if err := state.SaveWindow(tmp, w); err != nil {
		t.Fatalf("SaveWindow: %v", err)
	}

	encoded := state.EncodePaneKey("%7")
	data, err := os.ReadFile(filepath.Join(tmp, "windows", encoded+".json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, tag := range []string{"pane_key", "tool", "session_id", "tree", "tmux_session", "tmux_window", "boot_id", "updated"} {
		if _, ok := raw[tag]; !ok {
			t.Errorf("Window JSON missing tag %q", tag)
		}
	}
}

// ── LookupMapping / SetMapping tests ─────────────────────────────────────────

func TestLookupMapping_Hit(t *testing.T) {
	s := state.State{
		Mappings: map[string]state.Mapping{
			"ses_abc": {Tool: model.ToolClaude, Tree: "/repo/wt1", Choice: state.ChoiceWorktree},
		},
	}
	m, ok := state.LookupMapping(s, "ses_abc")
	if !ok {
		t.Fatal("LookupMapping: expected ok=true, got false")
	}
	if m.Tool != model.ToolClaude {
		t.Errorf("Tool = %q; want claude", m.Tool)
	}
	if m.Tree != "/repo/wt1" {
		t.Errorf("Tree = %q; want /repo/wt1", m.Tree)
	}
	if m.Choice != state.ChoiceWorktree {
		t.Errorf("Choice = %q; want %q", m.Choice, state.ChoiceWorktree)
	}
}

func TestLookupMapping_Miss(t *testing.T) {
	s := state.State{
		Mappings: map[string]state.Mapping{},
	}
	m, ok := state.LookupMapping(s, "ses_missing")
	if ok {
		t.Error("LookupMapping: expected ok=false for missing session, got true")
	}
	if m != (state.Mapping{}) {
		t.Errorf("LookupMapping miss: got non-zero Mapping %+v", m)
	}
}

func TestLookupMapping_NilMap(t *testing.T) {
	s := state.State{} // Mappings is nil
	m, ok := state.LookupMapping(s, "ses_any")
	if ok {
		t.Error("LookupMapping on nil map: expected ok=false, got true")
	}
	if m != (state.Mapping{}) {
		t.Errorf("LookupMapping nil map: got non-zero Mapping %+v", m)
	}
}

func TestSetMapping_RoundTrip_ChoiceWorktree(t *testing.T) {
	tmp := t.TempDir()

	// Start from empty state.
	s, err := state.LoadState(tmp)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	want := state.Mapping{Tool: model.ToolClaude, Tree: "/repo/wt1", Choice: state.ChoiceWorktree}
	state.SetMapping(&s, "ses_wt", want)

	if err := state.SaveState(tmp, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := state.LoadState(tmp)
	if err != nil {
		t.Fatalf("LoadState after save: %v", err)
	}

	got, ok := state.LookupMapping(loaded, "ses_wt")
	if !ok {
		t.Fatal("LookupMapping: expected ok=true after round-trip")
	}
	if got != want {
		t.Errorf("LookupMapping = %+v; want %+v", got, want)
	}
}

func TestSetMapping_RoundTrip_ChoiceNone(t *testing.T) {
	tmp := t.TempDir()

	s, err := state.LoadState(tmp)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	want := state.Mapping{Tool: model.ToolOpencode, Tree: "/repo", Choice: state.ChoiceNone}
	state.SetMapping(&s, "ses_none", want)

	if err := state.SaveState(tmp, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := state.LoadState(tmp)
	if err != nil {
		t.Fatalf("LoadState after save: %v", err)
	}

	got, ok := state.LookupMapping(loaded, "ses_none")
	if !ok {
		t.Fatal("LookupMapping: expected ok=true after round-trip (ChoiceNone)")
	}
	if got != want {
		t.Errorf("LookupMapping = %+v; want %+v", got, want)
	}
}

func TestSetMapping_LazyInit(t *testing.T) {
	// SetMapping on a State with nil Mappings must not panic.
	s := &state.State{} // Mappings is nil
	m := state.Mapping{Tool: model.ToolClaude, Tree: "/t", Choice: state.ChoiceWorktree}
	state.SetMapping(s, "ses_lazy", m)

	got, ok := state.LookupMapping(*s, "ses_lazy")
	if !ok {
		t.Fatal("LookupMapping after SetMapping on nil map: expected ok=true")
	}
	if got != m {
		t.Errorf("got %+v; want %+v", got, m)
	}
}

// ── SortedPaths determinism ───────────────────────────────────────────────────

func TestSortedPaths_Deterministic(t *testing.T) {
	// Multiple calls with same input should produce identical output.
	now := int64(1_000_000)
	la := now - 700_000
	projects := map[string]state.ProjectStat{
		"/c": {Rank: 3.0, LastAccessed: la},
		"/a": {Rank: 1.0, LastAccessed: la},
		"/b": {Rank: 2.0, LastAccessed: la},
	}
	paths1 := state.SortedPaths(projects, now)
	paths2 := state.SortedPaths(projects, now)
	if !equalSlice(paths1, paths2) {
		t.Errorf("non-deterministic: %v vs %v", paths1, paths2)
	}
	// Descending score order: /c=0.75, /b=0.50, /a=0.25.
	want := []string{"/c", "/b", "/a"}
	if !equalSlice(paths1, want) {
		t.Errorf("paths = %v; want %v", paths1, want)
	}
}

// ── make sure SortedPaths returns all paths ───────────────────────────────────

func TestSortedPaths_ReturnsAllPaths(t *testing.T) {
	now := int64(0)
	projects := map[string]state.ProjectStat{
		"/a": {Rank: 1.0},
		"/b": {Rank: 2.0},
		"/c": {Rank: 3.0},
	}
	paths := state.SortedPaths(projects, now)
	if len(paths) != 3 {
		t.Fatalf("len = %d; want 3", len(paths))
	}
	all := []string{"/a", "/b", "/c"}
	sort.Strings(paths)
	if !equalSlice(paths, all) {
		t.Errorf("paths (sorted) = %v; want %v", paths, all)
	}
}
