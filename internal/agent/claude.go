package agent

import (
	"os/exec"

	"github.com/miniature-pug/perch/internal/model"
)

// Claude is the Adapter for Anthropic's claude-code CLI.
// All PATH access goes through the LookPath seam. This way unit tests touch
// neither real binaries nor the filesystem.
type Claude struct {
	// Bin is the claude binary name or path. Detect uses it. Default: "claude".
	Bin string
	// LookPath resolves a binary on PATH. Detect uses it. Default: exec.LookPath.
	LookPath func(string) (string, error)
}

// Compile-time check: Claude satisfies the Adapter interface, including for
// the zero value. Methods must not panic on nil seams.
var _ Adapter = Claude{}

// NewClaude returns a Claude with all production defaults set. Use this
// constructor. The methods also fall back to defaults on their own, so a
// partial or zero-value Claude never panics.
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

// Detect reports whether the claude binary is on PATH.
func (c Claude) Detect() bool {
	_, err := c.lookPath()(c.bin())
	return err == nil
}

// ResumeArgs returns the arguments that resume sessionID in the current
// directory.
func (c Claude) ResumeArgs(sessionID string) []string {
	return []string{"--resume", sessionID}
}

// NewArgs builds the launch arguments for a new session. perch does not pass
// --model. Model selection is the harness's job.
func (c Claude) NewArgs() []string { return nil }
