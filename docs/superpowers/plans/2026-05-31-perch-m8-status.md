# M8 — Admin / status pipeline (sub-plan)

Status: **DONE** (M8-1 `ef50adf`, M8-2 `89e29f6`, M8-3 + data-loss fixes — see closeout)

Master plan item 8 (§9, §10, §11, §20.3, §21.5). DoD: *icons reflect a live agent's
working / waiting / done.* Built milestone-by-milestone on `feat/perch-v1` via
subagent-driven development (advisor before approach + before done; fresh Sonnet
implementers; two-stage parallel review; fix loops; gates verified personally).

Advisor consulted on approach (confirmed D1/D2/D4, surfaced 5 blind spots — baked in
below).

---

## Fixed decisions

**FD1 — `@perch_pane_status`, pane-scoped only (resolves the §9 NOTE contradiction).**
`perch status set <state>` resolves `$TMUX_PANE` and writes the **pane** option
`@perch_pane_status` (`tmux set-option -p -t $TMUX_PANE @perch_pane_status <state>`).
This matches the existing `@perch_session` convention (M4) and is what the admin reads.
The window-scoped `@perch_status` + tmux-status-bar integration described in §9's
Storage bullet is **dropped from v1**: perch's UI is the bubbletea TUI, not the tmux
status line, so the window option's only stated purpose has no consumer. Documented as
a decision; not a deferral.

**FD2 — `perch status set` is a stateless dumb sink; gating lives in the plugin.**
Per §9 ("handler shells out `perch status set <state>`") and §20.3 (test column is
literally *"Expected calls to `perch status set`"*): the dedup / stale-busy-gate /
re-arm state machine runs **in the opencode TS plugin** (per-session, in-process). The
CLI surface stays exactly `status set <working|waiting|done>` (§10) — no `status event`
verb, no daemon (§17). `internal/status.Set()` only resolves the pane and sets the
option.

**FD3 — `status.Machine` is the canonical reference, hand-ported to TS.** The state
machine is implemented as a pure Go `status.Machine` (event-in → maybe-fire-state-out)
so the §20.3 table maps 1:1 and is regression-locked in Go. `resources/perch-status.ts`
is a faithful hand-port of `Machine`. **Honest:** `Machine` is the tested spec mirror,
not on the runtime hot path; the live path is opencode → TS plugin → `perch status set`.

