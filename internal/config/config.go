// Package config implements two-layer TOML configuration loading for perch:
// a global config (~/.config/perch/config.toml) overlaid by a per-project
// .perch.toml discovered by walking up from the start directory.
//
// Security boundary: agent binary paths are global-only. projectConfig has no
// binary-related field, so a malicious .perch.toml that sets agent_bin or an
// [agents] table is structurally ignored by BurntSushi/toml — unknown keys are
// silently dropped, making the override impossible rather than merely discouraged.
package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Miniature-Pug/perch/internal/model"
)

// defaultSortOrder is the priority order for the left-panel project list.
var defaultSortOrder = []string{"running", "pinned", "frecency"}

// defaultAccent is the default UI accent colour.
const defaultAccent = "#EE6FF8"

// defaultRefreshMs is the status-tick interval in milliseconds.
const defaultRefreshMs = 1000

// ── Internal decode targets ───────────────────────────────────────────────────

// globalConfig is the TOML target for $XDG_CONFIG_HOME/perch/config.toml.
// It is the ONLY struct that carries agent binary resolution (Agents map).
// projectConfig deliberately has no equivalent field — this is the security boundary.
type globalConfig struct {
	Roots       []string `toml:"roots"`
	SortOrder   []string `toml:"sort_order"`
	Blacklist   []string `toml:"blacklist"`
	RefreshMs   *int     `toml:"refresh_ms"`
	WorktreeDir *string  `toml:"worktree_dir"`

	DefaultSession globalDefaultSession `toml:"default_session"`
	Theme          globalTheme          `toml:"theme"`

	// Agents maps a known tool name ("claude", "opencode") to an absolute binary
	// path. This field exists ONLY on globalConfig — project configs cannot feed it.
	Agents map[string]string `toml:"agents"`
}

type globalDefaultSession struct {
	Agent          string `toml:"agent"`
	StartupCommand string `toml:"startup_command"`
}

type globalTheme struct {
	Accent string `toml:"accent"`
}

// projectConfig is the TOML target for .perch.toml files found in the repo.
//
// SECURITY: this struct intentionally has NO binary-path field. Any key that
// looks like agent_bin, [agents], or similar in a .perch.toml is an unknown key
// and is silently dropped by BurntSushi/toml. The drop is structural, not a
// runtime check — it is impossible for project config to influence binary resolution.
type projectConfig struct {
	BaseBranch  *string  `toml:"base_branch"`
	WorktreeDir *string  `toml:"worktree_dir"`
	Agent       *string  `toml:"agent"`
	PostCreate  []string `toml:"post_create"`
	PreRemove   []string `toml:"pre_remove"`

	Files     projectFiles   `toml:"files"`
	Wildcards []wildcardRule `toml:"wildcard"`
}

type projectFiles struct {
	Copy    []string `toml:"copy"`
	Symlink []string `toml:"symlink"`
}

type wildcardRule struct {
	Pattern string     `toml:"pattern"`
	Agent   model.Tool `toml:"agent"`
}

// ── Public API ────────────────────────────────────────────────────────────────

// Theme holds display preferences merged from global config.
type Theme struct {
	Accent string
}

// Files holds per-worktree file management settings from project config.
type Files struct {
	Copy    []string
	Symlink []string
}

// WildcardRule is a sesh-style path-glob rule that assigns an agent to matched directories.
type WildcardRule struct {
	Pattern string
	Agent   model.Tool
}

// Config is the merged, public configuration consumed by the rest of perch.
// Callers should treat it as read-only after Load returns.
type Config struct {
	// Roots is the list of directories perch scans for git repos.
	Roots []string
	// SortOrder is the priority list for the left-panel project selector.
	SortOrder []string
	// Blacklist contains glob patterns that hide matching paths from the UI.
	Blacklist []string
	// RefreshMs is the status-tick interval in milliseconds.
	RefreshMs int
	// Agent is the default AI tool for new windows (project overrides global).
	Agent model.Tool
	// StartupCommand is an optional command sent to the agent after launch.
	StartupCommand string
	// Theme holds display preferences.
	Theme Theme

	// Per-project fields — zero values when no .perch.toml is found.
	BaseBranch  string
	WorktreeDir string
	Files       Files
	PostCreate  []string
	PreRemove   []string
	Wildcards   []WildcardRule

	// ProjectConfigPath is the absolute path of the .perch.toml that was loaded,
	// or empty when no project config was found.
	ProjectConfigPath string
	// ProjectConfigHash is the hex sha256 of the .perch.toml bytes, keyed to
	// the same file as ProjectConfigPath. Empty when no project config was found.
	// Do not import internal/trust from this package; the hash is computed inline
	// to avoid an import cycle.
	ProjectConfigHash string

	// agentBins is sourced exclusively from the global config. It is unexported
	// so callers cannot mutate it; access is via AgentBinary.
	agentBins map[string]string
}

