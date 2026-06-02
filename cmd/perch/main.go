// Command perch is a keyboard-first GUI for managing AI coding sessions
// (claude, opencode) across git worktrees. Run without arguments it launches
// the Wails desktop GUI; it also provides the setup, attach, resurrect,
// status, doctor, and version subcommands. See ARCHITECTURE.md for the full
// design.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/attach"
	"github.com/Miniature-Pug/perch/internal/config"
	"github.com/Miniature-Pug/perch/internal/discover"
	"github.com/Miniature-Pug/perch/internal/doctor"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/resurrect"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/status"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// version is injected at build time via ldflags:
//
//	-X main.version=$(git describe --tags --always --dirty)
//
// Falls back to "dev" when the binary is built without ldflags.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point. All handler output goes to stdout/stderr —
// never directly to os.Stdout/os.Stderr. Returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return handleLaunch("", stdout, stderr)
	}

	switch args[0] {
	case "setup":
		return handleSetup(args[1:], stdout, stderr)
	case "attach":
		return handleAttach(args[1:], stdout, stderr)
	case "resurrect":
		return handleResurrect(stdout, stderr)
	case "status":
		return handleStatus(args[1:], stdout, stderr)
	case "doctor":
		return doctor.Run(version, stdout, doctor.RealSystem())
	case "version":
		return handleVersion(stdout)
	// debug is intentionally hidden from printUsage — it is a diagnostic
	// surface, not part of the public CLI contract.
	case "debug":
		return handleDebug(args[1:], stdout, stderr)
	default:
		// Treat the first argument as a path to a project root.
		return handlePathArg(args[0], stdout, stderr)
	}
}

// handleLaunch is the default entry point: it resolves the project root (cwd
// when root==""), the discovery roots, and launches the Wails GUI via the
// launchGUI seam. The GUI owns the interactive shell; there is no terminal TUI.
func handleLaunch(root string, stdout, stderr io.Writer) int {
	_ = stdout
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "perch: cannot determine working directory: %v\n", err)
			return 1
		}
		root = cwd
	}
	if err := launchGUI(guiRoots(root)); err != nil {
		_, _ = fmt.Fprintf(stderr, "perch: %v\n", err)
		return 1
	}
	return 0
}

// attachDeps is the injectable dependency bundle for handleAttach. Production
// callers use attachProduction(); tests inject fakes so no real tmux or
// filesystem calls are made.
type attachDeps struct {
	// gather discovers live candidate sessions. Tests replace with a stub.
	gather func(ctx context.Context) ([]attach.Candidate, error)
	// tmuxClient is used for the inside-tmux switch-client call. Using a
	// FakeRunner-backed Tmux lets tests assert r.Calls contains switch-client.
	tmuxClient tmux.Tmux
	// getenv resolves environment variables (e.g. TMUX).
	getenv func(string) string
	// execProcess runs the outside-tmux attach-session exec seam. The argv is
	// pre-built from validated Candidate fields (never the raw query). This is
	// the one production path NOT unit-tested (tty-dependent exec seam). Tests
	// inject a stub that captures the argv for assertion.
	execProcess func(argv []string) int
}

// attachProduction returns attachDeps wired to real tmux and filesystem.
func attachProduction(root, baseDir string, now int64) attachDeps {
	t := tmux.New()
	gatherDeps := attach.Deps{
		Tmux:    t,
		Runner:  proc.ExecRunner{},
		Claude:  agent.NewClaude(),
		Root:    root,
		BaseDir: baseDir,
		Now:     now,
	}
	return attachDeps{
		gather: func(ctx context.Context) ([]attach.Candidate, error) {
			return attach.Gather(ctx, gatherDeps)
		},
		tmuxClient: t,
		getenv:     os.Getenv,
		execProcess: func(argv []string) int {
			c := exec.Command(argv[0], argv[1:]...) //nolint:gosec // controlled input
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			_ = c.Run()
			return 0
		},
	}
}

