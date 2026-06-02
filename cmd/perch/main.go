package main

import (
	"bufio"
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
	"github.com/Miniature-Pug/perch/internal/frame"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/resurrect"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/status"
	"github.com/Miniature-Pug/perch/internal/tmux"
	"github.com/Miniature-Pug/perch/internal/tui"
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
		return handleBootstrap("", stdout, stderr)
	}

	switch args[0] {
	case "--sidebar":
		return handleSidebar(args[1:], stdout, stderr)
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

// handleTUI launches the interactive TUI, blocking until the user quits.
func handleTUI(root string, stdout, stderr io.Writer) int {
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "perch: cannot determine working directory: %v\n", err)
			return 1
		}
		root = cwd
	}
	baseDir, err := state.StateDir()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch: %v\n", err)
		return 1
	}
	// Plain WithCancel: bubbletea installs its own SIGINT/SIGTERM handler;
	// a second signal handler races it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // cancels in-flight data loads after the program exits

	// Load global config once; degrade gracefully on error (Cfg stays nil → defaults).
	var loadedCfg *config.Config
	if globalPath, err := config.DefaultGlobalPath(); err == nil {
		if c, err := config.Load(globalPath, root); err == nil {
			loadedCfg = c
		}
	}
	var refreshMs int
	if loadedCfg != nil {
		refreshMs = loadedCfg.RefreshMs
	}

	cfg := tui.Config{
		Cfg:       loadedCfg,
		Tmux:      tmux.New(),
		Runner:    proc.ExecRunner{},
		Claude:    agent.NewClaude(),
		Root:      root,
		BaseDir:   baseDir,
		Now:       time.Now().Unix(),
		RefreshMs: refreshMs,
	}
	if exe, exeErr := os.Executable(); exeErr == nil {
		cfg.ExecPath = exe
	}
	if err := tui.Run(ctx, cfg); err != nil {
		_, _ = fmt.Fprintf(stderr, "perch: %v\n", err)
		return 1
	}
	return 0
}

// bootstrapDeps is the injectable dependency bundle for handleBootstrap.
// In production, bootstrapProduction() fills it from real OS/tmux calls.
// In tests, fields are replaced with fakes so no real tmux or TUI is needed.
type bootstrapDeps struct {
	// tmuxClient is the Tmux instance used to run frame.Ensure.
	tmuxClient tmux.Tmux
	// executable returns the path to the running binary (os.Executable).
	// Used to build the "perch --sidebar" command argv.
	executable func() (string, error)
	// fallback is called when frame setup fails. In production this is
	// handleTUI. In tests it is replaced with a stub.
	fallback func(root string, stdout, stderr io.Writer) int
	// attach performs the terminal hand-off after a successful frame bootstrap.
	// In production: outside tmux → exec tmux attach-session; inside tmux →
	// switch-client. This is the one path NOT unit-tested (tty-dependent).
	// In tests it is a no-op that records the call.
	attach func(ctx context.Context, t tmux.Tmux, frameSession string) int
	// strandedCount reports how many agent sessions a restart stranded (read-only).
	// nil in tests that don't exercise the offer → the offer is skipped.
	strandedCount func(ctx context.Context) (int, error)
	// confirm prompts the user to restore n stranded sessions. Production gates on
	// a tty and returns false on a non-tty (never blocks). nil → offer skipped.
	confirm func(n int) bool
	// reconcile runs resurrect.Reconcile. nil → offer skipped.
	reconcile func(ctx context.Context) (resurrect.Report, error)
}

// resurrectDeps builds resurrect.Deps from the real state dir + global config
// roots. ok is false when the state dir is unavailable (the caller then skips
// the offer rather than erroring out the launch).
func resurrectDeps(t tmux.Tmux) (resurrect.Deps, bool) {
	baseDir, err := state.StateDir()
	if err != nil {
		return resurrect.Deps{}, false
	}
	var roots []string
	if globalPath, gerr := config.DefaultGlobalPath(); gerr == nil {
		if cfg, cerr := config.Load(globalPath, ""); cerr == nil {
			roots = cfg.Roots
		}
	}
	return resurrect.Deps{
		Tmux:    t,
		Runner:  proc.ExecRunner{},
		BaseDir: baseDir,
		Now:     time.Now().Unix(),
		Roots:   roots,
	}, true
}

