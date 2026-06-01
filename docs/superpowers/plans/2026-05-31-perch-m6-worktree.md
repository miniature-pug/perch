# M6 — Worktree lifecycle + state (sub-plan)

Status: **IN PROGRESS**
Branch: `feat/perch-v1`
Master plan: `docs/superpowers/plans/2026-05-30-perch-v1-implementation.md` (M6 block, lines 294-305)
Spec refs: `plan.md §6.1` (mappings), `§7.1` (create), `§7.2` (remove two-tier + deferred cleanup), `§8` (config schema + path-safety), `§16.6` (DoD), `§18.2` (fork), `§20.1/§20.4/§20.5` (testing).

## Objective (verbatim, master plan M6)

> create (file seeding + `post_create`), 3-way mapping prompt + no-reprompt persistence, fork semantics, remove with two-tier guardrails + deferred cleanup.

DoD (`plan.md §16.6`): choices survive restart; "create worktree" forks into a new branch dir; remove safely handles dirty/locked.

## Scope boundary (M6 vs later)

M6 implements worktree create/remove, fork wiring, mapping no-reprompt, config validation wiring, cleanup dispatch, and the minimal `w`/`d`/`x` keys with **functional** (not generalized) modals. Explicitly deferred:
- Generalized binding-registry / generated-help / themed popup-stack → M9. M6 builds the smallest modal state that satisfies §20.5 (teatest-assertable confirm/prompt state in `tui.Model`).
- `perch merge` (§7.3) → optional, NOT M6.
- resurrect/boot_id reconcile (§7.4) → M7. M6's `Connect` window-reuse stays minimal (a live-index existence check) and does NOT pre-empt resurrect.
- `:` command bar, `?` help overlay, screen modes, theme → M9.

---

## Fixed decisions (M6)

Resolved by advisor before authoring; treat as constraints, do NOT re-litigate.