// handleAttach implements `perch attach <query>`:
//
//  1. Validates that a query was supplied (exit 2 otherwise).
//  2. Discovers live agent sessions via attach.Gather.
//  3. Fuzzy-matches the query with attach.Resolve:
//     - 0 matches → stderr "no session matches <query>", exit 1.
//     - 1 match   → attach the terminal to the matched session, exit 0.
//     - 2+ matches → print candidates to stderr, exit 2 ("ambiguous query").
//
// The tmux target is always derived from the validated Candidate fields
// (TmuxSession/TmuxWindow) — the raw query string is never passed to tmux.
func handleAttach(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "Usage: perch attach <query>")
		return 2
	}
	query := strings.Join(args, " ")
	if strings.TrimSpace(query) == "" {
		_, _ = fmt.Fprintln(stderr, "Usage: perch attach <query>")
		return 2
	}

	root, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch attach: cannot determine working directory: %v\n", err)
		return 1
	}
	baseDir, err := state.StateDir()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch attach: %v\n", err)
		return 1
	}

	return attachCore(attachProduction(root, baseDir, time.Now().Unix()), query, stdout, stderr)
}

// attachCore is the testable core of handleAttach. deps replaces real tmux and
// filesystem calls so tests can exercise every branch without a live server.
//
// argv-safety: the tmux target is always derived from the validated Candidate
// fields (TmuxSession/TmuxWindow/LiveTarget), never from the raw query string.
//
// Known limitation (spike Risk 6): if the matched agent session is currently
// displayed in the perch frame, its pane is physically inside the frame's
// placeholder layout. Attaching to it (whether via switch-client or
// attach-session) shows the placeholder, not the agent. Swapping the pane
// layout from the CLI is not attempted in v1 — use perch's sidebar switcher
// instead. A cheap detection hint: if future work wants to detect this, check
// whether the agent's home session contains only a "sleep" command pane.
func attachCore(deps attachDeps, query string, stdout, stderr io.Writer) int {
	ctx := context.Background()
	candidates, err := deps.gather(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch attach: discover: %v\n", err)
		return 1
	}

	res := attach.Resolve(query, candidates)
	switch res.Count {
	case 0:
		_, _ = fmt.Fprintf(stderr, "no session matches %q\n", query)
		return 1
	case 1:
		cand := *res.Matched
		if deps.getenv("TMUX") != "" {
			// Inside tmux: switch the current client to the agent's window.
			// Use the live window target when available; fall back to session-only.
			// SwitchClient routes through the runner so FakeRunner captures the call.
			target := cand.LiveTarget
			if target == "" {
				target = tmux.SessionTarget(cand.TmuxSession)
			}
			if err := deps.tmuxClient.SwitchClient(ctx, target); err != nil {
				_, _ = fmt.Fprintf(stderr, "perch attach: switch-client: %v\n", err)
				return 1
			}
			return 0
		}
		// Outside tmux: build the attach-session argv from validated Candidate
		// fields and pass it to the exec seam. The raw query never enters argv.
		argv := deps.tmuxClient.ExecArgs(deps.tmuxClient.AttachArgs(cand.TmuxSession)...)
		return deps.execProcess(argv)
	default:
		_, _ = fmt.Fprintf(stderr, "ambiguous query %q; matches:\n%s", query, attach.FormatAmbiguous(res.Ambiguous))
		return 2
	}
}