**FD4 — `status set` no-ops cleanly outside tmux.** `$TMUX_PANE` empty → print nothing,
**exit 0** (must not fail the agent's hook). All tmux access through the `tmux.Tmux` /
`proc.Runner` seam (FakeRunner in unit tests, never spawns).

**FD5 — `StatusLive` (●) stays as the "live, status-unknown" fallback.** A live pane
with no `@perch_pane_status` set is honestly *"live, agent hasn't reported."* Mapping:
`working`→`StatusWorking`🤖, `waiting`→`StatusWaiting`💬, `done`→`StatusDone`✓, unknown/
unset on a live pane→`StatusLive`●, no live pane→`StatusIdle`○.

**FD6 — append `@perch_pane_status` LAST in `paneFormat`; re-run resurrect.** `Pane` and
`ListPanesAll` are shared with `internal/resurrect`. Add the new field at the END of the
format string and the parser, then re-run resurrect unit + integration to prove no
positional-parse regression in the engine shipped in M7.

**FD7 — single-bool poll drop-guard, reuse the M5 D6 join.** The status tick is a
re-armed `tea.Tick(refresh_ms)` → `statusPollMsg`; overlapping polls are dropped with a
plain in-flight bool (single-goroutine `Update` makes atomic unnecessary despite §9's
k9s reference; mirror the existing `capturing` guard). Poll results reconcile onto rows
via the **existing `@perch_session` D6 live/idle join**, not a new key.

**FD8 — `perch setup` is additive + idempotent + hermetic-testable.**
- Claude (`~/.claude/settings.json`): merge perch's hook entries into the existing event
  arrays; never touch existing entries; write atomically (temp + rename). **Idempotent:**
  re-running must not append a second perch entry — dedup on the perch command string /
  marker. Exactly one perch entry per event after N runs.
- opencode (`~/.config/opencode/plugins/perch-status.ts`): written as a new file from
  `resources/`; existing plugins (e.g. `workmux-status.ts`) left in place.
- **Hermetic, non-negotiable:** every setup test and any manual run redirects `$HOME` /
  `$XDG_CONFIG_HOME` to a temp dir. NEVER merge against the real user config. (Same
  discipline as M7's `XDG_STATE_HOME`, higher stakes — a bad merge corrupts the user's
  real claude settings.)

**FD9 — Adapter gains `InstallStatusHook() error`.** Added to the `agent.Adapter`
interface now (the adapter.go comment defers it to "the setup milestone" — this is it).
Claude impl merges settings.json hooks; opencode impl writes the plugin file. `perch
setup` detects installed tools (reuse `Detect()`) and calls `InstallStatusHook()` on each.

---

## Deferred (recorded — do NOT re-litigate)

- **L1** `perch setup --replace` (opt-in destructive removal, §21.5) → **M9**. M8 ships
  additive-only.
- **L2** standalone `perch attach <query>` (explicitly *"ship if time allows"*, §10) →
  **M9**. In-TUI attach/jump (`↵` switch-client) already shipped in M5.
- **L3** auto-clear-on-focus tmux `pane-focus-in` hook (§9) → **M9**.
- **L4** live opencode end-to-end status proof → **manual / env-gated honesty note**
  (needs live LLM + auth + network; not hermetically drivable; adding a JS test runner
  violates the zero-npm-dep ethos for `perch-status.ts`). M8 proves: Go `Machine` tests
  (state-machine logic), hermetic claude path (set `@perch_pane_status` → admin reads +
  renders + colours it), TS plugin reviewed against `Machine`.

---

## Tasks

### M8-1 — `internal/status` + `perch status set`
- `internal/status/status.go`: `Set(ctx, deps, state) error` — validate state, read
  `$TMUX_PANE` (no-op exit-0 path handled by caller when empty), `SetPaneOption(-p, pane,
  @perch_pane_status, state)` via the tmux/runner seam. Plus `Machine` (FD3): pure
  state-machine type, methods like `OnSessionStatus(type)`, `OnPermissionAsked/Replied`,
  `OnUserMessage()` → returns `(state string, fire bool)`. Dedup per last-fired state,
  stale-busy gate (arm accept-busy on user message, disarm on done), waiting↔working.
- `internal/status/status_test.go`: the §20.3 table (dedup, stale-busy rejected, re-arm
  on user message, waiting→working, independent sessions) + `Set` via FakeRunner
  (assert argv, never spawn) + empty-`$TMUX_PANE` no-op + invalid-state.
- `cmd/perch/main.go`: replace the `handleStatus` stub — call `status.Set`; empty pane →
  exit 0 silently; invalid usage → exit 2.
- Gate: ≥80% on `internal/status`; full `go test ./...`, vet, gofmt, lint green.

### M8-2 — admin live status (TUI tick + render)
- `internal/tmux`: add `PerchStatus string` field to `Pane` (LAST), extend `paneFormat`
  with `#{@perch_pane_status}` at the end, update the parser. **Re-run resurrect unit +
  integration (FD6).**
- `internal/tui`: `tea.Tick(cfg.RefreshMs ms)` → `statusPollMsg`; re-arm each tick;
  in-flight bool drop-guard (FD7). Poll = `ListPanesAll`, build paneKey→status map, map
  string→`Status` (pure func, FD5), reconcile onto live rows via the D6 `@perch_session`
  join. `styles.go`: `statusColor(Status) lipgloss.AdaptiveColor` (pure func) — working/
  waiting/done/live/idle distinct, adaptive light/dark; delegate renders glyph in colour.
- `x` kill already wired (M6) — verify still works after the render changes.
- Gate: teatest where practical; full suite + integration green; resurrect unchanged.

### M8-3 — `perch setup` + resources (GATED on the two primary-source verifications)
- `internal/agent`: add `InstallStatusHook() error` to `Adapter`; implement on claude
  (settings.json additive idempotent atomic merge — schema per the **verified** claude
  hooks structure; Notification entry per the **verified** matcher capability, else plain
  `Notification → waiting` with an imprecision note) and opencode (write
  `resources/perch-status.ts` to `~/.config/opencode/plugins/`).
- `resources/perch-status.ts`: zero-npm-dep opencode plugin, hand-port of `Machine`
  (event map per §9), shells `await $\`perch status set <state>\`.quiet()`.
- `resources/` claude settings snippet (reference doc of the merged hooks).
- `cmd/perch/main.go`: replace the `setup` stub — detect tools, call
  `InstallStatusHook()` per installed adapter, report what was installed.
- `internal/doctor`: add hook presence checks (claude hooks in settings.json; perch-
  status.ts installed) per §21.4 — warn (not fail) when missing.
- All tests hermetic via `$HOME`/`$XDG_CONFIG_HOME` temp redirect (FD8); idempotency
  test (run-twice → one entry); merge-preserves-existing test.
- Gate: full suite + integration green; doctor reflects hook state; clean tree.

---

## Primary-source verification (done before M8-3; corrects §9)
- **claude** (code.claude.com/docs hooks): structure `{hooks:{<Event>:[{matcher,hooks:[{type:"command",command}]}]}}` confirmed; `Notification` matcher DOES support subtypes — `permission_prompt`, `elicitation_dialog` (regex alternation used); command hooks inherit the claude env (so `$TMUX_PANE` is present); write `~/.claude/settings.json`.
- **opencode** (opencode.ai/docs + packages/sdk types, v1.15.x): plugins auto-load from `~/.config/opencode/plugins/*.ts`; verified events `session.status` (`properties.status.type` ∈ busy/retry/idle, `properties.sessionID`), `session.idle`, `message.updated` (`properties.info.role`), `permission.updated`→waiting, `permission.replied`→working. **`question.*` does NOT exist** in the v1.15.x union (§9 listed it — removed). `permission.asked` is not in the typed union either (may fire untyped, lags the union) — the plugin handles it defensively.

## Honesty (closeout)
*demonstrated (hermetic / unit / integration):*
- `perch status set` writes pane-scoped `@perch_pane_status` (FakeRunner argv-exact); no-op exit-0 outside tmux.
- `status.Machine` passes the §20.3 table (dedup, stale-busy gate, re-arm on user message, waiting↔working, independent sessions); 95.3% pkg coverage.
- admin live tick reads `@perch_pane_status` and renders coloured working/waiting/done glyphs; selection preserved across the 1 s poll; in-flight drop-guard; applied-filter view does not blank (SetItems re-filter cmd captured). **FD6: no resurrect/tmux regression** — unit + `-race` integration green (field appended last, parser min-guard 8).
- `perch setup`: claude `settings.json` merge is **additive** (preserves foreign hooks + unrelated keys), **idempotent** (`perch status set` substring dedup), **atomic** (temp+rename), **refuses to clobber** a malformed-but-present `hooks`/event key (returns error), **preserves file mode** + numeric fidelity (`UseNumber`); opencode plugin written atomically, other plugins untouched. `TestDoctorSetupAgreement` proves doctor recognizes both, all in a `t.Setenv("HOME", tmp)` sandbox — the real `~/.claude` was never touched.

*manual / deferred:*
- **L4** live opencode end-to-end status — needs a live LLM + auth + network; not hermetically drivable, and a JS test runner would violate the zero-npm ethos. The Go `Machine` is the tested logic; `resources/perch-status.ts` is a reviewed hand-port of it.
- claude live-hook-firing — hermetic test proves the merge + doctor recognition + admin read/render/colour; the hook actually firing inside a live claude session is a manual note.
- opencode event uncertainties (§18.1): `permission.asked` and the `session.status`-idle branch are defensive (may be dead if those don't fire); `message.updated` sessionID resolved robustly as `info.sessionID ?? props.sessionID` — unverified at runtime.
- `CLAUDE_CONFIG_DIR` / `XDG_CONFIG_HOME` overrides not handled — setup writes `~/.claude/settings.json` + `~/.config/opencode/plugins/` (matches doctor exactly). Documented v1 limitation.
- `handleStatus` tmux-error (exit 1) branch not unit-covered (`tmux.New()` not injectable without a DI refactor; `Set` logic covered at the package level).
- **L1** `perch setup --replace`, **L2** standalone `perch attach <query>`, **L3** auto-clear-on-focus → **M9**.
