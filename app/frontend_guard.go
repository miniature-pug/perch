package app

import (
	"errors"
	"io/fs"
	"strings"
)

// embeddedIndexPath is the path, within the embedded assets FS, of the SPA's
// entry document. The root package embeds the build with `//go:embed
// all:frontend/dist`, so the file lands at this path inside the FS.
const embeddedIndexPath = "frontend/dist/index.html"

// errPlaceholderFrontend is returned when the binary embeds the committed
// placeholder stub instead of a real frontend build. The "perch: " prefix is
// added by the caller (cmd/perch handleLaunch). The message names the fix so a
// user who ran `make install` / `go install` (neither rebuilds the frontend)
// knows exactly what to do.
var errPlaceholderFrontend = errors.New(
	"this binary was built without the frontend. " +
		"Run 'make gui-build' (or 'make gui-install') and reinstall")

// detectPlaceholderFrontend reports whether indexHTML is the committed
// placeholder stub rather than a real Vite build. The stub is literally
// `<!doctype html><title>perch</title>` (see frontend/dist/index.html), which a
// plain `make install` / `go install` embeds because those targets do not
// rebuild the frontend — opening a blank window. A real Vite build references
// hashed asset bundles under /assets/ AND mounts the SPA into <div id="app">.
// Requiring BOTH markers reliably catches the stub (it has neither) without
// ever false-flagging a real build.
func detectPlaceholderFrontend(indexHTML string) bool {
	hasAssets := strings.Contains(indexHTML, "/assets/")
	hasMount := strings.Contains(indexHTML, `id="app"`)
	return !hasAssets || !hasMount
}

// checkFrontendIndex reads the embedded index.html and returns
// errPlaceholderFrontend when the frontend is the placeholder stub (or is
// missing entirely — an equally broken embed). It is called before the Wails
// window is created so a stub build fails fast with an actionable message
// instead of opening a blank window.
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
