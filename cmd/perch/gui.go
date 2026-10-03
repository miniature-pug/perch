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
//   - config.toml roots win. An explicitly named directory is added in front
//     of them, so `perch <path>` always covers <path>.
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
	roots := []string{start}
	for _, r := range configured {
		if r != start {
			roots = append(roots, r)
		}
	}
	return roots
}

// implicitRoots returns dir, plus its sibling worktree directory when dir is
// the top of a git repository or linked worktree.
func implicitRoots(dir string) []string {
	roots := []string{dir}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		roots = append(roots, filepath.Join(filepath.Dir(dir), filepath.Base(dir)+worktreeDirSuffix))
	}
	return roots
}
