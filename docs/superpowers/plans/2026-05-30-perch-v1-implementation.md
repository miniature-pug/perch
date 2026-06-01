# perch v1 — Implementation Plan (master)

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan milestone-by-milestone. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **This is a MASTER plan over 9 milestones (subsystems).** The detailed, bite-sized
> TDD sub-plan for each of M2–M9 is authored **at the start of that milestone** via
> `superpowers:writing-plans`, with the actual code state in context, and saved beside
> this file as `2026-05-30-perch-v1-mNN-<name>.md`. M0 and M1 are fully specified inline
> below (no prior-code dependency). The authoritative design spec is **`/plan.md`** —
> this plan references it by `§`-number and never restates its content.

**Goal:** Build `perch`, a keyboard-first Go TUI that orchestrates Claude Code and
opencode sessions across git repos/worktrees using tmux as the engine.

**Architecture:** A single small Go binary. A bubbletea (Charm v1) TUI drives tmux/git
and reads agent state through an adapter interface. State is a join over git (trees),
the agent tools (sessions), and tmux (live windows); perch adds two plain-JSON stores
and tmux pane options as the live source of truth. No DB, no daemon. Full design: `plan.md`.

**Tech Stack (versions LOCKED — verified against the Go module proxy / registries on
2026-05-30; see Version Table below):** Go (toolchain `go1.26.2`), Charm v1 trio
(bubbletea v1.3.10 / lipgloss v1.1.0 / bubbles v1.0.0), BurntSushi/toml v1.6.0, stdlib
`flag` for the CLI. Runtime: tmux ≥3.2, git ≥2.20, claude and/or opencode.

---

## 0. Working agreement (applies to EVERY milestone)

These are standing constraints from the user and `plan.md §1`. They are part of each
milestone's Definition of Done — not optional polish.

- **Checkpoint per milestone.** Complete a milestone fully, prove it green, report, then
  stop for review before starting the next. Do not run ahead.
- **At every milestone, bring the whole repo with it.** Before declaring a milestone
  done, check & update as needed: the build, unit tests, integration tests (where
  applicable), `Makefile` targets, `install.sh`/scripts, `README.md` + any how-to/docs,
  and `.tool-versions`. Nothing is left stale.
- **Code quality is a gate, not an afterthought.** Clean, self-explaining, well-named,
  small focused files with clear boundaries (one responsibility each — `plan.md §14`).
  No spaghetti. Follow Go community best practices and the prior-art patterns credited
  in `plan.md`. `make fmt vet lint` clean.
- **Comments only when they earn their place.** A comment explains a *decision* (why
  this approach / why this guard) or genuinely non-obvious logic. No comments that
  restate the code. Provenance comments crediting prior art (`plan.md §19`) are allowed
  where they document a non-obvious ported pattern.
- **TDD.** Table-driven tests first, against fixtures in `testdata/` (`plan.md §20`).
  Every shell-out goes through a `Runner` interface; unit tests use a `FakeRunner` and
  never spawn real processes (`plan.md §20.1`). Coverage ≥80% on `internal/` excluding
  `internal/tui/` (`plan.md §20.1`).
- **Frequent, small commits** on this branch (`feat/perch-v1`). Never commit/push to
  `main`. Conventional-commit style subjects.
- **Defensive by contract (`plan.md §1, §4`).** Every fragile/undocumented integration
  (adapters, agent file formats, pid trackers) is behind an interface, parses
  fault-tolerantly (skip-and-continue, never panic), and degrades to "feature
  unavailable" rather than crashing perch.
- **Supply chain (`plan.md §2`).** Exact-pin every dep; commit `go.sum`; `go mod vendor`
  and build `-mod=vendor -trimpath`; `.tool-versions` is the single source of truth for
  external tool versions — no version string duplicated anywhere else.

---

## Version Table (LOCKED — 2026-05-30)

Verified against the Go module proxy, GitHub releases, and go.dev. The Charm trio is
**mutually pinned**: `bubbles v1.0.0`'s own `go.mod` requires exactly the bubbletea and
lipgloss versions below — do not bump them independently.

