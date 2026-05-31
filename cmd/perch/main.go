package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/Miniature-Pug/perch/internal/discover"
	"github.com/Miniature-Pug/perch/internal/doctor"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
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
		return handleTUI("", stdout)
	}

	switch args[0] {
	case "setup":
		_, _ = fmt.Fprintln(stdout, "setup: not yet implemented (M8)")
		return 0
	case "resurrect":
		_, _ = fmt.Fprintln(stdout, "resurrect: not yet implemented")
		return 0
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

// handleTUI is the stub for the interactive TUI (implemented in M5).
func handleTUI(root string, stdout io.Writer) int {
	if root == "" {
		_, _ = fmt.Fprintln(stdout, "perch: TUI not yet implemented (M5)")
	} else {
		_, _ = fmt.Fprintf(stdout, "perch: TUI not yet implemented (M5) [root=%s]\n", root)
	}
	return 0
}

// handleStatus dispatches `perch status set <working|waiting|done>`.
func handleStatus(args []string, stdout, stderr io.Writer) int {
	if len(args) == 2 && args[0] == "set" {
		switch args[1] {
		case "working", "waiting", "done":
			_, _ = fmt.Fprintf(stdout, "status set %s: not yet implemented\n", args[1])
			return 0
		}
	}
	_, _ = fmt.Fprintln(stderr, "Usage: perch status set <working|waiting|done>")
	return 2
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
// the TUI stub, or prints usage to stderr and returns 2.
func handlePathArg(arg string, stdout, stderr io.Writer) int {
	info, err := os.Stat(arg)
	if err != nil || !info.IsDir() {
		_, _ = fmt.Fprintf(stderr, "perch: %q is not an existing directory\n", arg)
		printUsage(stderr)
		return 2
	}
	return handleTUI(arg, stdout)
}

// printUsage writes the usage summary to w.
// Note: "debug" is intentionally absent — it is a hidden diagnostic surface.
func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: perch [path]")
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
