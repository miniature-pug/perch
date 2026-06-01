package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Miniature-Pug/perch/internal/config"
	"github.com/Miniature-Pug/perch/internal/git"
	"github.com/Miniature-Pug/perch/internal/model"
	"github.com/Miniature-Pug/perch/internal/state"
	"github.com/Miniature-Pug/perch/internal/tmux"
	"github.com/Miniature-Pug/perch/internal/worktree"
)

// projectConfig returns the merged config for projectPath. When loader.Config
// is non-nil (test override) it is returned directly. Otherwise Load is called
// against the real filesystem.
func (m Model) projectConfig(projectPath string) (*config.Config, error) {
	if m.loader != nil && m.loader.Config != nil {
		return m.loader.Config, nil
	}
	gp, err := config.DefaultGlobalPath()
	if err != nil {
		return nil, err
	}
	return config.Load(gp, projectPath)
}

// preflightRemoveCmd returns a tea.Cmd that checks whether the current tmux
// client is focused on the worktree window for it, delivering a
// preflightRemoveMsg.
func (m Model) preflightRemoveCmd(it item) tea.Cmd {
	if m.loader == nil {
		// No loader: cannot check — treat as not focused, proceed to confirm.
		spec := modalState{
			kind:        modalRemoveConfirm,
			treePath:    it.treePath,
			branch:      it.tree,
			projectPath: it.projectPath,
			target:      it.liveTarget,
			paneKey:     it.captureTarget,
			tool:        it.tool,
			sessionID:   it.id,
		}
		return func() tea.Msg { return preflightRemoveMsg{spec: spec, focused: false} }
	}
	ldr := m.loader
	spec := modalState{
		kind:        modalRemoveConfirm,
		treePath:    it.treePath,
		branch:      it.tree,
		projectPath: it.projectPath,
		target:      it.liveTarget,
		paneKey:     it.captureTarget,
		tool:        it.tool,
		sessionID:   it.id,
	}
	return func() tea.Msg {
		ctx := ldr.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		sess, win, err := ldr.Tmux.CurrentClientWindow(ctx)
		focused := err == nil && tmux.WindowTarget(sess, win) == it.liveTarget
		return preflightRemoveMsg{spec: spec, focused: focused, err: err}
	}
}

// removeCmd returns a tea.Cmd that performs the full §7.2 worktree-remove
// sequence in order:
//  1. (unless skipPrep) load config + run pre_remove hooks
//  2. (unless skipPrep) unlock the internal worktree lock file
//  3. git worktree remove (force when force==true)
//  4. kill the live tmux window if present
//  5. remove the shadow state record if present
func (m Model) removeCmd(spec modalState, force, skipPrep bool) tea.Cmd {
	if m.loader == nil {
		return func() tea.Msg {
			return removeResultMsg{spec: spec, err: errors.New("tui: removeCmd: no loader configured")}
		}
	}
	ldr := m.loader
	return func() tea.Msg {
		ctx := ldr.ctx
		if ctx == nil {
			ctx = context.Background()
		}

		if !skipPrep {
			// 1. Load config and run pre_remove hooks.
			cfg, err := m.projectConfig(spec.projectPath)
			if err != nil {
				return removeResultMsg{spec: spec, err: err}
			}
			if len(cfg.PreRemove) > 0 {
				env := worktree.HookEnv{
					Handle:       filepath.Base(spec.treePath),
					WorktreePath: spec.treePath,
					ProjectRoot:  spec.projectPath,
					Branch:       spec.branch,
				}
				if err := worktree.RunHooks(ctx, ldr.Runner, spec.treePath, "pre_remove", cfg.PreRemove, env); err != nil {
					return removeResultMsg{spec: spec, err: err}
				}
			}

			// 2. Best-effort unlock so git worktree remove isn't blocked.
			internalName, _ := git.InternalName(spec.treePath)
			_ = git.RemoveLock(spec.projectPath, internalName)
		}

		// 3. Remove the worktree.
		err := git.RemoveWorktree(ctx, ldr.Runner, spec.projectPath, spec.treePath, force)
		if err != nil {
			if errors.Is(err, git.ErrWorktreeDirty) && !force {
				return removeResultMsg{spec: spec, dirty: true}
			}
			return removeResultMsg{spec: spec, err: err}
		}

		// 4. Kill the live tmux window if present.
		if spec.target != "" {
			_ = ldr.Tmux.KillWindow(ctx, spec.target)
		}

		// 5. Remove the shadow state record if present.
		if spec.paneKey != "" {
			_ = state.RemoveWindow(ldr.BaseDir, spec.paneKey)
		}

		return removeResultMsg{spec: spec}
	}
}