// AgentBinary returns the binary path for tool t. It uses the global-configured
// path when available, otherwise returns the bare tool name for $PATH resolution
// by the caller. Project config never feeds this method.
func (c *Config) AgentBinary(t model.Tool) string {
	if path, ok := c.agentBins[string(t)]; ok && path != "" {
		return path
	}
	return string(t)
}

// Validate checks that path-like project fields are safe. Two distinct rules apply:
//   - files.copy and files.symlink paths must stay inside the repo: no absolute
//     paths and no ".." components after filepath.Clean.
//   - worktree_dir may be a sibling, relative, or absolute path outside the repo
//     (the default placement is a sibling directory); it is rejected only if the
//     resolved path lands inside the repo's .git directory.
//
// Call after Load when repoRoot is known.
func (c *Config) Validate(repoRoot string) error {
	// worktree_dir is exempt from the in-repo guard; it may be a sibling or an
	// absolute path outside the repo. Only .git containment is forbidden.
	if err := validateWorktreeDir(repoRoot, c.WorktreeDir); err != nil {
		return err
	}
	for _, p := range c.Files.Copy {
		if err := checkSafe("files.copy", p); err != nil {
			return err
		}
	}
	for _, p := range c.Files.Symlink {
		if err := checkSafe("files.symlink", p); err != nil {
			return err
		}
	}
	return nil
}

// validateWorktreeDir rejects worktree_dir values that resolve into the repo's
// .git directory. Everything else — siblings, absolute paths outside the repo,
// or in-repo relative paths that don't touch .git — is permitted.
//
// Rationale: the default worktree placement is <project>__worktrees/<handle>,
// a sibling directory outside the repo, so worktree_dir cannot be constrained
// to inside the repo. The only hard prohibition is landing inside .git, which
// would corrupt Git's internals.
func validateWorktreeDir(repoRoot, worktreeDir string) error {
	if worktreeDir == "" {
		return nil
	}

	// Resolve to an absolute path: absolute inputs are used as-is; relative
	// inputs are joined onto repoRoot.
	var resolved string
	if filepath.IsAbs(worktreeDir) {
		resolved = filepath.Clean(worktreeDir)
	} else {
		resolved = filepath.Clean(filepath.Join(repoRoot, worktreeDir))
	}

	gitDir := filepath.Join(filepath.Clean(repoRoot), ".git")

	rel, err := filepath.Rel(gitDir, resolved)
	if err != nil {
		// Cannot compute a relative path — resolved is on a different volume
		// (Windows) or otherwise unrelatable. Treat as outside .git.
		return nil
	}
	// rel == "." means resolved IS gitDir; rel not starting with ".." means it
	// is inside gitDir. Use the same prefix idiom as checkSafe for consistency.
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
		return fmt.Errorf("config: worktree_dir %q must not be inside the repo's .git directory", worktreeDir)
	}
	return nil
}

// checkSafe rejects absolute paths and any path that after cleaning traverses
// above its start (i.e. begins with "..").
func checkSafe(field, p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("config: %s %q must be a relative path, not absolute", field, p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("config: %s %q escapes the repo root via '..'; paths must stay inside the repo", field, p)
	}
	return nil
}

// ── Loader ────────────────────────────────────────────────────────────────────

// DefaultGlobalPath returns the canonical path for the global config.toml
// following the XDG Base Directory spec: $XDG_CONFIG_HOME/perch/config.toml,
// falling back to ~/.config/perch/config.toml when XDG_CONFIG_HOME is unset.
func DefaultGlobalPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: resolve home dir for default global path: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "perch", "config.toml"), nil
}