| Module / Tool | Pin | Source / note |
|---|---|---|
| `github.com/charmbracelet/bubbletea` | `v1.3.10` | required by bubbles v1.0.0; released 2025-09-17 |
| `github.com/charmbracelet/lipgloss` | `v1.1.0` | required by bubbles v1.0.0; released 2025-03-12 |
| `github.com/charmbracelet/bubbles` | `v1.0.0` | released 2026-02-09 |
| `github.com/BurntSushi/toml` | `v1.6.0` | released 2025-12-18 |
| `github.com/charmbracelet/x/exp/teatest` | resolve latest pseudo-version via `go get` at M5 | TUI test harness |
| Go `toolchain` directive | `go1.26.2` | latest stable ≥30 days old (1.26.3 is local & newer; used to build) |
| golangci-lint (`/v2`, via `go run …@ver`) | `v2.11.4` | **corrected** from plan's v2.6.0; released 2026-03-22 |
| govulncheck (`golang.org/x/vuln/cmd/govulncheck`) | `v1.3.0` | plan was **correct**; released 2026-04-22 (≥30d) |
| `tmux` | `3.6` (min 3.2) | installed on host |
| `claude` | `2.1.158` | self-updating; reproducibility reference only |
| `opencode` | `1.15.12` | matches plan §4 reference |

> **Deviations from plan.md to apply when scaffolding:** Makefile `GOLANGCI := v2.6.0`
> → `v2.11.4`; add `toolchain go1.26.2` to go.mod; everything else in `plan.md §2/§15/§21`
> stands. `govulncheck` stays `v1.3.0` (a background research agent erroneously suggested
> a downgrade to v1.1.4; re-verified against the proxy — v1.3.0 is latest & qualifying).

---

## Resume protocol (read this first after a restart)

A fresh session reconstructs "where are we" from durable state, not memory:

1. `git -C /home/miniature_pug/github/perch log --oneline -20` and check the milestone
   checkboxes below — the last checked box + last commit is the resume point.
2. Read `plan.md` (full) and this master plan.
3. If a per-milestone sub-plan file exists for the in-progress milestone
   (`…-mNN-*.md`), resume from its unchecked steps. If not, author it now via
   `superpowers:writing-plans` before coding.
4. `make build && make test` (and `make test-integration` if past M4) to confirm the
   tree is green before continuing.
5. Continue the milestone; honour the §0 working agreement at its close.

tmux is now installed on the host (the user installed it and restarted the session to
make it available). Milestones M4+ and all integration tests may use it.

---

## Milestones

Build in this order (mirrors `plan.md §16`). Each milestone ends at a **checkpoint**:
report status, prove the DoD green, wait for review.

### M0 — Bootstrap: `.tool-versions` + `install.sh`  ✅ DONE (commits 1641f50→8dee82b)  *(no tmux needed for build; podman for container test)*

