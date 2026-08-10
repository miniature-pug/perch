// Command perch is a keyboard-first GUI that manages AI coding sessions
// (claude, opencode) across git worktrees. Run it without arguments, or
// with a path, and it launches the Wails desktop GUI. It also provides the
// attach, doctor, reload, and version subcommands (plus a hidden debug
// subcommand). See ARCHITECTURE.md for the full design.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/miniature-pug/perch/internal/discover"
	"github.com/miniature-pug/perch/internal/doctor"
	"github.com/miniature-pug/perch/internal/envsync"
	"github.com/miniature-pug/perch/internal/proc"
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

// run is the testable entry point. All handler output goes to stdout or
// stderr, never directly to os.Stdout or os.Stderr. run returns the exit
// code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return handleLaunch("", stdout, stderr)
	}

	switch args[0] {
	case "doctor":
		return doctor.Run(version, stdout, doctor.RealSystem())
	case "version":
		return handleVersion(stdout)
	// debug is intentionally hidden from printUsage. It is a diagnostic
	// surface, not part of the public CLI contract.
	case "debug":
		return handleDebug(args[1:], stdout, stderr)
	case "attach":
		return handleAttach(args[1:], stdout, stderr)
	case "reload":
		return handleReload(stdout, stderr)
	default:
		// Treat the first argument as a path to a project root.
		return handlePathArg(args[0], stdout, stderr)
	}
}

// handleLaunch is the default entry point. It resolves the project root
// (cwd when root==""), resolves the discovery roots, and launches the Wails
// GUI through the launchGUI seam. The GUI owns the interactive shell. There
// is no terminal TUI.
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

// handleVersion prints the perch version and build info.
// VCS fields from debug.ReadBuildInfo are optional. Under -trimpath builds
// and vendored builds, these stamps may be absent, so every access checks
// first.
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

// handleAttach implements `perch attach <query>`. When a perch window is
// already running, the Wails SingleInstanceLock forwards os.Args[1:] to it
// automatically. This raises the window, and routes the query to workspace
// selection through the workspace:attach event, and this process exits.
// When no perch instance is running, handleAttach launches a fresh GUI
// instead (the query is best-effort ignored in that case, which is
// acceptable for v1).
//
// Note: on Linux the forwarding process exits non-zero. This is expected,
// and does not indicate an error.
func handleAttach(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(strings.Join(args, " ")) == "" {
		_, _ = fmt.Fprintln(stderr, "Usage: perch attach <query>")
		return 2
	}
	return handleLaunch("", stdout, stderr)
}

// reloadHTTPTimeout bounds the loopback POST so a wedged endpoint cannot hang the
// session terminal. The endpoint is on 127.0.0.1 and responds immediately, so this
// is generous.
const reloadHTTPTimeout = 10 * time.Second

// handleReload implements `perch reload`, run inside a per-workspace session
// terminal. It reads its own environment (os.Environ, everything the user
// just exported) and the env-sync handles the drawer injected
// (PERCH_ENVSYNC_URL, PERCH_ENVSYNC_TOKEN, PERCH_ENVSYNC_WS). handleReload
// then POSTs the environment plus the workspace id to the app's loopback
// endpoint, with the Bearer token. The app computes the delta against its
// baseline, and relaunches the agent, preserving the conversation.
//
// handleReload NEVER launches the GUI. When the PERCH_ENVSYNC_* handles are
// absent, the command did not run inside a perch session terminal, so
// handleReload prints a friendly error and exits non-zero. It never prints
// or logs environment values. It prints only a generic confirmation.
func handleReload(stdout, stderr io.Writer) int {
	url := strings.TrimSpace(os.Getenv(envsync.EnvURL))
	token := strings.TrimSpace(os.Getenv(envsync.EnvToken))
	ws := strings.TrimSpace(os.Getenv(envsync.EnvWS))
	if url == "" || token == "" || ws == "" {
		_, _ = fmt.Fprintln(stderr, "perch reload: not inside a perch session terminal (PERCH_ENVSYNC_* not set).")
		_, _ = fmt.Fprintln(stderr, "Run it from a session shell drawer, or use the reload button there.")
		return 1
	}

	body, err := json.Marshal(envsync.SyncRequest{WorkspaceID: ws, Env: os.Environ()})
	if err != nil {
		// Never include the body in the error. It may hold secrets.
		_, _ = fmt.Fprintln(stderr, "perch reload: could not encode environment.")
		return 1
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "perch reload: could not build request.")
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: reloadHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch reload: could not reach the perch app: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "perch reload: the perch app rejected the request (%s).\n", resp.Status)
		return 1
	}

	_, _ = fmt.Fprintln(stdout, "perch: environment sent; relaunching the agent with your updated environment (your conversation is preserved).")
	return 0
}

// printUsage writes the usage summary to w.
// Note: printUsage intentionally omits "debug". It is a hidden diagnostic
// surface.
func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: perch [path]")
	_, _ = fmt.Fprintln(w, "       perch attach <query>")
	_, _ = fmt.Fprintln(w, "       perch doctor")
	_, _ = fmt.Fprintln(w, "       perch reload")
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
		map[string]discover.ProjectStat{},
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
// and the tree path. writeProjects is a pure formatting function. It performs
// no I/O beyond writing to w.
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