// worktreePreflightCmd returns a tea.Cmd that checks the existing session→worktree
// mapping and decides whether to open the picker or resume directly.
func (m Model) worktreePreflightCmd(it item) tea.Cmd {
	// Build the openModal state once — used in several branches below.
	openModal := modalState{
		kind:        modalNewSession,
		action:      0,
		tool:        it.tool,
		sessionID:   it.id,
		treePath:    it.treePath,
		branch:      it.tree,
		projectPath: it.projectPath,
	}

	if m.loader == nil {
		return func() tea.Msg {
			return worktreePreflightMsg{prompt: true, openModal: openModal}
		}
	}
	ldr := m.loader
	return func() tea.Msg {
		st, _ := state.LoadState(ldr.BaseDir)
		mp, ok := state.LookupMapping(st, it.id)
		if !ok {
			return worktreePreflightMsg{prompt: true, openModal: openModal}
		}
		if mp.Choice == state.ChoiceWorktree {
			// If the recorded worktree no longer exists, re-ask.
			if _, err := os.Stat(mp.Tree); err != nil {
				return worktreePreflightMsg{prompt: true, openModal: openModal}
			}
		}
		// Existing mapping found — resume into the recorded tree.
		return worktreePreflightMsg{
			spec: launchSpec{
				tool:        string(mp.Tool),
				sessionID:   it.id,
				branch:      it.tree,
				treePath:    mp.Tree,
				projectPath: it.projectPath,
				resume:      true,
			},
		}
	}
}

// worktreeCreateCmd performs the full worktree create chain:
// config load → validate → branch/path derivation → git worktree add →
// seed → post_create hooks → state mapping → launchSpec.
func (m Model) worktreeCreateCmd(ms modalState) tea.Cmd {
	if m.loader == nil {
		return func() tea.Msg {
			return worktreeCreatedMsg{err: errors.New("tui: worktreeCreateCmd: no loader configured")}
		}
	}
	ldr := m.loader
	return func() tea.Msg {
		ctx := ldr.ctx
		if ctx == nil {
			ctx = context.Background()
		}

		// 1. Load config.
		cfg, err := m.projectConfig(ms.projectPath)
		if err != nil {
			return worktreeCreatedMsg{err: err}
		}
		if cfg == nil {
			cfg = &config.Config{}
		}

		// 2. Validate config before any filesystem mutation.
		if err := cfg.Validate(ms.projectPath); err != nil {
			return worktreeCreatedMsg{err: err}
		}

		// 3. Derive branch and handle.
		uuid, err := newSessionID()
		if err != nil {
			return worktreeCreatedMsg{err: err}
		}
		short := uuid[:8]
		slug := git.SlugifyBranch(ms.branch)
		if slug == "" {
			slug = "worktree"
		}
		branch := "perch/" + slug + "-" + short
		handle := git.SlugifyBranch(branch)

		// 4. Resolve the filesystem path.
		treePath, err := git.WorktreePath(ms.projectPath, handle, cfg.WorktreeDir)
		if err != nil {
			return worktreeCreatedMsg{err: err}
		}

		// 5. Determine base branch.
		base := cfg.BaseBranch
		if base == "" {
			base = "HEAD"
		}

		// 6. Create the git worktree.
		if err := git.AddWorktree(ctx, ldr.Runner, ms.projectPath, branch, treePath, base); err != nil {
			return worktreeCreatedMsg{err: err}
		}

		// 7. Seed files.
		if err := worktree.Seed(ms.projectPath, treePath, cfg.Files); err != nil {
			return worktreeCreatedMsg{err: err}
		}

		// 8. Run post_create hooks.
		env := worktree.HookEnv{
			Handle:       handle,
			WorktreePath: treePath,
			ProjectRoot:  ms.projectPath,
			Branch:       branch,
		}
		if err := worktree.RunHooks(ctx, ldr.Runner, treePath, "post_create", cfg.PostCreate, env); err != nil {
			return worktreeCreatedMsg{err: err}
		}

		// 9. Record mapping.
		st, _ := state.LoadState(ldr.BaseDir)
		state.SetMapping(&st, ms.sessionID, state.Mapping{
			Tool:   model.Tool(ms.tool),
			Tree:   treePath,
			Choice: state.ChoiceWorktree,
		})
		_ = state.SaveState(ldr.BaseDir, st)

		// 10. Return the fork spec.
		return worktreeCreatedMsg{spec: launchSpec{
			tool:        ms.tool,
			sessionID:   ms.sessionID,
			branch:      branch,
			treePath:    treePath,
			projectPath: ms.projectPath,
			fork:        true,
		}}
	}
}

