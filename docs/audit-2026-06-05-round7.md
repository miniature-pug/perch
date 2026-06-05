# Round-7 Audit — 2026-06-05

**Branch:** `feat/perch-v1`
**Baseline:** `make test-all` GREEN before any change.
**Method:** 9-agent read-only fan-out (engagement delta, glass delta, attention/opencode delta, incomplete-removal hunt, magic-number/config sweep, dead-code sweep, doc/diagram drift, spec feature-parity, structure+test-quality). Every finding carries a falsifiable citation; opinion routed to a separate list. Exclusion set = the adjudicated decisions in `docs/audit-2026-06-03-spec-vs-code.md` (not resurfaced without new evidence). Delta scrutinized = `12789e2..HEAD` (rounds 3–6, the surface added after the 2026-06-03 20-agent audit).

---

## Headline

Six converged rounds left the codebase clean on feature parity (**0 cut corners** — every spec promise is implemented or has a deferral record) and clean on incomplete removals of compiled code (`go vet` + `tsc` both clean). The real findings are: **one HIGH logic bug** the gate could not see (approval notification mis-keyed), **one HIGH glass-defeat** (opaque wrapper behind a frosted child), orphaned test fixtures, a dead theme token, two small Go robustness gaps, and a set of magic-number / doc-drift tidies the user explicitly asked for.

---

## Findings — triaged

### Fix (verified real)

| ID | Sev | File:line | Verified problem | Fix |
|----|-----|-----------|------------------|-----|
| R7-1 | HIGH | `app/app.go:727` | dispatchNotify approval case requires `Kind=="state"`, but both monitors emit `Kind:"approval"` (claude_monitor.go:105, opencode_monitor.go:424). Event pump (app.go:649) dispatches every event → approval hits `default: return`. Blocking "Approval needed" in-app notify + OS desktop notification never fire for real tool approvals. Masking test (`seam_bugs_test.go`) injected `Kind:"state"`. | Change case to `evt.Kind == "approval" && evt.State == StateAwaitingApproval`. Fix masking test to inject `Kind:"approval"`; add a test asserting the blocking notify fires. |
| R7-2 | HIGH | `frontend/src/App.svelte:912-915` | `.notification-hub-dock { background: var(--perch-bg) }` is an opaque wrapper sitting directly behind `.notif-hub` (NotificationHub.svelte:50-57) which uses `backdrop-filter: var(--perch-glass-filter)`. The blur samples the dock's solid color → glass frost can never render on the hub. | Remove `background: var(--perch-bg)` from `.notification-hub-dock` (the hub owns its own glass background). |
| R7-3 | MED | `app/app.go` (Approve always-rule path) | `GetSettings()` error discarded (`s, _ := ...`) → on corrupt settings, the always-rule is silently dropped. | Propagate the error. |
| R7-4 | MED | `internal/notify/notify.go` (`New()` dbus probe) | Availability probe opens a `dbus.SessionBusPrivate()` connection, checks err, discards without closing → connection leak when dbus is available. | Close the probe connection (or use a lighter probe). |
| R7-5 | MED | `frontend/src/lib/DiffView.svelte` (initial `$effect`) | `diffStat` rejection silently sets `loading=false`, `files=[]` → a git error renders as "No changes". | Add an error state + brief "Could not load diff" message. |
| R7-6 | MED | `App.svelte` `.undo-toast` / `@keyframes toast-in` | `toast-in` animates `translateY` with no `prefers-reduced-motion` guard (every other animation in the file is guarded). | Add reduced-motion guard. |
| R7-7 | LOW | glass.css `[data-glass="off"]` + `@supports not` blocks | Glass-off resets `--perch-glass-bg`/`--perch-glass-filter` but NOT `--perch-glass-shadow` → the inset specular sheen persists on solid cards. | Set `--perch-glass-shadow: var(--perch-shadow-float)` in both blocks (this also makes the otherwise-unused `--perch-shadow-float` token live). |

### Cleanup (verified zero-reference)

| ID | Sev | Target | Proof | Action |
|----|-----|--------|-------|--------|
| R7-8 | — | `internal/agent/testdata/claude/**` (8 files) + `testdata/opencode/{session-list.json,session-list-malformed.json,session-list-empty}` | Repo-wide grep of all `*.go`: only `testdata/opencode/events.sse` is read (opencode_monitor_test.go:30). Orphaned by `ef9636c` (ListSessions removal) + token-subsystem removal. | Delete (keep `events.sse`). |
| R7-9 | — | `--perch-glass-sheen` (themes.css, all 9 theme blocks) | Zero consumers in any `.svelte`/`.css`/`.ts`. | Remove from all 9 themes. |

