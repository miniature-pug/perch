// Package doctor implements the `perch doctor` health check.
//
// It is read-only: it never installs, creates, or modifies anything.
// The package injects all OS interactions through the system interface. This
// makes the package fully unit-testable, without spawning a process or
// touching the real filesystem.
package doctor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	perch "github.com/miniature-pug/perch"
	"github.com/miniature-pug/perch/internal/model"
)

// ── OS boundary ───────────────────────────────────────────────────────────────

// system is the injectable OS boundary. All calls to the real system go
// through here, so tests can supply a fake, without spawning a process or
// touching the real FS.
type system interface {
	lookPath(name string) (string, error)
	output(name string, args ...string) ([]byte, error)
	homeDir() (string, error)
	stat(path string) error
	readFile(path string) ([]byte, error)
}

// realSystem is the production implementation of system.
type realSystem struct{}

// RealSystem returns the production system implementation.
func RealSystem() system {
	return &realSystem{}
}

func (r *realSystem) lookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (r *realSystem) output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

func (r *realSystem) homeDir() (string, error) {
	return os.UserHomeDir()
}

func (r *realSystem) stat(path string) error {
	_, err := os.Stat(path)
	return err
}

func (r *realSystem) readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// ── Version utilities ─────────────────────────────────────────────────────────

// versionRe matches the first dotted numeric version token (for example,
// "1.26.3", "3.6", "2.1.158"). versionRe requires at least one dot, so it
// never matches bare integers like "2", or a leading "go" word, by
// themselves.
var versionRe = regexp.MustCompile(`\d+(?:\.\d+)+`)

// versionPrefixRe strips a leading "v" immediately before a digit, so that
// "v1.2.3" is normalised to "1.2.3" before versionRe runs.
var versionPrefixRe = regexp.MustCompile(`\bv(\d)`)

// extractVersionToken extracts the first dotted version token from raw output
// (for example, "go version go1.26.3 linux/amd64" gives "1.26.3").
// extractVersionToken strips any leading "v" prefix, so "v1.2.3" yields
// "1.2.3". It returns "" when it finds no token.
func extractVersionToken(raw string) string {
	raw = strings.TrimSpace(raw)
	normalised := versionPrefixRe.ReplaceAllString(raw, "$1")
	match := versionRe.FindString(normalised)
	return match
}

// splitVersion converts "1.26.3" into []int{1, 26, 3}. splitVersion strips
// non-numeric parts within a component (for example, "3.6a" gives {3, 6}). A
// component that cannot be parsed at all counts as 0.
func splitVersion(v string) []int {
	// Strip trailing non-numeric suffix from each component individually.
	parts := strings.Split(v, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		// Take only leading digits in each component.
		j := 0
		for j < len(p) && p[j] >= '0' && p[j] <= '9' {
			j++
		}
		if j > 0 {
			n, err := strconv.Atoi(p[:j])
			if err == nil {
				nums[i] = n
			}
		}
	}
	return nums
}

// compareVersions returns -1 if a<b, 0 if a==b, and +1 if a>b, by comparing
// dotted numeric components (1.26.3 vs 1.26.2). compareVersions strips
// non-numeric suffixes defensively (for example, "3.6a" gives numeric parts
// only). Missing components count as 0 (1.26 == 1.26.0).
func compareVersions(a, b string) int {
	an := splitVersion(a)
	bn := splitVersion(b)
	// Pad shorter slice with zeros.
	for len(an) < len(bn) {
		an = append(an, 0)
	}
	for len(bn) < len(an) {
		bn = append(bn, 0)
	}
	for i := range an {
		if an[i] < bn[i] {
			return -1
		}
		if an[i] > bn[i] {
			return 1
		}
	}
	return 0
}

// ── Tool versions parsing ─────────────────────────────────────────────────────

// ParseToolVersions parses the raw content of a .tool-versions file into a
// map[name]version. ParseToolVersions defensively skips lines that are
// blank, that start with "#", or that lack a space separator. This function
// never panics on bad input.
func ParseToolVersions(raw string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r") // handle CRLF
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		m[parts[0]] = parts[1]
	}
	return m
}

// ── Tool descriptor table ─────────────────────────────────────────────────────

// toolDescriptor describes how to check a single external dependency.
type toolDescriptor struct {
	// name is the binary name (used for lookPath).
	name string
	// pinnedKey is the key in .tool-versions. Empty means no pin (for example, git).
	pinnedKey string
	// versionArgs are the arguments passed to get the version string.
	versionArgs []string
	// hardRequirement means absence → exit 1.
	hardRequirement bool
	// buildOnly means absence is a warn, not a fail (perch runs fine without it).
	buildOnly bool
	// agentTool means absence produces the one-of-agents warning message.
	agentTool bool
}

// tools is the ordered descriptor table. The order controls the display
// order. Run applies special "one-of" logic to the agent tools (claude,
// opencode) after it iterates this table.
var tools = []toolDescriptor{
	{
		name:            "go",
		pinnedKey:       "golang",
		versionArgs:     []string{"version"},
		hardRequirement: false,
		buildOnly:       true, // go is only needed at build time
	},
	{
		name:            "git",
		pinnedKey:       "", // system-managed, not pinned
		versionArgs:     []string{"--version"},
		hardRequirement: true,
		buildOnly:       false,
	},
	{
		name:            string(model.ToolClaude),
		pinnedKey:       string(model.ToolClaude),
		versionArgs:     []string{"--version"},
		hardRequirement: false, // governed by one-of-agents rule
		buildOnly:       false,
		agentTool:       true,
	},
	{
		name:            string(model.ToolOpencode),
		pinnedKey:       string(model.ToolOpencode),
		versionArgs:     []string{"--version"},
		hardRequirement: false, // governed by one-of-agents rule
		buildOnly:       false,
		agentTool:       true,
	},
}