// runHereCmd records a ChoiceNone mapping (session runs in its current tree)
// and returns the resume launchSpec without any git work.
func (m Model) runHereCmd(ms modalState) tea.Cmd {
	ldr := m.loader
	return func() tea.Msg {
		if ldr != nil {
			st, _ := state.LoadState(ldr.BaseDir)
			state.SetMapping(&st, ms.sessionID, state.Mapping{
				Tool:   model.Tool(ms.tool),
				Tree:   ms.treePath,
				Choice: state.ChoiceNone,
			})
			_ = state.SaveState(ldr.BaseDir, st)
		}
		return worktreeCreatedMsg{spec: launchSpec{
			tool:        ms.tool,
			sessionID:   ms.sessionID,
			branch:      ms.branch,
			treePath:    ms.treePath,
			projectPath: ms.projectPath,
			resume:      true,
		}}
	}
}

// runMainCmd records a ChoiceNone mapping pointing at the main checkout and
// returns the resume launchSpec.
func (m Model) runMainCmd(ms modalState) tea.Cmd {
	ldr := m.loader
	return func() tea.Msg {
		if ldr != nil {
			st, _ := state.LoadState(ldr.BaseDir)
			state.SetMapping(&st, ms.sessionID, state.Mapping{
				Tool:   model.Tool(ms.tool),
				Tree:   ms.projectPath,
				Choice: state.ChoiceNone,
			})
			_ = state.SaveState(ldr.BaseDir, st)
		}
		return worktreeCreatedMsg{spec: launchSpec{
			tool:        ms.tool,
			sessionID:   ms.sessionID,
			branch:      ms.branch,
			treePath:    ms.projectPath,
			projectPath: ms.projectPath,
			resume:      true,
		}}
	}
}

// killCmd returns a tea.Cmd that kills the tmux window at target and removes
// the shadow state record for paneKey (if non-empty). No git worktree remove.
func (m Model) killCmd(target, paneKey string) tea.Cmd {
	if m.loader == nil {
		return func() tea.Msg { return killResultMsg{} }
	}
	ldr := m.loader
	return func() tea.Msg {
		ctx := ldr.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		_ = ldr.Tmux.KillWindow(ctx, target)
		if paneKey != "" {
			_ = state.RemoveWindow(ldr.BaseDir, paneKey)
		}
		return killResultMsg{}
	}
}