### Config centralization / magic numbers (user-explicit requirement)

| ID | Sev | File:line | Literal | Fix |
|----|-----|-----------|---------|-----|
| R7-10 | LOW | `internal/hooklistener/listener.go:53` | `"127.0.0.1:0"` | Add `const loopbackHost = "127.0.0.1"`, use `loopbackHost+":0"`. |
| R7-11 | LOW | `internal/agent/opencode_monitor.go:190` | `"127.0.0.1:0"` (bare, despite `loopbackHost` defined at :48) | Use `loopbackHost+":0"`. |
| R7-12 | LOW | `frontend/src/lib/Sidebar.svelte:210,215,227` | pulse durations `1s`/`1.6s`, `opacity: 0.45` | Add `--perch-dur-attn-approval`, `--perch-dur-attn-input`, `--perch-attn-opacity-min` tokens. |
| R7-13 | LOW | `NewSessionDialog.svelte:57,58,73`; `App.svelte:711,717` | `"claude"`/`"opencode"` string literals | Add `AGENT_CLAUDE`/`AGENT_OPENCODE` to constants.ts; use them. |
| R7-14 | LOW | `frontend/src/lib/actions.ts:28` | fallback `380` (mirrors `--perch-dur-countup`) | Add `COUNTUP_FALLBACK_MS` const in constants.ts; import. |

### Docs / diagrams (drift — user asked to update)

| ID | File:line | Drift | Source of truth |
|----|-----------|-------|-----------------|
| R7-15 | `ARCHITECTURE.md:154` | `fs:changed` payload listed `—`; code emits `{workspaceId, path}` (app.go:550) | Code |
| R7-16 | `ARCHITECTURE.md:87-101` | bound-method table omits `SetWindowFocus` (app.go:211), `DiscoverRepos` (app.go:1221) | Code |
| R7-17 | `ARCHITECTURE.md:303-309` | registry table omits `model` field | Code |
| R7-18 | `docs/diagrams/discovery-state.mmd:14` | registry node omits `model` field | Code |
| R7-19 | `CONTRIBUTING.md:39-60` | make-target table omits `run`, `verify-all` (Makefile:47,108) | Code |
| R7-20 | `CHANGELOG.md` Round 6 | no `#### Removed` recording the dropped closing ritual | Doc (record the drop) |

---

## Reconciliation — findings surfaced but not in R7-1..R7-20 (each given a disposition)

