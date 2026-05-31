package doctor

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// ── fakeSystem ────────────────────────────────────────────────────────────────

// fakeSystem implements the system interface for tests. Every field may be
// set per-test; zero values produce sensible "nothing found" defaults.
type fakeSystem struct {
	// lookPathFn, if non-nil, is called by lookPath. Defaults to returning "not found".
	paths map[string]string // tool → absolute path; missing = not found

	// outputs maps "name args[0] args[1]..." to (stdout, error).
	outputs map[string]fakeOutput

	// homeDir value
	home    string
	homeErr error

	// statPaths: paths that exist (stat returns nil); others return ErrNotExist.
	statPaths map[string]bool

	// readFiles: maps path → content. Missing key returns ErrNotExist.
	readFiles map[string][]byte
	// readFileErr maps path → custom error (overrides readFiles logic).
	readFileErr map[string]error
}

type fakeOutput struct {
	out []byte
	err error
}

func (f *fakeSystem) lookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", errors.New("not found: " + name)
}

func (f *fakeSystem) output(name string, args ...string) ([]byte, error) {
	key := name
	for _, a := range args {
		key += " " + a
	}
	if fo, ok := f.outputs[key]; ok {
		return fo.out, fo.err
	}
	return nil, errors.New("no output configured for: " + key)
}

func (f *fakeSystem) homeDir() (string, error) {
	return f.home, f.homeErr
}

func (f *fakeSystem) stat(path string) error {
	if f.statPaths[path] {
		return nil
	}
	return os.ErrNotExist
}

