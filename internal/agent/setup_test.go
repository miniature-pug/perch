package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/resources"
)

// ── mergeClaudeHooks (pure, no I/O) ──────────────────────────────────────────

func TestMergeClaudeHooks_EmptyInput(t *testing.T) {
	out, err := mergeClaudeHooks(nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	hooks, ok := m["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected m.hooks to be a map")
	}

	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "Stop", "Notification"} {
		arr, ok := hooks[event].([]any)
		if !ok {
			t.Errorf("event %q missing or not an array", event)
			continue
		}
		if len(arr) == 0 {
			t.Errorf("event %q has no entries", event)
		}
		if !perchGroupPresent(arr) {
			t.Errorf("event %q has no perch entry", event)
		}
	}
}

func TestMergeClaudeHooks_EmptyObject(t *testing.T) {
	out, err := mergeClaudeHooks([]byte(`{}`), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	hooks, ok := m["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected m.hooks to be a map")
	}
	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "Stop", "Notification"} {
		arr, ok := hooks[event].([]any)
		if !ok || !perchGroupPresent(arr) {
			t.Errorf("event %q: perch entry missing", event)
		}
	}
}

func TestMergeClaudeHooks_CorrectMatchersAndCommands(t *testing.T) {
	out, err := mergeClaudeHooks(nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	hooks := m["hooks"].(map[string]any)

	cases := []struct {
		event, matcher, command string
	}{
		{"PostToolUse", "", "perch status set working"},
		{"UserPromptSubmit", "", "perch status set working"},
		{"Stop", "", "perch status set done"},
		{"Notification", "permission_prompt|elicitation_dialog", "perch status set waiting"},
	}

	for _, tc := range cases {
		arr, ok := hooks[tc.event].([]any)
		if !ok {
			t.Errorf("event %q not an array", tc.event)
			continue
		}
		found := false
		for _, item := range arr {
			group, ok := item.(map[string]any)
			if !ok {
				continue
			}
			matcher, _ := group["matcher"].(string)
			if matcher != tc.matcher {
				continue
			}
			hookList, ok := group["hooks"].([]any)
			if !ok {
				continue
			}
			for _, hRaw := range hookList {
				h, ok := hRaw.(map[string]any)
				if !ok {
					continue
				}
				if h["command"] == tc.command {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("event %q: matcher=%q command=%q not found in output", tc.event, tc.matcher, tc.command)
		}
	}
}

func TestMergeClaudeHooks_PreservesExistingForeignEntry(t *testing.T) {
	// A foreign PostToolUse entry from another tool.
	existing := []byte(`{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "workmux status push"}]
      }
    ]
  }
}`)
	out, err := mergeClaudeHooks(existing, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	hooks := m["hooks"].(map[string]any)
	arr, ok := hooks["PostToolUse"].([]any)
	if !ok {
		t.Fatal("PostToolUse missing")
	}
	// Both the foreign entry and the perch entry must be present.
	foundForeign := false
	foundPerch := false
	for _, item := range arr {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		hookList, _ := group["hooks"].([]any)
		for _, hRaw := range hookList {
			h, ok := hRaw.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := h["command"].(string)
			if cmd == "workmux status push" {
				foundForeign = true
			}
			if strings.Contains(cmd, "perch status set") {
				foundPerch = true
			}
		}
	}
	if !foundForeign {
		t.Error("foreign entry was removed — must preserve it")
	}
	if !foundPerch {
		t.Error("perch entry is missing from PostToolUse")
	}
}

func TestMergeClaudeHooks_Idempotent(t *testing.T) {
	// Running merge twice must yield exactly one perch entry per event.
	first, err := mergeClaudeHooks(nil, false)
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	second, err := mergeClaudeHooks(first, false)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(second, &m); err != nil {
		t.Fatalf("second output not valid JSON: %v", err)
	}
	hooks := m["hooks"].(map[string]any)

	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "Stop", "Notification"} {
		arr, ok := hooks[event].([]any)
		if !ok {
			t.Errorf("event %q missing after two merges", event)
			continue
		}
		// Count entries that contain a perch command.
		perchCount := 0
		for _, item := range arr {
			group, ok := item.(map[string]any)
			if !ok {
				continue
			}
			hookList, _ := group["hooks"].([]any)
			for _, hRaw := range hookList {
				h, ok := hRaw.(map[string]any)
				if !ok {
					continue
				}
				cmd, _ := h["command"].(string)
				if strings.Contains(cmd, "perch status set") {
					perchCount++
				}
			}
		}
		if perchCount != 1 {
			t.Errorf("event %q: expected exactly 1 perch entry after two merges, got %d", event, perchCount)
		}
	}
}

func TestMergeClaudeHooks_MalformedJSON(t *testing.T) {
	_, err := mergeClaudeHooks([]byte(`{not valid json`), false)
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestMergeClaudeHooks_PreservesUnrelatedTopLevelKeys(t *testing.T) {
	existing := []byte(`{"model": "claude-opus-4-5", "theme": "dark"}`)
	out, err := mergeClaudeHooks(existing, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if m["model"] != "claude-opus-4-5" {
		t.Errorf("model key lost; got %v", m["model"])
	}
	if m["theme"] != "dark" {
		t.Errorf("theme key lost; got %v", m["theme"])
	}
}

func TestMergeClaudeHooks_TrailingNewline(t *testing.T) {
	out, err := mergeClaudeHooks(nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) == 0 || out[len(out)-1] != '\n' {
		t.Error("output must end with a trailing newline")
	}
}

// ── Claude.InstallStatusHook (filesystem, HOME-sandboxed) ─────────────────────

func TestClaude_InstallStatusHook_WritesFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "") // don't let a real env var escape the sandbox

	c := NewClaude()
	if err := c.InstallStatusHook(false); err != nil {
		t.Fatalf("InstallStatusHook: %v", err)
	}

	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}

	// Check all four events and at least one perch command present.
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings.json not valid JSON: %v", err)
	}
	hooks, _ := m["hooks"].(map[string]any)
	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "Stop", "Notification"} {
		arr, ok := hooks[event].([]any)
		if !ok || !perchGroupPresent(arr) {
			t.Errorf("event %q: perch hook missing", event)
		}
	}
}