// handleResurrect implements `perch resurrect`. It resolves the state directory,
// runs the boot-id reconcile engine, and prints a human-readable summary to
// stdout. The only hard exit-1 condition is an unreadable state directory or a
// reconcile error; an empty reconcile (no shadow records) exits 0 with a
// "nothing to reconcile" message.
func handleResurrect(stdout, stderr io.Writer) int {
	baseDir, err := state.StateDir()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch: %v\n", err)
		return 1
	}

	// Load the configured discovery roots so the reconcile can reject shadow
	// records whose worktree path lies outside them (V7c). Degrade on error:
	// with no roots the containment guard is dormant rather than blocking.
	var roots []string
	if globalPath, gerr := config.DefaultGlobalPath(); gerr == nil {
		if cfg, cerr := config.Load(globalPath, ""); cerr == nil {
			roots = cfg.Roots
		}
	}

	deps := resurrect.Deps{
		Tmux:    tmux.New(),
		Runner:  proc.ExecRunner{},
		BaseDir: baseDir,
		Now:     time.Now().Unix(),
		Roots:   roots,
	}

	report, err := resurrect.Reconcile(context.Background(), deps)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch: %v\n", err)
		return 1
	}

	nRestored := len(report.Restored)
	nPruned := len(report.Pruned)
	nKept := len(report.Kept)
	nSkipped := len(report.Skipped)

	if nRestored == 0 && nPruned == 0 && nKept == 0 && nSkipped == 0 {
		_, _ = fmt.Fprintln(stdout, "resurrect: nothing to reconcile")
		return 0
	}

	_, _ = fmt.Fprintf(stdout, "resurrect: %d restored, %d pruned, %d kept, %d skipped\n",
		nRestored, nPruned, nKept, nSkipped)

	for _, key := range report.Restored {
		_, _ = fmt.Fprintf(stdout, "  restored %s\n", key)
	}
	for _, key := range report.Pruned {
		_, _ = fmt.Fprintf(stdout, "  pruned   %s\n", key)
	}
	for _, key := range report.Kept {
		_, _ = fmt.Fprintf(stdout, "  kept     %s\n", key)
	}
	for _, s := range report.Skipped {
		_, _ = fmt.Fprintf(stdout, "  skipped  %s (%s): %s\n", s.PaneKey, s.Tree, s.Reason)
	}

	return 0
}

// handleStatus dispatches `perch status set <working|waiting|done>`.
// Invalid usage → exit 2. Empty $TMUX_PANE → exit 0 silently (must not fail
// the agent's hook when run outside tmux). tmux error → print to stderr, exit 1.
func handleStatus(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "set" {
		_, _ = fmt.Fprintln(stderr, "Usage: perch status set <working|waiting|done>")
		return 2
	}
	st := args[1]
	// Validate state before reading the environment so bad args always exit 2.
	switch st {
	case status.StateWorking, status.StateWaiting, status.StateDone:
		// valid
	default:
		_, _ = fmt.Fprintln(stderr, "Usage: perch status set <working|waiting|done>")
		return 2
	}

	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		// Running outside tmux — silently succeed so the hook does not fail.
		return 0
	}

	deps := status.Deps{Tmux: tmux.New()}
	if err := status.Set(context.Background(), deps, pane, st); err != nil {
		_, _ = fmt.Fprintf(stderr, "perch status set: %v\n", err)
		return 1
	}
	return 0
}

// setupMessage returns the human-readable success line for a tool install. It
// is a pure function so it can be tested without I/O seams.
func setupMessage(name string, replace bool) string {
	verb := "installed"
	if replace {
		verb = "replaced"
	}
	switch name {
	case "claude":
		return fmt.Sprintf("setup: claude hooks %s (~/.claude/settings.json)", verb)
	case "opencode":
		return fmt.Sprintf("setup: opencode plugin %s (~/.config/opencode/plugins/perch-status.ts)", verb)
	default:
		return fmt.Sprintf("setup: %s hooks %s", name, verb)
	}
}

// handleSetup implements `perch setup [--replace]`. It detects installed AI
// coding tools and calls InstallStatusHook on each, reporting the result to
// stdout. Without --replace the operation is additive and idempotent — existing
// third-party hooks are never touched and re-running is safe. With --replace,
// any stale perch-owned hook entries are overwritten with the current block
// while all foreign configuration is preserved unchanged.
func handleSetup(args []string, stdout, stderr io.Writer) int {
	replace := false
	for _, a := range args {
		if a == "--replace" {
			replace = true
		}
	}

	adapters := []agent.Adapter{
		agent.NewClaude(),
		agent.NewOpencode(),
	}

	anyError := false
	anyInstalled := false

	for _, a := range adapters {
		if !a.Detect() {
			_, _ = fmt.Fprintf(stdout, "setup: %s not found — skipped\n", a.Name())
			continue
		}
		if err := a.InstallStatusHook(replace); err != nil {
			_, _ = fmt.Fprintf(stderr, "setup: %s: %v\n", a.Name(), err)
			anyError = true
			continue
		}
		anyInstalled = true
		_, _ = fmt.Fprintln(stdout, setupMessage(a.Name(), replace))
	}

	if !anyInstalled && !anyError {
		_, _ = fmt.Fprintln(stdout, "setup: no supported tools found — nothing installed")
	}

	if anyError {
		return 1
	}
	return 0
}

