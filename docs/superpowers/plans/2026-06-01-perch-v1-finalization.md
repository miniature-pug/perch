# perch v1 Finalization — Security, Feature-Completeness, Polish

> **CONTROLLING DOC for the post-M9 finalization phase.** Survives compaction; resume from here.
> **For agentic workers:** author a bite-sized TDD sub-plan per milestone (superpowers:writing-plans), execute via superpowers:subagent-driven-development (fresh implementer + two-stage review per task), checkpoint per milestone. Branch: `feat/perch-v1`.

## Standing principles (user, 2026-06-01 — override any earlier "v1-lean / defer" framing)

1. **Perfection over time.** Time is NOT a constraint. Goal: correct, efficient, simple, beautiful, lightweight. No shortcuts justified by effort.
2. **Security is non-negotiable.** No exploitable hole ships. Audit every attack vector, inside and outside the app.
3. **No deferrals.** "An LLM agent can't get tired" — there is no point deferring. Everything previously marked defer/optional/post-v1 is now IN SCOPE for v1. This explicitly REVERSES FD-M9-1 (`:` command bar) and FD-M9-2 (`perch attach`), and pulls in L1/L2/L3 (`perch setup --replace`, auto-clear-on-focus tmux hook), the kill-confirm-names-target polish, and the modal-over-body compositing residual.
4. **Documentation-complete.** Code, README, architecture, and diagrams must all be current and sufficient — a reader (human or agent) should understand the system without reading the source. Diagrams ARE needed (see M13); the single root README is not enough on its own.

