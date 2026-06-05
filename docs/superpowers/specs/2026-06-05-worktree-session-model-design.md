# Worktree-Native Session Model + Cleanup — Design

**Date:** 2026-06-05
**Branch:** `feat/perch-v1`
**Status:** Approved (design); pending implementation plan (writing-plans).

## Problem

Hands-on testing of the New Session flow surfaced that worktrees are the crux of
perch's value, not a side detail. The current flow has gaps and a wrong default:

- Model selection is exposed in the New Session dialog. perch is a management /
  productivity cockpit around agents — it must not govern what the harness does.
  Model choice belongs to the harness, not perch.
- The session/worktree model was never made explicit: how parallel sessions map
  to branches and trees, who creates branches, who merges them back, and how a
  user resumes prior work.
- Existing sessions *do* persist and list in the sidebar, but the resume path is
  undiscoverable (a saved row looks like a live one; empty state is blank).
- Bare worktrees receive no gitignored files (`.env`, local config), so an agent
  can launch into a tree where it cannot run.
- Stale worktrees/branches accumulate with no cleanup path.

## Research summary (primary sources)

- **Peer cockpits enforce worktrees and own the lifecycle** — workmux,
  claude-squad, uzi, crystal, conductor, vibe-kanban, phantom, gwq all do
  one-tree-per-session, tool-owned, with a first-class new-vs-resume choice.
  (workmux confirmed worktree-native; `--open-if-exists` is its resume idiom.)
