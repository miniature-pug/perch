package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Miniature-Pug/perch/internal/config"
	"github.com/Miniature-Pug/perch/internal/model"
)

// writeFile is a test helper that writes content to a file path, creating dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// initGitDir creates a bare .git directory so Load treats the dir as a repo root.
func initGitDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
}

// TestDefaults verifies that missing global and project configs produce well-defined defaults.
func TestDefaults(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	cfg, err := config.Load("", tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agent != model.ToolClaude {
		t.Errorf("default Agent = %q; want %q", cfg.Agent, model.ToolClaude)
	}
	if cfg.RefreshMs != 1000 {
		t.Errorf("default RefreshMs = %d; want 1000", cfg.RefreshMs)
	}
	if cfg.Theme.Accent != "#EE6FF8" {
		t.Errorf("default Accent = %q; want #EE6FF8", cfg.Theme.Accent)
	}
	// Assert the full slice, not just its length — a reordered or wrong default would fail here.
	// "pinned" was an unplanned tier and has been removed; the default is now ["running","frecency"].
	want := []string{"running", "frecency"}
	if !reflect.DeepEqual(cfg.SortOrder, want) {
		t.Errorf("default SortOrder = %v; want %v", cfg.SortOrder, want)
	}
}

// TestMissingGlobal verifies that an absent global config.toml is not an error.
func TestMissingGlobal(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	cfg, err := config.Load(filepath.Join(tmp, "nonexistent", "config.toml"), tmp)
	if err != nil {
		t.Fatalf("missing global should not error: %v", err)
	}
	// Must still produce defaults.
	if cfg.RefreshMs != 1000 {
		t.Errorf("RefreshMs = %d; want 1000 (defaults)", cfg.RefreshMs)
	}
}

// TestGlobalOnly verifies that a global config.toml is applied with no project override.
func TestGlobalOnly(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `
roots        = ["~/projects"]
sort_order   = ["running","pinned"]
refresh_ms   = 2000

[default_session]
agent = "opencode"

[theme]
accent = "#FF0000"
`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.RefreshMs != 2000 {
		t.Errorf("RefreshMs = %d; want 2000", cfg.RefreshMs)
	}
	if cfg.Agent != model.ToolOpencode {
		t.Errorf("Agent = %q; want opencode", cfg.Agent)
	}
	if cfg.Theme.Accent != "#FF0000" {
		t.Errorf("Accent = %q; want #FF0000", cfg.Theme.Accent)
	}
	if len(cfg.SortOrder) != 2 {
		t.Errorf("SortOrder len = %d; want 2", len(cfg.SortOrder))
	}
}

// TestProjectOverride verifies that a project config overrides the global agent.
func TestProjectOverride(t *testing.T) {
	tmp := t.TempDir()
	// Set up: repo root with .git, project .perch.toml inside.
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `
[default_session]
agent = "claude"
`)

	writeFile(t, filepath.Join(tmp, ".perch.toml"), `
agent = "opencode"
`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agent != model.ToolOpencode {
		t.Errorf("Agent = %q; want opencode (project override)", cfg.Agent)
	}
}

// TestNonOverriddenField verifies that a global field absent from project config is kept.
func TestNonOverriddenField(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `refresh_ms = 1000`)
	// Project does NOT set refresh_ms.
	writeFile(t, filepath.Join(tmp, ".perch.toml"), `agent = "opencode"`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RefreshMs != 1000 {
		t.Errorf("RefreshMs = %d; want 1000 (global, not overridden)", cfg.RefreshMs)
	}
}

// TestSecurityBinaryPath verifies the type-level guarantee: a project .perch.toml
// that attempts to set an agent binary path is structurally ignored — projectConfig
// has no binary field, so BurntSushi/toml silently drops the unknown key.
// The global path must remain in effect.
func TestSecurityBinaryPath(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	// Global explicitly maps the claude binary to a known trusted path.
	writeFile(t, globalPath, `
[agents]
claude   = "/usr/local/bin/claude"
opencode = "/usr/local/bin/opencode"
`)

	// Malicious project config tries to override the binary path.
	// projectConfig has no agents/agent_bin field — this key is silently dropped.
	writeFile(t, filepath.Join(tmp, ".perch.toml"), `
agent = "claude"

[agents]
claude = "/tmp/evil-binary"
`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Must return the GLOBAL path, not the project's attempted override.
	got := cfg.AgentBinary(model.ToolClaude)
	if got != "/usr/local/bin/claude" {
		t.Errorf("AgentBinary(claude) = %q; want /usr/local/bin/claude (project override must be structurally impossible)", got)
	}
}

// TestAgentBinaryFallback verifies that AgentBinary falls back to the bare name
// when no global mapping is configured.
func TestAgentBinaryFallback(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	cfg, err := config.Load("", tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AgentBinary(model.ToolClaude) != "claude" {
		t.Errorf("AgentBinary fallback should be bare name %q", "claude")
	}
	if cfg.AgentBinary(model.ToolOpencode) != "opencode" {
		t.Errorf("AgentBinary fallback should be bare name %q", "opencode")
	}
}

// TestProjectWalkUp verifies that Load walks up from a subdirectory to find .perch.toml.
func TestProjectWalkUp(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)
	writeFile(t, filepath.Join(tmp, ".perch.toml"), `agent = "opencode"`)

	// Start from a nested subdirectory.
	sub := filepath.Join(tmp, "a", "b", "c")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load("", sub)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent != model.ToolOpencode {
		t.Errorf("Agent = %q; want opencode (found by walking up)", cfg.Agent)
	}
}

// TestMalformedGlobal verifies that a syntactically invalid global config returns an error.
func TestMalformedGlobal(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `refresh_ms = [[[broken toml`)

	_, err := config.Load(globalPath, tmp)
	if err == nil {
		t.Error("expected error for malformed global config.toml")
	}
}

// TestMalformedProject verifies that a syntactically invalid project config returns an error.
func TestMalformedProject(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)
	writeFile(t, filepath.Join(tmp, ".perch.toml"), `agent = [[[broken`)

	_, err := config.Load("", tmp)
	if err == nil {
		t.Error("expected error for malformed .perch.toml")
	}
}