// confirmRestore is the production confirm seam: it gates on a tty so a
// non-interactive launch (piped/CI stdin) never blocks on a prompt — it returns
// false and the offer is skipped. Only an explicit y/yes restores.
func confirmRestore(n int) bool {
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) == 0 {
		return false // non-tty: never block on a prompt
	}
	_, _ = fmt.Fprintf(os.Stderr,
		"perch: %d session(s) were stranded by a restart. Restore them? [y/N] ", n)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func bootstrapProduction() bootstrapDeps {
	t := tmux.New()
	rdeps, rok := resurrectDeps(t)
	return bootstrapDeps{
		tmuxClient: t,
		executable: os.Executable,
		fallback: func(root string, stdout, stderr io.Writer) int {
			return handleTUI(root, stdout, stderr)
		},
		attach: func(ctx context.Context, t tmux.Tmux, frameSession string) int {
			argv := t.ExecArgs(t.AttachArgs(frameSession)...)
			c := exec.Command(argv[0], argv[1:]...) //nolint:gosec // controlled input
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			_ = c.Run()
			return 0
		},
		strandedCount: func(ctx context.Context) (int, error) {
			if !rok {
				return 0, nil
			}
			return resurrect.StrandedCount(ctx, rdeps)
		},
		confirm: confirmRestore,
		reconcile: func(ctx context.Context) (resurrect.Report, error) {
			if !rok {
				return resurrect.Report{}, nil
			}
			return resurrect.Reconcile(ctx, rdeps)
		},
	}
}

// handleBootstrap is the new default entry point when perch is run without a
// subcommand. It bootstraps the persistent perch frame session (M11-0 T4):
//
//  1. Resolves the project root (cwd when root=="").
//  2. Builds the sidebar command string using os.Executable.
//  3. Calls frame.Ensure to create or reuse the perch frame.
//  4. Attaches: switch-client when inside tmux, exec tmux attach-session outside.
//
// Graceful fallback: any error from frame.Ensure (or a missing tmux binary)
// prints a warning and falls through to handleTUI so perch always launches
// something useful.
func handleBootstrap(root string, stdout, stderr io.Writer) int {
	return bootstrap(bootstrapProduction(), root, stdout, stderr)
}

// bootstrap is the testable core of handleBootstrap. deps replaces real tmux
// and OS calls so tests can drive every branch without a live server or TUI.
func bootstrap(deps bootstrapDeps, root string, stdout, stderr io.Writer) int {
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "perch: cannot determine working directory: %v\n", err)
			return 1
		}
		root = cwd
	}

	// Build the sidebar argv: absolute path to this binary + "--sidebar". argv
	// (not a joined string) keeps a binary path containing spaces intact.
	sidebarArgv := []string{"perch", "--sidebar"} // fallback if os.Executable fails
	if exe, err := deps.executable(); err == nil && exe != "" {
		sidebarArgv = []string{exe, "--sidebar"}
	}

	ctx := context.Background()

	// Auto-offer resurrect when a server restart stranded agent sessions. This
	// runs BEFORE frame.Ensure so reconcile sees normal (non-frame) topology — at
	// restart time no perch frame exists yet, so restoring is safe here. Seams are
	// nil in tests that don't exercise the offer.
	if deps.strandedCount != nil && deps.confirm != nil && deps.reconcile != nil {
		if n, derr := deps.strandedCount(ctx); derr == nil && n > 0 && deps.confirm(n) {
			if rep, rerr := deps.reconcile(ctx); rerr != nil {
				_, _ = fmt.Fprintf(stderr, "perch: resurrect: %v\n", rerr)
			} else {
				_, _ = fmt.Fprintf(stdout, "perch: resurrect — %d restored, %d pruned, %d kept\n",
					len(rep.Restored), len(rep.Pruned), len(rep.Kept))
			}
		}
	}

	info, err := frame.Ensure(ctx, deps.tmuxClient, frame.DefaultFrameSession, root, sidebarArgv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch: frame setup: %v — falling back to direct TUI\n", err)
		return deps.fallback(root, stdout, stderr)
	}
	_ = info // session/pane ids are only needed by handleSidebar (injected via $TMUX_PANE)

	return deps.attach(ctx, deps.tmuxClient, frame.DefaultFrameSession)
}

