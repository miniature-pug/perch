// Package attach implements `perch attach <query>`: fuzzy-match one live/known
// agent session by query string and return the resolved tmux target so the
// caller can hand the terminal off to it.
//
// Architecture: Gather + Resolve are separated so that tests can exercise
// Resolve (pure) without touching the filesystem or spawning processes.
package attach

import (
	"context"
	"fmt"
	"strings"

	"github.com/sahilm/fuzzy"

	"github.com/Miniature-Pug/perch/internal/agent"
	"github.com/Miniature-Pug/perch/internal/discover"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/proc"
	"github.com/Miniature-Pug/perch/internal/tmux"
)

// Candidate is one attachable agent session discovered by Gather.
type Candidate struct {
	// Project is the project name (filepath.Base of the project root).
	Project string
	// Branch is the worktree branch name.
	Branch string
	// Tool is the agent tool identifier ("claude" or "opencode").
	Tool string
	// TmuxSession is the sanitised tmux session name for the project.
	TmuxSession string
	// TmuxWindow is the sanitised tmux window name for the branch.
	TmuxWindow string
	// LiveTarget is the tmux window target "=<session>:=<window>" of the live
	// pane, or empty string when the session is known but not live.
	LiveTarget string
	// IsLive reports whether the session has a live pane in tmux right now.
	IsLive bool
}

// MatchString returns the string used for fuzzy matching: "<project> <branch> <tool>".
func (c Candidate) MatchString() string {
	return c.Project + " " + c.Branch + " " + c.Tool
}

// Deps holds the injectable seams for Gather. Production callers use
// GatherDepsProduction(); tests inject fakes.
type Deps struct {
	// Tmux is used to list live panes (ListPanesAll).
	Tmux tmux.Tmux
	// Runner is used by discover and opencode adapters.
	Runner proc.Runner
	// Claude is the Claude session lister.
	Claude agent.Claude
	// Root is the filesystem root for project discovery.
	Root string
	// BaseDir is the perch state directory (for frecency ordering).
	BaseDir string
	// Now is the current unix timestamp (injected so tests can be deterministic).
	Now int64
}

// Gather discovers all candidate sessions (live + known agent sessions) and
// returns them as a slice of Candidate. Candidates from the same discovered
// tmux window are deduplicated by their TmuxSession+TmuxWindow pair.
//
// Only sessions with a live tmux pane are considered attachable in this
// implementation. Known-but-idle sessions are omitted because `perch attach`
// hands off the terminal to a live tmux session; launching an idle session is
// the TUI's job.
func Gather(ctx context.Context, deps Deps) ([]Candidate, error) {
	// Discover projects, already frecency-ordered.
	pts, err := discover.Projects(ctx, deps.Runner, deps.Root, discover.Options{}, nil, deps.Now)
	if err != nil {
		return nil, fmt.Errorf("attach: discover: %w", err)
	}

	// Snapshot live panes; build sessionID → pane index.
	panes, _ := deps.Tmux.ListPanesAll(ctx) // degrade on error: no live sessions
	liveBySession := buildLiveIndex(panes)

	// Claude sessions are global (not per-directory). Group by directory so
	// per-tree matching is O(1).
	claudeSessions, _ := deps.Claude.ListSessions(ctx)
	claudeByDir := agent.GroupByDirectory(claudeSessions)

	// Deduplicate by resolved window target to avoid counting multiple agent
	// sessions that all map to the same tmux window as separate matches.
	seen := make(map[string]bool)
	var candidates []Candidate

	for _, pt := range pts {
		proj := &pt.Project

		for i := range pt.Trees {
			tree := &pt.Trees[i]
			sessName := tmux.SessionName(proj.Path)
			winName := tmux.WindowName(tree.Branch)

			// Claude sessions for this tree.
			for _, s := range claudeByDir[tree.Path] {
				c := buildCandidate(s, proj.Name, tree.Branch, sessName, winName, liveBySession)
				if c.IsLive {
					key := c.TmuxSession + "\x00" + c.TmuxWindow
					if !seen[key] {
						seen[key] = true
						candidates = append(candidates, c)
					}
				}
			}

			// Opencode sessions for this tree.
			oc := agent.Opencode{
				Runner: deps.Runner,
				Bin:    "opencode",
				Dir:    tree.Path,
			}
			ocSessions, _ := oc.ListSessions(ctx)
			for _, s := range ocSessions {
				c := buildCandidate(s, proj.Name, tree.Branch, sessName, winName, liveBySession)
				if c.IsLive {
					key := c.TmuxSession + "\x00" + c.TmuxWindow
					if !seen[key] {
						seen[key] = true
						candidates = append(candidates, c)
					}
				}
			}
		}
	}

	return candidates, nil
}

