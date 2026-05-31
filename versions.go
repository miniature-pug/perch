package perch

import _ "embed"

// ToolVersions is the raw, embedded content of the repo's .tool-versions file —
// the single source of truth for pinned external tool versions. Embedding it
// keeps doctor's drift-check working from the installed binary without shipping
// the file separately or duplicating any version string.
//
//go:embed .tool-versions
var ToolVersions string