// handleVersion prints the perch version and build info.
// VCS fields from debug.ReadBuildInfo are optional — under -trimpath and
// vendored builds these stamps may be absent, so every access is guarded.
func handleVersion(stdout io.Writer) int {
	_, _ = fmt.Fprintf(stdout, "perch %s\n", version)
	_, _ = fmt.Fprintf(stdout, "go %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return 0
	}
	// Print VCS revision and time only when present (absent in -trimpath builds).
	var revision, vcsTime string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			vcsTime = s.Value
		}
	}
	if revision != "" {
		_, _ = fmt.Fprintf(stdout, "commit %s", revision)
		if vcsTime != "" {
			_, _ = fmt.Fprintf(stdout, " (%s)", vcsTime)
		}
		_, _ = fmt.Fprintln(stdout)
	}
	return 0
}

// handlePathArg validates args[0] as an existing directory root and launches
// the Wails GUI, or prints usage to stderr and returns 2.
func handlePathArg(arg string, stdout, stderr io.Writer) int {
	info, err := os.Stat(arg)
	if err != nil || !info.IsDir() {
		_, _ = fmt.Fprintf(stderr, "perch: %q is not an existing directory\n", arg)
		printUsage(stderr)
		return 2
	}
	return handleLaunch(arg, stdout, stderr)
}

// printUsage writes the usage summary to w.
// Note: "debug" is intentionally absent — it is a hidden diagnostic surface.
func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: perch [path]")
	_, _ = fmt.Fprintln(w, "       perch attach <query>")
	_, _ = fmt.Fprintln(w, "       perch setup")
	_, _ = fmt.Fprintln(w, "       perch resurrect")
	_, _ = fmt.Fprintln(w, "       perch status set <working|waiting|done>")
	_, _ = fmt.Fprintln(w, "       perch doctor")
	_, _ = fmt.Fprintln(w, "       perch version")
}

// handleDebug dispatches hidden debug sub-commands. These are not part of the
// public CLI and must never appear in printUsage.
func handleDebug(args []string, stdout, stderr io.Writer) int {
	if len(args) >= 1 {
		switch args[0] {
		case "discover":
			return handleDebugDiscover(args[1:], stdout, stderr)
		case "tmux":
			return handleDebugTmux(args[1:], stdout, stderr)
		}
	}
	_, _ = fmt.Fprintln(stderr, "Usage: perch debug discover [path]")
	_, _ = fmt.Fprintln(stderr, "       perch debug tmux [path]")
	return 2
}

