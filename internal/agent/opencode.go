package agent

import (
	"os/exec"

	"github.com/miniature-pug/perch/internal/model"
)

// Opencode is the Adapter for the opencode CLI.
// All PATH access goes through the LookPath seam. This way unit tests touch
// neither real binaries nor the network.
type Opencode struct {
	// Bin is the opencode binary name or path. Default: "opencode".
	Bin string
	// LookPath resolves a binary on PATH. Detect uses it. Default: exec.LookPath.
	LookPath func(string) (string, error)
}

// Compile-time check: Opencode satisfies the Adapter interface, including for
// the zero value. Methods must not panic on nil seams.
var _ Adapter = Opencode{}

// NewOpencode returns an Opencode with production defaults filled in.
func NewOpencode() Opencode {
	return Opencode{
		Bin:      string(model.ToolOpencode),
		LookPath: exec.LookPath,
	}
}

func (o Opencode) lookPath() func(string) (string, error) {
	if o.LookPath != nil {
		return o.LookPath
	}
	return exec.LookPath
}

func (o Opencode) bin() string {
	if o.Bin != "" {
		return o.Bin
	}
	return string(model.ToolOpencode)
}

// Name returns the canonical tool identifier.
func (o Opencode) Name() string { return string(model.ToolOpencode) }

// Detect reports whether the opencode binary is on PATH.
func (o Opencode) Detect() bool {
	_, err := o.lookPath()(o.bin())
	return err == nil
}

// ResumeArgs returns the arguments that resume sessionID in an interactive
// TUI. perch launches interactive panes, so this uses the top-level form
// (opencode --session <id>), not the one-shot "opencode run" subcommand.
func (o Opencode) ResumeArgs(sessionID string) []string {
	return []string{"--session", sessionID}
}

// NewArgs builds the launch arguments for a new interactive session. perch
// does not pass --model. opencode attach takes no --model flag. Model
// selection is the harness's job.
func (o Opencode) NewArgs() []string { return nil }
