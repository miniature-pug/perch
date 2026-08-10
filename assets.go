// Package perch (repo root) exists only to embed the built Svelte SPA. The
// embed MUST live here, not in app/, because go:embed cannot reference a path
// outside the embedding package's directory (no ".."), and frontend/ is a
// sibling of app/. cmd/perch passes Assets into app.Run.
package perch

import "embed"

// Assets is the built Svelte SPA. `all:` includes dotfiles, so the bundle
// never silently drops anything.
//
//go:embed all:frontend/dist
var Assets embed.FS
