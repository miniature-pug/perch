package main

import (
	perch "github.com/Miniature-Pug/perch"
	"github.com/Miniature-Pug/perch/app"
	"github.com/Miniature-Pug/perch/internal/config"
)

// launchGUI is the seam tests replace so `go test` never opens a webview.
var launchGUI = func(roots []string) error {
	return app.Run(perch.Assets, roots)
}

// guiRoots resolves the discovery roots the GUI scans. It loads the global
// config (degrading to [cwd] on any error) so the GUI and CLI agree on scope.
func guiRoots(cwd string) []string {
	globalPath := config.DefaultGlobalPath()
	if cfg, cerr := config.Load(globalPath, cwd); cerr == nil && len(cfg.Roots) > 0 {
		return cfg.Roots
	}
	return []string{cwd}
}