// Load assembles a Config from two sources:
//   - globalPath: path to the global config.toml. An empty string or a
//     non-existent file is not an error; defaults fill the gap.
//   - projectStartDir: directory to begin walking up from looking for .perch.toml.
//     Walk stops at the filesystem root or at the nearest directory containing .git.
//
// Merge semantics: defaults → global → project (per-field, project wins when non-nil).
// Caller provides both paths explicitly; Load has no hidden global state.
func Load(globalPath string, projectStartDir string) (*Config, error) {
	gc, err := loadGlobal(globalPath)
	if err != nil {
		return nil, err
	}

	pc, configPath, configBytes, err := findAndLoadProject(projectStartDir)
	if err != nil {
		return nil, err
	}

	return merge(gc, pc, projectStartDir, configPath, configBytes)
}

// loadGlobal reads the global config file. A missing file returns a zero globalConfig.
func loadGlobal(globalPath string) (*globalConfig, error) {
	gc := &globalConfig{}
	if globalPath == "" {
		return gc, nil
	}
	_, err := os.Stat(globalPath)
	if errors.Is(err, os.ErrNotExist) {
		return gc, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: stat global config %q: %w", globalPath, err)
	}
	if _, err := toml.DecodeFile(globalPath, gc); err != nil {
		return nil, fmt.Errorf("config: parse global config %q: %w", globalPath, err)
	}
	return gc, nil
}

// findAndLoadProject walks up from startDir looking for .perch.toml. Stops when
// it reaches a .git boundary or the filesystem root. Returns nil when not found.
// On success it also returns the resolved absolute path and raw bytes of the file
// so callers can compute a stable content hash without a second read.
func findAndLoadProject(startDir string) (*projectConfig, string, []byte, error) {
	if startDir == "" {
		return nil, "", nil, nil
	}

	dir := filepath.Clean(startDir)
	for {
		candidate := filepath.Join(dir, ".perch.toml")
		_, err := os.Stat(candidate)
		if err == nil {
			// Found — read and decode it.
			raw, err := os.ReadFile(candidate)
			if err != nil {
				return nil, "", nil, fmt.Errorf("config: read project config %q: %w", candidate, err)
			}
			pc := &projectConfig{}
			if _, err := toml.Decode(string(raw), pc); err != nil {
				return nil, "", nil, fmt.Errorf("config: parse project config %q: %w", candidate, err)
			}
			return pc, candidate, raw, nil
		}
		// Only treat "not found" as "keep walking"; any other error (e.g. permission
		// denied) must surface so the user knows something is wrong.
		if !errors.Is(err, os.ErrNotExist) {
			return nil, "", nil, fmt.Errorf("config: stat project config %q: %w", candidate, err)
		}

		// Stop at a .git boundary (repo root) or filesystem root.
		atBoundary, err := hasGitDir(dir)
		if err != nil {
			return nil, "", nil, err
		}
		if atBoundary || isRoot(dir) {
			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Filesystem root.
			break
		}
		dir = parent
	}
	return nil, "", nil, nil
}

