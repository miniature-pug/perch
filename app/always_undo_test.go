package app

import (
	"path/filepath"
	"testing"

	"github.com/miniature-pug/perch/internal/agent"
)

// ApproveAlways reports the rule it appended, and RemoveAlwaysRule deletes
// exactly that rule from the current list, keeping a rule another grant added
// in between (frontend review #10).
func TestApproveAlways_ReturnsAddedRule_UndoRemovesOnlyIt(t *testing.T) {
	a, _ := newAlwaysTestApp(t, "claude", nil)
	a.pending = map[string]agent.ApprovalReq{
		"r1:ws": {ReqID: "r1", Tool: "Bash", Input: "npm test", InputHash: hashInput("npm test")},
		"r2:ws": {ReqID: "r2", Tool: "Read", Input: "/a.txt", InputHash: hashInput("/a.txt")},
	}

	g1, err := a.ApproveAlways("r1:ws")
	if err != nil {
		t.Fatalf("ApproveAlways r1: %v", err)
	}
	if !g1.Added || g1.Rule.Tool != "Bash" || g1.Rule.Hash != hashInput("npm test") {
		t.Fatalf("grant = %+v, want an added Bash rule with the input hash", g1)
	}
	// Another grant lands before the user presses Undo.
	if _, err := a.ApproveAlways("r2:ws"); err != nil {
		t.Fatalf("ApproveAlways r2: %v", err)
	}

	removed, err := a.RemoveAlwaysRule(g1.Rule)
	if err != nil || !removed {
		t.Fatalf("RemoveAlwaysRule = %v, %v; want true, nil", removed, err)
	}
	s, _ := a.GetSettings()
	if len(s.AlwaysRules) != 1 || s.AlwaysRules[0].Tool != "Read" {
		t.Fatalf("rules after undo = %+v, want only the Read rule", s.AlwaysRules)
	}

	// Removing again is a no-op, not an error.
	removed, err = a.RemoveAlwaysRule(g1.Rule)
	if err != nil || removed {
		t.Fatalf("second RemoveAlwaysRule = %v, %v; want false, nil", removed, err)
	}
}

// A grant whose identical rule already exists adds nothing, so Undo must not
// remove the pre-existing rule.
func TestApproveAlways_DuplicateRuleIsNotReportedAsAdded(t *testing.T) {
	a, _ := newAlwaysTestApp(t, "claude", nil)
	existing := AlwaysRule{Agent: "claude", Tool: "Bash", Pattern: "ls", Hash: hashInput("ls")}
	if err := a.saveSettingsLocked(Settings{AlwaysRules: []AlwaysRule{existing}}); err != nil {
		t.Fatal(err)
	}
	a.pending = map[string]agent.ApprovalReq{
		"r1:ws": {ReqID: "r1", Tool: "Bash", Input: "ls", InputHash: hashInput("ls")},
	}
	g, err := a.ApproveAlways("r1:ws")
	if err != nil {
		t.Fatalf("ApproveAlways: %v", err)
	}
	if g.Added {
		t.Fatalf("grant = %+v, want Added=false for a duplicate rule", g)
	}
}

// Plain Approve keeps its old contract.
func TestApprove_AlwaysStillPersistsRule(t *testing.T) {
	a, _ := newAlwaysTestApp(t, "claude", nil)
	a.pending = map[string]agent.ApprovalReq{
		"r1:ws": {ReqID: "r1", Tool: "Bash", Input: "make", InputHash: hashInput("make")},
	}
	if err := a.Approve("r1:ws", "always"); err != nil {
		t.Fatal(err)
	}
	s, _ := a.GetSettings()
	if len(s.AlwaysRules) != 1 {
		t.Fatalf("rules = %+v, want one", s.AlwaysRules)
	}
}

// A preference save carries a snapshot of the rules read before a later
// "always" grant. SaveSettings must keep the rule on disk, not the snapshot.
func TestSaveSettings_KeepsRulesGrantedSinceSnapshot(t *testing.T) {
	cfgDir := t.TempDir()
	a := &App{settingsPath: filepath.Join(cfgDir, "settings.json")}
	snapshot, err := a.GetSettings() // no rules yet
	if err != nil {
		t.Fatal(err)
	}
	granted := AlwaysRule{Agent: "claude", Tool: "Bash", Pattern: "ls", Hash: hashInput("ls")}
	if err := a.saveSettingsLocked(Settings{AlwaysRules: []AlwaysRule{granted}}); err != nil {
		t.Fatal(err)
	}
	snapshot.Theme = "gruvbox"
	if err := a.SaveSettings(snapshot); err != nil {
		t.Fatal(err)
	}
	got, err := a.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != "gruvbox" {
		t.Errorf("theme not saved: %q", got.Theme)
	}
	if len(got.AlwaysRules) != 1 || got.AlwaysRules[0].Hash != granted.Hash {
		t.Errorf("rule granted after the snapshot was lost: %+v", got.AlwaysRules)
	}
}