// buildCandidate constructs one Candidate from a session and its project/tree
// metadata plus the live-pane index.
func buildCandidate(
	s model.Session,
	projectName, branch, sessName, winName string,
	liveBySession map[string]tmux.Pane,
) Candidate {
	pane, isLive := liveBySession[s.ID]
	var liveTarget string
	if isLive {
		liveTarget = tmux.WindowTarget(pane.Session, pane.Window)
	}
	return Candidate{
		Project:     projectName,
		Branch:      branch,
		Tool:        string(s.Tool),
		TmuxSession: sessName,
		TmuxWindow:  winName,
		LiveTarget:  liveTarget,
		IsLive:      isLive,
	}
}

// buildLiveIndex returns a map of sessionID → live Pane. Dead panes and panes
// with empty PerchSession are excluded. First-write-wins on duplicate IDs.
func buildLiveIndex(panes []tmux.Pane) map[string]tmux.Pane {
	idx := make(map[string]tmux.Pane, len(panes))
	for _, p := range panes {
		if p.Dead || p.PerchSession == "" {
			continue
		}
		if _, exists := idx[p.PerchSession]; exists {
			continue
		}
		idx[p.PerchSession] = p
	}
	return idx
}

// ResolveResult is the outcome of Resolve.
type ResolveResult struct {
	// Matched is the single matched candidate when len(Matched)==1.
	Matched *Candidate
	// Ambiguous holds all matches when there are 2 or more (caller should print
	// and exit 2).
	Ambiguous []Candidate
	// Count is the total number of matches (0, 1, or >1).
	Count int
}

// Resolve fuzzy-matches query against candidates and returns a ResolveResult.
// This is a pure function with no I/O — tests drive it directly.
//
// Matching:
//   - Each candidate's MatchString ("<project> <branch> <tool>") is ranked by
//     github.com/sahilm/fuzzy.
//   - 0 matches → ResolveResult{Count: 0}
//   - 1 match → ResolveResult{Count: 1, Matched: &candidates[i]}
//   - 2+ matches → ResolveResult{Count: n, Ambiguous: matched candidates}
//
// The query is only used for fuzzy matching and is never placed into a tmux
// target string — the target is always derived from the Candidate's TmuxSession
// and TmuxWindow fields.
func Resolve(query string, candidates []Candidate) ResolveResult {
	if len(candidates) == 0 {
		return ResolveResult{}
	}

	matchStrings := make([]string, len(candidates))
	for i, c := range candidates {
		matchStrings[i] = c.MatchString()
	}

	matches := fuzzy.Find(query, matchStrings)
	switch len(matches) {
	case 0:
		return ResolveResult{}
	case 1:
		c := candidates[matches[0].Index]
		return ResolveResult{Count: 1, Matched: &c}
	default:
		ambiguous := make([]Candidate, len(matches))
		for i, m := range matches {
			ambiguous[i] = candidates[m.Index]
		}
		return ResolveResult{Count: len(matches), Ambiguous: ambiguous}
	}
}

// FormatAmbiguous returns a multi-line description of ambiguous candidates for
// error messages, one per line: "  <project> <branch> (<tool>)".
func FormatAmbiguous(candidates []Candidate) string {
	var b strings.Builder
	for _, c := range candidates {
		_, _ = fmt.Fprintf(&b, "  %s %s (%s)\n", c.Project, c.Branch, c.Tool)
	}
	return b.String()
}