- **FD1 — 3-action prompt, binary persisted enum.** The new-session-in-a-git-repo prompt offers three *actions*: (a) new worktree, (b) run here (selected tree), (c) run in main checkout. The persisted `state.Mapping.Choice` stays a **string** with two values `"worktree"` | `"none"`; the action only changes which `tree` path is recorded (worktree → new tree path + `"worktree"`; here/main → existing tree path + `"none"`). Choice stays a string for forward-extensibility. Define enum consts (`ChoiceWorktree`, `ChoiceNone`) in M6 (`internal/state`).
- **FD2 — `worktree_dir` is EXEMPT from the in-repo guard.** §7.1 default placement `<project>__worktrees/<handle>` is a *sibling* outside the repo, so `worktree_dir` cannot be in-repo-constrained. Give it its own validation (resolve; reject landing inside `.git`; allow `..`/sibling/relative/absolute). Keep `checkSafe` STRICT (reject absolute + `..`) for `files.copy`/`files.symlink` ONLY. Edit `config.go Validate` + fix the doc comment + `plan.md §8` line ~539.
- **FD3 — modals functional in M6, generalized in M9.** Minimal modal/overlay state in `tui.Model` for the 3-action new-session prompt AND the two-tier remove confirm (initial + force escalation). §20.5 requires destructive `x`/`d` to produce a confirmation modal IN THE MODEL (teatest-assertable). Do NOT build the M9 abstraction.
- **FD4 — focused-in-tree guardrail.** §7.2 "if tree is the one you're focused in → hard error" guards **LIST-DRIVEN** removes only. Resolve the attached client's current window via a new `Tmux` method over `display-message`, map window→tree through the live index (`@perch_session`→pane→`Window.Tree`), hard-error if it equals the remove target. The **from-inside** trigger is handled separately by deferred-self-close (the window tears itself down via `run-shell`). Keep the two paths explicitly distinct.
- **FD5 — claude fork verified HEADLESSLY.** §18.2: claude forks natively via `--resume <id> --fork-session` launched with cwd=worktree. M6-4 demonstrates a real headless fork in a tmux pty (mirroring M5's resume demo). opencode fork = `ErrForkUnsupported` → fresh session (already coded). NOT pre-filed as manual.
- **FD6 — cleanup dispatch via Runner.** The deferred-self-close script (built by existing pure `tmux.CleanupScript` + `RunShellArgs`) is dispatched via a NEW `Tmux.RunShell(ctx, script)` routed through `proc.Runner` (unit-testable via `FakeRunner.Calls`), NOT `tea.ExecProcess`. M6-5 adds a `KillWindow` method for the list-driven remove path.

### Advisor-resolved loose ends (binding sub-decisions)

- **L1 — `run-shell` MUST be backgrounded.** Existing `RunShellArgs` returns `["run-shell", script]` with no `-b`; synchronous run would kill the pane mid-teardown. `Tmux.RunShell` inserts `-b` (do NOT mutate the pure builder's two-element contract). Nested bare `tmux` calls inside the script inherit `$TMUX` → under the socketed test server they target the test server (no `-L` inside the script).
- **L2 — base = `BaseBranch` else `HEAD`.** `AddWorktree` takes `base` as an explicit arg; caller passes `cfg.BaseBranch` when non-empty, else `"HEAD"` (project HEAD commit, §7.1).
- **L3 — `Validate(repoRoot)` wired at the create/command layer**, not inside `Load` (which has no repoRoot).
- **L4 — RESOLVED (M6-4): forked claude id IS pinnable.** Empirically verified against claude v2.1.159 (print-mode + interactive tmux-pty, with control showing auto-mint when unpinned): `claude --resume <seed> --fork-session --session-id <new>` lands the fork on exactly `<new>` (no mutual-exclusivity in `--help`; forked conversation renders). So perch pre-mints the forked id → it is KNOWN at launch → `@perch_session` stamped from the first load cycle (NOT the deferred/empty case). launchCmd fork branch: claude composes `ForkInto(seed) + NewArgs(NewOpts{SessionID: minted})` (adapter owns all flags); opencode `errors.Is(err, agent.ErrForkUnsupported)` → byte-identical opencode-new fallback (no sid, no stamp).
- **L5 — two distinct path-safety layers.** (a) `checkSafe` validates config *strings* (relative, no `..`) → M6-3, `files.*` only. (b) §7.1 "inside repo before any FS op" operates on the *post-glob resolved absolute path* → M6-2, a different check. M6-2 does NOT lean on `checkSafe`.
- **L6 — lockfile `<handle>` ≠ branch slug.** `.git/worktrees/<name>/locked` uses git's internal worktree dir name; derive from the worktree's `.git` gitdir pointer, not the handle.
- **L7 — git slugify ≠ tmux `sanitize`.** Keep both; do not force-share across the package boundary.

---

## Tasks

### M6-1 — git worktree add/remove/prune + placement resolver + slugify

- **Files:** `internal/git/worktree.go` (new), `internal/git/worktree_test.go` (new). Leave `git.go`'s read/parse layer untouched.
- **Build (all via `proc.Runner`):**
  - `func SlugifyBranch(branch string) string` — filesystem-safe slug. Keep `[A-Za-z0-9._-]`, map `/` + other runes to `-`, collapse repeats, trim, lowercase; empty → `"worktree"`. Distinct from `tmux.sanitize` (L7); pure.
  - `func WorktreePath(projectRoot, handle, worktreeDir string) (string, error)` — placement resolver. `worktreeDir==""` → `<dir(projectRoot)>/<base(projectRoot)>__worktrees/<handle>`. Set → resolve relative-to-projectRoot (or absolute as-is) then join `<resolved>/<handle>`. Pure.
  - `func AddWorktree(ctx, r proc.Runner, repoRoot, branch, path, base string) error` — `git -C <repoRoot> worktree add -b <branch> <path> <base>` (L2). Branch-exists stderr → `ErrBranchExists` sentinel. Wrap stderr like `ListWorktrees`.
  - `func RemoveWorktree(ctx, r proc.Runner, repoRoot, path string, force bool) error` — `git -C <repoRoot> worktree remove <path>` (+`--force`). Non-force failure matching dirty/untracked/locked/submodule phrases → `ErrWorktreeDirty` sentinel. Else wrap verbatim.
  - `func PruneWorktrees(ctx, r proc.Runner, repoRoot string) error` — `git -C <repoRoot> worktree prune`.
  - `func RemoveLock(repoRoot, internalName string) error` — remove `<repoRoot>/.git/worktrees/<internalName>/locked` (FS op, not Runner). `os.ErrNotExist` → nil (L6).
  - `func InternalName(ctx, r proc.Runner, repoRoot, treePath string) (string, error)` — read `<treePath>/.git` gitdir pointer (`gitdir: .../.git/worktrees/<name>`) → `<name>` (L6); fallback `filepath.Base(treePath)`.
- **Tests (FakeRunner; table; assert `Calls`):** SlugifyBranch cases; WorktreePath default/relative/absolute; AddWorktree exact argv + HEAD/BaseBranch + `ErrBranchExists`; RemoveWorktree clean/force/`ErrWorktreeDirty`(dirty,locked); PruneWorktrees argv+wrap; RemoveLock missing→nil/present→removed (tmpdir); InternalName parse + fallback.
- **Green gate:** `make fmt vet lint test`; `go mod verify`; ≥80% on `internal/git`. No new deps.

### M6-2 — file seeding (copy/symlink) + hook execution

- **Files:** new package `internal/worktree`: `seed.go`, `hooks.go`, `seed_test.go`, `hooks_test.go`. Depends on `config` + `proc`.
- **Build:**
  - `func Seed(repoRoot, treePath string, files config.Files) error` — copy `files.copy` globs (relative to repoRoot) into treePath preserving subpath; create **relative** symlinks for `files.symlink` globs. **Security gate (L5, verbatim):** before any FS op resolve each post-glob match to abs+clean and verify inside `repoRoot`; escaping match = hard error (no skip).
  - `relSymlinkTarget(linkPath, target string) (string, error)` — `filepath.Rel(dir(linkPath), target)`.
  - `func RunHooks(ctx, r proc.Runner, treePath, phase string, cmds []string, env HookEnv) error` — each hook via `r.RunInDir(ctx, treePath, "sh", "-c", "export PERCH_*=...; <cmd>")`, values `shellQuote`-d (local helper mirroring tmux/cleanup.go). Env seam = `sh -c` export prefix (avoids widening `proc.Runner`); documented tradeoff.
  - `type HookEnv struct{ Handle, WorktreePath, ProjectRoot, Branch string }`. `--no-hooks` gated by caller.
- **Tests (FakeRunner + tmpdir):** copy expands+copies; symlink relative+resolves; security match outside repo → hard error, no FS op; RunHooks Call Dir==treePath, name `sh`, quoted exports, order preserved, empty→no calls.
- **Green gate:** `make fmt vet lint test`; ≥80% on `internal/worktree`; security test fires before any FS mutation.

### M6-3 — config validate split + mapping no-reprompt

- **Files:** edit `internal/config/config.go` (`Validate`, comment) + tests; edit `internal/state/state.go` (enum + lookup/writer) + tests; edit `plan.md §8` ~539.
- **Build:**
  - `Validate(repoRoot)` (FD2/L5): drop `worktree_dir` from `checkSafe` loop; add `validateWorktreeDir(repoRoot, c.WorktreeDir)` (resolve; reject only if inside `<repoRoot>/.git`; allow `..`/sibling/abs; empty→noop). `checkSafe` stays strict for `files.copy`+`files.symlink`. Fix `Validate` doc comment.
  - `plan.md §8` ~539: clarify `files.*` stay inside repo; `worktree_dir` may be sibling/relative/absolute (rejected only if inside `.git`).
  - `state.go` (FD1): `const ChoiceWorktree="worktree"`, `ChoiceNone="none"`; update `Mapping` comment; `func LookupMapping(s State, sessionID string) (Mapping, bool)`; `func SetMapping(s *State, sessionID string, m Mapping)`. Keep low-contention atomic-write contract.
- **Tests:** config worktree_dir sibling/abs OK, `.git/...` rejected, files `..` rejected, `.env` OK; state lookup hit/miss, SetMapping round-trip both Choices, cold-start miss.
- **Green gate:** `make fmt vet lint test`; ≥80% holds for config+state; plan.md §8 corrected.

### M6-4 — fork wiring + headless claude-fork demonstration

- **Files:** edit `internal/tui/launch.go` (`launchSpec`+`launchCmd`); fork unit cases; headless demo as `//go:build integration` test or recorded transcript (FD5).
- **Build:** add `fork bool` to `launchSpec`. launchCmd fork branch: `adapter.ForkInto(spec.sessionID, spec.treePath)`; `errors.Is(err, agent.ErrForkUnsupported)` → fresh `NewArgs` (L4); claude argv = `claude --resume <id> --fork-session`, cwd=treePath. Stamping per L4 (M6-4 demo decides `--session-id` pinning). cwd already = `spec.treePath` via `Launch`.
- **Headless demo (FD5):** isolated tmux pty + real git repo + real claude session → `AddWorktree` → launch fork with cwd=worktree → `CapturePane` asserts forked conversation renders, new branch dir exists, `--session-id` pin behavior. Gate behind binary presence; absent → recorded not-demonstrated-here.
- **Tests (teatest + FakeRunner):** claude fork argv + cwd; opencode fork → `ErrForkUnsupported` → `NewArgs` fallback argv. Model-state only.
- **Green gate:** `make fmt vet lint test`; fork unit green; demo result recorded.

### M6-5 — cleanup dispatch + KillWindow + shadow teardown + window-reuse

- **Files:** `internal/tmux/runshell.go` (new) + test; edit `connect.go` (reuse) + test; `DeferredRemove` in `internal/worktree`.
- **Build:**
  - `func (o Tmux) RunShell(ctx, script string) error` (FD6/L1) — argv `o.args("run-shell","-b",script)` via runner. Do NOT change pure `RunShellArgs`.
  - `func (o Tmux) KillWindow(ctx, target string) error` — `kill-window -t <target>`; exit ≥1 → already gone (mirror KillSession).
  - `func (o Tmux) CurrentClientWindow(ctx) (session, window string, err error)` (FD4) — `display-message -p -F '#{session_name}\x1f#{window_name}'`.
  - `Connect` window-reuse: before `NewWindow`, `ListPanes(WindowTarget(session,window))` → non-dead pane → reuse its ID; else create. Minimal, no resurrect/boot_id. Update stale comment.
  - `func DeferredRemove(ctx, t tmux.Tmux, baseDir string, opts tmux.CleanupOpts, paneKey string, now int64, suffix string) error` in `internal/worktree`: build CleanupScript → `t.RunShell` → `state.RemoveWindow`. List-driven path (separate) = sync `git.RemoveWorktree` → `t.KillWindow` → `state.RemoveWindow`.
- **Tests (FakeRunner):** RunShell `-b` argv (+`-L` when Socket); KillWindow argv + exit≥1→nil; CurrentClientWindow parse + wrap; Connect reuse (existing pane→no new-window / empty→new-window); DeferredRemove dispatch+RemoveWindow; list-driven ordering no run-shell.
- **Green gate:** `make fmt vet lint test`; ≥80% on new tmux+worktree fns; no regression in M4/M5 connect tests.

### M6-6 — TUI: `w`/`d`/`x` keys + minimal modals + guardrails

- **Files:** edit `keys.go`, `app.go`; new `internal/tui/modal.go` + tests/teatest.
- **Build:** add `Worktree`(`w`), `Remove`(`d`), `Kill`(`x`) bindings. `modal.go` minimal (FD3): `modalKind` {none,newSession,removeConfirm,forceConfirm}, `modalState{kind,target,action,treePath,branch,projectPath}`, `Model.modal`. Modal handler routes keys first when `kind!=none`.
  - `w`: no-reprompt `LookupMapping` first; else open `modalNewSession`; action 0 worktree → Slugify+WorktreePath+`Validate(repoRoot)`(L3)+AddWorktree(base L2)+Seed+RunHooks(post_create)+`launchCmd{fork:true}`+SetMapping(worktree,newTree)+SaveState; action 1 here → launchCmd selected tree + SetMapping(none); action 2 main → MainWorktree path + launchCmd + SetMapping(none).
  - `d`: guards FIRST (hard-error, no modal): main-checkout (`IsMain`); focused-in-tree (FD4: `CurrentClientWindow`→live-index tree match). Else `modalRemoveConfirm` → RunHooks(pre_remove unless --no-hooks) → RemoveLock(internalName) → RemoveWorktree(force=false): `ErrWorktreeDirty` → `modalForceConfirm` → RemoveWorktree(force=true) → KillWindow → RemoveWindow. (List-driven only; from-inside path exercised by debug/integration harness, FD4 distinction preserved.)
  - `x`: confirm modal → KillWindow(liveTarget)+RemoveWindow. Window only; no worktree/branch removal.
  - Footer: add `w worktree · d remove · x kill`.
- **Tests (teatest, model-state + FakeRunner Calls only; §20.5; no rendered strings, no golden):** `w` opens newSession + action0 issues AddWorktree+Seed+fork; no-reprompt skips modal; `d` main-checkout→launchErr,no modal,no git; `d` focused-in-tree→hard error,no modal; `d` clean→confirm→RemoveWorktree(force=false)→KillWindow+RemoveWindow; `d` dirty→forceConfirm→RemoveWorktree(force=true); `x`→confirm→KillWindow,no git worktree remove; Esc→modalNone,no destructive call.
- **Green gate:** `make fmt vet lint test`; teatest green; `internal/tui` excl. coverage gate but non-tui helpers covered. §20.5 satisfied.

### M6-7 — integration tests + closeout

- **Files:** extend `internal/tmux/integration_test.go` or add `internal/worktree/integration_test.go` (`//go:build integration`); closeout edits `plan.md §16.6` + master plan M6 + `.gitignore`.
- **Integration (real git + real tmux test socket, §20.4):** create→porcelain reflects; seed→file exists; remove clean→gone; remove dirty→`ErrWorktreeDirty`→force→gone; deferred self-close (from-inside, FD6/L1)→window gone+tree removed+shadow record gone (≤2s poll); mapping survives restart (SetMapping+SaveState→fresh LoadState→LookupMapping).
- **Closeout (honesty, mirror M5 D4):** run unit+integration green, go mod verify, gofmt, coverage ≥80% excl tui. Record demonstrated (claude headless fork; deferred self-close e2e) vs manual (alt-screen handover of remove into user's own terminal — can't drive headlessly; opencode fork = ErrForkUnsupported by design). Update plan.md §16.6 + master plan M6 → DONE + verification note. `.gitignore` `.perch_trash_*` if needed.

---

## Gates (whole-milestone)

- `make fmt vet lint test` green (unit, `-race`, no external deps).
- `make test-integration` green on real tmux 3.6 + git.
- `go mod verify` clean; gofmt clean; golangci-lint 0.
- Coverage ≥80% on `internal/` **excluding `internal/tui/`** (§20.1).
- No new third-party deps. Charm pins unchanged.
- §20.5 satisfied: destructive `d`/`x` produce teastable model-state modal; assertions model-state/FakeRunner Calls only; no golden.
- DoD §16.6: choices survive restart; create worktree forks into new branch dir; remove handles dirty/locked.

## Open / honesty

- **Demonstrated:** claude headless fork into new worktree (FD5); deferred self-close teardown e2e (FD6/L1).
- **Manual/deferred:** full alt-screen handover of a remove into the user's own terminal (in-script `switch-client` not headlessly drivable — same as M5 attach). opencode fork = `ErrForkUnsupported` by design. If `claude` absent on build machine, fork demo recorded not-demonstrated-here.
- **Empirically-pending:** L1 (`-b` required, confirmed in integration), L6 (internal worktree name parsing, create integration). [L4 RESOLVED in M6-4 — fork id pins via `--session-id`.]

## Whole-milestone checklist

- [x] M6-1 git worktree add/remove/prune + placement + slugify + lock/internal-name — `internal/git/worktree.go`; 91.1% cov; spec SHIP + quality 0 bug/0 risk; lint 0
- [x] M6-2 seeding (copy/symlink, security gate L5) + hooks (env via `sh -c`) — `internal/worktree/{seed,hooks}.go`; 83.2% cov; TOCTOU closed (resolved-path reuse); spec SHIP + lint 0
- [x] M6-3 config validate split (FD2/L5) + worktree_dir validation + mapping enum + lookup/writer + plan.md §8 fix — config 92.1%/state 84.9%; SHIP; `.gitfoo`/`foo/../.git` regression guards added; lint 0
- [x] M6-4 fork wiring (claude native pin+stamp, opencode ErrForkUnsupported→fresh fallback) + headless claude-fork demo (FD5) — L4 empirically RESOLVED (claude honours `--session-id` on `--fork-session`, fork id known at launch); `internal/tui/launch.go` compose `ForkInto+NewArgs{SessionID}` (no raw flags in TUI); unit tests (FakeRunner/model-state) `TestLaunch_Fork{Claude,OpencodeFallback}`; `//go:build integration` demo `launch_fork_integration_test.go` (skips absent deps, temp-guarded cleanup); spec SHIP + quality SHIP on production path; 2 integration-test BLOCKERs fixed (guard var, exec timeout); gate green (lint 0, all tests pass)
- [x] M6-5 `Tmux.RunShell`(`-b`, L1; pure `RunShellArgs` untouched) + `KillWindow` (KillSession exit-policy) + `CurrentClientWindow` (FD4, `\x1f` SplitN-2) + `Connect` reuse (live-pane→reuse / absent|dead→NewWindow) + `worktree.DeferredRemove` (RunShell-then-RemoveWindow, no orphan on dispatch fail) — tmux 97.6% / worktree 83.8%; spec SHIP + quality SHIP; 2 NITs fixed (comment label, call-count assert); 1 M4 Connect test reconciled to reuse; lint 0
- [x] M6-6 TUI `w`/`d`/`x` + minimal modals (FD3) + main-checkout & focused-in-tree guards (FD4) + no-reprompt + footer — `modal.go` state machine; `w` 3-action picker (new worktree/run here/run in main) with no-reprompt `LookupMapping` (stale-tree → re-prompt); worktree-create chain Validate→AddWorktree(base L2)→Seed→post_create→SetMapping(worktree)→fork launch; branch name `perch/<slug(src)>-<8hex>` (uniqueness by construction, free-form deferred M9); full §7.2 `d` chain (pre_remove→InternalName→RemoveLock→remove(force=false)→dirty→forceConfirm→remove(force=true)→KillWindow→RemoveWindow), guards isMain (hard) + focused-in-tree (`CurrentClientWindow`, live-only); `x` kill (window only); built in 2 passes; spec SHIP + quality SHIP; fixes: closure-deferred state writes, preflight stale-guard symmetry, +3 test gaps; lint 0. KNOWN LIMIT: Seed/post_create failure after AddWorktree leaves an orphaned worktree dir (unique branch, non-blocking; full rollback deferred) — see M6-7 honesty note.
- [ ] M6-7 integration (§20.4) + deferred-self-close e2e + mapping-survives-restart + closeout
- [ ] Final whole-milestone review
- [ ] gates green; coverage ≥80% excl `internal/tui`
- [ ] plan.md §8/§16.6 + master plan M6 corrected; M6 marked DONE
