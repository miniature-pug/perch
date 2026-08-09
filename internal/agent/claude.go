package agent

import (
	"os/exec"

	"github.com/miniature-pug/perch/internal/model"
)

// Claude is the Adapter for Anthropic's claude-code CLI.
// All PATH access is funnelled through the LookPath seam so unit tests touch
// neither real binaries nor the filesystem.
type Claude struct {
	// Bin is the claude binary name or path used by Detect. Default "claude".
	Bin string
	// LookPath resolves a binary on PATH; used by Detect. Default exec.LookPath.
	LookPath func(string) (string, error)
}

// Compile-time guarantee that Claude satisfies the Adapter interface, including
// for the zero value (methods must not panic on nil seams).
var _ Adapter = Claude{}

// NewClaude returns a Claude with all production defaults filled in. It is the
// supported constructor; methods also fall back to defaults internally so a
// partially-constructed or zero Claude never panics.
func NewClaude() Claude {
	return Claude{
		Bin:      string(model.ToolClaude),
		LookPath: exec.LookPath,
	}
}

func (c Claude) lookPath() func(string) (string, error) {
	if c.LookPath != nil {
		return c.LookPath
	}
	return exec.LookPath
}

func (c Claude) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return string(model.ToolClaude)
}

// Name returns the canonical tool identifier.
func (c Claude) Name() string { return string(model.ToolClaude) }

// Detect reports whether the claude binary resolves on PATH.
func (c Claude) Detect() bool {
	_, err := c.lookPath()(c.bin())
	return err == nil
}

// ResumeArgs returns the args to resume sessionID in the current directory.
func (c Claude) ResumeArgs(sessionID string) []string {
	return []string{"--resume", sessionID}
}

// NewArgs builds the launch args for a fresh session. perch does not pass
// --model; model selection is the harness's concern.
func (c Claude) NewArgs() []string { return nil }