func TestClaude_InstallStatusHook_CreatesClaudeDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	c := NewClaude()
	if err := c.InstallStatusHook(false); err != nil {
		t.Fatalf("InstallStatusHook: %v", err)
	}

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude")
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Errorf("~/.claude directory was not created")
	}
}

func TestClaude_InstallStatusHook_Idempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	c := NewClaude()
	if err := c.InstallStatusHook(false); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := c.InstallStatusHook(false); err != nil {
		t.Fatalf("second call: %v", err)
	}

	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings.json not readable after second install: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings.json not valid JSON after second install: %v", err)
	}
	hooks, _ := m["hooks"].(map[string]any)

	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "Stop", "Notification"} {
		arr, _ := hooks[event].([]any)
		count := 0
		for _, item := range arr {
			group, ok := item.(map[string]any)
			if !ok {
				continue
			}
			hookList, _ := group["hooks"].([]any)
			for _, hRaw := range hookList {
				h, ok := hRaw.(map[string]any)
				if !ok {
					continue
				}
				cmd, _ := h["command"].(string)
				if strings.Contains(cmd, "perch status set") {
					count++
				}
			}
		}
		if count != 1 {
			t.Errorf("event %q: expected 1 perch entry after 2 installs, got %d", event, count)
		}
	}
}

// ── Opencode.InstallStatusHook (filesystem, HOME-sandboxed) ───────────────────

func TestOpencode_InstallStatusHook_WritesPlugin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	o := NewOpencode()
	if err := o.InstallStatusHook(false); err != nil {
		t.Fatalf("InstallStatusHook: %v", err)
	}

	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "opencode", "plugins", "perch-status.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("perch-status.ts not written: %v", err)
	}
	if string(data) != resources.PerchStatusTS {
		t.Errorf("perch-status.ts content mismatch:\ngot: %q\nwant: %q", string(data), resources.PerchStatusTS)
	}
}

func TestOpencode_InstallStatusHook_PreservesSiblingPlugin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "opencode", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(dir, "other-plugin.ts")
	siblingContent := []byte("// some other plugin\n")
	if err := os.WriteFile(sibling, siblingContent, 0o644); err != nil {
		t.Fatal(err)
	}

	o := NewOpencode()
	if err := o.InstallStatusHook(false); err != nil {
		t.Fatalf("InstallStatusHook: %v", err)
	}

	got, err := os.ReadFile(sibling)
	if err != nil {
		t.Fatalf("sibling plugin was removed: %v", err)
	}
	if string(got) != string(siblingContent) {
		t.Errorf("sibling plugin content changed: got %q", got)
	}
}

// ── mergeClaudeHooks: type-guard error cases ──────────────────────────────────

func TestMergeClaudeHooks_HooksIsArray_ReturnsError(t *testing.T) {
	// "hooks" exists but is an array, not an object — must refuse with an error.
	input := []byte(`{"hooks": []}`)
	_, err := mergeClaudeHooks(input, false)
	if err == nil {
		t.Error("expected error when hooks is an array, got nil")
	}
}

func TestMergeClaudeHooks_EventKeyIsObject_ReturnsError(t *testing.T) {
	// hooks.PostToolUse exists but is an object, not an array — must refuse.
	input := []byte(`{"hooks": {"PostToolUse": {"x": 1}}}`)
	_, err := mergeClaudeHooks(input, false)
	if err == nil {
		t.Error("expected error when hooks.PostToolUse is an object, got nil")
	}
}