- **8 "unexport candidate" Go symbols** (`discover.DefaultMaxDepth`/`DefaultPrune`, `notify.RunFunc`, `doctor.ParseToolVersions`, `fs.ShouldExclude`/`RevealRunner`/`SetRevealRunner`, `proc.ExitCode`): **NOT dead, NOT actioned.** The dead-code agent's "zero prod callers" was a false positive (grep conflated "not used cross-package" with "dead"). Verified: `DefaultMaxDepth` (discover.go:69), `DefaultPrune` (74), `ParseToolVersions` (227), `ShouldExclude` (218/241), `ExitCode` (207) all have production callers. And 5 of the 8 (`RunFunc`, `ShouldExclude`, `RevealRunner`, `SetRevealRunner`, `ExitCode`) are referenced by **black-box** `*_test` packages (`notify_test`, `fs_test`, `proc_test`) or are injection seams (the project's established exported-seam pattern, already covered by the 2026-06-03 audit's "test seams kept by design" exclusion) — unexporting them would break the build. `ParseToolVersions`/`DefaultMaxDepth`/`DefaultPrune` are package-internal but legitimately exported defaults/helpers; unexporting is cosmetic-only and elective. None warrant action.
- **status-review-pill untested at App level** (engagement agent): **CLOSED** — added an App.test.ts assertion (Alpha's mock diffStat returns 2 files → pill `aria-label="2 files to review"`). Round-6 logic, unit-testable in jsdom.
- **Terminal.focus() untested** (engagement agent): **CLOSED** — added a Terminal.test.ts case asserting the exported `focus()` forwards to the xterm instance (the awaiting-input auto-focus mechanism).
- **`OpenShell` uses `context.Background()`** (structure agent, LOW): **NOT actioned.** Works correctly — `Bridge.Close()` is the sole lifecycle owner for shell panes and sends SIGKILL explicitly. Tying a cancellable context would add complexity for no functional gain; recorded, not changed.
- `focusAwaitingInput`/`emphasizeInput` integration path, all glass/feel/visual: **manual-smoke-only** (jsdom/WebKit ceiling) — not unit-testable, flagged in smoke-checklist, not findings.

## Deliberate non-actions (decided, with rationale)

- **`--perch-fs-h1/h2`, `--perch-lh-h1/h2`, `--perch-sp-5..8`** (flagged "unused" by dead-code sweep): **KEPT.** These are the documented design-token scales (spec §7.4 promises the type scale incl. headings 24/20 and the 8pt grid). A scale is intentionally complete; removing mid-scale steps creates spec drift and an inconsistent system. Parity agent independently cites them as fulfilling §7.4.
- **`app/app.go` 1294-line file split** (structure agent, MED): **NOT actioned.** 1294 lines for the central Wails IPC binding is a defensible "one file = the bound API surface" convention, not an egregious god-file. An elective same-package reorg has zero functional benefit and its diff-noise would bury the one HIGH bug (R7-1) in review right before manual smoke. "Properly structured" = fix what's messy, not refactor working code for its own sake. If wanted, it's a separate clearly-scoped PR. (Advisor-affirmed.)
- **R7-1 double-notification check (cleared):** verified `onAgentEvent` (App.svelte:185-195) only updates state + shows the approval card; the hub is fed exclusively by `onNotify` (197-201) ← dispatchNotify's `notify` event. So R7-1 produces exactly one hub entry, not two.
- **`TestFakeNotifierRecordsCalls`** (tautological): real dbus/runner path is already covered by `TestRunnerSeamFallback`; the fake-self-test is redundant but harmless documentation of the double. Low value to remove. Left.
- **All `Tokens`/`Cost`/`TailTranscript`/`perch setup`/`internal/worktree`/closing-ritual**: removals verified CLEAN (go vet + tsc clean, zero survivors). Not re-litigated.
- **Manual-smoke-only**: all glass frost appearance, animation feel, real-agent/WebKit/D-Bus/OnFileDrop behavior. The fixes above remove static blockers; visual confirmation remains a smoke item.

---

## Execution log

**Batch A — Go correctness + centralization + cleanup (gate GREEN, `make test-all` all gates passed; 61 e2e).**
- `d590b18` fix(notify): R7-1 (approval Kind) + R7-3 (settings-read error) + R7-4 (dbus probe leak). R7-1 verified failing-before-fix; 5 tests that encoded the buggy `Kind:"state"` approval contract moved to `Kind:"approval"`. Double-notification ruled out (hub fed only by onNotify).
- `921a86d` refactor(go): R7-10 + R7-11 (loopbackHost const).
- `afe5aa0` test: R7-8 (deleted orphaned agent testdata; only events.sse retained).

**Batch B — frontend correctness + centralization (gate GREEN; 61 e2e).**
- `3ec83a6` fix(ui): R7-2 (hub glass defeat), R7-5 (diff error state +test), R7-6 (undo-toast motion guard), R7-7 (glass-off shadow → --perch-shadow-float), R7-9 (dead --perch-glass-sheen removed), R7-12 (attn-pulse tokens), R7-13 (AGENT_* consts), R7-14 (COUNTUP_FALLBACK_MS).

**Batch C — docs/diagram drift (no code; gate re-run as final whole-round confirmation).**
- R7-15..R7-20 applied: ARCHITECTURE (fs:changed payload, SetWindowFocus/DiscoverRepos rows, registry `model` field), discovery-state.mmd (`model`), CONTRIBUTING (`run`/`verify-all` targets), CHANGELOG (Round-6 closing-ritual Removed record + a Round-7 entry).

## Outcome

All 20 actionable findings resolved (R7-1..R7-20); deliberate non-actions recorded above with rationale. `make test-all` GREEN after each batch. The one HIGH logic bug (R7-1) is verified failing-before-fix and isolated in its own commit. All feel/visual/real-agent behavior remains manual-smoke-only (the fixes remove static blockers; they do not substitute for smoke). NOT pushed; merge to main is user-only.
