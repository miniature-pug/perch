package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/Miniature-Pug/perch/internal/discover"
	"github.com/Miniature-Pug/perch/internal/doctor"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/state"
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
	if len(args) >= 1 && args[0] == "discover" {
		return handleDebugDiscover(args[1:], stdout, stderr)
	}
	_, _ = fmt.Fprintln(stderr, "Usage: perch debug discover [path]")
	return 2
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

	for _, pt := range projects {
		_, _ = fmt.Fprintf(stdout, "%s  %s\n", pt.Project.Name, pt.Project.Path)
		for _, tr := range pt.Trees {
			marker := " "
			if tr.IsMain {
				marker = "*"
			}
			_, _ = fmt.Fprintf(stdout, "  %s %s  %s\n", marker, tr.Branch, tr.Path)
		}
	}
	return 0
}
