//go:build dev

package app

// This file is compiled ONLY under `-tags dev` (set automatically by
// `wails dev`). It exists to document — and make grep-able — the boundary:
// the dev reload websocket (ws://localhost:34115) is a Wails-internal,
// dev-tag-only facility. Production (`wails build`, plain `go build`) never
// compiles it. Do NOT add any options.App field here that opens a port in
// release builds; if a network listener is ever needed it must stay behind
// this `//go:build dev` tag.
const devReloadServerIsDevOnly = true
