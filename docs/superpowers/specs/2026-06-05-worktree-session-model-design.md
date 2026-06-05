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
- Sessions are bound to a worktree with no option to run directly in the repo —
  no way to keep a permanent, in-place session (e.g. working on `main`).
- The home screen has no shell, so when perch asks the user to fix a git state
  (clean a dirty tree, resolve a checkout) they must leave the GUI to do it —
  immersion-breaking.
- Stale worktrees/branches accumulate with no cleanup path.

## Research summary (primary sources)

- **Peer cockpits enforce worktrees and own the lifecycle** — workmux,
  claude-squad, uzi, crystal, conductor, vibe-kanban, phantom, gwq all do
  one-tree-per-session, tool-owned, with a first-class new-vs-resume choice.
  (workmux confirmed worktree-native; `--open-if-exists` is its resume idiom.)
- **Claude Code has first-party worktrees** — `--worktree`/`-w`, `worktree.baseRef`,
  `.worktreeinclude`, `WorktreeCreate`/`WorktreeRemove` hooks, and the desktop app
  auto-creates a worktree per session.
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

A worktree session is **bound to its tree.** Its cwd is the worktree path; perch
spawns the pty there. **It cannot be launched without that worktree** — delete the
tree and perch can no longer reopen the session (the path is gone). The harness's
underlying conversation id might in principle resume elsewhere, but perch never
launches a session outside its bound directory. This binding is exactly why a
non-worktree mode is offered (see §4).

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
| Remove a session's tree on request; cleanup of stale trees/branches it created | **perch** |
| Show each session's diff for review | **perch** |
| Choosing base, naming branches, branching strategy | **user** (perch suggests a name) |
| What the agent edits / commits inside the tree | **agent** |
| Merging / rebasing branches back together (fan-in) | **user** (or agent on request) |
| Providing any gitignored local config (`.env` etc.) inside a tree | **user** |

perch owns the **fan-out** (spin up N isolated branch+tree+agent). perch is
**hands-off on fan-in** — it shows diffs, lets you stage/commit, and never merges.

**Gitignored files are not carried.** `git worktree add` copies no gitignored
files, and perch will not either: if it can't be committed, it doesn't travel with
the worktree. A worktree session that needs `.env`/local config is the user's to
set up (in the shell), or they use a non-worktree session (§4) for a repo whose
working copy already has that config.

### Branch creation: who owns it

`git worktree add` requires a ref to exist, and the pinned shell lives *inside*
the tree (cwd = tree) so it cannot bootstrap the branch the tree sits on
(chicken-and-egg). So the **initial branch is unavoidably perch's mechanical
job** — it runs `git worktree add -b <branch> <tree> <baseRef>`. But:

- The user **always chooses** the name and base. perch never silently invents a
  branch — the dialog shows the name (suggestion prefilled, editable).
- The **shell owns everything after** — extra branches, renames, fixups, commits,
  merges. It is pinned and already in the worktree, convenient at the right time.
- Non-worktree mode (§4) creates **no** branch — it uses an existing branch.

### Harness launch

perch launches the **CLI** harness (`claude` / `opencode`, never the desktop app)
with `cmd.Dir = <session cwd>` and **never passes `--worktree`/`-w`**. The CLI only
self-creates a worktree when explicitly flagged; passing no flag means no nesting,
no conflict by construction. To the agent, the cwd is just a normal git checkout.

## Component changes

### 1. New Session dialog (rewrite) — `frontend/src/lib/NewSessionDialog.svelte`

Fields, in order:

1. **Repo** — dropdown from `DiscoverRepos()` (now hidden-dir-pruned — done).
2. **Worktree** — yes/no toggle, **default yes** (see §4).
3. **Starting point** — base-ref dropdown of the repo's branches; default = the
   repo's current branch. *(worktree mode only)*
