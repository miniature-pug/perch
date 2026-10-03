package app

import (
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// wtFilePrefix is the asset-server path under which the image preview loads
// a worktree file: /wt-file/<workspaceID>/<worktree-relative path>. The
// embedded SPA assets never use this prefix, so Wails falls through to
// worktreeFileHandler for it (AssetServer.Handler serves every GET the
// embedded assets cannot).
const wtFilePrefix = "/wt-file/"

// maxPreviewImageBytes caps one served image, so a huge file in a worktree
// cannot be streamed into the webview by the preview.
const maxPreviewImageBytes = 20 << 20

// previewImageTypes is the only content the handler serves: raster images and
// SVG, by extension. Anything else is 404, so the endpoint cannot be used to
// read arbitrary worktree files into the webview.
var previewImageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

// worktreeFileHandler serves preview images from a session's worktree
// (FEX-11). An absolute filesystem path in <img src> resolves against the
// embedded asset server and never loads; this handler is what the Preview
// points at instead. It serves a file only when:
//   - the workspace id is valid and registered,
//   - the relative path is clean and does not escape the worktree,
//   - the file, after resolving symlinks, is a regular file inside both the
//     resolved worktree and the configured roots,
//   - its extension is one of previewImageTypes, and it is at most
//     maxPreviewImageBytes.
func (a *App) worktreeFileHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		abs, mime, ok := a.resolvePreviewImage(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(abs)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxPreviewImageBytes {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", mime)
		h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		// An SVG opened as a document must not run script or load anything.
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = io.Copy(w, io.LimitReader(f, maxPreviewImageBytes))
	})
}

// resolvePreviewImage maps a /wt-file/<id>/<rel> URL path to the absolute
// file it may serve and its MIME type. ok is false for anything the handler
// must not serve.
func (a *App) resolvePreviewImage(urlPath string) (abs, mime string, ok bool) {
	rest, found := strings.CutPrefix(urlPath, wtFilePrefix)
	if !found {
		return "", "", false
	}
	id, rel, found := strings.Cut(rest, "/")
	if !found || validateSessionID(id) != nil || rel == "" {
		return "", "", false
	}
	if strings.ContainsRune(rel, 0) || strings.Contains(rel, "\\") {
		return "", "", false
	}
	// The URL path is already percent-decoded; it must be clean and relative.
	if path.Clean(rel) != rel || validateRelFile(rel) != nil {
		return "", "", false
	}
	mime, found = previewImageTypes[strings.ToLower(path.Ext(rel))]
	if !found {
		return "", "", false
	}
	ws, found := a.store.Get(id)
	if !found || ws.WorktreePath == "" {
		return "", "", false
	}
	treeResolved, err := filepath.EvalSymlinks(ws.WorktreePath)
	if err != nil {
		return "", "", false
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(ws.WorktreePath, filepath.FromSlash(rel)))
	if err != nil {
		return "", "", false
	}
	if !strings.HasPrefix(resolved, treeResolved+string(filepath.Separator)) {
		return "", "", false
	}
	if validateWorktreeUnderRoots(resolved, a.roots) != nil {
		return "", "", false
	}
	return resolved, mime, true
}