- **Claude Code has first-party worktrees** — `--worktree`/`-w`, `worktree.baseRef`,
  `.worktreeinclude` (copies gitignored files), `WorktreeCreate`/`WorktreeRemove`
  hooks, and the desktop app auto-creates a worktree per session.
  (https://code.claude.com/docs/en/worktrees, .../best-practices)
- **opencode has no native worktree CLI surface** — only an internal
  `Worktree.create()` API; community plugins fill the gap.
  (https://opencode.ai/docs, https://github.com/sst/opencode/blob/dev/specs/project.md)

**Decisive consequence:** a cross-harness cockpit must own the worktree mechanism
for opencode regardless (no native flag exists). Given perch must own it for one
harness, owning it uniformly for both beats a per-harness split. perch's
differentiation is the **unified cross-harness cockpit**, not the worktree
mechanism — "native" exists only for Claude.

## Core model

**The atom: one session = one branch + one worktree. Always a new branch (or an
explicitly-chosen existing branch checked out into its own tree).**

This is forced by a hard git rule, not a perch preference: *a branch can be
checked out in only one worktree at a time* (`git worktree add` on an
already-checked-out branch fails). Therefore N parallel sessions require N
branches. perch creates them; the user names them.

### Scenario mapping

| Goal | Sessions | Starting point (base) | perch creates |
|---|---|---|---|
| 2 features from `main` | 2 | `main` | 2 new branches + 2 trees (independent) |
| 5 from a feature branch | 5 | `feature-x` | 5 **new** sub-branches + 5 trees |
| 3 parallel, merged back into one feature | 3 | `feature-x` | 3 **new** sub-branches + 3 trees; user merges back |

"Are they new branches too?" — yes, necessarily: git won't let multiple worktrees
share one branch.

### Responsibility boundary

| Concern | Owner |
|---|---|
| Create branch + worktree per session | **perch** |
| Enforce one-branch-one-tree (offer *resume* on collision) | **perch** |
| Copy env/gitignored files into the new tree | **perch** |
| Remove a session's tree on request; cleanup of stale trees/branches it created | **perch** |
| Show each session's diff for review | **perch** |
| Choosing base, naming branches, branching strategy | **user** (perch suggests a name) |
| What the agent edits / commits inside the tree | **agent** |
| Merging / rebasing branches back together (fan-in) | **user** (or agent on request) |

perch owns the **fan-out** (spin up N isolated branch+tree+agent). perch is
**hands-off on fan-in** — it shows diffs, lets you stage/commit, and never merges.
Merging is the user's git workflow, which lives in the pinned in-tree shell.

### Branch creation: who owns it

`git worktree add` requires a ref to exist, and the pinned shell lives *inside*
the tree (cwd = tree) so it cannot bootstrap the branch the tree sits on
(chicken-and-egg). So the **initial branch is unavoidably perch's mechanical
job** — it runs `git worktree add -b <branch> <tree> <baseRef>`. But:

- The user **always chooses** the name and base. perch never silently invents a
  branch — the dialog shows the name (suggestion prefilled, editable).
- The **shell owns everything after** — extra branches, renames, fixups, commits,
  merges. It is pinned and already in the worktree, so it is convenient at the
  right time. It is the escape hatch for any git workflow perch should not bake in.

### Harness launch

perch launches the **CLI** harness (`claude` / `opencode`, never the desktop app)
with `cmd.Dir = <worktree>` and **never passes `--worktree`/`-w`**. The CLI only
self-creates a worktree when explicitly flagged; passing no flag means no
nesting, no conflict by construction. To the agent, the tree is just a normal
git checkout on a branch — nothing special is conveyed.

## Component changes

### 1. New Session dialog (rewrite) — `frontend/src/lib/NewSessionDialog.svelte`

Fields, in order:

1. **Repo** — dropdown from `DiscoverRepos()` (now hidden-dir-pruned — done).
2. **Starting point** — base-ref dropdown of the repo's branches; default = the
   repo's current branch.
3. **Branch** — text field for the new branch name, prefilled with a suggestion,
   editable, required, slug-validated. A **"use existing branch"** toggle switches
   this to a dropdown of branches not already checked out, which perch checks out
   directly (no `-b`).
4. **Agent** — `claude` / `opencode`.

**Removed:** the model field (and the opencode "Selected in the opencode TUI"
note that paired with it) — entirely.

**Styling fixes** (also fixes the hands-on report):
- `<option>` gets `background: var(--perch-bg); color: var(--perch-text)` so the
  OS popup is dark, not yellow-on-white.
- `.field-select` / `.field-input` get `min-width: 0` so a long repo path can no
  longer push the control past the dialog's `max-width`.

### 2. Create flow — `app/app.go`

- New-branch mode: `git worktree add -b <branch> <tree> <baseRef>` (base ref is
  now the user's choice, not hardcoded `HEAD`).
- Existing-branch mode: `git worktree add <tree> <branch>` (no `-b`).
- Copy the env include-list (§4) from the repo root into the new tree.
- Launch the harness in `cwd=tree`.
- **Collision:** if the branch is already checked out (git refuses), look it up in
  the registry; if it is an existing perch session, return a signal that the
  dialog turns into a **Resume** offer for that session. One branch ↔ one tree,
  always — never two registry records for the same tree.

**Bound-method signature** (final shape to be settled in the plan; intent fixed):
`CreateWorkspace(agent, repoPath, baseRef, branch string)` for the new-branch
case, with an existing-branch entry point (separate method or `baseRef==""`
sentinel). The `model` parameter is removed.

### 3. Model-selection removal (full blast radius)

Frontend: `NewSessionDialog.svelte` (state, `$effect` reset, `onCreate`),
`App.svelte` (`handleCreate` + `createWorkspace` call), `wails.ts` (interface +
wrapper), `constants.ts` (`DEFAULT_MODEL`), `NewSessionDialog.test.ts` (the
model-field cases), `App.test.ts` (model-passed assertions).

Go: `CreateWorkspace` signature, `registry.Workspace.Model`, `agent.NewOpts.Model`,
`Monitor.Prepare(...)` interface + the three impls (`ClaudeMonitor`,
`OpencodeMonitor`, `FakeMonitor`), and the `--model` construction in
`claude.go`/`opencode.go` `NewArgs`. After removal, perch passes no `--model`;
the harness chooses its own model.

### 4. Env propagation — new Settings field + git helper

Bare `git worktree add` copies no gitignored files. perch copies a **configurable
include-list** from the repo root into each new tree on create.

- New Settings field `WorktreeInclude []string`, centralized (no hardcoded
  literal at the call site). Default: `[".env", ".env.local", ".env.*"]`.
- Glob-matched against the repo root only; **never** a blanket copy of all
  gitignored files (must not copy `node_modules`/build output).
- A `git`/`fs` helper performs the copy; missing files are skipped silently.

### 5. Resume / session list — `Sidebar.svelte`, `App.svelte`

Workspaces already persist (`~/.config/perch/workspaces.json`) and list via
`ListWorkspaces()` on mount. The gaps to close:

- **Relabel** each sidebar row: `branch · agent · last-active` (today the row is
  not obviously a resumable saved session).
- **Resume preview:** clicking a row shows a glimpse (branch, last activity,
  diffstat) and a confirm; confirm reopens the bound tree (`--resume`/`--session`
  if a session id was captured, else a fresh agent in the same tree). An **Open
  for more** affordance reopens fully to explore.
- **Empty state:** when there are no sessions, the pane shows "No sessions yet —
  start one" with the New Session affordance, instead of a blank pane.
- **Verify during implementation:** a successfully created session must appear as
  a row. The hands-on "left pane completely empty" report must be reproduced and
  confirmed to be the empty-state/discoverability gap and not a
  creation/persistence bug.

### 6. Cleanup — staleness-triggered, user-driven, perch-owned only

**Scope.** perch only ever offers to clean sessions **it created** (its tracked
worktrees/branches, sourced from the registry). It never touches worktrees or
branches the user made outside perch — that would be governing the user's repo.

**Trigger.** On launch, if any perch session is unused past the threshold
(new Settings field `StaleThresholdDays`, default **30**), show a **dismissible
banner**: "N sessions unused >Nd — review." Banner, not a blocking modal — it
never gates the app. Zero stale → no nudge. Clicking opens the cleanup panel.

**Panel** (`CleanupPanel.svelte`). One row per stale session:
`[checkbox] <session name> · <branch> · <agent> · <last-active> · <diffstat> ·
<state badge> · [Open]`. A master **Select all** checkbox toggles every row.
**Remove selected** → confirm → per row: `git worktree remove` then
`git branch -d`.

**Snapshot.** The row *is* the snapshot (name, branch, agent, last-active,
diffstat, state badge). **Open** reopens the session so the user can explore
before deciding.

**Default-checked set (data-preserving).** Only **safe** rows (clean working tree
AND branch fully merged into its base) are checked when the panel opens. Rows
with uncommitted changes or unmerged commits are shown **unchecked** with a ⚠
badge, so a one-click "Select all → Remove" can never destroy unmerged work.
Select-all still checks them when the user deliberately wants them gone.

**Safety.**
- The cleanup panel is the **only** place a branch is deleted. Automatic branch
  deletion: never.
- `git branch -d` (safe delete — git refuses an unmerged branch). Force `-D` is
  available only behind an extra explicit confirm.
- Per-session "Remove workspace" (outside the panel) removes the **tree** and the
  registry record with an uncommitted-changes guard (warn + confirm); it leaves
  the branch intact (branch deletion is the cleanup panel's job).

**Backend helpers (re-introduced).** Round 4 removed worktree
list/remove helpers as dead code; they now have a real consumer:
- `RemoveWorktree(repo, tree)` — `git worktree remove` (force only on confirm).
- worktree dirty check — `git status --porcelain` in the tree.
- branch-merged check — `git branch --merged <base>` / `rev-list` count.
- safe/force branch delete — `git branch -d` / `-D`.
- stale-session enumeration — registry `LastActive` + per-tree git state. Sourced
  from the registry (perch's own sessions); does **not** require a general
  `git worktree list` of the whole repo.

## Out of scope (deliberate, not deferrals)

- **Fan-in / merge orchestration** — hands-off by design (see boundary above).
- **Cleaning the user's externally-made worktrees/branches** — perch only manages
  its own.
- **Conflict pre-warning across open sessions** — not in this iteration.
- **Per-harness delegation to `claude --worktree`** — rejected; perch owns the
  mechanism uniformly for cross-harness parity.

## Already landed this session (related fixes, not part of this plan)

- White-rectangle / phantom-scroll fix: global reset (`frontend/src/reset.css`
  imported first in `main.ts`) + Wails `BackgroundColour` (`app/options.go`).
- Repo discovery now skips hidden dot-directories (`internal/discover/discover.go`
  + `TestHiddenDirSkip`), so `~/.pyenv` / `~/.npm` no longer appear as repos.

## Testing

- **Go (gate-verifiable):** worktree add with chosen base; existing-branch
  checkout; collision → resume signal; env include copy (globs, skip-missing,
  no blanket copy); RemoveWorktree + dirty guard; branch-merged detection; safe
  vs force branch delete; stale enumeration by threshold; model param fully gone
  from `CreateWorkspace`/`Prepare`/`NewArgs`.
- **Frontend (vitest):** dialog without a model field; base-ref + branch fields;
  existing-branch toggle; option/`min-width` styling present; sidebar row labels;
  empty-state; cleanup panel select-all, safe-only default-check, ⚠ on
  unmerged/dirty, Remove wiring.
- **Manual smoke (gate-blind — append to `docs/superpowers/smoke-checklist.md`):**
  real parallel sessions on separate branches in one repo each with its own tree;
  `.env` present in a fresh tree so the agent runs; resume reopens the same
  conversation; cleanup banner appears past threshold and removal frees the tree;
  dropdown popup is dark; no white border/scroll.

## Honest ceiling

Real WebKit rendering, real-agent round-trips, real desktop notifications, and
parallel real worktrees remain user-gated manual smoke. The automated gate proves
the contracts (git operations, dialog shape, cleanup logic); it cannot prove the
rendered/cross-process behavior.
