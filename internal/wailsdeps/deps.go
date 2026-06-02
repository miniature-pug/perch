//go:build tools

// Package wailsdeps pins backend dependencies that are not yet imported by
// production code. The "tools" build tag ensures this file is never compiled
// in normal builds while still keeping the modules in go.mod and vendor/.
package wailsdeps

import (
	_ "github.com/creack/pty"
	_ "github.com/wailsapp/wails/v2"
)