State of play at handoff: M0–M9 DONE on `feat/perch-v1` (16 M9 commits, last `685029b`). All gates green (build+embed, unit + `-race` integration on real git + tmux 3.6, lint 0, gofmt, `go mod verify`, `make vulncheck` 0-affecting). v1 was *feature-complete-minus-deferrals*; this doc removes the deferrals and adds the security pass. **Do NOT cut the `v0.1.0` tag** until M10–M12 are done AND the user approves (protected-branch/release policy — tag is the user's action).

---

## M10 — Security deep-dive (DO FIRST; a hole here outweighs any feature)

**Method:** one read-only investigator subagent per vector → returns file:line + verdict (exploitable / mitigated / N-A) with primary-source evidence (read the actual code, the vendored libs, the §20.1 Runner usage). Then fix each confirmed hole TDD, asserting the fix via `FakeRunner.Calls` (no real spawns) and hermetic FS tests (`t.Setenv HOME`, temp dirs). Re-verify. Write a `docs/.../security-audit.md` findings ledger (durable). Threat model = (a) malicious/untrusted **repo** opened in perch, (b) malicious **agent output**, (c) malicious **tmux/env** state, (d) supply chain.

### Attack-vector catalog (audit EACH; this is the work-list)

**INSIDE the app**

1. **`.perch.toml` hook RCE — HIGHEST.** `post_create` / `pre_remove` / `pre_merge` are shell commands sourced from PROJECT-level config. Opening perch on an untrusted repo → its `.perch.toml` runs arbitrary commands (classic malicious-repo vector, cf. git hooks / direnv / VS Code tasks). AUDIT: are these executed? when? gated? Decide a mitigation and implement: e.g. **trust-on-first-use prompt per repo** (record trusted repo roots in global state; never run a repo's hooks until the user approves it once), or hooks global-only, or explicit per-repo opt-in. Whatever ships must default-deny untrusted repos. This likely interacts with `internal/config` discovery (§8.1 three-tier) and the worktree create/remove Cmds (`internal/tui` worktree_actions, `internal/worktree`).
2. **`.perch.toml` agent / startup_command injection.** Confirm (memory) agent-binary resolution stays GLOBAL-ONLY — project config may only pick a *known* agent + model/prompt, never an arbitrary executable path. Audit `startup_command` (is it shelled? interpolated?) and any per-project `agent` field. (Spec: there is a security guard to port verbatim — `plan.md` ~L424. Verify it exists and holds.)
3. **Argument/command injection via interpolated values.** Branch names, session titles, paths, and the new `perch attach <query>` all flow into `git`/`tmux` argv. The §20.1 Runner uses exec argv (no shell) — VERIFY there is NO `sh -c "...$var..."` anywhere. Then check **argument injection**: a branch/path/query beginning with `-` could be parsed as a flag (e.g. a branch literally named `--upload-pack=…`, or a path `-X`). Ensure `--` separators precede every user-controlled positional in git/tmux invocations; validate branch names against git's ref rules.
4. **Path traversal / symlink escape.** Worktree paths and `.perch.toml [files] copy`/`symlink` must stay inside the repo (memory: validate paths stay inside repo — VERIFY enforced for worktree create AND [files] ops). A `symlink` target or `copy` source pointing at `../../.ssh/id_rsa` (or a symlinked dir) could exfiltrate/clobber. Resolve symlinks and assert containment.
5. **capture-pane escape-sequence injection into the preview.** Preview renders `tmux capture-pane -e -p` (ANSI preserved) of UNTRUSTED agent output into a `bubbles/viewport`. Malicious output can carry terminal control sequences (OSC 52 clipboard write, window-title set, cursor/scroll-region abuse, hyperlink spoofing). AUDIT what reaches the terminal; decide sanitization (strip/escape dangerous CSI/OSC, or whitelist SGR colour only). Note claude-squad uses `-e` too — but "others do it" is not a security argument.
6. **tmux option-value injection & session spoofing.** `@perch_session` / `@perch_pane_status` values feed the pane-format parser (`\x1f`/`\x1e` delimiters) and the session-join. A value containing a delimiter/newline could corrupt parsing; a forged `@perch_session` could spoof another session's identity in the join (first-write-wins). VERIFY: `@perch_session` is validated as a UUID before trust; parser is robust to delimiter injection; `perch status set` validates the state enum before any tmux write (it does — re-confirm).
7. **State-store path injection / parse DoS.** `windows/<paneKey>.json` — if `paneKey` derives from tmux and can contain `/` or `..`, a write could escape the `windows/` dir. Frecency/state JSON parsing of attacker-influenced files: guard against huge/deeply-nested JSON (DoS), and validate keys/paths on load.
8. **Discovery symlink/loop traversal.** The discovery scan under `root` — does it follow symlinks (escape root → scan `$HOME`/secrets) or loop infinitely? Bound depth, don't follow symlinks out of root.
9. **`perch setup` hook-install hardening (re-verify M8).** Already atomic + refuse-malformed + mode-preserve + `UseNumber`. Re-verify: no path traversal via `$HOME`/`CLAUDE_CONFIG_DIR`/XDG; embedded `perch-status.ts` only ever shells `perch status set <enum>` (no attacker interpolation); the merge can't be coerced into executing arbitrary hooks.
10. **`perch status set` sink.** Reads `$TMUX_PANE`; stateless; validates state ∈ {working,waiting,done} before the tmux write; empty `$TMUX_PANE` → exit 0. Re-confirm no write occurs on invalid input.

**OUTSIDE the app (supply chain / build)**

11. **The 1 govulncheck finding.** `make vulncheck` reports 1 vuln in a *required* module not called by our code. IDENTIFY it (`govulncheck -show verbose`), assess reachability, and update/pin the dep out if a qualifying (≥30-day, stable) version exists; otherwise document why it's unreachable. Re-run clean.
12. **Dependency & vendor integrity.** Re-confirm `go mod verify` + committed `/vendor` + the supply-chain policy (§ deps ≥30d/stable, exact pins). `govuln` pinned via `GOVULN`. No transitive surprise.
13. **Build hygiene.** `-trimpath` (done), version-only ldflags (done), no secrets/paths leaked into the binary; confirm `make cross` artifacts are clean.

**DoD M10:** every vector above has a written verdict; every confirmed hole is fixed with a regression test; `make vulncheck` clean (or documented-unreachable); `security-audit.md` ledger committed.

---

## M11 — Feature-complete (NO deferrals)

### M11-1 — `:` command bar (reverses FD-M9-1)
The earlier deferral reasoning was "no command set + would rip out the tested `/` filter." Resolution: **keep the list's built-in `/` filter as-is; add `:` as its OWN command prompt** (a `bubbles/textinput` shown as a bottom/overlay prompt), not a unified two-mode widget. This avoids re-architecting the tested filter while delivering the `:` feature. Design a real, useful command set (single-screen app → commands are quick actions + CLI verbs surfaced in-TUI), e.g.:
- `:q` / `:quit`, `:help`/`:?`
- `:setup`, `:doctor`, `:resurrect` (run the CLI verb; for verbs that write to the terminal, hand off via `tea.ExecProcess`)
- `:attach <query>` (in-TUI form of the M11-2 fuzzy attach)
- `:new [tool]`, `:reload` (reload config/theme — explicit, no watcher)
- jump-to-project by name (`:<projname>` or `:cd <name>`) — re-uses the frecency-ordered project list
Generate `:`-command help into the `?` overlay. 100 ms debounce only if a command does live preview (most don't). Unknown command → toast. Validate/parse args (feeds M10 #3 — `:attach <query>` is user input → no injection). teatest model-state tests for: open `:`, type, run each command → state/cmd; esc cancels; unknown → toast.

### M11-2 — `perch attach <query>` (reverses FD-M9-2)
CLI subcommand (`cmd/perch/main.go` dispatch): fuzzy-match a session by title/branch/project across discovery, then `tmux switch-client` (inside tmux) or `attach` (outside), no TUI. Reuse discovery + the existing attach path (`internal/tui` attachTo logic → likely extract a shared helper into a non-TUI package). Handle: 0 matches (error, exit non-zero), 1 match (attach), N matches (list candidates, exit non-zero with guidance — or pick best by frecency? decide: deterministic best-match by frecency + a `--print`/list affordance). Query is untrusted input → argv-safe (M10 #3). Tests via FakeRunner asserting the switch/attach argv; integration test on real tmux.

### M11-3 — pull-in deferred L-items
- **L1 `perch setup --replace`** — re-install hooks/plugin overwriting an existing perch block (still additive to *foreign* config; only replaces the perch-owned section). Idempotent, atomic, refuse-malformed (same guarantees as M8 setup).
- **L3 auto-clear-on-focus** — when a pane gains focus, clear its `@perch_pane_status` (a focused session is no longer "waiting/done"). tmux `pane-focus-in` hook installed by `perch setup`, OR cleared on attach/switch from the TUI. Decide the daemon-free mechanism (§17: no watcher) — likely a one-shot clear on the TUI's switch/attach action + an optional tmux hook line.
- **TUI auto-offer-resurrect on start** — if a server restart is detected (boot_id mismatch, §7.4), offer `perch resurrect` from the TUI on launch.

**DoD M11:** `:` bar + `perch attach` + L1/L3 + auto-resurrect shipped, model/CLI tested, README updated (commands + keybindings + `:` reference), help overlay regenerated, gates green.

---

## M12 — Polish (simple · beautiful · lightweight)

- **Modal over body (compositing).** M9 renders modals on a blank centered canvas (body hidden) to avoid overflow. Implement true over-the-body overlay (dim/!backdrop + modal box composited on top) so the user sees context while confirming. Keep the width/height invariants (no overflow).
- **Kill-confirm names the target.** `x` → "Kill window?" must name the session/branch being killed (remove-confirm already does). Same for any destructive confirm.
- **Sidebar / switcher UX (user requirement, 2026-06-01).** The left selector MUST stay **static (visible) by default** so it works as a switcher — confirmed already true (`modeNormal` default → two-pane). Add a **dedicated collapse toggle** (single key, e.g. a "toggle sidebar" binding) rather than relying only on the `z`/`Z` 3-mode cycle. Keep the bar from ever auto-hiding. NOTE the boundary: this is the in-TUI switcher (pick → `↵` hand off → return on detach); an always-on-screen sidebar *while inside a live session* is a §17 reversal (daemon/tmux split) — only build that if the user explicitly confirms.
- **Theme pass** — final palette review for light/dark contrast + colourblind-safe status hues; ensure `CompleteColor` fallback for ANSI/256-colour terminals (§12); the `?`/modal overlays legible on both themes.
- **Visual smoke verification.** §20.5 excludes screenshot tests, so layout is proven only by dimension-invariants + no-panic. Do a real `./bin/perch` smoke-run matrix (normal/narrow/tiny terminals; empty/populated; toast/modal/help/`:` open) and record observations — this is the only "human looks at it" check and must happen before tag.
- **Binary size / lightweight.** Confirm the static binary stays lean (currently ~5 MB); no accidental heavy deps added by M10–M12.
- **Docs final pass.** README, CLAUDE.md/AGENTS.md if present, `.tool-versions`, `.gitignore` current; every command/keybinding/config field matches source (re-run the README fact-check).

**DoD M12 / v1:** all states polished; full gate green incl. `make vulncheck`; security ledger clean; visual smoke-run recorded; then PAUSE for user approval to cut `v0.1.0` (user's action, not mine).

---

## M13 — Documentation-complete

Current state (2026-06-01 scan): ZERO diagrams (no `.mmd`/`.svg`/mermaid anywhere); only the root `README.md` (fact-accurate per M9 but thin) + `LICENSE`; NO `ARCHITECTURE.md`/`CONTRIBUTING.md`/`CHANGELOG.md`/`AGENTS.md`; NO package `doc.go` godoc comments. The `docs/` tree holds only internal superpowers plans, not user/architecture docs.

**Diagrams ARE needed** — author as **mermaid** (renders on GitHub; also keep `.mmd` sources per the user's `*.mmd` convention so they're regenerable/diff-able). Minimum set:
1. **Architecture / component diagram** (§2) — TUI ↔ `internal/{tui,discover,agent,tmux,git,worktree,state,config,resurrect,status,proc}`; the one-Runner shell-out boundary (§20.1); the two JSON stores; "no daemon" called out.
2. **Status-pipeline sequence diagram** (§9) — agent hook/plugin → `perch status set <state>` → tmux `@perch_pane_status` → admin tick-poll → glyph. Daemon-free.
3. **Worktree + session lifecycle** (§7) — create → run → pause/resume → remove (two-tier guard) → resurrect (boot_id reconcile).
4. **Discovery & state relationships** (§3/§5/§6) — root → projects → trees → sessions; frecency; `windows/<paneKey>.json` shadow.
5. Promote the **tmux two-pane layout** ASCII (§11) into the README.

**README** — content is accurate but incomplete for a doc-complete v1. Add: a visual (the §11 ASCII layout and/or an asciinema/screenshot if tooling allows), the **security/trust model** (the M10 repo-trust decision — tell users untrusted-repo hooks are gated), the full `:` command + `perch attach` reference (post-M11), a link to `ARCHITECTURE.md`, and badges (build/license) if cheap. Re-run the README fact-check after M11/M12 so every command/keybinding/config field still matches source.

**New docs to create:**
- `ARCHITECTURE.md` — prose + the embedded mermaid diagrams above; the design decisions (Charm v1 lock, no-daemon, Runner principle, adapter interface, frecency, resurrect).
- `CONTRIBUTING.md` — build/test/lint (`make build|test|test-integration|lint|vulncheck`), the §20.1 Runner+FakeRunner testing principle, the supply-chain policy (deps ≥30d/stable, exact pins, `go mod verify`, `govulncheck`), branch policy.
- `CHANGELOG.md` — v0.1.0 entry (Keep-a-Changelog style).
- Package **`doc.go`** (or package-comment) godoc for each `internal/*` + `cmd/perch` so `go doc` is useful.
- Optional `AGENTS.md` at repo root documenting conventions for future agents (mirrors what's learned across M0–M13).

**DoD M13:** diagrams authored + embedded + render; README enhanced + re-fact-checked; ARCHITECTURE/CONTRIBUTING/CHANGELOG created; godoc package comments present (`go doc ./...` useful); all docs consistent with the shipped code (no stale command/flag/keybinding). Update co-located docs touched by M10–M12 as you go (don't leave M13 to find drift).

---

## Execution order
M10 (security) → M11 (features) → M12 (polish) → M13 (docs+diagrams) → user cuts `v0.1.0` tag. Checkpoint after each milestone. Author the bite-sized sub-plan at each milestone start (superpowers:writing-plans), execute via subagent-driven-development. Every fix gets a regression test; no silent scope. Keep `internal/tui` excluded from the ≥80% coverage rule; all other packages ≥80%. M13 doc updates that are *touched by* M10–M12 should be made within those milestones (keep docs current as you go); M13 is the final completeness sweep + the diagrams + the new top-level docs.
