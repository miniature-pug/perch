package tui

import (
	"strings"

	"github.com/sahilm/fuzzy"
)

// cmdKind enumerates the recognised ':' command-bar verbs.
type cmdKind int

const (
	cmdUnknown cmdKind = iota
	cmdQuit
	cmdNew
	cmdAttach
	cmdProj
	cmdSetup
	cmdDoctor
	cmdResurrect
	cmdHelp
)

// cmdSpec is the parsed result of a command-bar line. When parseErr is non-empty
// the command is invalid: the dispatcher surfaces parseErr as a toast and takes
// no action (kind stays cmdUnknown). An empty/whitespace line yields cmdUnknown
// with no parseErr — a silent cancel.
type cmdSpec struct {
	kind     cmdKind
	arg      string // free-form query for attach/proj (everything after the verb)
	replace  bool   // setup --replace
	parseErr string
}

// parseCommand parses a command-bar line (the text typed after the leading ':')
// into a cmdSpec. Verbs are matched case-insensitively; the query argument for
// attach/proj preserves the user's original casing and inner spacing.
func parseCommand(input string) cmdSpec {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return cmdSpec{kind: cmdUnknown}
	}
	fields := strings.Fields(trimmed)
	verb := strings.ToLower(fields[0])
	rest := fields[1:]
	// arg is everything after the first token, original case + inner spacing.
	arg := strings.TrimSpace(trimmed[len(fields[0]):])

	switch verb {
	case "q", "quit":
		if len(rest) > 0 {
			return cmdSpec{parseErr: "quit takes no arguments"}
		}
		return cmdSpec{kind: cmdQuit}
	case "new":
		if len(rest) > 0 {
			return cmdSpec{parseErr: "new takes no arguments"}
		}
		return cmdSpec{kind: cmdNew}
	case "attach":
		if arg == "" {
			return cmdSpec{parseErr: "attach needs a query: :attach <text>"}
		}
		return cmdSpec{kind: cmdAttach, arg: arg}
	case "proj", "project":
		if arg == "" {
			return cmdSpec{parseErr: "proj needs a name: :proj <text>"}
		}
		return cmdSpec{kind: cmdProj, arg: arg}
	case "setup":
		spec := cmdSpec{kind: cmdSetup}
		for _, tok := range rest {
			switch tok {
			case "--replace":
				spec.replace = true
			default:
				return cmdSpec{parseErr: "setup: unknown flag " + tok}
			}
		}
		return spec
	case "doctor":
		if len(rest) > 0 {
			return cmdSpec{parseErr: "doctor takes no arguments"}
		}
		return cmdSpec{kind: cmdDoctor}
	case "resurrect":
		if len(rest) > 0 {
			return cmdSpec{parseErr: "resurrect takes no arguments"}
		}
		return cmdSpec{kind: cmdResurrect}
	case "help", "h", "?":
		if len(rest) > 0 {
			return cmdSpec{parseErr: "help takes no arguments"}
		}
		return cmdSpec{kind: cmdHelp}
	default:
		return cmdSpec{parseErr: "unknown command: " + verb}
	}
}

// resolveItem fuzzy-matches query against the session rows currently in the list
// and returns the index of the best match, or -1 when nothing matches. Matching
// uses the same FilterValue (project + tree + title + tool) as the '/' filter and
// skips non-session rows (the "start new" placeholders).
func (m Model) resolveItem(query string) int {
	listItems := m.list.Items()
	var (
		idxs    []int
		targets []string
	)
	for i, li := range listItems {
		it, ok := li.(item)
		if !ok || !it.isSession {
			continue
		}
		idxs = append(idxs, i)
		targets = append(targets, it.FilterValue())
	}
	matches := fuzzy.Find(query, targets)
	if len(matches) == 0 {
		return -1
	}
	return idxs[matches[0].Index]
}
