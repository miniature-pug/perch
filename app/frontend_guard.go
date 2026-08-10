package app

import (
	"errors"
	"io/fs"
	"strings"
)

// embeddedIndexPath is the path of the SPA's entry document in the embedded
// assets FS. The root package embeds the build with `//go:embed
// all:frontend/dist`. The file lands at this path in the FS.
const embeddedIndexPath = "frontend/dist/index.html"

// errPlaceholderFrontend occurs when the binary embeds the committed
// placeholder stub, not a real frontend build. The caller (cmd/perch
// handleLaunch) adds the "perch: " prefix. The message names the fix. A user
// who runs `make install` or `go install` does not rebuild the frontend. The
// message tells this user what to do next.
var errPlaceholderFrontend = errors.New(
	"this binary was built without the frontend. " +
		"Run 'make gui-build' (or 'make gui-install') and reinstall")

// detectPlaceholderFrontend reports whether indexHTML is the committed
// placeholder stub, not a real Vite build. The stub is exactly
// `<!doctype html><title>perch</title>` (see frontend/dist/index.html). A
// plain `make install` or `go install` embeds this stub, because neither
// target rebuilds the frontend. The app then opens a blank window. A real
// Vite build references hashed asset bundles under /assets/ and mounts the
// SPA into <div id="app">. This function checks for both markers. The stub
// has neither marker, so the check catches it. The check never flags a real
// build as a placeholder.
func detectPlaceholderFrontend(indexHTML string) bool {
	hasAssets := strings.Contains(indexHTML, "/assets/")
	hasMount := strings.Contains(indexHTML, `id="app"`)
	return !hasAssets || !hasMount
}

// checkFrontendIndex reads the embedded index.html file. It returns
// errPlaceholderFrontend when the frontend is the placeholder stub. It also
// returns errPlaceholderFrontend when the index.html file is missing, an
// equally broken embed. The caller runs checkFrontendIndex before the Wails
// window opens. A stub build then fails fast with a clear message, instead of
// opening a blank window.
func checkFrontendIndex(assets fs.FS) error {
	data, err := fs.ReadFile(assets, embeddedIndexPath)
	if err != nil {
		return errPlaceholderFrontend
	}
	if detectPlaceholderFrontend(string(data)) {
		return errPlaceholderFrontend
	}
	return nil
}
