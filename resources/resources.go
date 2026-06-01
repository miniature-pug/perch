// Package resources holds static assets embedded into the perch binary.
// claude-hooks.json is reference documentation for the merged hooks structure;
// the actual merge is performed programmatically by internal/agent.Claude.
package resources

import _ "embed"

// PerchStatusTS is the embedded opencode plugin, written verbatim to
// ~/.config/opencode/plugins/perch-status.ts by `perch setup`.
//
//go:embed perch-status.ts
var PerchStatusTS string