- [x] Objective: pin all external tool versions and provide a POSIX install script.
- Spec refs: `plan.md §21` (whole), `§16.0`.
- Deliverables:
  - [x] `/.tool-versions` (repo root) with the LOCKED pins above:
        `golang 1.26.2`, `tmux 3.6`, `claude 2.1.158`, `opencode 1.15.12`. (`git` omitted
        — system-managed, `plan.md §21.2`.) One `<tool> <version>` per line.
  - [x] `/install.sh` (repo root, `#!/bin/sh`, POSIX, no bashisms) exactly per
        `plan.md §21.3`: arch/OS detect → `tool_version()` reader → ordered steps
        (go, tmux, git, claude, opencode, build perch, `perch setup`) → flags
        (`--skip-agents`, `--skip-build`, `--skip-setup`, `--prefix=`, `--yes`) →
        `[skip]/[install]/[ok]/[warn]` output → idempotent. Hardened: atomic go
        extraction (temp→verify→swap), loud `curl||die`, fail-fast unknown flags,
        tmux pin-drift warn, `perch setup` via full binary path.
  - [x] `shellcheck install.sh` clean (it's part of `make lint` per §21.3).
- DoD (`plan.md §16.0`): script exits 0 inside `podman run --rm -v "$PWD":/perch ubuntu:24.04 sh /perch/install.sh --skip-agents` **and** the fedora:41 equivalent (podman substitutes for docker). `perch version` and `perch doctor` are stubs until M1 — so for M0 the install-script DoD is the container exit-0 (build step `--skip-build` until the binary exists, or run after M1). **Sequencing note:** `install.sh` step 6 (build) and step 7 (`perch setup`) depend on M1's binary; in M0 verify steps 1–5 + idempotency with `--skip-build --skip-setup`, then close the loop at end of M1.

### M1 — Skeleton: module, CLI, config, state, `doctor`  ✅ DONE  *(no tmux needed)*

- [x] Objective: a building binary with the dependency tree, CLI dispatch, config +
      state packages, and a working `perch doctor` / `perch version`.
- Spec refs: `plan.md §2` (deps/policy), `§6` (state), `§8` (config), `§14` (layout),
      `§15` (Makefile), `§21.4` (doctor), `§16.1`.
- Deliverables:
  - [x] `go.mod` with exact-pinned deps + `toolchain go1.26.2`; `go.sum` committed;
        `go mod vendor` → commit `/vendor`; `GOFLAGS=-mod=vendor`.
  - [x] `Makefile` per `plan.md §15` with the corrected `GOLANGCI := v2.11.4`.
  - [x] `cmd/perch/main.go`: stdlib `flag` + subcommand `switch` over the 7 verbs
        (`plan.md §10`); unknown verb → usage + exit 2.
  - [x] `internal/config`: TOML load + global/project merge + defaults + security
        boundary (`plan.md §8.2`); table-driven tests (`§20.3 config` cases).
  - [x] `internal/state`: `state.json` + `windows/*.json` schemas (`§6.1/§6.2`),
        atomic write (temp+rename), frecency (`§6.3` exact formula); tests (`§20.3
        state` cases incl. bucket boundaries + aging + cold start).
  - [x] `internal/model`: `Project`, `Tree`, `Session`, `Window` types (no deps).
  - [x] `perch doctor` (`§21.4`): reads `.tool-versions`, checks deps, exit 0/1 per spec.
  - [x] `perch version`: version/build info via `-ldflags -X main.version`.
- DoD (`plan.md §16.1`): `make build && ./bin/perch doctor` reports tool presence;
      `make test` green; coverage gate met for `config`+`state`; `make fmt vet lint
      vendor verify` clean. Then close M0's install.sh build/setup steps end-to-end.
- [x] M1 verified complete 2026-05-30 — install.sh seam closed (ubuntu:24.04 + fedora:41 exit 0); coverage: config 92.1%, state 84.0%, doctor 91.5%, model 100%; all make gates green.

### M2 — Discovery + git  *(no tmux needed)*  ✅ DONE

- [x] Objective: bounded repo scan + authoritative worktree enumeration + frecency order.
- Spec refs: `plan.md §5`, `§7.1` (worktree add later, but porcelain parse here),
      `§6.3`, `§16.2`.
- Deliverables: `internal/discover` (bounded scan, prune `node_modules`/`vendor`/`.git`,
      max-depth, `.git`-as-file detection, nested repos), `internal/git` (`worktree list
      --porcelain` parser behind `Runner`), frecency-ordered project list. Fixtures:
      `testdata/git/worktree-list-porcelain-{multi,single,linked}.txt`. Tests: `§20.3
      discover` cases.
- DoD (`plan.md §16.2`): debug command lists projects+trees under a root — fast, pruned,
      frecency-ordered. §0 gate.

- [x] **M2 verified complete 2026-05-30.** Sub-plan: `2026-05-30-perch-m2-discovery-git.md`.
      Commit chain `b6d99bd`→`9ae9193` (proc → git → discover → catalog/cmd → review fixes).
      Packages: `internal/proc` (shared `Runner`/`ExecRunner`/`FakeRunner`, §20.1),
      `internal/git` (`ParsePorcelain`/`ListWorktrees`/`MainWorktree`/`ToTrees`),
      `internal/discover` (`Scan` candidate finder + `Projects` catalog), `cmd/perch`
      (hidden `perch debug discover [path]`). Coverage: proc 100%, git 91.8%, discover
      90.8%, cmd/perch 79.5%. All gates green (`make test -race`, lint 0 issues, vet,
      build, `go mod verify`). Smoke: `perch debug discover` lists the perch repo itself.
- [x] **Architecture: entry[0]-keyed pipeline.** `discover.Scan` returns `.git` candidate
      paths only; `git` parses porcelain and keys each project by `entry[0]` (the main
      worktree, which git always lists first regardless of invocation cwd —
      empirically verified). `discover.Projects` dedups by that canonical main path, so a
      linked-worktree candidate (`.git`-as-file inside root) collapses onto its owning repo
      with the correct name. Frecency order reuses `state.SortedPaths` over a zero-filled
      copy of the stats map (cold start → alphabetical); no new ordering code.
- [x] **Spec tension resolved (§5 vs §20.3).** §5 prose says "stop descending once a repo
      root is found"; §20.3 enumerates a nested-repos test requiring the inner repo to be
      found as a separate project. The enumerated test is authoritative → `Scan` continues
      descending after a hit (pruning `node_modules`/`vendor`/`.git` + max-depth keep it
      fast). Behavior verified by `TestNestedRepos`.
- [x] **Convention set:** test fixtures live per-package at `internal/<pkg>/testdata/`
      (Go idiom; §14 invokes "Go best practices"), not a repo-root `testdata/` tree.
      M3+ follows suit. No new deps — `BurntSushi/toml` remains the only one.
- Deferred (not M2 scope): wiring `state.LoadState` into `debug discover` — frecency is
      cold-start until selection bumping lands with the TUI (M5); empty stats yields the
      same output today. `attach` verb, persistence-on-select, worktree `add`/`remove`
      (§7.1/§7.2) all later milestones.

### M3 — Adapters: claude + opencode  *(no tmux needed)* ✅ DONE

- [x] Objective: `Adapter` interface with both agents' `ListSessions` + arg builders.
- Spec refs: `plan.md §4` (whole), `§16.3`, `§18.2/3/4`.
- Deliverables: `internal/agent/adapter.go` (interface + `NewOpts`), `claude.go`
      (JSONL enumerate + slug decode + pid-tracker discovery, fault-tolerant),
      `opencode.go` (`session list --format json`, group by `directory`).
      Resume/fork/new arg builders. Fixtures: `internal/agent/testdata/claude/*`,
      `internal/agent/testdata/opencode/*`. Tests: `§20.3 agent` cases (slug decode
      incl. ambiguous, malformed-JSONL skip, empty, opencode valid/empty/malformed).
- DoD (`plan.md §16.3`): unit tests parse real fixtures; sessions group by directory. §0 gate.

> **Sub-plan:** `docs/superpowers/plans/2026-05-30-perch-m3-adapters.md` (the
> authoritative M3 spec — supersedes the master plan where tagged `[DIVERGENCE]`).
>
> **Verification:** commit chain `6ccb06a`→`978bd51` (proc RunInDir) → `594f854`
> (sub-plan) → `b8511a0`→`b7cde9d` (Adapter interface) → `bed66a3` (claude) →
> `da65667` (opencode) → `d06d21b` (final-review polish). Gates green: full suite
> `go test -race` (10 pkgs), `make lint` 0 issues, `go vet`, `go build`, gofmt
> clean. Coverage: `internal/agent` 94.0%, `internal/proc` 100%. Real-world smoke
> (against this machine's installs): claude enumerated 11 real sessions with correct
> directories/titles; opencode listed its 1 session (ms→s correct) and returned
> 0-sessions-no-error from an empty-scope dir; both `Detect()` true.
>
> **Plan↔reality corrections recorded** (in the sub-plan + `plan.md §4`/`§18`):
> opencode list is **project-scoped not global** (§18.3 was wrong); claude fork is
> **native `--fork-session`, no file copy** (§18.2 was wrong); no opencode `--roots`
> flag; opencode JSON is `{id,title,directory,created,updated,projectId}` with
> **unix-ms** timestamps and **empty-bytes (not `[]`)** empty output; claude title =
> last `ai-title`.aiTitle (fallback first non-meta user msg); interactive resume =
> top-level `opencode --session`; adapters return `model.Session` (not a separate
> `agent.Session`).
>
> **Architecture:** `internal/proc.Runner` gained `RunInDir(ctx, dir, …)` — opencode
> session scope is set only by process cwd, so M5 enumerates per-tree via RunInDir
> (DB-read stays forbidden). Directory precedence for claude: in-transcript `cwd` →
> pid-tracker `cwd` → greedy stat-guided slug-decode.
>
> **Deferred:** `InstallStatusHook`/`ReadyHeuristic`/`TrustPrompt` (setup/TUI
> milestones — M8); opencode `--fork` support (post-v1, returns `ErrForkUnsupported`);
> `ListSessions` ctx-threading through the claude FS walk (M5, currently `_ ctx`);
> claude fork-session-into-worktree end-to-end verification (M6).

### M4 — tmux control  *(tmux REQUIRED — now available)* ✅ DONE

- [x] Objective: create session/window in a dir, run a command, switch/attach, set/read
      `@perch_*`, write the window shadow record.
- Spec refs: `plan.md §3` (tmux layout/conventions), `§6.2`, `§9` (option storage),
      `§16.4`, `§20.4` (integration harness on a dedicated `-L perch-test-<pid>` socket).
- Deliverables: `internal/tmux` (connect sequence with `=`-exact targeting, `switch-client`
      vs `attach`, pane-option set/get, deferred-cleanup builder for later). Integration
      tests gated `//go:build integration` on a private socket (`§20.4`).
- DoD (`plan.md §16.4`): debug command opens a shell window in a chosen tree and
      round-trips a pane option + shadow record; `make test-integration` green. §0 gate.

> **Sub-plan:** `docs/superpowers/plans/2026-05-30-perch-m4-tmux.md` (the authoritative
> M4 spec — supersedes the master plan where tagged `[DIVERGENCE]`).
>
> **Verification:** commit chain `dad0991 → eaf2f47 → 1172323 → 5d2ea4e → 4a0fdbc →
> 691b634 → 9562429 → 752e70c → d750ff4`. Coverage: `internal/tmux` 97.7%,
> `internal/proc` 100%. `make test` + `make test-integration` both green (real tmux
> 3.6). The debug `tmux` command demonstrates the DoD: `Connect` → `@perch_session`
> round-trip → shadow-record write + read-back. A real-tmux integration test proves
> the objective's "run a command" clause: `SendKeys` (`-l` + separate `Enter`) executes
> `echo PERCH_$((6*7))` and `CapturePane` reads back `PERCH_42` (output, not just typed
> keys — proves Enter fired and the shell ran it).
>
> **Plan↔reality corrections applied** (in the sub-plan + `plan.md §3/§7.2/§9/§20.4`):
> cold-start classified by exit code not stderr text (E1/E2); name sanitization rule
> for `.`/`:` and the `[A-Za-z0-9_/-]` safe set (E5); window target form
> `=session:=window` anchored on both parts (E4); `-n <window>` + `-P -F '#{pane_id}'`
> on create (E3); `send-keys -l` + separate `Enter` (E6); liveness key is
> `#{pane_current_command}` not `pane_pid` (E7); read paths from `list-panes` not
> `list-sessions` (E11); cleanup is built-not-wired (M4) / dispatched (M6); §9
> `@perch_status` scope/name contradiction flagged for M8.
>
> **New package:** `internal/tmux` — `tmux.go` (reads/targets/names+parse),
> `connect.go` (mutations + `Connect` + `AttachArgs`/`ExecArgs`), `cleanup.go` (§7.2
> builder built-not-wired), `integration_test.go` (reusable `newTestServer` harness +
> 5 integration tests). `internal/proc` gained `ExitCode` + `FakeExitError`.
>
> **Deferred:** attach execution + agent-argv shell-quoting → M5; cleanup execution +
> worktree Remove → M6; resurrect reconcile → M7; live-agent `#{pane_current_command}`
> semantic → M7; `@perch_status` scope reconciliation → M8.

### M5 — TUI open flow  *(tmux REQUIRED)*

- [ ] Objective: unified two-pane list (project→tree→session), fuzzy filter, live
      preview, `↵` runs the right resume/new command in a tmux window.
- Spec refs: `plan.md §11`, `§12` (Charm v1 component+API map), `§13` (claude-squad
      match list), `§16.5`, `§20.5` (teatest rules — state assertions, never rendered
      strings). Resolve `teatest` version via `go get` here.
- Deliverables: `internal/tui` (`app.go`, `list.go`, `styles.go`, `keys.go`, viewport
      preview via `tmux capture-pane`). `tea.ExecProcess` for attach; plain `tea.Cmd`
      goroutines for non-interactive tmux queries (never block Update/View — `§1`).
- DoD (`plan.md §16.5`): resume a real claude and a real opencode session from the list. §0 gate.

### M6 — Worktree lifecycle + state  *(tmux REQUIRED)*

- [ ] Objective: create (file seeding + `post_create`), 3-way mapping prompt + no-reprompt
      persistence, fork semantics, remove with two-tier guardrails + deferred cleanup.
- Spec refs: `plan.md §7.1/§7.2`, `§6.1` (mappings), `§8` (hooks, path-safety gate),
      `§4` (fork-into: claude copy+resume, opencode fresh), `§16.6`, `§18.2`. Verify the
      claude fork copy+resume end-to-end here (`§18.2`).
- Deliverables: `internal/git` worktree add/remove/lifecycle; path-safety guard ported
      verbatim (`§7.1` — reject absolute/`..`); deferred self-close via `tmux run-shell`
      (`§7.2`, mandatory mv→prune→branch→rm order); shadow-record teardown.
- DoD (`plan.md §16.6`): choices survive restart; "create worktree" forks into a new
      branch dir; remove safely handles dirty/locked (integration `§20.4`). §0 gate.

### M7 — Recovery: `perch resurrect`  *(tmux REQUIRED)*

- [x] Objective: boot_id reconcile rebuilds windows after a tmux server restart.
- Spec refs: `plan.md §7.4`, `§6.2` (`boot_id`), `§16.7`.
- Deliverables: read `windows/*.json`, batched `list-panes -a`, two-track reconcile
      (live/intentional-close/restore), descendant-path tree match.
- DoD (`plan.md §16.7`): after killing the test tmux server, `perch resurrect` rebuilds
      windows from shadow records (integration `§20.4`). §0 gate.

### M8 — Admin/status  *(tmux REQUIRED)*

- [x] Objective: live status tick + drop-guard, status colours, `x` kill, attach/jump;
      `perch setup` installs claude hooks + writes `perch-status.ts`; verify opencode
      status end-to-end. ✅ DONE (M8-1..M8-3) — see
      `docs/superpowers/plans/2026-05-31-perch-m8-status.md`.
- Spec refs: `plan.md §9` (whole pipeline + state machine), `§21.5` (additive setup),
      `§16.8`, `§18.1`. `resources/perch-status.ts` + claude settings snippet.
- Deliverables: `internal/status` (`perch status set`, option read helpers, state
      machine — dedup, stale-busy gate, re-arm on user message); tick poll with
      in-flight drop-guard (`§9`); `perch setup` additive merge (atomic write). Tests:
      `§20.3 status` state-machine cases.
- DoD (`plan.md §16.8`): icons reflect a live agent's working/waiting/done. §0 gate.

### M9 — UX layer  *(tmux for live; teatest for model)*

- [x] Objective: command/filter bar, contextual keybindings + generated help, confirm
      modals, screen modes, theme pass, empty/error/small-screen states, README. **DONE.**
- Spec refs: `plan.md §11` (interaction model), `§12` (components), `§16.9`.
- Deliverables (as shipped — see `docs/superpowers/plans/2026-05-31-perch-m9-ux.md`):
      `internal/tui` (`toast.go`, `screen.go`, `help.go`; modal/confirm already in
      `modal.go` from M8); contextual generated footer + `?` overlay from `bubbles/help`
      (`Model` implements `help.KeyMap`); screen modes (`z`/`Z`) + narrow vertical reflow;
      transient toasts; empty state; theme verified v1-correct in `styles.go`. README
      install + usage. **Deferred (FD-M9-1/2):** `:` command bar (would require ripping out
      the tested list-built-in `/` filter for a non-existent command set — YAGNI) and
      standalone `perch attach`. The `table.go`/`prompt.go` files were not needed (no admin
      table screen; no standalone prompt widget — the `:` bar is deferred).
- DoD (`plan.md §16.9`): ✅ all states handled; ✅ teatest model tests green; ✅ README complete. §0 gate green.

---

## Self-review (against plan.md)

- **Spec coverage:** Every `plan.md §` with an implementation obligation maps to a
  milestone: §2/§15/§21→M0/M1, §6/§8→M1, §5/§7.1-parse→M2, §4→M3, §3/§9-storage→M4,
  §11/§12/§13→M5/M9, §7→M6, §7.4→M7, §9→M8. §10 CLI verbs land across M1 (doctor/
  version), M7 (resurrect), M8 (setup/status); optional `perch attach` (§10) and
  `perch merge` (§7.3) are explicitly v1-optional — schedule only if a milestone closes
  early; otherwise out of scope (§17 YAGNI).
- **Non-goals respected:** no daemon, no DB, no watchers, no GH integration, no
  sandboxing (`plan.md §17`).
- **Placeholders:** none — M0/M1 are concrete; M2–M9 explicitly defer their bite-sized
  code to a per-milestone sub-plan authored at execution time (legitimate per the
  writing-plans Scope Check: one plan per subsystem).
- **Version consistency:** the LOCKED table is the only place pins live in this plan;
  `.tool-versions` (M0) is the runtime single source of truth.

---

## Execution handoff

Per the user's choice: **checkpoint per milestone.** Recommended executor:
`superpowers:subagent-driven-development` (fresh subagent per milestone/task, review
between). At each milestone start, author its detailed bite-sized sub-plan, execute,
prove the §0 DoD gate green, commit, then checkpoint with the user before the next.