// sidebarDeps is the injectable dependency bundle for handleSidebar. In
// production, sidebarProduction() fills it. In tests, runTUI is replaced with
// a stub so no Bubble Tea program is started.
type sidebarDeps struct {
	// tmuxClient is used by frame.SidebarContext to discover the frame context.
	tmuxClient tmux.Tmux
	// getenv resolves environment variables (e.g. TMUX_PANE).
	getenv func(string) string
	// runTUI wraps tui.Run. Tests replace this with a stub that captures the
	// Config without launching a real terminal program.
	runTUI func(ctx context.Context, cfg tui.Config) error
}

func sidebarProduction() sidebarDeps {
	return sidebarDeps{
		tmuxClient: tmux.New(),
		getenv:     os.Getenv,
		runTUI:     tui.Run,
	}
}

// handleSidebar is the inner TUI that runs inside the frame's sidebar pane.
// It resolves its own pane id from $TMUX_PANE, discovers the sibling placeholder
// pane via frame.SidebarContext, then launches the TUI with the frame fields set.
func handleSidebar(args []string, stdout, stderr io.Writer) int {
	return sidebar(sidebarProduction(), args, stdout, stderr)
}

// sidebar is the testable core of handleSidebar. deps replaces real tmux and
// TUI calls so tests can exercise routing without a live server or terminal.
func sidebar(deps sidebarDeps, args []string, stdout, stderr io.Writer) int {
	// args currently unused (reserved for future sidebar-specific flags).
	_ = args

	root, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch: cannot determine working directory: %v\n", err)
		return 1
	}
	baseDir, err := state.StateDir()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frameSession, placeholderPane, err := frame.SidebarContext(ctx, deps.tmuxClient, deps.getenv)
	if err != nil {
		// Not inside a frame (e.g. invoked directly for testing) — run without
		// frame context (direct-TUI mode) so the sidebar is still useful.
		_, _ = fmt.Fprintf(stderr, "perch: sidebar context: %v — running without frame\n", err)
	}

	// Load global config once; degrade gracefully on error (Cfg stays nil → defaults).
	var loadedCfg *config.Config
	if globalPath, gerr := config.DefaultGlobalPath(); gerr == nil {
		if c, cerr := config.Load(globalPath, root); cerr == nil {
			loadedCfg = c
		}
	}
	var refreshMs int
	if loadedCfg != nil {
		refreshMs = loadedCfg.RefreshMs
	}

	cfg := tui.Config{
		Cfg:             loadedCfg,
		Tmux:            deps.tmuxClient,
		Runner:          proc.ExecRunner{},
		Claude:          agent.NewClaude(),
		Root:            root,
		BaseDir:         baseDir,
		Now:             time.Now().Unix(),
		RefreshMs:       refreshMs,
		FrameSession:    frameSession,
		PlaceholderPane: placeholderPane,
	}
	if exe, exeErr := os.Executable(); exeErr == nil {
		cfg.ExecPath = exe
	}
	if runErr := deps.runTUI(ctx, cfg); runErr != nil {
		_, _ = fmt.Fprintf(stderr, "perch: %v\n", runErr)
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
	case "working", "waiting", "done":
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

// handlePathArg validates args[0] as an existing directory root and bootstraps
// the perch frame (or falls back to direct TUI), or prints usage to stderr and
// returns 2.
func handlePathArg(arg string, stdout, stderr io.Writer) int {
	info, err := os.Stat(arg)
	if err != nil || !info.IsDir() {
		_, _ = fmt.Fprintf(stderr, "perch: %q is not an existing directory\n", arg)
		printUsage(stderr)
		return 2
	}
	return handleBootstrap(arg, stdout, stderr)
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

	if err := t.SetPaneOption(ctx, target, "@perch_session", syntheticID); err != nil {
		_, _ = fmt.Fprintf(stderr, "perch debug tmux: set-option: %v\n", err)
		return 1
	}

	got, err := t.GetPaneOption(ctx, target, "@perch_session")
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

	shadowPath := filepath.Join(baseDir, "windows", state.EncodePaneKey(paneID)+".json")

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
