package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/miniature-pug/perch/internal/registry"
)

// newWtFileTestApp registers one workspace whose worktree sits under a
// configured root, and returns the app, the worktree path and a directory
// outside the root.
func newWtFileTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	root := t.TempDir()
	outside := t.TempDir()
	wt := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(wt, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := registry.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(registry.Workspace{ID: "ws-img", WorktreePath: wt, Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	return &App{store: store, roots: []string{root}}, wt, outside
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestWorktreeFileHandler_ServesImagesInsideTheWorktree(t *testing.T) {
	a, wt, _ := newWtFileTestApp(t)
	png := []byte("\x89PNG\r\n\x1a\nfake")
	if err := os.WriteFile(filepath.Join(wt, "docs", "logo.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "My Diagram.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := a.worktreeFileHandler()

	rec := get(t, h, http.MethodGet, "/wt-file/ws-img/docs/logo.png")
	if rec.Code != http.StatusOK || rec.Body.String() != string(png) {
		t.Fatalf("logo.png: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}

	rec = get(t, h, http.MethodGet, "/wt-file/ws-img/My%20Diagram.svg")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("svg: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("svg must carry a restrictive CSP")
	}
}

func TestWorktreeFileHandler_RefusesEverythingElse(t *testing.T) {
	a, wt, outside := newWtFileTestApp(t)
	mustWrite := func(p string, b string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(wt, "secret.txt"), "token")
	mustWrite(filepath.Join(wt, "docs", "a.png"), "png")
	mustWrite(filepath.Join(outside, "out.png"), "outside")
	if err := os.Symlink(filepath.Join(outside, "out.png"), filepath.Join(wt, "escape.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wt, "dir.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	big, err := os.Create(filepath.Join(wt, "huge.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(maxPreviewImageBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := big.Close(); err != nil {
		t.Fatal(err)
	}

	h := a.worktreeFileHandler()
	for _, target := range []string{
		"/wt-file/ws-img/secret.txt",         // not an image type
		"/wt-file/ws-img/escape.png",         // symlink out of the worktree
		"/wt-file/ws-img/docs/../../x.png",   // escapes via ..
		"/wt-file/ws-img/%2e%2e/outside.png", // encoded ..
		"/wt-file/ws-img//etc/passwd.png",    // absolute after the id
		"/wt-file/ws-img/dir.png",            // not a regular file
		"/wt-file/ws-img/huge.png",           // over the size cap
		"/wt-file/ws-img/missing.png",        // does not exist
		"/wt-file/unknown/docs/a.png",        // unregistered workspace
		"/wt-file/bad%20id/docs/a.png",       // invalid id
		"/wt-file/ws-img",                    // no path
		"/other/ws-img/docs/a.png",           // wrong prefix
	} {
		if rec := get(t, h, http.MethodGet, target); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, rec.Code)
		}
	}
	if rec := get(t, h, http.MethodPost, "/wt-file/ws-img/docs/a.png"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rec.Code)
	}
}

func TestWorktreeFileHandler_WorktreeOutsideRootsIsRefused(t *testing.T) {
	a, wt, _ := newWtFileTestApp(t)
	if err := os.WriteFile(filepath.Join(wt, "a.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.roots = []string{t.TempDir()} // the worktree is no longer under a root
	if rec := get(t, a.worktreeFileHandler(), http.MethodGet, "/wt-file/ws-img/a.png"); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}