4. **Branch** — text field for the new branch name, prefilled with a suggestion,
   editable, required, slug-validated; or a **"use existing branch"** toggle that
   switches it to a dropdown of branches not already checked out, checked out
   directly (no `-b`). *(worktree mode only)*
   In **non-worktree** mode this becomes a single branch dropdown (the branch to
   run in, in the repo itself).
5. **Agent** — `claude` / `opencode`.

**Removed:** the model field (and its paired opencode note) — entirely.

**Styling fixes** (also fixes the hands-on report):
- `<option>` gets `background: var(--perch-bg); color: var(--perch-text)` so the
  OS popup is dark, not yellow-on-white.
- `.field-select` / `.field-input` get `min-width: 0` so a long repo path can no
  longer push the control past the dialog's `max-width`.

### 2. Create flow + registry — `app/app.go`, `internal/registry/registry.go`

**Registry change (required).** Today `Workspace` stores only `WorktreePath`, no
repo root — but git worktree removal and cleanup need the repo root. Add:
- `RepoPath string` — the source repo root.
- `Worktree bool` — true for an isolated-tree session, false for a non-worktree
  (in-repo) session. (Distinguishes the two; gates removal/cleanup safety.)
- Remove `Model string` (model removal, §3).

For a worktree session `WorktreePath` is the linked tree; for a non-worktree
session `WorktreePath == RepoPath` (the cwd is the repo root) and `Worktree=false`.

**Worktree mode create:**
- New-branch: `git worktree add -b <branch> <tree> <baseRef>` (base ref is the
  user's choice, not hardcoded `HEAD`).
- Existing-branch: `git worktree add <tree> <branch>` (no `-b`).
- Launch the harness in `cwd=tree`.
- **Collision:** if the branch is already checked out (git refuses), look it up in
  the registry; if it is an existing perch session, the dialog turns into a
  **Resume** offer for that session. One branch ↔ one tree, always.

**Non-worktree mode create (§4):** no `git worktree add`, no new branch; checkout
the selected branch in the repo root if it differs from the current checkout
(guarded — git refuses on a dirty/conflicting tree, surface the error); launch the
harness in `cwd=repoRoot`.

**Bound-method signature** (final shape settled in the plan; intent fixed): a
create method taking `agent, repoPath, baseRef, branch, worktree` (the `model`
parameter is removed). May split into worktree / non-worktree entry points.

### 3. Model-selection removal (full blast radius)

Frontend: `NewSessionDialog.svelte` (state, `$effect` reset, `onCreate`),
`App.svelte` (`handleCreate` + `createWorkspace` call), `wails.ts` (interface +
wrapper), `constants.ts` (`DEFAULT_MODEL`), `NewSessionDialog.test.ts` (model
cases), `App.test.ts` (model-passed assertions).

Go: `CreateWorkspace` signature, `registry.Workspace.Model`, `agent.NewOpts.Model`,
`Monitor.Prepare(...)` interface + three impls (`ClaudeMonitor`, `OpencodeMonitor`,
`FakeMonitor`), and the `--model` construction in `claude.go`/`opencode.go`
`NewArgs`. After removal perch passes no `--model`; the harness chooses its model.

### 4. Worktree toggle & non-worktree sessions

New Session offers **Worktree: yes/no**, default **yes**.

- **Yes (default)** — the isolated model above: new branch + tree, disposable,
  cleanup-eligible, bound to its tree.
- **No** — the session runs **directly in the repo root** on the selected branch.
  No tree is created, no branch is created; if the selected branch differs from
  the repo's current checkout, perch checks it out in the repo root (clean-tree
  guard). This is the **permanent / in-place** session — e.g. working on `main`.
  It is **never auto-cleaned** (there is no throwaway tree) and is **excluded from
  the cleanup panel** entirely.

**Safety (critical):** removal and cleanup must branch on `Worktree`. A
non-worktree session's `WorktreePath` *is the user's real repo* — perch must
**never** run `git worktree remove` / `git branch -d` against it. Closing a
non-worktree session only stops the agent and drops the registry record.

