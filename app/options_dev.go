//go:build dev

package app

// devReloadServerIsDevOnly marks the dev-only reload boundary for this file.
// Go compiles this file only under the `dev` build tag, which `wails dev`
// sets. This comment makes the boundary easy to find with grep.
// The dev reload websocket (ws://localhost:34115) is a Wails-internal
// facility for the dev tag only. Production builds, `wails build` or a plain
// `go build`, never compile this file. Do not add an options.App field here
// that opens a port in release builds. If perch ever needs a network
// listener, keep the listener behind this `//go:build dev` tag.
const devReloadServerIsDevOnly = true