// ── mergeClaudeHooks: large-integer numeric fidelity ─────────────────────────

func TestMergeClaudeHooks_PreservesLargeInteger(t *testing.T) {
	// A JSON integer > 2^53 must round-trip exactly (not be corrupted to float64).
	const large = "9007199254740993"
	input := []byte(`{"foo": ` + large + `}`)
	out, err := mergeClaudeHooks(input, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, large) {
		t.Errorf("large integer %s was lost in output:\n%s", large, outStr)
	}
}

// ── mergeClaudeHooks: replace mode ───────────────────────────────────────────

// TestMergeClaudeHooks_Replace_RemovesStaleKeepsForeign is the key
// discriminating test: a settings.json containing a stale perch hook (old
// command string that still contains "perch status set") plus a foreign hook
// must, after replace=true, have exactly the current perchHooks entries and no
// stale perch entry, while the foreign entry is fully preserved.
func TestMergeClaudeHooks_Replace_RemovesStaleKeepsForeign(t *testing.T) {
	// Stale perch entry uses an old (non-current) command suffix, but it still
	// contains "perch status set" so isPerchGroup detects it.
	existing := []byte(`{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "old-matcher",
        "hooks": [{"type": "command", "command": "perch status set obsolete-arg"}]
      },
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "workmux status push"}]
      }
    ]
  }
}`)
	out, err := mergeClaudeHooks(existing, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	hooks := m["hooks"].(map[string]any)
	arr, ok := hooks["PostToolUse"].([]any)
	if !ok {
		t.Fatal("PostToolUse missing")
	}

	foundForeign := false
	perchCount := 0
	foundStale := false
	for _, item := range arr {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		hookList, _ := group["hooks"].([]any)
		for _, hRaw := range hookList {
			h, ok := hRaw.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := h["command"].(string)
			if cmd == "workmux status push" {
				foundForeign = true
			}
			if strings.Contains(cmd, "perch status set") {
				perchCount++
				if cmd == "perch status set obsolete-arg" {
					foundStale = true
				}
			}
		}
	}

	if !foundForeign {
		t.Error("foreign entry was removed — must be preserved in replace mode")
	}
	if foundStale {
		t.Error("stale perch entry still present after replace — must be removed")
	}
	if perchCount != 1 {
		t.Errorf("expected exactly 1 current perch entry in PostToolUse after replace, got %d", perchCount)
	}

	// Verify the retained perch entry uses the current command.
	wantCmd := "perch status set working" // perchHooks[0].command for PostToolUse
	found := false
	for _, item := range arr {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		hookList, _ := group["hooks"].([]any)
		for _, hRaw := range hookList {
			h, ok := hRaw.(map[string]any)
			if !ok {
				continue
			}
			if h["command"] == wantCmd {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("current perch command %q not found after replace", wantCmd)
	}
}

// TestMergeClaudeHooks_Replace_Idempotent ensures two successive replace=true
// calls yield byte-identical output — no duplicate perch entries and fully
// stable output.
func TestMergeClaudeHooks_Replace_Idempotent(t *testing.T) {
	first, err := mergeClaudeHooks(nil, true)
	if err != nil {
		t.Fatalf("first replace: %v", err)
	}
	second, err := mergeClaudeHooks(first, true)
	if err != nil {
		t.Fatalf("second replace: %v", err)
	}

	// Byte-identical: MarshalIndent sorts map keys and dropPerchGroups preserves
	// order, so two successive replace calls must produce exactly the same bytes.
	if !bytes.Equal(first, second) {
		t.Errorf("replace is not byte-idempotent:\nfirst:  %s\nsecond: %s", first, second)
	}

	var m map[string]any
	if err := json.Unmarshal(second, &m); err != nil {
		t.Fatalf("second output not valid JSON: %v", err)
	}
	hooks := m["hooks"].(map[string]any)

	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "Stop", "Notification"} {
		arr, ok := hooks[event].([]any)
		if !ok {
			t.Errorf("event %q missing after two replace calls", event)
			continue
		}
		perchCount := 0
		for _, item := range arr {
			group, ok := item.(map[string]any)
			if !ok {
				continue
			}
			hookList, _ := group["hooks"].([]any)
			for _, hRaw := range hookList {
				h, ok := hRaw.(map[string]any)
				if !ok {
					continue
				}
				cmd, _ := h["command"].(string)
				if strings.Contains(cmd, "perch status set") {
					perchCount++
				}
			}
		}
		if perchCount != 1 {
			t.Errorf("event %q: expected 1 perch entry after 2 replace calls, got %d", event, perchCount)
		}
	}
}

// TestMergeClaudeHooks_Replace_RefuseMalformed ensures replace mode still
// refuses a malformed settings.json without writing anything (the refusal
// happens before any I/O, so no temp file is created).
func TestMergeClaudeHooks_Replace_RefuseMalformed(t *testing.T) {
	_, err := mergeClaudeHooks([]byte(`{not valid json`), true)
	if err == nil {
		t.Error("expected error for malformed JSON in replace mode, got nil")
	}
}

// TestMergeClaudeHooks_Replace_PreservesLargeInteger verifies UseNumber
// fidelity is preserved in replace mode (large integers must not be corrupted).
func TestMergeClaudeHooks_Replace_PreservesLargeInteger(t *testing.T) {
	const large = "9007199254740993"
	input := []byte(`{"foo": ` + large + `}`)
	out, err := mergeClaudeHooks(input, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), large) {
		t.Errorf("large integer %s was lost in replace mode output:\n%s", large, string(out))
	}
}

// ── Claude.InstallStatusHook: replace mode (filesystem) ──────────────────────

// TestClaude_InstallStatusHook_Replace_RemovesStaleKeepsForeign is the
// end-to-end filesystem variant of the replace test: a real settings.json with
// a stale perch entry + a foreign entry → replace re-installs only current
// perch hooks; foreign entry survives; file mode preserved.
func TestClaude_InstallStatusHook_Replace_RemovesStaleKeepsForeign(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")

	staleSettings := []byte(`{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "stale-matcher",
        "hooks": [{"type": "command", "command": "perch status set old-status"}]
      },
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "foreign-tool run"}]
      }
    ]
  }
}`)
	if err := os.WriteFile(path, staleSettings, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	c := NewClaude()
	if err := c.InstallStatusHook(true); err != nil {
		t.Fatalf("InstallStatusHook(replace=true): %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings.json after replace: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings.json not valid JSON after replace: %v", err)
	}

	hooks := m["hooks"].(map[string]any)
	arr, ok := hooks["PostToolUse"].([]any)
	if !ok {
		t.Fatal("PostToolUse missing after replace")
	}

	foundForeign := false
	foundStale := false
	perchCount := 0
	for _, item := range arr {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		hookList, _ := group["hooks"].([]any)
		for _, hRaw := range hookList {
			h, ok := hRaw.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := h["command"].(string)
			if cmd == "foreign-tool run" {
				foundForeign = true
			}
			if strings.Contains(cmd, "perch status set") {
				perchCount++
				if cmd == "perch status set old-status" {
					foundStale = true
				}
			}
		}
	}

	if !foundForeign {
		t.Error("foreign entry was removed — must be preserved in replace mode")
	}
	if foundStale {
		t.Error("stale perch entry still present after replace")
	}
	if perchCount != 1 {
		t.Errorf("PostToolUse: expected 1 current perch entry after replace, got %d", perchCount)
	}

	// All four events must have exactly one current perch entry.
	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "Stop", "Notification"} {
		arr, ok := hooks[event].([]any)
		if !ok {
			t.Errorf("event %q missing after replace", event)
			continue
		}
		if !perchGroupPresent(arr) {
			t.Errorf("event %q: perch entry missing after replace", event)
		}
	}

	// File mode must be preserved (was 0640).
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat settings.json: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Errorf("file mode after replace: got %04o, want 0640", got)
	}
}