// ── Check result ──────────────────────────────────────────────────────────────

// checkResult is the outcome of checking a single tool.
type checkResult struct {
	name    string
	tag     string // "[ok]" or "[warn]" or "[fail]"
	version string // installed version or status message
	path    string // binary path (empty for hook/server checks)
	isHard  bool   // if true and tag != "[ok]", hard failure
}

// ── Main Run function ─────────────────────────────────────────────────────────

// Run executes the doctor health check, writes a human-readable report to w,
// and returns the exit code: 0 when all hard requirements are satisfied, 1
// when any are missing.
//
// version is the perch version string injected from main (via ldflags).
func Run(version string, w io.Writer, sys system) int {
	pinnedVersions := ParseToolVersions(perch.ToolVersions)

	// results collects all tool-check rows for tabwriter rendering.
	var results []checkResult
	hardFail := false
	warnings := 0

	// Track agent presence for the one-of-agents rule.
	agentsPresent := 0
	agentToolCount := 0

	for _, td := range tools {
		r := checkTool(td, pinnedVersions, sys)
		results = append(results, r)

		if r.isHard && r.tag != "[ok]" {
			hardFail = true
		} else if r.tag == "[warn]" {
			warnings++
		}

		// Track agent presence generically via the agentTool flag.
		if td.agentTool {
			agentToolCount++
			if r.path != "" {
				agentsPresent++
			}
		}
	}

	// ── One-of-agents rule ────────────────────────────────────────────────────
	// Neither agent's checkTool sets hardFail. Run resolves the combined state
	// here. Both absent → hard fail with a synthetic row. The synthetic row
	// does not count toward warnings. It is a hard fail, not a warning.
	if agentToolCount > 0 && agentsPresent == 0 {
		hardFail = true
		results = append(results, checkResult{
			name:    "agents",
			tag:     "[fail]",
			version: "at least one agent (claude or opencode) is required",
			isHard:  true,
		})
	}

	// ── Render ────────────────────────────────────────────────────────────────
	_, _ = fmt.Fprintf(w, "\nperch %s\n\n", version)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range results {
		if r.path != "" {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", r.tag, r.name, r.version, r.path)
		} else {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", r.tag, r.name, r.version)
		}
	}
	_ = tw.Flush()

	// ── Summary ───────────────────────────────────────────────────────────────
	_, _ = fmt.Fprintln(w)
	if warnings == 0 {
		_, _ = fmt.Fprintln(w, "All checks passed.")
	} else {
		noun := "warnings"
		if warnings == 1 {
			noun = "warning"
		}
		_, _ = fmt.Fprintf(w, "%d %s.\n", warnings, noun)
	}

	if hardFail {
		return 1
	}
	return 0
}

// siblingAgent returns the name of the other agent tool in the descriptor
// table. The absence message uses this name, so the message names the
// partner rather than itself. siblingAgent falls back to "the other agent"
// when the table has fewer than two agent entries.
func siblingAgent(name string) string {
	for _, t := range tools {
		if t.agentTool && t.name != name {
			return t.name
		}
	}
	return "the other agent"
}

// checkTool evaluates a single toolDescriptor and returns a checkResult.
//
// Drift rule: warn only when installed < pinned. installed >= pinned gives
// [ok]. This differs from install.sh's install-time "warn on any mismatch"
// check. An ongoing health check should not flag newer-than-pin as a
// problem, because go toolchains and agent CLIs self-update to newer
// versions routinely.
func checkTool(td toolDescriptor, pinned map[string]string, sys system) checkResult {
	path, err := sys.lookPath(td.name)
	if err != nil {
		// Binary not found.
		msg := "not found"
		tag := "[warn]"
		hard := false
		if td.hardRequirement {
			tag = "[fail]"
			hard = true
		}
		if td.buildOnly {
			msg = "not found (build-only; not required to run perch)"
		}
		// One-of-agents: the absence message names the sibling agent, so the row
		// stays self-consistent (for example, "ok if opencode present" appears
		// on the claude row). Run resolves whether this is a hard fail. Here it
		// is just a warn.
		if td.agentTool {
			msg = fmt.Sprintf("not found (at least one agent is required — ok if %s present)", siblingAgent(td.name))
		}
		return checkResult{
			name:    td.name,
			tag:     tag,
			version: msg,
			isHard:  hard,
		}
	}

	// Binary found. Determine installed version.
	raw, err := sys.output(td.name, td.versionArgs...)
	var installedVer string
	if err != nil {
		installedVer = "unknown (version check failed)"
	} else {
		installedVer = extractVersionToken(string(raw))
		if installedVer == "" {
			installedVer = "unknown (unparseable output)"
		}
	}

	// Drift check runs only when there is a pin and the installed version
	// could be parsed.
	tag := "[ok]"
	displayVer := installedVer
	if td.pinnedKey != "" {
		if pinnedVer, ok := pinned[td.pinnedKey]; ok {
			if installedVer != "" && !strings.HasPrefix(installedVer, "unknown") {
				// Warn only when installed < pinned. Newer or equal is fine.
				if compareVersions(installedVer, pinnedVer) < 0 {
					tag = "[warn]"
					displayVer = installedVer + " (below pin " + pinnedVer + ")"
				}
			}
			// If installed == "unknown", checkTool cannot drift-check. It
			// leaves the tag as [ok] (no crash).
		}
	}

	return checkResult{
		name:    td.name,
		tag:     tag,
		version: displayVer,
		path:    path,
	}
}