// handleDebugTmux implements `perch debug tmux [path]`.
// It creates a tmux session/window for the given tree path (defaulting to cwd),
// sets and reads back the @perch_session pane option, writes a shadow record,
// and prints a summary report to stdout. Intentionally uses the user's real tmux
// server (default socket) so the session can be inspected after the command runs.
func handleDebugTmux(args []string, stdout, stderr io.Writer) int {
	var tree string
	if len(args) >= 1 {
		tree = args[0]
	} else {
		var err error
		tree, err = os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "perch debug tmux: cannot determine working directory: %v\n", err)
			return 2
		}
	}

	info, err := os.Stat(tree)
	if err != nil || !info.IsDir() {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: %q is not an existing directory\n", tree)
		_, _ = fmt.Fprintln(stderr, "Usage: perch debug tmux [path]")
		return 2
	}

	t := tmux.New()
	base := filepath.Base(tree)
	session := tmux.SessionName(base)
	window := tmux.WindowName(base)
	ctx := context.Background()

	paneID, err := t.Connect(ctx, session, window, tree)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: connect: %v\n", err)
		return 1
	}

	syntheticID := "perch-debug-" + paneID
	target := tmux.WindowTarget(session, window)

	if err := t.SetPaneOption(ctx, target, tmux.OptionPerchSession, syntheticID); err != nil {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: set-option: %v\n", err)
		return 1
	}

	got, err := t.GetPaneOption(ctx, target, tmux.OptionPerchSession)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: get-option: %v\n", err)
		return 1
	}
	if got != syntheticID {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: @perch_session round-trip mismatch: set %q got %q\n", syntheticID, got)
		return 1
	}

	bootID, err := t.BootID(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: boot-id: %v\n", err)
		return 1
	}

	w := model.Window{
		PaneKey:     paneID,
		Tool:        model.ToolClaude,
		SessionID:   syntheticID,
		Tree:        tree,
		TmuxSession: session,
		TmuxWindow:  window,
		BootID:      bootID,
		Updated:     time.Now().Unix(),
	}

	// StateDir resolves the path only — it does not create it. SaveWindow
	// creates baseDir/windows/ itself via os.MkdirAll, so no manual mkdir needed.
	baseDir, err := state.StateDir()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: state dir: %v\n", err)
		return 1
	}

	if err := state.SaveWindow(baseDir, w); err != nil {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: save window: %v\n", err)
		return 1
	}

	windows, err := state.LoadWindows(baseDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: load windows: %v\n", err)
		return 1
	}

	found := false
	for _, rec := range windows {
		if rec.PaneKey == paneID {
			found = true
			break
		}
	}
	if !found {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: shadow record for pane %q not found after save\n", paneID)
		return 1
	}

	shadowPath := filepath.Join(baseDir, state.WindowsDirName, state.EncodePaneKey(paneID)+".json")

	_, _ = fmt.Fprintf(stdout, "tmux session : %s\n", session)
	_, _ = fmt.Fprintf(stdout, "tmux window  : %s\n", window)
	_, _ = fmt.Fprintf(stdout, "pane id      : %s\n", paneID)
	_, _ = fmt.Fprintf(stdout, "@perch_session set=%q got=%q (round-trip OK)\n", syntheticID, got)
	_, _ = fmt.Fprintf(stdout, "boot id      : %s\n", bootID)
	_, _ = fmt.Fprintf(stdout, "shadow record: %s\n", shadowPath)
	_, _ = fmt.Fprintf(stdout, "\nWindow left open for inspection.\n")
	_, _ = fmt.Fprintf(stdout, "To remove: tmux kill-session -t '=%s'\n", session)
	return 0
}

// handleDebugDiscover implements `perch debug discover [path]`.
// It lists all git projects under root (defaulting to cwd) with their
// worktrees, ordered by frecency (cold start → alphabetical).
func handleDebugDiscover(args []string, stdout, stderr io.Writer) int {
	var root string
	if len(args) >= 1 {
		root = args[0]
	} else {
		var err error
		root, err = os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "perch debug discover: cannot determine working directory: %v\n", err)
			return 2
		}
	}

	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		_, _ = fmt.Fprintf(stderr, "perch debug discover: %q is not an existing directory\n", root)
		return 2
	}

	projects, err := discover.Projects(
		context.Background(),
		proc.ExecRunner{},
		root,
		discover.Options{},
		map[string]state.ProjectStat{},
		time.Now().Unix(),
	)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch debug discover: %v\n", err)
		return 1
	}

	if len(projects) == 0 {
		_, _ = fmt.Fprintf(stdout, "no git projects found under %s\n", root)
		return 0
	}

	writeProjects(stdout, projects)
	return 0
}

// writeProjects writes a formatted listing of projects and their trees to w.
// Each project header line is "Name  Path". Each tree line is indented two
// spaces, followed by "*" (main) or " " (linked), the branch name, two spaces,
// and the tree path. This is a pure formatting function; it performs no I/O
// beyond writing to w.
func writeProjects(w io.Writer, projects []*discover.ProjectTrees) {
	for _, pt := range projects {
		_, _ = fmt.Fprintf(w, "%s  %s\n", pt.Project.Name, pt.Project.Path)
		for _, tr := range pt.Trees {
			marker := " "
			if tr.IsMain {
				marker = "*"
			}
			_, _ = fmt.Fprintf(w, "  %s %s  %s\n", marker, tr.Branch, tr.Path)
		}
	}
}
