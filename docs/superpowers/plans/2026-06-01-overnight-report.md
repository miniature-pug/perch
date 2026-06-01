# perch — Overnight Autonomous Run, Morning Report (2026-06-01 → 06-02)

Branch `feat/perch-v1`. 18 commits, `244713b`→`da309a2` (base `4c04161`). Every commit builds and
is test-green; nothing pushed, nothing tagged. **Closing gate state: ALL GREEN** — build+embed,
gofmt, vet, `go test -race -tags=integration ./...` (18 pkgs), `golangci-lint` 0, `go mod verify`,
`make vulncheck` = "No vulnerabilities found", binary 5.1 MB.

## ✅ Shipped

### M10 — Security (COMPLETE)
Full attack-vector audit (`docs/security-audit.md`) + every confirmed hole fixed with an
**exploit-encoding** regression test (each fails on the un-fixed code):
- **V1 (CRITICAL) trust-on-first-use** for `.perch.toml` hooks — opening an untrusted repo could run
  arbitrary `sh -c` via `post_create`/`pre_remove` with no gate. New `internal/trust` store
  (config-path→content-hash, mode 0600) + a TUI approve-always/once/deny modal; TOCTOU closed by
  re-hashing before exec; **adversarially reviewed — no untrusted-hook path found**.
- **V6a/V6b** tmux option-value injection (delimiter/newline desync + unvalidated `@perch_session`).
- **V3-A/V3-B/V2′** git ref validation (`--upload-pack=`/`--no-checkout` flag injection) + `--`
  guards + project `worktree_dir` made relative-only.
- **V7b/V7c** state-file read caps (OOM) + resurrect tree-under-root.
- **V11** `golang.org/x/sys` → v0.44.0 (vulncheck now clean); **V5** capture-pane `-e` guard-test.
- 9 vectors verified already-mitigated/clean (agent-binary injection, path traversal, discovery
  symlink, setup hardening, status sink, supply chain).

### M11-0 — Persistent-frame switcher (CORE COMPLETE — the "website-in-a-terminal" model)
Replaces the old `↵`=switch-client picker ("everything else is gone"). Design proven by an isolated
tmux spike (`docs/.../2026-06-01-perch-core-model-spike.md`): **swap-pane + per-agent sessions**.
- Frame = a `perch` tmux session: sidebar pane (the TUI) + a main pane that shows the selected agent
  LIVE. Selecting swaps the agent's pane into the main slot (no re-resume); switching is a two-step
  swap; `q` swaps the displayed agent **home before** killing the frame (so it survives).
- tmux frame/swap primitives (T1, with a real-curses-app **reflow gate** that passed); pure swap
  state-machine (T2); Enter→swap-in + quit teardown with a **data-loss integration gate + negative
  control** (T3); `perch` bootstrap + `perch --sidebar` entrypoint, FD-03 `@perch_frame` guard,
  **graceful fallback to the direct TUI** if frame setup fails (T4); resurrect now **restores
  crash-stranded agents** instead of silently pruning them (T5).
- Default tmux socket (NOT a private socket — that would break `perch status set`). `attachTo` is
  retained as the no-tmux/fallback path (not dead code).

### M11-2 — `perch attach <query>` (COMPLETE)
CLI fuzzy-match (project/branch/tool) → attach. 0 matches exit 1; 1 → switch-client/attach; 2+ →
list candidates exit 2 (never guesses). Query is match-only — can't reach tmux argv.

### M11-3 (PARTIAL) + M12 (PARTIAL)
- **auto-clear `@perch_pane_status` on swap-in** (focusing an agent drops its stale badge). ✅
- **`perch setup --replace`** — scalpel-replaces the perch-owned hook block, preserves foreign
  config, keeps all four install guarantees. ✅
- **kill-confirm names its target** ("Kill <branch>?" not "Kill window?"). ✅

## ⚠️ NEEDS YOUR DECISION (top FD — blocks finishing M12 polish)
**"How do I go back?" — the one metaphor leak.** With the default-socket design, once focus is in the
agent's main pane, keystrokes go to the agent; returning to the sidebar uses a **tmux prefix + ←/→**
(or `prefix + o`). This is strictly better than before (sidebar stays visible; switching is in-perch),
but the refocus gesture is a tmux binding, not a perch key. Options for your sign-off:
1. **Keep default-socket + document `prefix ←/→`** (recommended; zero config pollution, status hook
   works). 2. Private socket for slick no-prefix Alt+←/→ (FD-01) — but it **breaks `perch status
   set`** and inside-tmux bootstrap. 3. A perch-keys opt-in table (FD-02).
I went with **(1)**; please confirm before I wire the help-overlay/README wording around it.

## 🔬 HUMAN SMOKE-RUN (only the live attach + agent reflow need a real terminal)
Everything is FakeRunner/private-socket tested except the tty-only attach handshake. Please run:
```
go build -o bin/perch ./cmd/perch
./bin/perch                      # expect: a tmux frame — sidebar list (left) + main pane (right)
#   select a session (↵)         # expect: that agent appears LIVE in the main pane, sidebar stays
#   prefix + →                   # expect: focus moves into the agent; interact normally
#   prefix + ←                   # expect: focus back to the sidebar; select another (↵)
#   q                            # expect: perch closes
tmux ls                          # expect: your agent sessions SURVIVE (not killed with the frame)
```
Watch for: agent (claude/opencode TUI) reflow when swapped into the wider main pane (pre-resize +
refresh-client should make it clean). `perch attach <query>` from a plain shell is also worth a try.
If the frame misbehaves, `perch` still falls back to the direct TUI; the whole run is revertable
commit-by-commit.

## ⏭ REMAINING (resume order)
- **M11-1** `:` command bar (own textinput overlay; keep the `/` list filter): `:q`, `:setup`,
  `:doctor`, `:resurrect`, `:attach <q>`, `:new`, jump-to-project; help into `?`.
- **M11-3** auto-offer-resurrect on TUI start (boot_id mismatch → offer `perch resurrect`).
- **M12** modal-over-body compositing (dim backdrop, keep width/height invariants); dedicated sidebar
  collapse toggle; theme/contrast pass; help/keys/footer text refresh (Enter wording + the prefix-nav
  hint per the FD above); confirm binary stays ~5 MB.
- **M13** docs+diagrams: mermaid (architecture, status sequence, worktree lifecycle, discovery/state)
  + README §11 layout/keys/security-trust-model + ARCHITECTURE/CONTRIBUTING/CHANGELOG + godoc.
- Then **you** cut `v0.1.0` (never tagged by me).
```