Multiple non-worktree sessions on one repo share that single working copy and its
current branch (it is one checkout); that is the user's choice, like two terminals.

### 5. Resume / session list — `Sidebar.svelte`, `App.svelte`

Workspaces already persist (`~/.config/perch/workspaces.json`) and list via
`ListWorkspaces()` on mount. The gaps to close:

- **Relabel** each sidebar row: `branch · agent · last-active` (today the row is
  not obviously a resumable saved session).
- **Resume preview:** clicking a row shows a glimpse (branch, last activity,
  diffstat) and a confirm; confirm reopens the bound cwd (`--resume`/`--session`
  if a session id was captured, else a fresh agent in the same cwd). An **Open for
  more** affordance reopens fully to explore.
- **Empty state:** the main-area welcome card already exists (Welcome to perch +
  New Session + quick-start); the home view gains a home shell beneath it (§7).
  The *sidebar* gains an empty hint when no sessions exist.
- **Verify during implementation:** a successfully created session must appear as a
  row. The hands-on "left pane completely empty" report must be reproduced and
  confirmed to be the empty-state/discoverability gap, not a creation/persistence
  bug.

### 6. Removal & cleanup

**Single "Remove session"** (existing path; user-triggered only). Today it is the
command-palette command `session:remove` → confirm dialog → an undo-toast window →
`RemoveWorkspace(id)`, which stops the agent + pty and drops the registry record
but **leaves the tree and branch on disk** (the dialog says so). There is no
keybinding, no sidebar affordance, and no automatic trigger. Change:
- Worktree session → also `git worktree remove` the tree (guarded: warn + confirm
  on uncommitted changes). The **branch stays** (branch deletion is the cleanup
  panel's job). Update the ConfirmDialog copy accordingly.
- Non-worktree session → unchanged (stop agent + drop record only; never touch
  the repo root or its branch).

**Stale cleanup** (new). Scope: perch-created **worktree** sessions only; never
non-worktree sessions and never worktrees/branches the user made outside perch.

- **Trigger:** on launch, if any worktree session is unused past the threshold
  (new Settings field `StaleThresholdDays`, default **30**), show a **dismissible
  banner**: "N sessions unused >Nd — review." Banner, not a blocking modal. Zero
  stale → no nudge. Clicking opens the cleanup panel.
- **Panel** (`CleanupPanel.svelte`): one row per stale session —
  `[checkbox] <session name> · <branch> · <agent> · <last-active> · <diffstat> ·
  <state badge> · [Open]`. Master **Select all** toggles every row. **Remove
  selected** → confirm → per row: `git worktree remove` then `git branch -d`.
- **Snapshot** = the row itself; **Open** reopens the session to explore first.
- **Default-checked (data-preserving):** only **safe** rows (clean working tree AND
  branch merged into its base) are checked on open. Rows with uncommitted changes
  or unmerged commits are shown **unchecked** with a ⚠. Select-all still checks them
  if the user deliberately wants them gone.
- **Safety:** the cleanup panel is the **only** place a branch is deleted (auto =
  never). `git branch -d` (safe — git refuses unmerged); force `-D` only behind an
  extra confirm.

**Backend helpers (re-introduced; round 4 removed them as dead code, now with a
real consumer):** `RemoveWorktree(repo, tree)` (`git worktree remove`, force only on
confirm); worktree dirty check (`git status --porcelain`); branch-merged check
(`git branch --merged <base>` / rev-list); safe/force branch delete; stale
enumeration (registry `LastActive` + per-tree git state, filtered to `Worktree`
sessions). Sourced from the registry — no general `git worktree list` of the repo.

### 7. Home screen + home shell

Today the no-session main area is a centered welcome card (`empty-state`: "Welcome
to perch", a New Session button, Claude/Opencode quick-start) — switched on by
`activeId == null`. It has **no shell**, so a user told to fix a git state must
leave perch.

Restructure the home view as a vertical split, mirroring a session view:
- **Upper area** — the existing welcome/actions card (New Session + quick-start),
  with room for future home actions.
- **Lower area** — a **home shell**: a pinned, collapsible/resizable shell at a
  home cwd, so pre-session fixes (clean a dirty tree, git ops) happen in-app.

Shell scoping (answers "separate per session/view?" — **yes**):
- **Home shell** — a single pty keyed `shell-home`, cwd = perch's launch directory
  (fallback `$HOME`). Persists for the app lifetime, so navigating into and back
  out of a session does not kill a running command.
- **Per-session shell** — unchanged: `shell-{id}`, cwd = the session's
  worktree/repo path, re-keyed (re-mounted) per active workspace.

These are distinct ptys; the home shell and every session shell are independent.
Launching/selecting a session sets `activeId`, switching the main area from the
home view to that session's view (its own shell drawer beneath it).

**Implementation note:** verify `OpenShell` accepts a standalone paneId/cwd not
tied to a registered workspace (the home shell is not a workspace); if it requires
a workspace record, generalize it to spawn a bare pty for the home pane.

## Out of scope (deliberate, not deferrals)

- **Fan-in / merge orchestration** — hands-off by design.
- **Carrying gitignored files into a tree** — rejected on principle (uncommittable
  = not carried); non-worktree mode is the escape for config-dependent repos.
- **Cleaning the user's externally-made worktrees/branches** — perch manages only
  its own.
- **Conflict pre-warning across open sessions** — not this iteration.
- **Per-harness delegation to `claude --worktree`** — rejected; perch owns the
  mechanism uniformly for cross-harness parity.

## Already landed this session (related fixes, not part of this plan)

- White-rectangle / phantom-scroll fix: global reset (`frontend/src/reset.css`
  imported first in `main.ts`) + Wails `BackgroundColour` (`app/options.go`).
- Repo discovery now skips hidden dot-directories (`internal/discover/discover.go`
  + `TestHiddenDirSkip`), so `~/.pyenv` / `~/.npm` no longer appear as repos.

## Testing

- **Go (gate-verifiable):** worktree add with chosen base; existing-branch
  checkout; collision → resume signal; non-worktree create runs in repo root +
  branch checkout guard; registry `RepoPath`/`Worktree` round-trip; single-remove
  removes the tree for worktree sessions and never touches the repo root for
  non-worktree sessions; cleanup excludes non-worktree sessions; RemoveWorktree +
  dirty guard; branch-merged detection; safe vs force branch delete; stale
  enumeration by threshold; model param fully gone from
  `CreateWorkspace`/`Prepare`/`NewArgs`.
- **Frontend (vitest):** dialog without a model field; worktree toggle (fields
  shown/hidden per mode); base-ref + branch fields; existing-branch toggle;
  option/`min-width` styling; sidebar row labels; empty-state; cleanup panel
  select-all, safe-only default-check, ⚠ on unmerged/dirty, Remove wiring; home
  view renders the welcome card AND a home shell whose paneId is `shell-home`
  (distinct from any `shell-<id>`); selecting a session switches away from home.
- **Manual smoke (gate-blind — append to `docs/superpowers/smoke-checklist.md`):**
  real parallel worktree sessions on separate branches in one repo; a non-worktree
  session on `main` runs in place and survives; resume reopens the same
  conversation; cleanup banner past threshold and removal frees the tree but not a
  non-worktree session; dropdown popup is dark; no white border/scroll.

## Honest ceiling

Real WebKit rendering, real-agent round-trips, real desktop notifications, and
parallel real worktrees remain user-gated manual smoke. The automated gate proves
the contracts (git operations, dialog shape, removal/cleanup logic); it cannot
prove the rendered/cross-process behavior.
