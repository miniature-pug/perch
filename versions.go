package perch

import _ "embed"

// ToolVersions is the raw, embedded content of the repo's .tool-versions
// file: the single source of truth for pinned external tool versions.
// Embedding the file lets doctor's drift check work from the installed
// binary, without shipping the file separately or duplicating any version
// string.
//
//go:embed .tool-versions
var ToolVersions string