// configHash computes the hex sha256 of a .perch.toml's raw bytes. It is
// intentionally a local helper rather than importing internal/trust, which would
// create an import cycle (trust imports nothing from config). The one-liner is
// identical to trust.Hash; duplication is acceptable here.
func configHash(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// hasGitDir reports whether dir contains a .git entry (directory or file for
// worktrees). Only os.ErrNotExist means "no boundary here"; any other error
// (e.g. permission denied) is returned so the caller can surface it.
func hasGitDir(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("config: stat .git in %q: %w", dir, err)
}

// isRoot reports whether dir is the filesystem root.
func isRoot(dir string) bool {
	return dir == filepath.Dir(dir)
}

// parseAgent converts a non-empty string value from config into a model.Tool.
// An empty string is treated as absent and is not an error — the caller must
// guard with a non-empty check before calling. A non-empty but unrecognised
// value is always an error; the user made a typo and must fix it.
func parseAgent(s string) (model.Tool, error) {
	t := model.Tool(s)
	if t.Valid() {
		return t, nil
	}
	return "", fmt.Errorf("config: unknown agent %q (valid: claude, opencode)", s)
}

// merge applies globalConfig and projectConfig on top of defaults.
// Pointer fields on projectConfig are only applied when non-nil (explicit in TOML).
// configPath and configBytes are the resolved .perch.toml path and its raw bytes;
// both empty when no project config was found.
func merge(gc *globalConfig, pc *projectConfig, startDir string, configPath string, configBytes []byte) (*Config, error) {
	cfg := &Config{
		// Defaults.
		RefreshMs: defaultRefreshMs,
		Agent:     model.ToolClaude,
		Theme:     Theme{Accent: defaultAccent},
		SortOrder: append([]string(nil), defaultSortOrder...),
	}

	// Apply global.
	if len(gc.Roots) > 0 {
		expanded, err := expandRoots(gc.Roots)
		if err != nil {
			return nil, err
		}
		cfg.Roots = expanded
	} else {
		// Default roots to the start directory so callers get something useful.
		if startDir != "" {
			cfg.Roots = []string{startDir}
		}
	}
	if len(gc.SortOrder) > 0 {
		cfg.SortOrder = gc.SortOrder
	}
	if len(gc.Blacklist) > 0 {
		cfg.Blacklist = gc.Blacklist
	}
	if gc.RefreshMs != nil {
		cfg.RefreshMs = *gc.RefreshMs
	}
	if gc.DefaultSession.Agent != "" {
		t, err := parseAgent(gc.DefaultSession.Agent)
		if err != nil {
			return nil, fmt.Errorf("config: global default_session.agent: %w", err)
		}
		cfg.Agent = t
	}
	if gc.DefaultSession.StartupCommand != "" {
		cfg.StartupCommand = gc.DefaultSession.StartupCommand
	}
	if gc.Theme.Accent != "" {
		cfg.Theme.Accent = gc.Theme.Accent
	}
	// agentBins is global-only — never touched by project config.
	cfg.agentBins = gc.Agents

	// Apply global worktree_dir (may be absolute — global config is trusted).
	if gc.WorktreeDir != nil {
		cfg.WorktreeDir = *gc.WorktreeDir
	}

	// Apply project (non-nil pointer fields override; slices replace when non-empty).
	if pc != nil {
		if pc.Agent != nil && *pc.Agent != "" {
			t, err := parseAgent(*pc.Agent)
			if err != nil {
				return nil, fmt.Errorf("config: project agent: %w", err)
			}
			cfg.Agent = t
		}
		if pc.BaseBranch != nil {
			cfg.BaseBranch = *pc.BaseBranch
		}
		if pc.WorktreeDir != nil {
			// Security (V2'): project config may only set a relative worktree_dir.
			// An absolute path in .perch.toml would allow a malicious repo to place
			// the worktree (and any seeded files) anywhere writable on the system.
			// Absolute paths are accepted only from the global/user config above.
			if filepath.IsAbs(*pc.WorktreeDir) {
				return nil, fmt.Errorf("config: project worktree_dir %q must be relative", *pc.WorktreeDir)
			}
			cfg.WorktreeDir = *pc.WorktreeDir
		}
		if len(pc.Files.Copy) > 0 {
			cfg.Files.Copy = pc.Files.Copy
		}
		if len(pc.Files.Symlink) > 0 {
			cfg.Files.Symlink = pc.Files.Symlink
		}
		if len(pc.PostCreate) > 0 {
			cfg.PostCreate = pc.PostCreate
		}
		if len(pc.PreRemove) > 0 {
			cfg.PreRemove = pc.PreRemove
		}
		if len(pc.Wildcards) > 0 {
			cfg.Wildcards = make([]WildcardRule, len(pc.Wildcards))
			for i, w := range pc.Wildcards {
				cfg.Wildcards[i] = WildcardRule(w)
			}
		}

		// Record provenance so trust-gate callers can key on path+hash.
		if configPath != "" {
			cfg.ProjectConfigPath = configPath
			cfg.ProjectConfigHash = configHash(configBytes)
		}
	}

	return cfg, nil
}

// expandRoots replaces a leading ~/ with the user's home directory.
// HOME resolution is lazy: os.UserHomeDir is only called when at least one root
// actually needs expansion. If it fails in that case, an error is returned so
// the caller knows the path cannot be made usable.
func expandRoots(roots []string) ([]string, error) {
	out := make([]string, len(roots))
	var home string
	for i, r := range roots {
		if strings.HasPrefix(r, "~/") {
			if home == "" {
				var err error
				home, err = os.UserHomeDir()
				if err != nil {
					return nil, fmt.Errorf("config: expand root %q: cannot resolve home directory: %w", r, err)
				}
			}
			out[i] = filepath.Join(home, r[2:])
		} else {
			out[i] = r
		}
	}
	return out, nil
}
