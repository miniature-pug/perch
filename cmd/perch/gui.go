package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	perch "github.com/miniature-pug/perch"
	"github.com/miniature-pug/perch/app"
	"github.com/miniature-pug/perch/internal/config"
)

// launchGUI is the seam tests replace so `go test` never opens a webview.
var launchGUI = func(roots []string) error {
	return app.Run(perch.Assets, roots)
}

// worktreeDirSuffix names the sibling directory that holds a repo's linked
// worktrees (<parent>/<repo>__worktrees/<slug>); it matches internal/git.
const worktreeDirSuffix = "__worktrees"

// guiRoots resolves the discovery roots the GUI scans, so the GUI and CLI
// agree on scope. start is the absolute launch directory (cwd, or the
// path given on the command line); explicit reports that the user named it.
//
//   - config.toml roots win. The roots of an explicitly named directory
//     (see implicitRoots) are added in front of them, so `perch <path>`
//     always covers <path> and its worktree sessions.
//   - With no configured roots, start is the only root, plus its sibling
//     "<repo>__worktrees" directory when start is a repository, so the
//     default worktree sessions of a `perch` launched inside a repo stay
//     under a root.
//   - A config.toml that cannot be read or parsed is reported on stderr and
//     treated as absent, instead of being silently ignored.
func guiRoots(start string, explicit bool, stderr io.Writer) []string {
	var configured []string
	if cfg, err := config.Load(config.DefaultGlobalPath(), ""); err != nil {
		_, _ = fmt.Fprintf(stderr, "perch: ignoring the config file: %v\n", err)
	} else {
		configured = cfg.Roots
	}
	if len(configured) == 0 {
		return implicitRoots(start)
	}
	if !explicit {
		return configured
	}
	roots := implicitRoots(start)
	seen := map[string]bool{}
	for _, r := range roots {
		seen[r] = true
	}
	for _, r := range configured {
		if !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}
	return roots
}

// implicitRoots returns the roots that cover dir: dir itself, its
// symlink-resolved spelling when that differs, and, when dir is the top of a
// git repository or linked worktree, the sibling worktree directory of the
// resolved spelling. Discovery reports repositories by their resolved path,
// so worktree paths are derived from it; the sibling spelled through a
// symlink (which need not exist yet) would not contain them.
func implicitRoots(dir string) []string {
	roots := []string{dir}
	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	}
	if resolved != dir {
		roots = append(roots, resolved)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		roots = append(roots, filepath.Join(filepath.Dir(resolved), filepath.Base(resolved)+worktreeDirSuffix))
	}
	return roots
}