func (f *fakeSystem) readFile(path string) ([]byte, error) {
	if err, ok := f.readFileErr[path]; ok {
		return nil, err
	}
	if data, ok := f.readFiles[path]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

// fullSystem returns a fakeSystem where everything is present and healthy.
// Tests override individual fields to simulate failures.
func fullSystem(home string) *fakeSystem {
	claudeSettingsPath := home + "/.claude/settings.json"
	opencodePluginPath := home + "/.config/opencode/plugins/perch-status.ts"
	claudeSettingsContent, _ := json.Marshal(map[string]interface{}{
		"hooks": map[string]interface{}{
			"Notification": []interface{}{
				map[string]interface{}{"command": "perch status set done"},
			},
		},
	})

	return &fakeSystem{
		home: home,
		paths: map[string]string{
			"go":       "/usr/local/go/bin/go",
			"tmux":     "/usr/bin/tmux",
			"git":      "/usr/bin/git",
			"claude":   "/home/user/.local/bin/claude",
			"opencode": "/home/user/.local/bin/opencode",
		},
		outputs: map[string]fakeOutput{
			"go version":         {out: []byte("go version go1.26.3 linux/amd64")},
			"tmux -V":            {out: []byte("tmux 3.6")},
			"git --version":      {out: []byte("git version 2.43.0")},
			"claude --version":   {out: []byte("2.1.158")},
			"opencode --version": {out: []byte("1.15.12")},
			"tmux list-sessions": {out: []byte("sess: 1 windows"), err: nil},
		},
		statPaths: map[string]bool{
			claudeSettingsPath: true,
			opencodePluginPath: true,
		},
		readFiles: map[string][]byte{
			claudeSettingsPath: claudeSettingsContent,
		},
	}
}

// ── ParseToolVersions ─────────────────────────────────────────────────────────

func TestParseToolVersions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "normal input",
			input: "golang 1.26.2\ntmux 3.6\nclaude 2.1.158\nopencode 1.15.12\n",
			want: map[string]string{
				"golang":   "1.26.2",
				"tmux":     "3.6",
				"claude":   "2.1.158",
				"opencode": "1.15.12",
			},
		},
		{
			name:  "blank lines and comments skipped",
			input: "golang 1.26.2\n\n# comment\ntmux 3.6\n   \n",
			want: map[string]string{
				"golang": "1.26.2",
				"tmux":   "3.6",
			},
		},
		{
			name:  "garbage line (no space) skipped",
			input: "golang 1.26.2\ngarbageline\ntmux 3.6\n",
			want: map[string]string{
				"golang": "1.26.2",
				"tmux":   "3.6",
			},
		},
		{
			name:  "empty input",
			input: "",
			want:  map[string]string{},
		},
		{
			name:  "windows line endings",
			input: "golang 1.26.2\r\ntmux 3.6\r\n",
			want: map[string]string{
				"golang": "1.26.2",
				"tmux":   "3.6",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseToolVersions(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %d, want %d (got=%v, want=%v)", len(got), len(tc.want), got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// ── compareVersions ───────────────────────────────────────────────────────────

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.26.3", "1.26.2", 1},
		{"1.26.2", "1.26.3", -1},
		{"1.26.2", "1.26.2", 0},
		{"1.27.0", "1.26.99", 1},
		{"2.0.0", "1.99.99", 1},
		// Missing components treated as 0.
		{"1.26", "1.26.0", 0},
		{"1.26.1", "1.26", 1},
		{"1.26", "1.26.1", -1},
		// Non-numeric suffixes stripped.
		{"3.6a", "3.6", 0},
		{"2.1.158-foo", "2.1.158", 0},
		{"3.7a", "3.6", 1},
		// Multi-component.
		{"1.0.0", "1.0.0", 0},
		{"10.0.0", "9.99.99", 1},
	}
	for _, tc := range tests {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			got := compareVersions(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// ── extractVersionToken ───────────────────────────────────────────────────────

func TestExtractVersionToken(t *testing.T) {
	tests := []struct {
		raw  string
		want string // empty means "not found" → empty string back
	}{
		{"go version go1.26.3 linux/amd64", "1.26.3"},
		{"tmux 3.6", "3.6"},
		{"git version 2.43.0", "2.43.0"},
		{"2.1.158", "2.1.158"},
		{"Claude Code 2.1.158 (build abc)", "2.1.158"},
		{"some tool v1.2.3-beta", "1.2.3"},
		{"no version here", ""},
		{"", ""},
		{"v1.0", "1.0"},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got := extractVersionToken(tc.raw)
			if got != tc.want {
				t.Errorf("extractVersionToken(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// ── Exit code matrix ──────────────────────────────────────────────────────────

func TestRunExitCode_AllPresent(t *testing.T) {
	sys := fullSystem("/home/tester")
	code := Run("v0.1.0-dev", io.Discard, sys)
	if code != 0 {
		t.Errorf("expected exit 0 when all deps present, got %d", code)
	}
}

func TestRunExitCode_TmuxMissing(t *testing.T) {
	sys := fullSystem("/home/tester")
	delete(sys.paths, "tmux")
	code := Run("v0.1.0-dev", io.Discard, sys)
	if code != 1 {
		t.Errorf("expected exit 1 when tmux missing, got %d", code)
	}
}

func TestRunExitCode_GitMissing(t *testing.T) {
	sys := fullSystem("/home/tester")
	delete(sys.paths, "git")
	code := Run("v0.1.0-dev", io.Discard, sys)
	if code != 1 {
		t.Errorf("expected exit 1 when git missing, got %d", code)
	}
}

func TestRunExitCode_BothAgentsMissing(t *testing.T) {
	sys := fullSystem("/home/tester")
	delete(sys.paths, "claude")
	delete(sys.paths, "opencode")
	code := Run("v0.1.0-dev", io.Discard, sys)
	if code != 1 {
		t.Errorf("expected exit 1 when both agents missing, got %d", code)
	}
}

func TestRunExitCode_OneAgentMissing_IsWarn(t *testing.T) {
	sys := fullSystem("/home/tester")
	delete(sys.paths, "opencode")
	var out strings.Builder
	code := Run("v0.1.0-dev", &out, sys)
	if code != 0 {
		t.Errorf("expected exit 0 when one agent missing (other present), got %d", code)
	}
	// The missing agent row should carry the one-of-agents rationale.
	if !strings.Contains(out.String(), "at least one agent is required") {
		t.Errorf("expected one-of-agents message in output; got:\n%s", out.String())
	}
}

func TestRunExitCode_ClaudeMissing_IsWarn(t *testing.T) {
	// Reverse of the opencode-missing case: opencode present, claude absent.
	// Must be exit 0 (warn, not fail) and the claude row must name opencode as sibling.
	sys := fullSystem("/home/tester")
	delete(sys.paths, "claude")
	var out strings.Builder
	code := Run("v0.1.0-dev", &out, sys)
	if code != 0 {
		t.Errorf("expected exit 0 when claude missing and opencode present, got %d", code)
	}
	output := out.String()
	if !strings.Contains(output, "at least one agent is required") {
		t.Errorf("expected one-of-agents message in output; got:\n%s", output)
	}
	if !strings.Contains(output, "ok if opencode present") {
		t.Errorf("expected sibling name 'opencode' in claude-absent message; got:\n%s", output)
	}
}

func TestRunExitCode_GoMissing_IsWarn(t *testing.T) {
	sys := fullSystem("/home/tester")
	delete(sys.paths, "go")
	code := Run("v0.1.0-dev", io.Discard, sys)
	if code != 0 {
		t.Errorf("expected exit 0 when go (build-only) missing, got %d", code)
	}
}

// ── Drift warning logic ───────────────────────────────────────────────────────

func TestRunDrift_NewerInstalled_NoWarn(t *testing.T) {
	// go 1.26.3 installed, pin is 1.26.2 → installed > pinned → go row must be [ok].
	// fullSystem already reports go1.26.3; no override needed.
	sys := fullSystem("/home/tester")
	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()

	// Find the go row by its binary path (tabwriter may vary spacing).
	var goLine string
	for _, l := range strings.Split(output, "\n") {
		if strings.Contains(l, "/usr/local/go/bin/go") {
			goLine = l
			break
		}
	}
	if goLine == "" {
		t.Fatal("go row not found in output:\n" + output)
	}
	if !strings.Contains(goLine, "[ok]") {
		t.Errorf("expected [ok] on go row when installed > pin; got: %s", goLine)
	}
	if strings.Contains(goLine, "[warn]") || strings.Contains(goLine, "(below pin") {
		t.Errorf("unexpected below-pin warn on go row when installed > pin; got: %s", goLine)
	}
}

func TestRunDrift_OlderInstalled_Warn(t *testing.T) {
	sys := fullSystem("/home/tester")
	// Simulate claude at 2.0.0 (below pin 2.1.158)
	sys.outputs["claude --version"] = fakeOutput{out: []byte("2.0.0")}
	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	if !strings.Contains(output, "[warn]") {
		t.Error("expected a warn line for claude below pin")
	}
}

// ── Hooks checks ──────────────────────────────────────────────────────────────

func TestRunHooks_ClaudeSettingsMissing(t *testing.T) {
	sys := fullSystem("/home/tester")
	home := "/home/tester"
	claudeSettingsPath := home + "/.claude/settings.json"
	delete(sys.statPaths, claudeSettingsPath)
	delete(sys.readFiles, claudeSettingsPath)

	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	if !strings.Contains(output, "claude hooks") {
		t.Errorf("expected claude hooks warning when settings.json absent; got:\n%s", output)
	}
}

func TestRunHooks_ClaudeSettingsMalformed(t *testing.T) {
	sys := fullSystem("/home/tester")
	home := "/home/tester"
	claudeSettingsPath := home + "/.claude/settings.json"
	sys.statPaths[claudeSettingsPath] = true
	sys.readFiles[claudeSettingsPath] = []byte("this is not json {{{")

	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	if !strings.Contains(output, "claude hooks") {
		t.Errorf("expected claude hooks warning when settings.json malformed; got:\n%s", output)
	}
}

func TestRunHooks_ClaudeSettingsPresent_NoPerch(t *testing.T) {
	sys := fullSystem("/home/tester")
	home := "/home/tester"
	claudeSettingsPath := home + "/.claude/settings.json"
	// Valid JSON but no perch hook reference.
	data, _ := json.Marshal(map[string]interface{}{"theme": "dark"})
	sys.readFiles[claudeSettingsPath] = data

	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	if !strings.Contains(output, "claude hooks") {
		t.Errorf("expected claude hooks warning when settings.json has no perch hook; got:\n%s", output)
	}
}

func TestRunHooks_ClaudeSettingsOk(t *testing.T) {
	sys := fullSystem("/home/tester")
	// fullSystem already sets up a valid perch-containing settings.json.
	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	// Should NOT have a claude hooks warning.
	lines := strings.Split(output, "\n")
	for _, l := range lines {
		if strings.Contains(l, "[warn]") && strings.Contains(l, "claude hooks") {
			t.Errorf("unexpected claude hooks warn when perch hook present: %s", l)
		}
	}
}

func TestRunHooks_OpencodePluginMissing(t *testing.T) {
	sys := fullSystem("/home/tester")
	home := "/home/tester"
	opencodePluginPath := home + "/.config/opencode/plugins/perch-status.ts"
	delete(sys.statPaths, opencodePluginPath)

	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	if !strings.Contains(output, "perch-status.ts") {
		t.Errorf("expected opencode plugin warning; got:\n%s", output)
	}
}

// ── tmux server check ─────────────────────────────────────────────────────────

func TestRunTmuxServer_Down(t *testing.T) {
	sys := fullSystem("/home/tester")
	sys.outputs["tmux list-sessions"] = fakeOutput{err: errors.New("no server running")}

	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	if !strings.Contains(output, "server not running") {
		t.Errorf("expected tmux server not running warning; got:\n%s", output)
	}
}

func TestRunTmuxServer_Up(t *testing.T) {
	sys := fullSystem("/home/tester")
	// Already up in fullSystem.
	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	// Should not contain "server not running".
	if strings.Contains(output, "server not running") {
		t.Errorf("unexpected 'server not running' when tmux is up; got:\n%s", output)
	}
}

// ── tmux missing: no server check attempted ───────────────────────────────────

func TestRunTmuxMissing_NoServerCheck(t *testing.T) {
	sys := fullSystem("/home/tester")
	delete(sys.paths, "tmux")
	// If server check were attempted without binary, it would fail with "no output configured".
	// This test ensures Run doesn't panic or error on that path.
	var out strings.Builder
	code := Run("v0.1.0-dev", &out, sys)
	if code != 1 {
		t.Errorf("expected exit 1 (hard fail: tmux missing), got %d", code)
	}
}

// ── Summary warning count ─────────────────────────────────────────────────────

func TestRunSummary_DynamicWarnCount(t *testing.T) {
	// Trigger exactly one warning (opencode missing) + none of the hook warnings.
	// We craft a system where hooks are OK, tmux server is up, but opencode is absent.
	home := "/home/tester"
	claudeSettingsPath := home + "/.claude/settings.json"
	opencodePluginPath := home + "/.config/opencode/plugins/perch-status.ts"
	claudeSettingsContent, _ := json.Marshal(map[string]interface{}{
		"hooks": map[string]interface{}{
			"Notification": []interface{}{
				map[string]interface{}{"command": "perch status set done"},
			},
		},
	})

	sys := &fakeSystem{
		home: home,
		paths: map[string]string{
			"go":     "/usr/local/go/bin/go",
			"tmux":   "/usr/bin/tmux",
			"git":    "/usr/bin/git",
			"claude": "/home/user/.local/bin/claude",
			// opencode absent
		},
		outputs: map[string]fakeOutput{
			"go version":         {out: []byte("go version go1.26.3 linux/amd64")},
			"tmux -V":            {out: []byte("tmux 3.6")},
			"git --version":      {out: []byte("git version 2.43.0")},
			"claude --version":   {out: []byte("2.1.158")},
			"tmux list-sessions": {out: []byte("sess: 1 windows")},
		},
		statPaths: map[string]bool{
			claudeSettingsPath: true,
			opencodePluginPath: true,
		},
		readFiles: map[string][]byte{
			claudeSettingsPath: claudeSettingsContent,
		},
	}

	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()

	// Summary line should mention "1 warning". No hook warnings → no "perch setup" suffix.
	if !strings.Contains(output, "1 warning") {
		t.Errorf("expected '1 warning' in summary; got:\n%s", output)
	}
	// No hook warnings present, so setup suffix should not appear.
	if strings.Contains(output, "perch setup") {
		t.Errorf("unexpected 'perch setup' suffix when no hook warnings; got:\n%s", output)
	}
}

func TestRunSummary_ZeroWarnings(t *testing.T) {
	sys := fullSystem("/home/tester")
	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	// With all OK, summary should say 0 warnings or "All checks passed".
	if strings.Contains(output, "[warn]") {
		t.Errorf("expected no warnings with healthy system; got:\n%s", output)
	}
}

// ── Output structure ──────────────────────────────────────────────────────────

func TestRunOutput_ContainsHeader(t *testing.T) {
	sys := fullSystem("/home/tester")
	var out strings.Builder
	Run("v1.2.3", &out, sys)
	if !strings.Contains(out.String(), "v1.2.3") {
		t.Errorf("expected version in output header; got:\n%s", out.String())
	}
}

func TestRunOutput_ContainsPaths(t *testing.T) {
	sys := fullSystem("/home/tester")
	var out strings.Builder
	Run("v0.1.0-dev", &out, sys)
	output := out.String()
	if !strings.Contains(output, "/usr/bin/tmux") {
		t.Errorf("expected tmux path in output; got:\n%s", output)
	}
}
