package agent

import (
	"os/exec"

	"github.com/Miniature-Pug/perch/internal/model"
)

// Opencode is the Adapter for the opencode CLI.
// All PATH access is funnelled through the LookPath seam so unit tests touch
// neither real binaries nor the network.
type Opencode struct {
	// Bin is the opencode binary name or path. Default "opencode".
	Bin string
	// LookPath resolves a binary on PATH; used by Detect. Default exec.LookPath.
	LookPath func(string) (string, error)
}

// Compile-time guarantee that Opencode satisfies the Adapter interface,
// including for the zero value (methods must not panic on nil seams).
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

// Detect reports whether the opencode binary resolves on PATH.
func (o Opencode) Detect() bool {
	_, err := o.lookPath()(o.bin())
	return err == nil
}

// ResumeArgs returns the args to resume sessionID in an interactive TUI. perch
// launches interactive panes, so this uses the top-level form (opencode
// --session <id>), not the one-shot "opencode run" subcommand.
func (o Opencode) ResumeArgs(sessionID string) []string {
	return []string{"--session", sessionID}
}

// NewArgs builds the launch args for a fresh interactive session. perch does
// not pass --model; opencode attach accepts no --model flag (model selection
// is the harness's concern).
func (o Opencode) NewArgs() []string { return nil }