// TestPathSafety exercises Validate's path guards and Load-time validation.
//
// worktree_dir uses a distinct rule: project config may only set a relative
// worktree_dir (V2'); an absolute path in the project config is rejected at
// Load time. files.copy and files.symlink must stay inside the repo (no
// absolute, no "..").
func TestPathSafety(t *testing.T) {
	cases := []struct {
		name            string
		perchToml       string
		expectLoadError bool // true when Load itself must error (e.g. project absolute worktree_dir)
		expectError     bool // true when Validate must error (only checked when Load succeeds)
	}{
		// ── worktree_dir: project may only use relative paths ──
		{
			name:        "worktree_dir sibling (dotdot) accepted",
			perchToml:   `worktree_dir = "../wt"`,
			expectError: false,
		},
		{
			// V2': project config may not set an absolute worktree_dir — Load must error.
			name:            "worktree_dir absolute in project config rejected at Load",
			perchToml:       `worktree_dir = "/abs/elsewhere"`,
			expectLoadError: true,
		},
		{
			name:        "worktree_dir in-repo relative (not .git) accepted",
			perchToml:   `worktree_dir = "wt"`,
			expectError: false,
		},
		{
			name:        "worktree_dir pointing at .git/foo rejected",
			perchToml:   `worktree_dir = ".git/foo"`,
			expectError: true,
		},
		{
			name:        "worktree_dir pointing at .git rejected",
			perchToml:   `worktree_dir = ".git"`,
			expectError: true,
		},
		{
			name:        "worktree_dir empty accepted",
			perchToml:   `base_branch = "main"`, // no worktree_dir key → empty
			expectError: false,
		},
		// ── files.copy: must stay inside repo ──
		{
			name: "absolute file in files.copy rejected",
			perchToml: `
[files]
copy = ["/etc/passwd"]
`,
			expectError: true,
		},
		{
			name: "dotdot in files.copy rejected",
			perchToml: `
[files]
copy = ["../../etc/passwd"]
`,
			expectError: true,
		},
		{
			name: "valid files.copy .env accepted",
			perchToml: `
[files]
copy = [".env", ".env.local"]
`,
			expectError: false,
		},
		// ── files.symlink: must stay inside repo ──
		{
			name: "absolute file in files.symlink rejected",
			perchToml: `
[files]
symlink = ["/usr/bin/node"]
`,
			expectError: true,
		},
		{
			name: "valid files.symlink node_modules accepted",
			perchToml: `
[files]
symlink = ["node_modules"]
`,
			expectError: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			initGitDir(t, dir)
			writeFile(t, filepath.Join(dir, ".perch.toml"), tc.perchToml)

			cfg, err := config.Load("", dir)
			if tc.expectLoadError {
				if err == nil {
					t.Error("expected Load to error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load failed unexpectedly: %v", err)
			}

			err = cfg.Validate(dir)
			if tc.expectError && err == nil {
				t.Error("expected validation error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}

// TestValidateWorktreeDir exercises validateWorktreeDir through Validate directly,
// covering cases that would require complex .perch.toml setup otherwise.
func TestValidateWorktreeDir(t *testing.T) {
	cases := []struct {
		name        string
		worktreeDir string
		expectError bool
	}{
		{"sibling via dotdot", "../wt", false},
		{"absolute outside repo", "/abs/elsewhere", false},
		{"in-repo relative not .git", "wt", false},
		{"dotdot into .git/foo", ".git/foo", true},
		{"equals .git", ".git", true},
		{"gitfoo not inside .git", ".gitfoo", false},            // prefix-bug guard: .gitfoo is a sibling of .git, not inside it
		{"dotdot games resolving to .git", "foo/../.git", true}, // Clean must run before the .git check
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			initGitDir(t, dir)

			cfg := &config.Config{WorktreeDir: tc.worktreeDir}
			err := cfg.Validate(dir)
			if tc.expectError && err == nil {
				t.Error("expected validation error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}

// TestValidateFilePaths verifies Validate rejects unsafe files.copy/files.symlink
// and accepts safe ones, independent of worktree_dir handling.
func TestValidateFilePaths(t *testing.T) {
	dir := t.TempDir()
	initGitDir(t, dir)

	t.Run("files.copy escape rejected", func(t *testing.T) {
		cfg := &config.Config{Files: config.Files{Copy: []string{"../escape"}}}
		if err := cfg.Validate(dir); err == nil {
			t.Error("expected error for files.copy ../escape")
		}
	})
	t.Run("files.copy absolute rejected", func(t *testing.T) {
		cfg := &config.Config{Files: config.Files{Copy: []string{"/abs"}}}
		if err := cfg.Validate(dir); err == nil {
			t.Error("expected error for files.copy /abs")
		}
	})
	t.Run("files.copy .env accepted", func(t *testing.T) {
		cfg := &config.Config{Files: config.Files{Copy: []string{".env"}}}
		if err := cfg.Validate(dir); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("files.symlink node_modules accepted", func(t *testing.T) {
		cfg := &config.Config{Files: config.Files{Symlink: []string{"node_modules"}}}
		if err := cfg.Validate(dir); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// TestRootsHomeDirExpansion verifies that roots entries with leading ~/ are expanded.
func TestRootsHomeDirExpansion(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `roots = ["~/projects"]`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Roots) == 0 {
		t.Fatal("Roots is empty")
	}
	if strings.HasPrefix(cfg.Roots[0], "~") {
		t.Errorf("Roots[0] = %q; ~ was not expanded", cfg.Roots[0])
	}
}

// TestProjectFilesAndHooks verifies that project file/hook lists merge correctly.
func TestProjectFilesAndHooks(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	// post_create must come before [files] in TOML; once a table header is
	// opened, subsequent bare keys belong to that table, not the document root.
	writeFile(t, filepath.Join(tmp, ".perch.toml"), `
base_branch  = "develop"
worktree_dir = "wt"
post_create  = ["direnv allow"]

[files]
copy    = [".env"]
symlink = ["node_modules"]
`)

	cfg, err := config.Load("", tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %q; want develop", cfg.BaseBranch)
	}
	if cfg.WorktreeDir != "wt" {
		t.Errorf("WorktreeDir = %q; want wt", cfg.WorktreeDir)
	}
	if len(cfg.Files.Copy) != 1 || cfg.Files.Copy[0] != ".env" {
		t.Errorf("Files.Copy = %v; want [.env]", cfg.Files.Copy)
	}
	if len(cfg.Files.Symlink) != 1 || cfg.Files.Symlink[0] != "node_modules" {
		t.Errorf("Files.Symlink = %v; want [node_modules]", cfg.Files.Symlink)
	}
	if len(cfg.PostCreate) != 1 || cfg.PostCreate[0] != "direnv allow" {
		t.Errorf("PostCreate = %v; want [direnv allow]", cfg.PostCreate)
	}
}

// TestWildcardRules verifies that per-project wildcard entries decode correctly.
func TestWildcardRules(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	writeFile(t, filepath.Join(tmp, ".perch.toml"), `
[[wildcard]]
pattern = "**/experiments/*"
agent   = "claude"
`)

	cfg, err := config.Load("", tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Wildcards) != 1 {
		t.Fatalf("Wildcards len = %d; want 1", len(cfg.Wildcards))
	}
	if cfg.Wildcards[0].Pattern != "**/experiments/*" {
		t.Errorf("Pattern = %q; want **/experiments/*", cfg.Wildcards[0].Pattern)
	}
	if cfg.Wildcards[0].Agent != model.ToolClaude {
		t.Errorf("Wildcard Agent = %q; want claude", cfg.Wildcards[0].Agent)
	}
}

// TestDefaultGlobalPath verifies §8.1 XDG discovery: XDG_CONFIG_HOME when set,
// falling back to ~/.config/perch/config.toml when unset.
func TestDefaultGlobalPath(t *testing.T) {
	t.Run("XDG_CONFIG_HOME set", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmp)
		got, err := config.DefaultGlobalPath()
		if err != nil {
			t.Fatalf("DefaultGlobalPath: %v", err)
		}
		want := filepath.Join(tmp, "perch", "config.toml")
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("XDG_CONFIG_HOME unset falls back to ~/.config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		got, err := config.DefaultGlobalPath()
		if err != nil {
			t.Fatalf("DefaultGlobalPath: %v", err)
		}
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".config", "perch", "config.toml")
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})
}

// TestNoProjectToml verifies that no .perch.toml found is not an error.
func TestNoProjectToml(t *testing.T) {
	tmp := t.TempDir()
	// No .git, no .perch.toml — walk will hit root.
	cfg, err := config.Load("", tmp)
	if err != nil {
		t.Fatalf("no project toml should not error: %v", err)
	}
	// Defaults must be present.
	if cfg.Agent != model.ToolClaude {
		t.Errorf("Agent = %q; want claude", cfg.Agent)
	}
}

// TestInvalidAgentInGlobal verifies that an explicitly-set but unrecognised agent
// value in the global config surfaces as a clear error from Load.
func TestInvalidAgentInGlobal(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `
[default_session]
agent = "claud"
`)

	_, err := config.Load(globalPath, tmp)
	if err == nil {
		t.Fatal("expected error for invalid global agent value, got nil")
	}
	if !strings.Contains(err.Error(), "claud") {
		t.Errorf("error %q does not mention the bad value %q", err.Error(), "claud")
	}
}

// TestInvalidAgentInProject verifies that an explicitly-set but unrecognised agent
// value in a project .perch.toml surfaces as a clear error from Load.
func TestInvalidAgentInProject(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	writeFile(t, filepath.Join(tmp, ".perch.toml"), `agent = "opencoode"`)

	_, err := config.Load("", tmp)
	if err == nil {
		t.Fatal("expected error for invalid project agent value, got nil")
	}
	if !strings.Contains(err.Error(), "opencoode") {
		t.Errorf("error %q does not mention the bad value %q", err.Error(), "opencoode")
	}
}

// TestValidAgentStillWorks verifies that a correctly-spelled agent value in either
// config still loads without error after the parseAgent helper was introduced.
func TestValidAgentStillWorks(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `
[default_session]
agent = "opencode"
`)
	writeFile(t, filepath.Join(tmp, ".perch.toml"), `agent = "claude"`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("valid agents should not error: %v", err)
	}
	// Project overrides global — project agent wins.
	if cfg.Agent != model.ToolClaude {
		t.Errorf("Agent = %q; want claude (project overrides global)", cfg.Agent)
	}
}

// TestProjectConfigPath verifies that Load populates ProjectConfigPath and
// ProjectConfigHash when a .perch.toml is found, and that both are empty when no
// project config exists.
func TestProjectConfigPath(t *testing.T) {
	t.Run("with perch.toml", func(t *testing.T) {
		tmp := t.TempDir()
		initGitDir(t, tmp)

		content := `post_create = ["echo hi"]`
		tomlPath := filepath.Join(tmp, ".perch.toml")
		writeFile(t, tomlPath, content)

		cfg, err := config.Load("", tmp)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if cfg.ProjectConfigPath != tomlPath {
			t.Errorf("ProjectConfigPath = %q; want %q", cfg.ProjectConfigPath, tomlPath)
		}
		if cfg.ProjectConfigHash == "" {
			t.Error("ProjectConfigHash must be non-empty when .perch.toml is found")
		}
		// Verify hash is stable across calls.
		cfg2, _ := config.Load("", tmp)
		if cfg.ProjectConfigHash != cfg2.ProjectConfigHash {
			t.Error("ProjectConfigHash must be deterministic")
		}
		// Verify hash changes when content changes.
		writeFile(t, tomlPath, content+" # edited")
		cfg3, _ := config.Load("", tmp)
		if cfg.ProjectConfigHash == cfg3.ProjectConfigHash {
			t.Error("ProjectConfigHash must change when file content changes")
		}
	})

	t.Run("without perch.toml", func(t *testing.T) {
		tmp := t.TempDir()
		initGitDir(t, tmp)

		cfg, err := config.Load("", tmp)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		if cfg.ProjectConfigPath != "" {
			t.Errorf("ProjectConfigPath = %q; want empty when no .perch.toml", cfg.ProjectConfigPath)
		}
		if cfg.ProjectConfigHash != "" {
			t.Errorf("ProjectConfigHash = %q; want empty when no .perch.toml", cfg.ProjectConfigHash)
		}
	})
}

// TestAbsentAgentTakesDefault verifies that omitting agent entirely (absent, not empty
// string) leaves the default in place and does not error.
func TestAbsentAgentTakesDefault(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	// Global with no [default_session] at all, project with no agent key.
	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `refresh_ms = 500`)
	writeFile(t, filepath.Join(tmp, ".perch.toml"), `base_branch = "main"`)

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("absent agent should not error: %v", err)
	}
	if cfg.Agent != model.ToolClaude {
		t.Errorf("Agent = %q; want claude (default)", cfg.Agent)
	}
}

// TestWorktreeDirProvenance is the exploit test for V2'.
// A project .perch.toml must NOT be allowed to set an absolute worktree_dir
// (which would let a malicious repo place worktrees anywhere writable on the system).
// Only the global config may set an absolute worktree_dir.
// This test MUST FAIL on un-fixed code (which accepts project absolute).
func TestWorktreeDirProvenance(t *testing.T) {
	t.Run("project absolute worktree_dir rejected at Load", func(t *testing.T) {
		tmp := t.TempDir()
		initGitDir(t, tmp)
		// Exploit: malicious .perch.toml tries to place worktree at /etc/perch.
		writeFile(t, filepath.Join(tmp, ".perch.toml"), `worktree_dir = "/etc/perch"`)

		_, err := config.Load("", tmp)
		if err == nil {
			t.Error("V2' exploit: Load must error when project sets absolute worktree_dir, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "must be relative") {
			t.Errorf("V2' exploit: error message %q must contain 'must be relative'", err.Error())
		}
	})

	t.Run("global absolute worktree_dir accepted", func(t *testing.T) {
		tmp := t.TempDir()
		initGitDir(t, tmp)
		globalPath := filepath.Join(tmp, "config.toml")
		writeFile(t, globalPath, `worktree_dir = "/abs/worktrees"`)
		// No project .perch.toml — global absolute should be accepted.

		cfg, err := config.Load(globalPath, tmp)
		if err != nil {
			t.Fatalf("global absolute worktree_dir should be accepted: %v", err)
		}
		if cfg.WorktreeDir != "/abs/worktrees" {
			t.Errorf("WorktreeDir = %q; want /abs/worktrees", cfg.WorktreeDir)
		}
	})

	t.Run("project relative worktree_dir accepted", func(t *testing.T) {
		tmp := t.TempDir()
		initGitDir(t, tmp)
		writeFile(t, filepath.Join(tmp, ".perch.toml"), `worktree_dir = "wt"`)

		cfg, err := config.Load("", tmp)
		if err != nil {
			t.Fatalf("project relative worktree_dir should be accepted: %v", err)
		}
		if cfg.WorktreeDir != "wt" {
			t.Errorf("WorktreeDir = %q; want wt", cfg.WorktreeDir)
		}
		// The .git containment guard must still apply.
		if err := cfg.Validate(tmp); err != nil {
			t.Errorf("Validate unexpectedly errored for relative wt: %v", err)
		}
	})

	t.Run("project absolute rejected even if not .git", func(t *testing.T) {
		// Defense-in-depth: even a benign-looking absolute path like /tmp/wt
		// must be rejected if it comes from the project config.
		tmp := t.TempDir()
		initGitDir(t, tmp)
		writeFile(t, filepath.Join(tmp, ".perch.toml"), `worktree_dir = "/tmp/wt"`)

		_, err := config.Load("", tmp)
		if err == nil {
			t.Error("project absolute worktree_dir /tmp/wt must be rejected")
		}
	})
}

// TestExpandRootsNoTildeHomeMissing verifies that when no root contains a leading ~/,
// a missing HOME directory is harmless and Load succeeds.
func TestExpandRootsNoTildeHomeMissing(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	// Use an absolute root (no tilde) — HOME resolution must not be attempted.
	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `roots = ["`+tmp+`"]`)

	// Unset HOME so os.UserHomeDir would fail if called.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", tmp) // prevent DefaultGlobalPath from needing HOME

	cfg, err := config.Load(globalPath, tmp)
	if err != nil {
		t.Fatalf("non-tilde roots with missing HOME should not error: %v", err)
	}
	if len(cfg.Roots) == 0 || cfg.Roots[0] != tmp {
		t.Errorf("Roots = %v; want [%s]", cfg.Roots, tmp)
	}
}

// TestExpandRootsTildeHomeMissing verifies that when a root needs ~/ expansion but
// HOME cannot be resolved, Load returns a clear error.
func TestExpandRootsTildeHomeMissing(t *testing.T) {
	tmp := t.TempDir()
	initGitDir(t, tmp)

	globalPath := filepath.Join(tmp, "config.toml")
	writeFile(t, globalPath, `roots = ["~/projects"]`)

	// Force HOME to be empty so os.UserHomeDir fails.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", tmp)

	_, err := config.Load(globalPath, tmp)
	if err == nil {
		t.Fatal("expected error when ~/ root cannot be expanded due to missing HOME, got nil")
	}
	if !strings.Contains(err.Error(), "home") && !strings.Contains(err.Error(), "HOME") {
		t.Errorf("error %q does not mention home directory resolution", err.Error())
	}
}