// ── Opencode.InstallStatusHook: replace mode (filesystem) ────────────────────

// TestOpencode_InstallStatusHook_Replace_OverwritesAndPreservesMode ensures
// replace=true overwrites perch-status.ts with the current embedded content
// and preserves the existing file mode.
func TestOpencode_InstallStatusHook_Replace_OverwritesAndPreservesMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "opencode", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "perch-status.ts")

	// Write stale content with a custom mode.
	staleContent := []byte("// stale content\n")
	if err := os.WriteFile(path, staleContent, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	o := NewOpencode()
	if err := o.InstallStatusHook(true); err != nil {
		t.Fatalf("InstallStatusHook(replace=true): %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read perch-status.ts after replace: %v", err)
	}
	if string(got) != resources.PerchStatusTS {
		t.Errorf("perch-status.ts content mismatch after replace:\ngot: %q\nwant: %q", string(got), resources.PerchStatusTS)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat perch-status.ts: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Errorf("file mode after replace: got %04o, want 0640", got)
	}
}

// ── Claude.InstallStatusHook: mode preservation ───────────────────────────────

func TestClaude_InstallStatusHook_PreservesFileMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")

	// Write a settings.json with mode 0644 and chmod explicitly to defeat umask.
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewClaude()
	if err := c.InstallStatusHook(false); err != nil {
		t.Fatalf("InstallStatusHook: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat settings.json: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Errorf("file mode after InstallStatusHook: got %04o, want 0644", got)
	}
}
