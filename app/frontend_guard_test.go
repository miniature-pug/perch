package app

import (
	"errors"
	"testing"
	"testing/fstest"
)

func TestDetectPlaceholderFrontend(t *testing.T) {
	// The exact committed stub (frontend/dist/index.html at HEAD).
	const stub = "<!doctype html><title>perch</title>\n"
	if !detectPlaceholderFrontend(stub) {
		t.Error("the committed stub must be detected as a placeholder frontend")
	}

	// A realistic Vite build: hashed asset bundles under /assets/ plus the SPA
	// mount element. Must NOT be flagged as a placeholder.
	const real = `<!doctype html>` +
		`<html lang="en"><head><meta charset="UTF-8" />` +
		`<title>perch</title>` +
		`<script type="module" crossorigin src="/assets/index-qB16fjIl.js"></script>` +
		`<link rel="stylesheet" crossorigin href="/assets/index-Cy86ZcYM.css">` +
		`</head><body><div id="app"></div></body></html>`
	if detectPlaceholderFrontend(real) {
		t.Error("a real Vite build must NOT be detected as a placeholder frontend")
	}

	// Defensive: a document missing either marker is treated as a placeholder.
	if !detectPlaceholderFrontend(`<div id="app"></div>`) {
		t.Error("an index without /assets/ bundles must be treated as a placeholder")
	}
	if !detectPlaceholderFrontend(`<script src="/assets/index-abc.js"></script>`) {
		t.Error("an index without the app mount element must be treated as a placeholder")
	}
}

func TestCheckFrontendIndex(t *testing.T) {
	stubFS := fstest.MapFS{
		embeddedIndexPath: {Data: []byte("<!doctype html><title>perch</title>\n")},
	}
	if err := checkFrontendIndex(stubFS); !errors.Is(err, errPlaceholderFrontend) {
		t.Errorf("stub embed must yield errPlaceholderFrontend, got %v", err)
	}

	realFS := fstest.MapFS{
		embeddedIndexPath: {Data: []byte(
			`<script type="module" src="/assets/index-abc.js"></script><div id="app"></div>`)},
	}
	if err := checkFrontendIndex(realFS); err != nil {
		t.Errorf("a real embed must pass the guard, got %v", err)
	}

	// A missing index.html is an equally broken embed.
	if err := checkFrontendIndex(fstest.MapFS{}); !errors.Is(err, errPlaceholderFrontend) {
		t.Errorf("a missing index must yield errPlaceholderFrontend, got %v", err)
	}
}
