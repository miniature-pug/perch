# Spec-vs-Code Audit — 2026-06-03

**Branch:** `feat/perch-v1`
**Date:** 2026-06-03
**Spec:** `docs/superpowers/specs/2026-06-02-perch-cockpit-design.md`

---

## Remediation Status (2026-06-04)

Every finding below was re-verified against current code (verify-before-fix), then
fixed, dismissed as stale, or deferred with a recorded reason. Security fixes each
carry a fail-before-fix test. Commits on `feat/perch-v1`:

| Commit | Findings resolved |
|---|---|
| `966c51d` | C-1 (launch newline) |
| `e23337b` | C-2 + H-2, H-3, H-4, M-10 (opencode rewrite against verified v1.15.12 API) |
| `f0399af` | M-7 (opencode running/idle) |
| `2dbd218` | M-3, M-4, M-12, M-13, L-4, L-5, L-7, L-8, L-9, L-10, L-11, L-12, L-20 (security + reliability) |
| `52ee818` | H-1/L-32, H-6/M-6, M-5, M-29 (token meter, StateDone, dead Notification case) |
| `8339d0d` | H-7, H-8, H-9, H-10, H-11, H-12, M-8/N-25, M-15, M-16, M-17, M-18(token), M-19, M-20, L-13, L-14, L-21, L-22, N-8, N-10, N-23, N-24 |
| `aee0ad5` | M-1, M-2, M-14, M-21, M-22, M-24(GroupByDirectory), M-25/L-30, M-28, L-24, L-25, L-27, L-28, L-29, N-11, N-12, N-13, N-14, N-15, N-16, N-19, N-20 |
| `ef610f6` | M-18(adoption), N-7, L-1, L-2, L-3, N-4, N-5, L-18(documented) |

**Stale / not-a-bug (no change):** M-9 (no `permission.replied` in real API), L-15
(New-session guard already present), L-34 (zero `<svelte:element>` uses), N-1, N-2,
N-3 (confirmed-green bindings), N-9 (Unicode glyphs are an intentional choice).

**Already-correct / pre-existing (verified, no change):** H-13 and M-30 — both are
documented in `docs/superpowers/smoke-checklist.md` / `CONTRIBUTING.md` / `Makefile`;
the real-WebKit + real-agent end-to-end coverage is a manual smoke gate by design.

**Kept by design (required interface / intentional stub):** M-24 `Adapter.ListSessions`,
L-17 `ResumeArgs`, L-31 `ForkInto`, N-21 `ErrForkUnsupported` (all part of the frozen
`Adapter` interface); L-33 `NewOpts.Prompt/SessionID/Agent` (read by `NewArgs`,
forward-design — only `Model` is set in the cockpit path).

**Accepted non-fix (recorded rationale):**
- M-26 — `notify.New()` dbus path needs a live D-Bus session; CI-skip is acceptable, the runner seam is covered.
- M-27 — `events.sse` is hand-authored but pinned to the verified v1.15.12 wire contract (the rewrite fixed the envelope); a recorded capture requires a real binary (manual-smoke gate).
- L-6 — single-level `.gitignore` awareness is a documented limitation.
- L-16 — opencode heartbeat→watchdog not added; the reconnect loop already recovers dropped streams.
- L-19 — always-rule revocation already persists through the backend `SaveSettings`; a dedicated `DeleteAlwaysRule` is cosmetic on a localhost-only app.
- L-23 / N-17 / N-18 — `FakeMonitor`/`FakeNotifier`/`*WithServer` test doubles live in production package source but are excluded from the production binary by dead-code elimination; relocation to a dedicated `internal/agenttest` package is deferred as higher-risk/low-value near release.
- N-6 — `maybeAutoApprove` reads settings from disk per request; not cached (approval frequency is low and a cache would complicate the `settingsMu` invariant).

**Resolved after audit:**
- **H-5 / M-11** — `resources/perch-status.ts` removed; `Opencode.InstallStatusHook` replaced with a no-op; `opencodePluginOk` check removed from doctor. opencode status flows via SSE through `OpencodeMonitor` (unaffected).
- **M-23 / L-26 / N-22** — `internal/worktree` deleted (zero production importers confirmed); dead `config.Config` fields (`SortOrder`, `RefreshMs`, `Agent`, `StartupCommand`, `Theme`, `BaseBranch`, `WorktreeDir`, `Files`, `PostCreate`, `PreRemove`, `Wildcards`) plus `config.Validate` and `config.AgentBinary`/`agentBins` removed.
- **`internal/worktree`** — package deleted.

### Round-2 review (2026-06-04)

| Commit | Changes |
|---|---|
| `fcfc4f7` | Go magic-number elimination + XDG single-source refactor |
| `8df23c3` | Frontend constants centralization + CSS token additions |
| `12789e2` | Four spec cut-corners: diffstat counts, sidebar collapse, attach focus, opencode model field |

**Config-centralization pass (fcfc4f7 / 8df23c3):** All Go tuning values are now named package-level `const`s (pty dimensions, timeouts, buffer sizes, file modes, dbus addresses, token sizes, frecency multipliers, event prefixes). Raw `"claude"`/`"opencode"` literals replaced with `model.ToolClaude`/`model.ToolOpencode`. XDG config-dir resolution previously cloned in `internal/config` and `internal/registry` is now single-sourced in `registry.DefaultConfigDir()`; `config.DefaultGlobalPath()` delegates to it; the app-dir name `"perch"` is defined exactly once (`registry.appName`). Frontend: `frontend/src/lib/constants.ts` is the single home for all FE tuning constants; Wails event names are `EVT_*` constants in `wails.ts`. CSS token additions: `--perch-shadow-float`, `--perch-scrim`, the full `--perch-z-*` stacking scale, `--perch-fs-shell`/`--perch-lh-shell`.

**z-index 300/300 collision (8df23c3):** The prior command-palette/undo-toast z-index tie is resolved by the new named scale: `--perch-z-undo-toast: 300`, `--perch-z-command-palette: 310`.

**SettingsPanel wrong-hex fallbacks (8df23c3):** Dead/incorrect hardcoded hex colour values in `SettingsPanel` removed; all colour references go through CSS tokens.

**Four spec cut-corners now implemented (12789e2):**
- **Diffstat counts §5.3** — sidebar rows + status line display `+N −N` from `DiffStat`, refreshed per-workspace on `fs:changed`.
- **Sidebar collapse §7.2** — `Ctrl-b` + toggle rail; persisted via `layout.collapsed["sidebar"]`, same pattern as shell drawer.
- **`perch attach` focus §6.5** — `SingleInstanceLock` in `app/options.go`; second launch → `onSecondInstance` → raises window + emits `"workspace:attach" {query}`; frontend routes by exact `worktreePath` then case-insensitive substring. Second process exits non-zero on Linux (expected).
- **opencode model field §6.3** — `NewSessionDialog` hides the model input for `agent=opencode` and shows "Selected in the opencode TUI".

**Unchanged / not affected this round:**
- Spike-4 (`DisableWebViewDrop` + `preventDefault` Wails `OnFileDrop` #3686 mitigation) — confirmed present in `app/options.go:17,44` and `DragDrop.svelte:17,39`; remains a documented accepted deferral exercised in the manual WebKit smoke. Status unchanged.

---

## Methodology

A 20-agent fan-out audit compared the `feat/perch-v1` codebase against the cockpit design spec across 10 dimensions. Each dimension ran as a dedicated Review agent (static-read, traced-binding, or ran-test verification methods as appropriate) followed by a separate Completeness-Critic agent that checked for missed checks, under-verified claims, and surfaced additional findings. All findings were scoped against §4 (confirmed-deletions / non-goals) to avoid flagging intentionally removed code as regressions. Evidence strings preserve exact file:line citations. Severities follow the ordering: critical > high > medium > low > nit.

---

## Summary Table

| Dimension | Critical | High | Medium | Low | Nit | Critic Verdict |
|---|---|---|---|---|---|---|
| binding-drift | 0 | 0 | 2 | 3 | 5 | Gaps: `StateDone` never emitted; mock Caps all-true vs Go zero-value; `tool` kind dead literal |
| backend-pty-fs-git | 0 | 0 | 1 | 5 | 1 | Gaps: `CopyPath` missing path validation; `Hunks()` blind to staged-only files |
| backend-claude | 2 | 1 | 4 | 2 | 0 | Gaps: tool-start/end events absent; SSE reconnect missing; StateIdle produces no notification |
| backend-opencode | 2 | 3 | 4 | 4 | 1 | Gaps: SSE envelope likely nested {type,properties}; perch-status.ts plugin silently broken |
| app-security-approvals | 0 | 0 | 2 | 5 | 2 | Gaps: TOCTOU race in Approve(); truncated input breaks exact-match guarantee; CopyPath unvalidated |
| frontend-wiring | 0 | 2 | 5 | 4 | 0 | Gaps: font setting dead; gt/gT missing; ambient notifications no auto-dismiss |
| frontend-editor-design-keymap | 0 | 4 | 2 | 2 | 1 | Gaps: CM6 syntax highlighting absent; click-to-enter TERMINAL unwired |
| dead-code | 0 | 0 | 3 | 7 | 5 | Gaps: `Adapter.ListSessions` 0 production callers; `TailTranscript` never called in production |
| deletions-deps-config | 0 | 0 | 1 | 3 | 7 | Gaps: 4th Svelte CVE not assessed; tmux pin guard in install.sh silently empty |
| test-quality-deferrals | 1 | 2 | 4 | 4 | 0 | Gaps: App.test.ts unread; `Event.Cost` never asserted for opencode; e2e build-tag gate not surfaced |

---

## Critical Findings

### C-1: `launchCmd` written to PTY without trailing newline — agent binary never auto-executes
- **Dimension:** backend-claude / backend-opencode
- **SpecRef:** §6.2
- **Category:** logic-bug
- **Evidence:** `app/app.go:507: br.Write([]byte(launchCmd))` — no `\n` appended. `claude_monitor.go:183` returns `strings.Join(...)` without newline; `opencode_monitor.go:50` returns `fmt.Sprintf(...)` without newline. A login shell PTY requires `\r` or `\n` to execute a line; without it the text appears in the shell prompt but the agent is never started. Tests mask this: `app_test.go:428` injects `"claude --resume abc\n"` (with newline) via `SetLaunchCmd`, and the e2e test uses a fake bridge that ignores writes.
- **VerificationMethod:** static-read
- **Recommendation:** Append `"\n"` to launchCmd in `app.go` before writing, or have each `Monitor.Prepare` append it. Fix and add a test that uses the real `ClaudeMonitor.Prepare` return value and asserts a trailing newline.

### C-2: `OpencodeMonitor` production instance has empty `serverURL` and `password` — SSE and Approve are dead
- **Dimension:** backend-claude / backend-opencode / test-quality-deferrals
- **SpecRef:** §6.3
- **Category:** wiring-drift
- **Evidence:** `internal/agent/monitor.go:83: return newOpencodeMonitor(adapter), nil`. `internal/agent/opencode_monitor.go:27: func newOpencodeMonitor(a Adapter) *OpencodeMonitor { return &OpencodeMonitor{...} }` — `serverURL` and `password` are zero-string. `Start()` then issues `http.Get("" + "/event")` which fails on invalid URL; `Approve()` POSTs to `""+"/permission"` with `Bearer ""`. The only working constructor `NewOpencodeMonitorWithServer` is used exclusively in tests (no production caller). This means the entire opencode SSE channel — events, approvals, token/cost — silently fails at runtime.
- **VerificationMethod:** traced-binding
- **Recommendation:** Generate a random password in `newOpencodeMonitor` (or in `Prepare`), embed it in the launch command, and propagate `serverURL` from opencode's actual serve address. The serve address is only known after `opencode serve` starts; `Prepare` must either use a fixed port or read it from opencode's stdout/env.

---

## High Findings

### H-1: `ClaudeMonitor.Capabilities()` advertises `Tokens:true` but `TailTranscript` is never called — token/cost meter always empty for Claude
- **Dimension:** backend-claude
- **SpecRef:** §6.2, §10 spike #2
- **Category:** half-done
- **Evidence:** `internal/agent/claude_monitor.go:37: Capabilities()` returns `Caps{Approvals: true, Attention: true, Tokens: true}`. `TailTranscript` (`claude_monitor.go:57–95`) exists and is tested (`claude_monitor_test.go:150`), but `grep -rn TailTranscript` across all non-test Go files yields zero results — it is never called in production. The `SessionStart` hook delivers `transcript_path` in `he.TranscriptPath` (`hooklistener/listener.go:24`) but `translateAndEmit` (`claude_monitor.go:100–101`) ignores it entirely. No usage event ever reaches the frontend for Claude sessions.
- **VerificationMethod:** static-read
- **Recommendation:** Wire `TailTranscript` in `translateAndEmit`'s `SessionStart` case: when `he.TranscriptPath != ""`, call `go m.TailTranscript(ctx, he.TranscriptPath)`. Until then, `Capabilities` should return `Tokens: false` to avoid misleading the UI.

### H-2: No SSE reconnect loop and no `GET /question` reconcile in `OpencodeMonitor` — connection drop silently terminates SSE forever
- **Dimension:** backend-opencode / backend-claude
- **SpecRef:** §6.3
- **Category:** missing
- **Evidence:** Spec §6.3 line 108: `"Reconnect by re-opening + reconciling via GET /question and the SQLite store."` The `Start()` goroutine in `opencode_monitor.go:55-76` runs a single scan loop; when the scanner finishes (server closes, network drop, context cancel) the goroutine returns with no retry, backoff, or call to `GET /question`. `grep -rn 'reconnect|/question|backoff|retry' internal/agent/` returns zero matches outside test files.
- **VerificationMethod:** static-read
- **Recommendation:** Wrap the SSE scan loop in an exponential-backoff retry (e.g., 1s/2s/4s capped at 30s), and on each reconnect call `GET /question` to retrieve any permission requests that fired while disconnected, re-emitting them as approval events before resuming the stream.

### H-3: `POST /permission` endpoint shape contradicts itself across plan sections — unverified against real opencode server
- **Dimension:** backend-opencode
- **SpecRef:** §6.3
- **Category:** logic-bug
- **Evidence:** Implementation (`opencode_monitor.go:148-158`) posts to `/permission` with body `{"permissionId": reqID, "decision": decision}` — body-based ID routing. Spike README (`docs/superpowers/spikes/README.md:81,67`) says `'POST /permission/{id}/respond'` with body `{"decision": ...}` — path-based ID routing. Plan line 1555 also uses the path-based form. The plan's own line 5383 matches the implementation's body-based form. Spike 3 result file `docs/superpowers/spikes/3-opencode-sse-approval.md` does not exist (spike never run), so the actual opencode server API is unverified. If the real API requires path-based routing, every approval call silently fails (http.Do returns 200 body.Close regardless of status, and HTTP 4xx/5xx is not checked at `opencode_monitor.go:158-159`).
- **VerificationMethod:** static-read
- **Recommendation:** Run spike 3 against a real opencode binary, record the actual endpoint shape and response codes, then align the implementation. Also add HTTP status checking in `Approve`: return an error if `resp.StatusCode >= 400`.

### H-4: SSE fixture is synthetic, not a recorded capture — real opencode frame field names unverified; likely nested envelope mismatch
- **Dimension:** backend-opencode
- **SpecRef:** §6.3, §10 spike #3
- **Category:** test-tautology
- **Evidence:** `testdata/opencode/events.sse` was authored to match the implementation's `sseFrame` struct (`sessionId`, `permissionId`, `step.tokens.input/output`, `step.cost`, `error` fields all match the struct tags in `opencode_monitor.go:78-92`). Spike 3 was never run — no result file exists. Internal corroboration from two sources (`resources/perch-status.ts:38–40` reads `event?.properties ?? {}` and `props.sessionID`; plan spike harness lines 1527–1536 accesses `event.properties.id`) strongly suggests the real envelope is nested `{type, properties:{…}}`. The monitor's flat `sseFrame` (`opencode_monitor.go:78–92`) would silently drop all events if the shape is wrong.
- **VerificationMethod:** static-read
- **Recommendation:** Run spike 3, capture 2–3 real SSE frames directly from `opencode serve`, diff field names against `sseFrame` struct tags, and replace or augment `events.sse` with the captured output. Pay particular attention to whether the real stream uses a nested `{type,properties}` envelope vs. the current all-JSON-body approach.

### H-5: `perch-status.ts` plugin installed but silently broken — calls removed `perch status set` CLI command
- **Dimension:** backend-opencode
- **SpecRef:** §6.3, §6.5
- **Category:** wiring-drift
- **Evidence:** `resources/perch-status.ts:14` shells `perch status set ${state}` on every event, but the `status` verb was removed from the CLI (`cmd/perch/main_test.go:310` asserts exit 2 for `'status set working'`; no dispatch in `main.go`). The plugin catches the error silently. Combined with the SSE wiring drift (H-2/C-2), opencode state reporting reaches perch through zero paths. The `claude.go` hooks (`claude.go:198–201`) use the same command string but via a hook listener, not this CLI path — so the Claude path is unaffected.
- **VerificationMethod:** static-read
- **Recommendation:** Either restore the `status` CLI verb routing to the registry+GUI path, or replace the shell-command approach with direct SSE-based signaling through the monitor. The plugin currently does nothing.

### H-6: `StateDone` never emitted by any monitor — spec §8 ambient turn-done notification unreachable
- **Dimension:** binding-drift / backend-claude
- **SpecRef:** §8 line 157; §6.4; `app/app.go:620`
- **Category:** logic-bug
- **Evidence:** `app/app.go:620: case evt.Kind == "state" && evt.State == agent.StateDone` triggers the `'ambient / Turn complete / toast 5–7s'` path per §8. But no monitor ever emits `StateDone`: `ClaudeMonitor.translateAndEmit` maps `Stop → StateIdle` (`claude_monitor.go:103`); `OpencodeMonitor.translateSSE` maps `step.ended → Kind:"usage"` only (`opencode_monitor.go:106–110`), never `StateIdle` or `StateDone`. `app_e2e_test.go:304–306` explicitly notes: `'StateDone appears only in dispatchNotify as a consumer enum; the monitor never emits it.'`
- **VerificationMethod:** static-read
- **Recommendation:** Either rename `dispatchNotify`'s `StateDone` case to handle `StateIdle` (which is what `Stop` actually produces), or emit `StateDone` from `translateAndEmit` on `Stop` events. For opencode, add a `StateIdle` emission after `step.ended` to drive the `'turn complete'` notification.

### H-7: Stage view-switcher buttons are completely unstyled — default browser chrome
- **Dimension:** frontend-wiring / frontend-editor-design-keymap
- **SpecRef:** §7.3, §7.5
- **Category:** design
- **Evidence:** `frontend/src/lib/Stage.svelte:25` — `<button aria-pressed={view === s.id} onclick={() => onView(s.id)}>{s.label}</button>`. The `<style>` block at `Stage.svelte:39–48` has no rule targeting `.stage-bar button` or `button[aria-pressed]`. The `stage-bar` uses `var(--perch-surface)` background, but the buttons inside it carry no color, border-radius, font, or padding tokens — they render with default browser chrome (white fill, system border, system font).
- **VerificationMethod:** static-read
- **Recommendation:** Add `.stage-bar button` and `.stage-bar button[aria-pressed='true']` rules using `var(--perch-text)`, `var(--perch-bg)`, `var(--perch-accent)`, `var(--perch-border)`, `var(--perch-dur)`, `var(--perch-ease)`, plus `focus-visible` outline using `var(--perch-accent)`.

### H-8: `FileTree` 'Send to agent' context-menu item is a dead route
- **Dimension:** frontend-wiring
- **SpecRef:** §7.7
- **Category:** wiring-drift
- **Evidence:** `frontend/src/lib/FileTree.svelte:41` — `menuSend()` calls `onOpen('@mention:' + node.path)`. `App.svelte:534` wires `FileTree` as `onOpen={(p) => { codePath = p; }}`. The string `'@mention:/abs/path'` is stored into `codePath`; the `$effect` at `App.svelte:55-63` calls `readFile` on it (which fails), and `Editor` receives it as `path` (a bogus value). There is zero code path from this action to `writeToPty`.
- **VerificationMethod:** static-read
- **Recommendation:** `App.svelte` must distinguish `@mention:`-prefixed paths in its `onOpen` handler and route them to `sendToAgent()` instead of setting `codePath`, or `FileTree` needs a separate `onSendToAgent` callback prop.

### H-9: CM6 syntax highlighting absent — editor renders code without token colors
- **Dimension:** frontend-editor-design-keymap
- **SpecRef:** §7.3
- **Category:** missing
- **Evidence:** CM6 requires an explicit `syntaxHighlighting(defaultHighlightStyle)` (or a theme like `oneDark` that bundles it) in the extensions array. `Editor.svelte:148-222` lists all extensions: `changedLinesField`, `changedGutter`, `search`, `highlightSelectionMatches`, `keymap`, `bracketMatching`, `selectionListener`, `languageForPath()`, `EditorView.lineWrapping`, `EditorView.theme` — none is `syntaxHighlighting`. `@codemirror/theme-one-dark` is in `package.json` but not imported. `EditorView.theme()` covers CM chrome/UI classes only, not `.tok-keyword`/`.tok-string`/`.tok-comment` etc.
- **VerificationMethod:** static-read
- **Recommendation:** Import `syntaxHighlighting` and `defaultHighlightStyle` from `@codemirror/language` and add `syntaxHighlighting(defaultHighlightStyle)` to the extensions array in `Editor.svelte`. Or import `oneDark` from `@codemirror/theme-one-dark` which bundles it.

### H-10: TERMINAL mode 'click terminal' entry not wired
- **Dimension:** frontend-editor-design-keymap
- **SpecRef:** §7.6
- **Category:** missing
- **Evidence:** `mode.svelte.ts:7` defines `enterTerminal()`. `App.svelte:418` calls it only on `case 'i'`. No `onclick`, `onpointerdown`, or `onmousedown` handler anywhere in `Terminal.svelte`, `DragDrop.svelte`, or `App.svelte` calls `mode.enterTerminal()`. The plan's keymap table (plan line 328) is unambiguous: TERMINAL is entered via `'i in normal; click terminal'`. Only the keyboard path is implemented.
- **VerificationMethod:** static-read
- **Recommendation:** Add an `onclick` handler to the terminal zone that calls `mode.enterTerminal()` when `mode.current === 'normal'`.

### H-11: TERMINAL mode 'click-out' (click chrome) to return to NORMAL is not wired
- **Dimension:** frontend-editor-design-keymap
- **SpecRef:** §7.6
- **Category:** missing
- **Evidence:** `App.svelte:332` is the only call to `mode.leaveTerminal()` — it's inside the keyboard handler for the `Ctrl-\ Ctrl-n` sequence. No `onclick` or `mousedown` handler in `App.svelte` or `Terminal.svelte` triggers `leaveTerminal`. Clicking on the sidebar, menubar, shell drawer, or any chrome while in TERMINAL mode leaves the mode indicator showing TERMINAL but perch never calls `leaveTerminal()`.
- **VerificationMethod:** static-read
- **Recommendation:** Add an `onclick` handler to the app-root or non-terminal zones that calls `mode.leaveTerminal()` when `mode.current === 'terminal'`. Alternatively add a capture-phase `mousedown` listener on the app root that checks the clicked target is not inside a `.terminal` element.

### H-12: `--perch-text-dim` fails WCAG AA 4.5:1 in 8 of 9 themes
- **Dimension:** frontend-editor-design-keymap
- **SpecRef:** §7.4
- **Category:** design
- **Evidence:** Spec §7.4: `'WCAG 2.2 AA — text ≥ 4.5:1'`. `--perch-text-dim` is used for informational text (workspace branch in `Sidebar:213`, status labels `Sidebar:224`, notification metadata `NotificationHub:201`, caption text). Measured contrast (text-dim vs bg): tokyo-night `#565f89/#1a1b26` = 2.76:1 FAIL; catppuccin `#6c7086/#1e1e2e` = 3.36:1 FAIL; dracula `#6272a4/#282a36` = 3.03:1 FAIL; nord `#616e88/#2e3440` = 2.43:1 FAIL; rose-pine `#6e6a86/#191724` = 3.42:1 FAIL; one-dark `#5c6370/#282c34` = 2.32:1 FAIL; perch-cyan `#586069/#0d1117` = 2.97:1 FAIL; light `#78716c/#f5f0e8` = 4.23:1 FAIL. Only gruvbox passes at 5.30:1.
- **VerificationMethod:** static-read
- **Recommendation:** Lighten `--perch-text-dim` in each failing theme to achieve ≥4.5:1 against `--perch-bg`. Example fixes (approximate): tokyo-night → `#7a82a8`; catppuccin → `#9399b2`; dracula → `#9399b2`; nord → `#8490a8`; rose-pine → `#9893a8`; one-dark → `#8390a0`; perch-cyan → `#8b95a0`; light → `#5c5651`.

### H-13: E2E (Playwright) runs against Vite preview with mocked bindings — zero real binding coverage
- **Dimension:** test-quality-deferrals
- **SpecRef:** §10
- **Category:** test-tautology
- **Evidence:** `playwright.config.ts:14-19` uses `webServer: { command: 'npm run preview' }` pointing at `localhost:4173` — a Vite static build, not a Wails binary. `frontend/e2e/_mock.ts:73-263` injects `window.go.app.App` stubs via `addInitScript` before every test. All 61 e2e tests (including `approval.spec.ts`) interact with the injected mock, not with real Wails IPC. Both vitest and Playwright are blind to: (a) the real Wails method name/signature mapping; (b) CSS class names applied by the real WebKit2GTK renderer; (c) any Go panic or type mismatch in bound methods.
- **VerificationMethod:** static-read
- **Recommendation:** The e2e suite must run against the real Wails ELF binary (or a headless WebKit2GTK test harness) to close this gap. Until then, document this in the smoke checklist and mark it as requiring manual verification against the built ELF.

---

## Medium Findings

### M-1: `ResizePty` — Go takes `uint16` but TS/mock accept unbounded `number`
- **Dimension:** binding-drift
- **SpecRef:** §5.1
- **Category:** wiring-drift
- **Evidence:** `app/app.go:674: func (a *App) ResizePty(paneID string, cols, rows uint16) error`. `frontend/src/lib/wails.ts:75: ResizePty(paneId: string, cols: number, rows: number): Promise<void>`. Wails JSON bridge coerces JS number (float64) to uint16 silently — a caller passing `cols=80000` or `cols=-1` gets silent truncation/wrap to a nonsensical pty size. The mock (`e2e/_mock.ts:185-188`) accepts any number with no bounds check.
- **VerificationMethod:** static-read
- **Recommendation:** Add a frontend guard before calling `ResizePty`: clamp `cols` and `rows` to `[1, 65535]`. Alternatively document the uint16 constraint in the `wails.ts` interface comment.

### M-2: Mock `caps` all-true vs Go zero-value `Caps` on fresh workspace
- **Dimension:** binding-drift
- **SpecRef:** §5.2
- **Category:** wiring-drift
- **Evidence:** `_mock.ts:162-167` returns `caps:{approvals:true,attention:true,tokens:true}`; `app/app.go:384-393` returns `WorkspaceVM` with `Caps` unset (zero value = all false). `WORKSPACE_FIXTURE` (most e2e specs) also hardcodes `caps` all-true. E2e passes with caps-enabled UI affordances that production would not show until a monitor attaches.
- **VerificationMethod:** static-read
- **Recommendation:** Change the mock fixture to return `caps:{approvals:false,attention:false,tokens:false}` so e2e tests reflect actual production startup state.

### M-3: `gitApplyPatch` bypasses `proc.Runner` — apply path untestable via `FakeRunner`
- **Dimension:** backend-pty-fs-git
- **SpecRef:** §5.3
- **Category:** design
- **Evidence:** `internal/git/hunk.go:282-292`: `gitApplyPatch` creates `exec.CommandContext` directly, never routing through the `proc.Runner` parameter accepted by `applyHunkByIndex` (`line 216`). `StageHunk` and `DiscardHunk` pass a runner for the initial `git diff` (`line 220`) but then call `gitApplyPatch` which ignores it entirely for the `git apply` step. `stage_test.go` (lines 54, 88, 117, 163) confirms all staging tests require real git processes via `ExecRunner`.
- **VerificationMethod:** static-read
- **Recommendation:** Extend `proc.Runner` with a `RunStdin` (or `RunWithInput`) variant and route `gitApplyPatch` through it, or accept the integration-test-only constraint and document it explicitly.

### M-4: `Hunks()` blind to staged-only files — `StageHunk`/`DiscardHunk` unreachable for them
- **Dimension:** backend-pty-fs-git
- **SpecRef:** §5.3
- **Category:** half-done
- **Evidence:** `DiffStat` uses both `git diff --numstat` and `git diff --cached --numstat` (`hunk.go:76-82`), so staged-only files appear in the diff panel. `Hunks()` uses only `git diff` (no `--cached` flag, `hunk.go:137`), so staged-only files return zero hunks — making hunk-level stage/discard impossible for them.
- **VerificationMethod:** static-read
- **Recommendation:** Add a `--cached` variant to `Hunks()` (or a separate `StagedHunks()` call) so the frontend can retrieve hunks for staged-only files and offer discard/unstage operations on them.

### M-5: `'Notification'` hook event in `translateAndEmit` is dead code — never registered in `perchMonitorEvents`
- **Dimension:** backend-claude
- **SpecRef:** §6.2
- **Category:** dead-code
- **Evidence:** `internal/agent/claude_monitor.go:106: case "Notification": ev = Event{Kind: "state", State: StateIdle}` exists in `translateAndEmit`. But `perchMonitorEvents` (`claude_monitor.go:160`) = `["PreToolUse", "Stop", "StopFailure", "SessionStart"]` — `Notification` is absent. `mergeMonitorHooks` and `removeMonitorHooks` only iterate `perchMonitorEvents`, so no Notification hook is ever written to `.claude/settings.json`. Claude never fires this hook type to the monitor listener; the case is unreachable.
- **VerificationMethod:** static-read
- **Recommendation:** Either add `"Notification"` to `perchMonitorEvents` or remove the dead `Notification` case from `translateAndEmit`.

### M-6: `dispatchNotify` 'Turn complete' ambient notification never fires — `StateDone` and `StateIdle` both unhandled
- **Dimension:** backend-claude
- **SpecRef:** §6.4, §8
- **Category:** logic-bug
- **Evidence:** `app/app.go:620`: `case evt.Kind == "state" && evt.State == agent.StateDone` triggers ambient toast. `ClaudeMonitor.translateAndEmit` maps `Stop → StateIdle` (`claude_monitor.go:103`); `dispatchNotify` has no case for `StateIdle` — the `default: return` branch is silent. `StateIdle` also produces no routine notification per §8.
- **VerificationMethod:** static-read
- **Recommendation:** Add a `StateIdle` case to `dispatchNotify` that emits a routine-tier hub notification, and either rename or add a `StateDone` emitter in the monitors for the ambient toast.

### M-7: `OpencodeMonitor` has no idle state transition — sidebar stays 'running' after turn ends
- **Dimension:** backend-claude / backend-opencode
- **SpecRef:** §6.3, §6.4
- **Category:** missing
- **Evidence:** `opencode_monitor.go:106-110`: the `step.ended` case emits only `Kind:"usage"`; `m.state` is not updated. After an opencode turn completes the agent's sidebar status icon remains `'running'` (`StateRunning`, set at `step.started:105`) indefinitely. Spec §6.4 lists `'done'` and `'idle'` as valid displayed states.
- **VerificationMethod:** static-read
- **Recommendation:** Map `session.next.step.ended` to both a usage event AND a `StateIdle` state event.

### M-8: `tool-start/end` events completely absent from `ClaudeMonitor` and `OpencodeMonitor`
- **Dimension:** backend-claude (critic-surfaced)
- **SpecRef:** §6.1
- **Category:** missing
- **Evidence:** Spec §6.1 `AgentAdapter.Events()` docstring explicitly includes `'tool-start/end'`. `perchMonitorEvents` (`claude_monitor.go:160`) lists only `{PreToolUse, Stop, StopFailure, SessionStart}`; `PostToolUse` is absent; no `translateAndEmit` case for `tool-start/end` exists; `translateSSE` has no equivalent. Frontend `switch` statements with a `'tool'` case silently dead-branch.
- **VerificationMethod:** static-read
- **Recommendation:** Register `PostToolUse` in `perchMonitorEvents` and add a `"PostToolUse"` case in `translateAndEmit` that emits `Event{Kind:"tool"}`. Similarly add a tool-end event in the opencode SSE path.

### M-9: `permission.v2.replied` SSE event not handled — approval card stays open after remote decision
- **Dimension:** backend-opencode
- **SpecRef:** §6.3
- **Category:** missing
- **Evidence:** Spec §6.3 line 108 includes `'permission.v2.asked/replied'`. `translateSSE` (`opencode_monitor.go:100-126`) handles `permission.v2.asked` but has no case for `permission.v2.replied`; it falls to `default:return`. If the TUI or another client resolves the permission, perch's approval card never closes and the state stays `StateAwaitingApproval`.
- **VerificationMethod:** static-read
- **Recommendation:** Add `case "permission.v2.replied"` that emits `Event{Kind:"state", State:StateRunning}` so the frontend can dismiss the approval card.

### M-10: `Approve()` ignores HTTP response status — 4xx/5xx errors silently swallowed in opencode monitor
- **Dimension:** backend-opencode
- **SpecRef:** §6.3
- **Category:** logic-bug
- **Evidence:** `opencode_monitor.go:141-159`: the `Approve` method sends the POST, then calls `resp.Body.Close()` and returns only network-level errors. If the server returns 400 or 404 (permission ID unknown), the error is discarded. The approval silently appears to succeed while the tool call remains blocked.
- **VerificationMethod:** static-read
- **Recommendation:** After `Do()`, check `resp.StatusCode`: if `>= 400`, drain and close the body and return a descriptive error including the status code.

### M-11: `perch-status.ts` event names differ from spec §6.3 SSE event names
- **Dimension:** backend-opencode (critic-surfaced)
- **SpecRef:** §6.3
- **Category:** wiring-drift
- **Evidence:** `resources/perch-status.ts:42–65` handles `session.status`, `session.idle`, `permission.asked`, `permission.replied`. The spec §6.3 line 108 names `session.next.step.started/ended/failed`, `permission.v2.asked/replied`. The names differ (no `v2` prefix, `session.idle` vs `session.next.step.*`). This discrepancy — combined with spike 3 never having been run — leaves the actual opencode SSE event name schema unresolved.
- **VerificationMethod:** static-read
- **Recommendation:** Run spike 3 to determine the authoritative event names, then align `translateSSE` and `perch-status.ts` to the recorded output.

### M-12: TOCTOU race: `GetSettings`/`SaveSettings` in `Approve()` unguarded by mutex
- **Dimension:** app-security-approvals (critic-surfaced)
- **SpecRef:** §8
- **Category:** logic-bug
- **Evidence:** `app/app.go:912-915` drops the mutex before calling `GetSettings()`/`SaveSettings()` at lines `922-936`. Two concurrent `'always'` decisions from different workspace monitors can both read the same settings, both see 0 existing rules, both pass the dedup check, and the last `SaveSettings()` wins — silently dropping the first rule.
- **VerificationMethod:** static-read
- **Recommendation:** Hold the app mutex (or a separate settings mutex) across the `GetSettings → check duplicate → append → SaveSettings` sequence in `Approve()`.

### M-13: `AlwaysRule.Pattern` stores truncated input — 'exact match' fails for inputs > 4096 bytes
- **Dimension:** app-security-approvals (critic-surfaced)
- **SpecRef:** §8
- **Category:** logic-bug
- **Evidence:** `claude_monitor.go:114-115` caps `ApprovalReq.Input` at `MaxApprovalInputLen` (4096 bytes) before storing in the pending map. That truncated string becomes `AlwaysRule.Pattern` (`app.go:931`). `maybeAutoApprove` compares `r.Pattern == req.Input` where `req.Input` is also truncated. Two distinct tool inputs that share a 4096-byte prefix — e.g., a `WriteFile` call where content differs after byte 4096 — auto-approve the second call even though the user only approved the first.
- **VerificationMethod:** static-read
- **Recommendation:** Use a content hash (e.g., SHA-256) as `AlwaysRule.Pattern` rather than the truncated string, so two inputs with the same 4096-byte prefix but different tails hash differently. Alternatively raise `MaxApprovalInputLen` to a value that makes collisions impractical for real tool inputs.

### M-14: `FsNode` interface missing `modified` and `untracked` fields — git-status coloring in `FileTree` is always dead
- **Dimension:** frontend-wiring
- **SpecRef:** §5.3
- **Category:** wiring-drift
- **Evidence:** `frontend/src/lib/wails.ts:21` — `export interface FsNode { name: string; path: string; isDir: boolean; }`. `FileTree.svelte:74` uses `node.modified` and `node.untracked` for CSS classes. `internal/fs/fs.go:19-23` — `Node` struct has only `Name`, `Path`, `IsDir` fields. Both the TypeScript interface and the Go struct are missing these fields; the coloring is structurally unreachable.
- **VerificationMethod:** static-read
- **Recommendation:** Add `Modified bool` and `Untracked bool` to `fs.Node` (populate from `git status --porcelain` per §5.3) and add them to the `wails.ts` `FsNode` interface; or remove the dead CSS classes from `FileTree`.

### M-15: `sendToAgent` sends raw text without `@mention` prefix — contradicts spec §7.3
- **Dimension:** frontend-wiring
- **SpecRef:** §7.3
- **Category:** logic-bug
- **Evidence:** `App.svelte:453-457` — `sendToAgent(text)` encodes `text` as raw UTF-8 bytes and calls `writeToPty`. Spec §7.3 states `'Editor↔agent: select → @mention / Send to agent'`. `DragDrop.svelte:25` sends `@${p} ` for file paths but plain text for in-app drops. The spec intent implies the `@mention` format for selections.
- **VerificationMethod:** static-read
- **Recommendation:** Decide: if selections/hunks should arrive as `@mention`-prefixed text, add the prefix in `sendToAgent`. If raw text insertion is intended, document the deviation from the spec wording.

### M-16: `ShellDrawer` has its own internal `collapsed` state conflicting with `layout.collapsed['shell']`
- **Dimension:** frontend-wiring
- **SpecRef:** §7.2
- **Category:** logic-bug
- **Evidence:** `ShellDrawer.svelte:8` — `let collapsed = $state(false)`. `App.svelte:616` uses `style:display={layout.collapsed['shell'] ? 'none' : undefined}` on the shell-drawer-zone. `Ctrl-\`` at `App.svelte:403` toggles `layout.collapsed['shell']`. The two collapse mechanisms are completely independent: `Ctrl-\`` hides the zone but doesn't update `ShellDrawer`'s internal state; the in-drawer button toggles the internal flag but doesn't persist to the layout store.
- **VerificationMethod:** static-read
- **Recommendation:** Remove the local `collapsed` state from `ShellDrawer` and pass the collapsed state as a prop from `App.svelte`, or have the `ShellDrawer` button call `layout.setCollapsed`. Pick one authoritative source.

### M-17: Approval card only shown for active workspace — queued approvals for non-active workspaces invisible; `onDecision` cleanup uses wrong key
- **Dimension:** frontend-wiring
- **SpecRef:** §8
- **Category:** logic-bug
- **Evidence:** `App.svelte:652` — `{#if active && approvals[active.id]}` renders `ApprovalCard` only for the active workspace. The `onDecision` handler at `App.svelte:462` uses `activeId` to clear the approval: `const { [activeId]: _, ...rest } = approvals`. If a queued approval belongs to a non-active workspace, it can be resolved via Approve-all but the map cleanup uses the wrong key (`activeId` instead of the queued item's `wsId`).
- **VerificationMethod:** static-read
- **Recommendation:** The `onDecision` handler should key deletion by `reqId`-to-`wsId` lookup rather than hardcoding `activeId`.

### M-18: `--perch-border` fails WCAG AA 3:1 for border/icon visibility in all 9 themes
- **Dimension:** frontend-editor-design-keymap
- **SpecRef:** §7.4
- **Category:** design
- **Evidence:** Spec §7.4: `'borders/icons/focus-ring ≥ 3:1'`. Measured border vs bg: gruvbox `#504945/#282828` = 1.67:1; tokyo-night `#414868/#1a1b26` = 1.91:1; catppuccin `#45475a/#1e1e2e` = 1.80:1; dracula `#44475a/#282a36` = 1.56:1; nord `#434c5e/#2e3440` = 1.45:1; rose-pine `#403d52/#191724` = 1.69:1; one-dark `#3e4451/#282c34` = 1.43:1; perch-cyan `#30363d/#0d1117` = 1.55:1; light `#c7bfb2/#f5f0e8` = 1.61:1. All fail. Note: focus-ring uses `--perch-accent` (all themes ≥4.43:1 — pass); the border token is used for structural borders only.
- **VerificationMethod:** static-read
- **Recommendation:** Audit which uses of `--perch-border` are on interactive controls vs. decorative structural dividers. Bump `--perch-border` luminance ~40% in each theme for interactive uses, or rely on `--perch-accent` for interactive border/focus states.

### M-19: Font setting is a completely dead UI control
- **Dimension:** frontend-wiring (critic-surfaced)
- **SpecRef:** §7.7
- **Category:** half-done
- **Evidence:** `SettingsPanel.svelte:47` saves font via `settingsStore.setFont()` but `App.svelte` never reads `settings.font`. `ThemeProvider` accepts no font prop and no `data-font` CSS selector exists — the three font choices have no visual effect.
- **VerificationMethod:** static-read
- **Recommendation:** Read `settings.font` in `App.svelte` or `ThemeProvider` and apply it to the document root (e.g., `document.documentElement.setAttribute('data-font', settings.font)` or a CSS variable).

### M-20: `CommandPalette` 'recents first' ordering not implemented
- **Dimension:** frontend-wiring / frontend-editor-design-keymap (critic-surfaced)
- **SpecRef:** §7.7
- **Category:** missing
- **Evidence:** `CommandPalette.svelte:40-45` groups by `command.group` only, with no recent-use tracking or reordering. No localStorage/store tracks command invocation history anywhere in the codebase. Commands always appear in definition order within groups. Spec §7.7: `'fuzzy, prefix-grouped (Agent:/Pane:/File:/View:), recents first, inline keybindings'`.
- **VerificationMethod:** static-read
- **Recommendation:** Add a command-invocation history store (e.g., `Map<string, number>` for last-used timestamp) and sort matching commands by recency before rendering.

### M-21: `internal/match` package has 0 production importers — dead package
- **Dimension:** dead-code
- **SpecRef:** —
- **Category:** dead-code
- **Evidence:** `grep -rn '"github.com/Miniature-Pug/perch/internal/match"'` across non-test, non-vendor `.go` files returns 0 results. Only `internal/match/match_test.go` imports it. The single export `MatchAny` (`match.go:16`) has 0 production call sites.
- **VerificationMethod:** static-read
- **Recommendation:** Remove `internal/match` package. The `doublestar` vendor dependency may also become prunable.

### M-22: `status.Machine`, `status.Set`, `status.Deps` — dead in production
- **Dimension:** dead-code
- **SpecRef:** —
- **Category:** dead-code
- **Evidence:** `internal/status/status.go`: `Machine` (line 38), `NewMachine` (line 43), `Set` (line 21), `Deps` (line 18) are all exported. `grep` for these in non-test non-vendor `.go` files returns 0 results. The package is imported by `claude.go` only for its three string constants (`StateWorking/StateDone/StateWaiting` at lines 198–201). The entire state-machine logic was designed for the old tmux-status CLI path.
- **VerificationMethod:** static-read
- **Recommendation:** Extract only the three `StateX` constants into a smaller file or inline them in `claude.go`. Remove `Machine`, `NewMachine`, `sessionState`, `Set`, `Deps`, and all their methods (~130 lines of dead logic).

### M-23: `config.Config`: most fields loaded but never read in production
- **Dimension:** dead-code
- **SpecRef:** —
- **Category:** dead-code
- **Evidence:** `cmd/perch/gui.go:18-19` only reads `cfg.Roots` from `config.Load`. Fields `SortOrder`, `RefreshMs`, `Agent`, `StartupCommand`, `Theme`, `BaseBranch`, `WorktreeDir`, `Files`, `PostCreate`, `PreRemove`, `Wildcards`, and `AgentBinary()` are all populated but have 0 production read sites outside the orphaned worktree package and tests.
- **VerificationMethod:** static-read
- **Recommendation:** Either wire these config fields to the GUI or document them as forward-stubs. `config.Config.AgentBinary()` itself has 0 production call sites as well.

### M-24: `Adapter.ListSessions` — exported interface method with 0 production call sites
- **Dimension:** dead-code (critic-surfaced)
- **SpecRef:** §4
- **Category:** dead-code
- **Evidence:** `claude.go:543`, `opencode.go:193`, `adapter.go:35-40`. `GroupByDirectory` (`opencode.go:234`) depends on `ListSessions` but is itself 0-production-callers. Only test files call `ListSessions`.
- **VerificationMethod:** static-read
- **Recommendation:** Unexport or remove `ListSessions` from the `Adapter` interface if not needed by cockpit production paths.

### M-25: `install.sh` still installs tmux — removed architecture dependency
- **Dimension:** deletions-deps-config
- **SpecRef:** §4
- **Category:** config
- **Evidence:** `install.sh lines 213–275`: full Step 2 block (21 tmux references) installs tmux >= 3.2, checks a pin from `.tool-versions`. But `.tool-versions` has NO tmux entry. `doctor.go` runtime dep list (`lines 183–220`) has NO tmux entry. `README.md line 11` states `'There is no tmux'`. The cockpit replaced tmux with `creack/pty` directly.
- **VerificationMethod:** static-read
- **Recommendation:** Remove Step 2 (tmux install block, lines 213–275) from `install.sh`.

### M-26: `notify_test.go` tests only `FakeNotifier` self-recording — real D-Bus path has 0 automated test coverage
- **Dimension:** test-quality-deferrals
- **SpecRef:** §10 spike 5
- **Category:** test-tautology
- **Evidence:** `internal/notify/notify_test.go:9-18` (`TestFakeNotifierRecordsCalls`) asserts `f.Calls` grows when `FakeNotifier.Notify` is called — literally asserting that a struct appends to its own slice. The `New()` function at `notify.go:43` — which tries dbus first and falls back to runner — has 0 test coverage.
- **VerificationMethod:** static-read
- **Recommendation:** Add a test that exercises `notify.New()` with a mocked dbus, or confirms graceful degradation when dbus is unavailable.

### M-27: `opencode events.sse` fixture is hand-constructed — real opencode field names unverified
- **Dimension:** test-quality-deferrals
- **SpecRef:** §10 spike 3
- **Category:** test-tautology
- **Evidence:** `testdata/opencode/events.sse` contains 4 lines with synthetic values (`ses-1`, `perm-1`, `cost:0.0012`). No `'recorded from live opencode'` header comment. `TestOpencodeMonitorSSEParser` exercises the parser against hand-shaped data only — any undocumented field or envelope wrapping in real opencode output would not be caught.
- **VerificationMethod:** static-read
- **Recommendation:** Capture a real opencode SSE stream and replace `events.sse` with sanitized real output. At minimum add a comment distinguishing hand-crafted from recorded fixtures.

### M-28: `Event.Cost` never asserted in opencode SSE integration test
- **Dimension:** test-quality-deferrals (critic-surfaced)
- **SpecRef:** §6.3, §10
- **Category:** test-tautology
- **Evidence:** `TestOpencodeMonitorSSEParser` (`opencode_monitor_test.go:17`) reads `events.sse` which contains `cost:0.0012`. `translateSSE` populates `ev.Cost=f.Step.Cost` (`opencode_monitor.go:110`). The test at lines `62-63` only asserts `got[1].Tokens==140`; `got[1].Cost` is never read or compared. A zero-value Cost would pass silently.
- **VerificationMethod:** static-read
- **Recommendation:** Add `assert got[1].Cost == 0.0012` to `TestOpencodeMonitorSSEParser`.

### M-29: `TailTranscript` (Claude) emits no `Cost` — `ClaudeAdapter` token cost permanently zero
- **Dimension:** test-quality-deferrals (critic-surfaced)
- **SpecRef:** §6.2, §10 spike 2
- **Category:** half-done
- **Evidence:** `claude_monitor.go:88` emits `Event{Kind:'usage', Tokens: total}` with `Cost` omitted (zero). The transcript fixture (`transcript-usage.jsonl`) contains no cost field. Spec §6.2 marks token/cost as a validation spike; cost derivation from the Claude transcript is not present. No test asserts `Cost>0` for Claude usage events.
- **VerificationMethod:** static-read
- **Recommendation:** Document that Claude sessions provide tokens only (not cost) since the claude CLI JSONL format does not include cost fields. Update `Capabilities()` commentary and the TokenMeter UI to distinguish token-only vs token+cost modes.

### M-30: `app_e2e_test.go` build-tag gate not surfaced — integration test excluded from `go test ./...`
- **Dimension:** test-quality-deferrals (critic-surfaced)
- **SpecRef:** §10
- **Category:** test-tautology
- **Evidence:** `app/app_e2e_test.go line 1: //go:build integration`. The file is excluded from plain `go test ./...`. The reviewer described it as a completed integration test covering the full loop without noting this critical gate. The 61 passing tests the main thread reported are Playwright tests, not this Go headless loop.
- **VerificationMethod:** static-read
- **Recommendation:** Document in `CONTRIBUTING.md` / `Makefile` that `-tags integration` is required for the headless full-loop test. Add it to the CI gate or make checklist.

---

## Low Findings

### L-1: `agent:event` missing `sessionId` field in TypeScript `AgentEvent` interface
- **Dimension:** binding-drift
- **SpecRef:** —
- **Evidence:** `internal/agent/monitor.go:52: SessionID string \`json:"sessionId,omitempty"\`` is part of `agent.Event`. `frontend/src/lib/wails.ts:10-13`: `AgentEvent` interface has `workspaceId, kind, state?, tokens?, cost?, approval?, err?` — no `sessionId` field.
- **VerificationMethod:** static-read
- **Recommendation:** Add `sessionId?: string` to `AgentEvent` in `wails.ts`.

### L-2: `DiscoverRepos` `Head` field always empty-string in Go output; mock returns `'abc1234'`
- **Dimension:** binding-drift
- **SpecRef:** —
- **Evidence:** `app/app.go:1051-1053`: `Head` is zero value `''`. `e2e/_mock.ts:248`: `worktrees: [{ path: '/home/user/perch', branch: 'main', head: 'abc1234' }]`.
- **VerificationMethod:** static-read
- **Recommendation:** Change the mock fixture to `head: ''` for `DiscoverRepos` worktrees.

### L-3: Go `Event` struct missing `omitempty` on `State`/`Tokens`/`Cost` — TS optional contract broken
- **Dimension:** binding-drift (critic-surfaced)
- **SpecRef:** `internal/agent/monitor.go:45-47`
- **Evidence:** `State`, `Tokens`, `Cost` have no `omitempty`, so a usage event always sends `state:""`, and a state event always sends `tokens:0,cost:0.0`. TS declares `state?: AgentState` (optional), which implies `undefined`-when-absent; instead frontend receives `state:""` (a present empty string outside the union).
- **VerificationMethod:** static-read
- **Recommendation:** Add `omitempty` tags to `State`, `Tokens`, and `Cost` in `monitor.go:45-47`.

### L-4: PTY closer `f.Close()` before `Kill` creates narrow PID-reuse window
- **Dimension:** backend-pty-fs-git
- **SpecRef:** §5.1
- **Evidence:** `internal/pty/bridge.go:98-104`: `closer()` calls `f.Close()` first, which unblocks `pumpReader`, which calls `cmd.Wait()` concurrently. `Kill(-cmd.Process.Pid, SIGKILL)` executes immediately after `f.Close()` — after `Wait()` could theoretically reap the zombie and allow PID reuse. Kill could target a new unrelated process group in that narrow window.
- **VerificationMethod:** static-read
- **Recommendation:** Swap order: send SIGKILL first, then `f.Close()`.

### L-5: `WriteFile` validates parent dir, not the file path itself — symlink target bypass
- **Dimension:** backend-pty-fs-git
- **SpecRef:** §5.4
- **Evidence:** `app/app.go:833`: `WriteFile` calls `validateWorktreeUnderRoots(filepath.Dir(absPath), a.roots)` rather than validating `absPath` directly. `ReadFile` at `app/app.go:821` validates `absPath` directly via `EvalSymlinks`, creating an inconsistency.
- **VerificationMethod:** static-read
- **Recommendation:** In `WriteFile`, validate `absPath` directly (not just its `Dir`), accepting that for new files the path may not exist yet.

### L-6: Gitignore awareness is single-level only — nested `.gitignore` files ignored
- **Dimension:** backend-pty-fs-git
- **SpecRef:** §5.4
- **Evidence:** `internal/fs/fs.go:27-28` (ListDir) and `fs.go:129` (Watch): both load patterns only from `absDir/.gitignore` (top-level). Subdirectory `.gitignore` files are never consulted.
- **VerificationMethod:** static-read
- **Recommendation:** Document as a known limitation in `CLAUDE.md`/spec, or implement per-directory `.gitignore` loading to match real git behavior.

### L-7: `fsnotify` error events are silently dropped — watcher stalls undetected
- **Dimension:** backend-pty-fs-git
- **SpecRef:** §5.4
- **Evidence:** `internal/fs/fs.go:165-168`: the watcher loop receives from `fw.Errors` but discards every error with a blank case body. Real fsnotify errors (ENOSPC when inotify watch limit is exhausted, EBADF after close, etc.) are swallowed silently.
- **VerificationMethod:** static-read
- **Recommendation:** Log or surface fsnotify errors. On ENOSPC or EBADF, fire `onChange` with a sentinel path so the frontend can show a stale-indicator.

### L-8: `CopyPath` bound method has no path validation against configured roots
- **Dimension:** backend-pty-fs-git / app-security-approvals (critic-surfaced)
- **SpecRef:** §5.4, §9
- **Evidence:** `app/app.go:849-853`: `CopyPath` accepts `absPath` and writes it to the system clipboard with no `validateWorktreeUnderRoots` call. Every other fs-touching bound method has validation. A compromised or buggy frontend can call `CopyPath('/etc/shadow')` without error.
- **VerificationMethod:** static-read
- **Recommendation:** Add `validateWorktreeUnderRoots(absPath, a.roots)` as the first call in `CopyPath`.

### L-9: Non-`PreToolUse` hook events can be silently dropped under backpressure
- **Dimension:** backend-claude
- **SpecRef:** §6.2
- **Evidence:** `internal/hooklistener/listener.go:94–98`: non-`PreToolUse` events use `select { case l.events <- ev: default: }` — a non-blocking send that silently discards the event if the 64-slot buffer is full. `Stop`/`StopFailure`/`SessionStart` events can be lost, causing the sidebar state to freeze.
- **VerificationMethod:** static-read
- **Recommendation:** Use a blocking send (without `default:`) for `Stop`, `StopFailure`, and `SessionStart` events, or increase the buffer.

### L-10: `evt.SessionID` from hook SSE persisted to registry without charset validation — theoretical shell-injection vector
- **Dimension:** app-security-approvals
- **SpecRef:** §9
- **Evidence:** `app/app.go:551-555`: `evt.SessionID` is stored in `store.LastSessionID` without validation. On next `OpenWorkspace`, the session ID is written into a shell command that goes to the login-shell PTY (`app/app.go:506-508: br.Write([]byte(launchCmd))`). A sessionID containing shell metacharacters would be interpreted by the shell.
- **VerificationMethod:** traced-binding
- **Recommendation:** Apply `validateSessionID` (or equivalent allowlist) to `evt.SessionID` before storing to registry in the event-pump goroutine (`app/app.go ~line 552`).

### L-11: `OpenWorkspace`, `CloseWorkspace`, `RemoveWorkspace` do not validate the workspace `id` argument
- **Dimension:** app-security-approvals
- **SpecRef:** §9
- **Evidence:** `app/app.go:403-406: func (a *App) OpenWorkspace(id string)` immediately calls `a.store.Get(id)` without `validateSessionID`. `CloseWorkspace` (line 689) and `RemoveWorkspace` (line 717) follow the same pattern. `WriteToPty` (line 656), `ResizePty` (line 675), and `OpenShell` (line 726) all call `validateSessionID`.
- **VerificationMethod:** static-read
- **Recommendation:** Add `validateSessionID(id)` as the first call in `OpenWorkspace`, `CloseWorkspace`, and `RemoveWorkspace` for defense-in-depth consistency.

### L-12: Approval `pending` map entries never cleaned up on `CloseWorkspace` or shutdown
- **Dimension:** app-security-approvals
- **SpecRef:** §8
- **Evidence:** `app/app.go:914`: `delete(a.pending, reqID)` is the ONLY place pending entries are removed — inside `Approve()`. `CloseWorkspace` (lines 689–713) and shutdown (lines 146–165) neither clear `a.pending` for the closing workspace's `reqIDs`. Entries accumulate indefinitely across many workspace open/close cycles.
- **VerificationMethod:** static-read
- **Recommendation:** In `CloseWorkspace` (and shutdown), iterate `a.pending` and delete entries whose `workspaceID` suffix matches the closing workspace ID.

### L-13: `TokenMeter` `capsTokens` prop declared but unused — no hide-when-unsupported logic
- **Dimension:** frontend-wiring
- **SpecRef:** §6.4
- **Evidence:** `TokenMeter.svelte:3` — `capsTokens` is declared in the props interface but is never referenced in the template or script. `App.svelte:638` renders `<TokenMeter ... capsTokens={active.caps.tokens} />` unconditionally. Spec §6.1 says UI degrades per `Caps`; for agents where `caps.tokens === false` the meter should be hidden or show `'—'`.
- **VerificationMethod:** static-read
- **Recommendation:** Use `capsTokens` in `TokenMeter` to conditionally render `'—'` or hide the meter when tokens are not supported.

### L-14: `session:close` command does not clean up `approvals` or `fsVersion` frontend state
- **Dimension:** frontend-wiring
- **SpecRef:** §6.5
- **Evidence:** `App.svelte:285` — `session:close` calls `closeWorkspace`. After that, the `approvals` record (line 34) and `fsVersion` record (line 35) for that workspace are never cleaned up. The workspace remains in the `workspaces` list and the `ApprovalCard` continues to render stale approvals.
- **VerificationMethod:** static-read
- **Recommendation:** After `closeWorkspace` resolves, delete the entry from `approvals` and `fsVersion` keyed by the closed workspace id.

### L-15: Sidebar `New session` button passes a `MouseEvent` to `openNewSession` — wrong API contract
- **Dimension:** frontend-wiring
- **SpecRef:** §7.1
- **Evidence:** `Sidebar.svelte:78` — `<button ... onclick={onNew}>`. `App.svelte:496` wires it as `onNew={openNewSession}`. The event object is forwarded; `openNewSession` (`App.svelte:179`) has a defensive guard that catches this, but the caller is still wrong.
- **VerificationMethod:** static-read
- **Recommendation:** Change `Sidebar`'s `onNew` button to `onclick={() => onNew()}` so the event is never forwarded.

### L-16: `heartbeat` SSE event not handled — no keep-alive mechanism in opencode monitor
- **Dimension:** backend-opencode
- **SpecRef:** §6.3
- **Evidence:** Spec §6.3 line 108 lists `'heartbeat'` in the SSE event set. `translateSSE` (`opencode_monitor.go:100-126`) has no case for heartbeat; it falls to `default:return`. Combined with the absent reconnect loop, there is no mechanism to detect a stale-but-open SSE connection.
- **VerificationMethod:** static-read
- **Recommendation:** Add a heartbeat case (or use a read-deadline/idle-timer) to reset a watchdog timer.

### L-17: `Opencode.ResumeArgs` is dead code in the opencode monitor path
- **Dimension:** backend-opencode
- **SpecRef:** —
- **Evidence:** `Opencode.ResumeArgs` (`opencode.go:83-85`) returns `["--session", sessionID]`. `OpencodeMonitor.Prepare` (`opencode_monitor.go:43-51`) builds its own attach string with hardcoded `--session` rather than calling `adapter.ResumeArgs`. `grep -rn ResumeArgs` outside `internal/agent/` returns 0 results.
- **VerificationMethod:** traced-binding
- **Recommendation:** Either have `OpencodeMonitor.Prepare` call `o.adapter.(Opencode).ResumeArgs(resumeID)` for consistency, or document the intentional divergence.

### L-18: `--continue` resume path missing from `OpencodeMonitor.Prepare`
- **Dimension:** backend-opencode (critic-surfaced)
- **SpecRef:** §6.3
- **Evidence:** Spec §6.3 `'resume --session <id> / --continue'`. `OpencodeMonitor.Prepare` (`opencode_monitor.go:43–51`) only appends `--session <id>` when `resumeID` is non-empty; the `--continue` option is not implemented.
- **VerificationMethod:** static-read
- **Recommendation:** Add a `--continue` path for when no `resumeID` is known.

### L-19: `always-allow` rule revocation is frontend-only — no backend-authoritative delete method
- **Dimension:** app-security-approvals (critic-surfaced)
- **SpecRef:** §8
- **Evidence:** Revocation is frontend-only: `SettingsPanel.svelte:58-60` filters the array client-side and calls `SaveSettings` with the pruned list. There is no dedicated `DeleteAlwaysRule` bound method; a compromised frontend could omit the filter step.
- **VerificationMethod:** static-read
- **Recommendation:** Add a `DeleteAlwaysRule(agent, tool string) error` bound method so revocation goes through a validated backend call.

### L-20: `opencode_monitor.go` comment falsely asserts `resumeID` is validated upstream
- **Dimension:** app-security-approvals (critic-surfaced)
- **SpecRef:** §9
- **Evidence:** `opencode_monitor.go:47` comments `'resumeID charset is [A-Za-z0-9_-] (validated upstream), so plain concatenation is safe'`. This comment is false — `resumeID` comes from the registry's `LastSessionID` which is set from hooklistener JSON without charset validation (`app.go:552`).
- **VerificationMethod:** static-read
- **Recommendation:** Either add the advertised charset validation at `app.go:552`, or correct the comment to remove the false safety claim.

### L-21: `gt`/`gT` session tab-cycle keys not implemented
- **Dimension:** frontend-wiring / frontend-editor-design-keymap (critic-surfaced)
- **SpecRef:** §7.7, §7.6
- **Evidence:** The g-prefix block at `App.svelte:358-364` handles only `'d'` and `'e'`; pressing `'g'` then `'t'` or `'T'` silently cancels with no action.
- **VerificationMethod:** static-read
- **Recommendation:** In the `pendingG` block, add `'t'` (next view) and `'T'` (prev view) handlers cycling through `['agent','code','diff']`.

### L-22: Ambient (tier-2) notifications have no auto-dismiss timer
- **Dimension:** frontend-wiring (critic-surfaced)
- **SpecRef:** §8
- **Evidence:** Spec §8: `'ambient (turn done) → toast 5-7s'`. `NotificationHub` renders all items as a persistent list with no `setTimeout`-based removal; items only leave via `onDismiss` (markRead).
- **VerificationMethod:** static-read
- **Recommendation:** Add a `setTimeout`-based auto-dismiss (5–7 seconds) for tier-2 (ambient) notifications in `NotificationHub` or `notifications.svelte.ts`.

### L-23: `agent.FakeMonitor` + `ApproveCall` — test doubles in production package
- **Dimension:** dead-code
- **SpecRef:** —
- **Evidence:** `internal/agent/fake_monitor.go` exports `FakeMonitor`, `NewFakeMonitor`, `ApproveCall`, and several test-only helper methods, all in `package agent` (not a `_test.go` file), so they ship in the production binary.
- **VerificationMethod:** static-read
- **Recommendation:** Move `fake_monitor.go` to an `agent_test` helper package or a dedicated `internal/agent/agenttest` package.

### L-24: `agent.GroupByDirectory` — exported, 0 production call sites
- **Dimension:** dead-code
- **SpecRef:** —
- **Evidence:** `internal/agent/opencode.go:234` exports `GroupByDirectory`. `grep` in non-test non-vendor `.go` returns 0 results. Only `opencode_test.go:177` uses it.
- **VerificationMethod:** static-read
- **Recommendation:** Unexport or remove `GroupByDirectory`.

### L-25: `model.Project.IsGit` — written, never read in production
- **Dimension:** dead-code
- **SpecRef:** —
- **Evidence:** `internal/model/model.go:31` declares `IsGit bool`. `grep` for `.IsGit\b` returns only the one write site: `internal/discover/catalog.go:68`. No code ever reads the field.
- **VerificationMethod:** static-read
- **Recommendation:** Remove `IsGit` field and its set in `catalog.go:68`.

### L-26: `config.Validate()` — exported, never called in production
- **Dimension:** dead-code
- **SpecRef:** —
- **Evidence:** `internal/config/config.go:175` exports `Validate(repoRoot string) error`. `grep` for `.Validate(` in non-test non-vendor `.go` returns 0 results.
- **VerificationMethod:** static-read
- **Recommendation:** Unexport or remove.

### L-27: `wails.ts` `worktrees` export — no production importer
- **Dimension:** dead-code
- **SpecRef:** —
- **Evidence:** `frontend/src/lib/wails.ts:85: export const worktrees = (repo: string) => app().Worktrees(repo)`. `grep` in non-test `.svelte` and `.ts` files returns 0 import results.
- **VerificationMethod:** static-read
- **Recommendation:** Remove the `worktrees` export from `wails.ts`.

### L-28: Svelte 5.55.5 CVE 3 (GHSA-rcqx-6q8c-2c42, DOM clobbering) — residual surface in `Preview.svelte`
- **Dimension:** deletions-deps-config
- **SpecRef:** —
- **Evidence:** `svelte@5.55.5` matches GHSA-rcqx-6q8c-2c42 (DOM clobbering, affects CSR). `Preview.svelte:17` passes `DOMPurify.sanitize(markedOutput, {USE_PROFILES:{html:true}})` into `{@html html}`. `DOMPurify` with `USE_PROFILES` does not strip `id`/`name` attributes, which are the DOM clobbering vector. Risk is low (user's own files in a Wails desktop app), but unmitigated.
- **VerificationMethod:** static-read
- **Recommendation:** Upgrade Svelte to >=5.55.7 when available. Optionally add `FORBID_ATTR: ['id','name']` to the `DOMPurify.sanitize` call in `Preview.svelte:17`.

### L-29: `@playwright/test` pinned to a date-stamped alpha (`1.61.0-alpha-2026-06-03`)
- **Dimension:** deletions-deps-config
- **SpecRef:** —
- **Evidence:** `frontend/package.json devDependencies: "@playwright/test": "1.61.0-alpha-2026-06-03"`. Alpha/nightly builds may contain breaking changes between dates.
- **VerificationMethod:** static-read
- **Recommendation:** Pin to the latest stable Playwright release unless the alpha-specific features are intentional.

### L-30: `install.sh` tmux pin guard (lines 256–271) silently empty — `TMUX_PINNED` never set
- **Dimension:** deletions-deps-config (critic-surfaced)
- **SpecRef:** §4
- **Evidence:** `install.sh lines 256–271`: `TMUX_PINNED=$(tool_version tmux)` — since tmux has no entry in `.tool-versions`, `TMUX_PINNED` is empty string and the pin-drift warning is silently skipped.
- **VerificationMethod:** static-read
- **Recommendation:** Remove the entire Step 2 (L-25 recommendation), which would eliminate this dead guard as well.

### L-31: `Adapter.ForkInto` — exported interface method with 0 production call sites
- **Dimension:** dead-code (critic-surfaced)
- **SpecRef:** §4
- **Evidence:** `opencode.go:91-100`, `claude.go:102-108`, `adapter.go:47-52`. Only `claude_test.go:295` and `opencode_test.go:213` call `ForkInto`.
- **VerificationMethod:** static-read
- **Recommendation:** Unexport or remove `ForkInto` from the `Adapter` interface.

### L-32: `ClaudeMonitor.TailTranscript` — exported method with 0 production call sites
- **Dimension:** dead-code (critic-surfaced)
- **SpecRef:** §10
- **Evidence:** `claude_monitor.go:55-95`. Only called from `claude_monitor_test.go:157`. Related to H-1 (it should be called in production but is not).
- **VerificationMethod:** static-read
- **Recommendation:** Wire `TailTranscript` in production (see H-1) or document as deliberately deferred.

### L-33: `NewOpts.Prompt`/`.SessionID`/`.Agent` — dead fields at the sole production call site
- **Dimension:** dead-code (critic-surfaced)
- **SpecRef:** §4
- **Evidence:** The only production `NewOpts` construction is `NewOpts{Model: model}` (`claude_monitor.go:181`); `Prompt`, `SessionID` (Claude branch), and `Agent` (opencode branch) are never set in production, making the conditional branches that handle them unreachable.
- **VerificationMethod:** static-read
- **Recommendation:** Remove or document as future-expansion stubs.

### L-34: 4th Svelte CVE (GHSA-9rmh-mm8f-r9h6, ReDoS in `<svelte:element>`) not assessed
- **Dimension:** deletions-deps-config (critic-surfaced)
- **SpecRef:** §10
- **Evidence:** `npm audit` reports 4 moderate advisories for svelte. GHSA-9rmh-mm8f-r9h6 (`ReDoS in <svelte:element> Tag Validation`) was not listed or assessed. `grep` of `frontend/src/` finds zero uses of `<svelte:element>`, so the attack surface is nil in practice.
- **VerificationMethod:** static-read
- **Recommendation:** Upgrade Svelte to clear all 4 advisories when >=5.55.7 is available.

---

## Nit Findings

### N-1: `pty:data` `[]int` / `number[]` encoding correct per spec §5.1 (confirmed green)
- **Dimension:** binding-drift | `internal/pty/bridge.go:140-150`, `frontend/src/lib/wails.ts:103` | Confirmed aligned — `[]int` avoids Wails base64 encoding of `[]byte`. Add explanatory comment only.

### N-2: `pty:exit` payload shape aligned (confirmed green)
- **Dimension:** binding-drift | `internal/pty/bridge.go:123`, `frontend/src/lib/wails.ts:106` | Confirmed: Go map arg lands as first positional arg to JS callback, shape matches. Document the variadic-to-positional Wails event convention.

### N-3: All 25+ bound method names and arg counts align (confirmed green)
- **Dimension:** binding-drift | `app/app.go`, `wails.ts`, `e2e/_mock.ts` | All 25 exported `*App` receiver methods present and name-identical across all three surfaces. No action.

### N-4: `DiffStat` NUL-strip guard is dead code without `-z` flag
- **Dimension:** backend-pty-fs-git | `internal/git/hunk.go:62-64` | `strings.Index(path, '\x00')` guard: `git status --porcelain` (without `-z`) never produces NUL-separated output. Remove the dead guard or switch to `--porcelain -z`.

### N-5: `atomicWriteApp` relies on implicit `os.CreateTemp` 0600 — should have explicit `chmod`
- **Dimension:** app-security-approvals | `app/app.go:787-807` | `os.CreateTemp` uses O_EXCL | mode 0600 on Linux so the file is always created 0600, but no explicit `os.Chmod` call documents this invariant (unlike `claude_monitor.go:317-326` which is explicit). Add `os.Chmod(tmpName, 0o600)` for consistency and auditability.

### N-6: `AlwaysRule` auto-approval performs `GetSettings` disk read on every approval request
- **Dimension:** app-security-approvals | `app/app.go:576-608` | `maybeAutoApprove` calls `GetSettings()` (reads and JSON-unmarshals from disk) on every `PreToolUse` event. Cache `AlwaysRules` in memory, invalidated on `SaveSettings`.

### N-7: `ConfirmDialog` exposes both camelCase and lowercase prop variants — migration artifact
- **Dimension:** frontend-wiring | `frontend/src/lib/ConfirmDialog.svelte:5-10` | Both `onConfirm`/`onconfirm` (and Cancel counterparts) present; lowercase variants are dead duplicates. Remove lowercase variants.

### N-8: `§7.4` heading type token (`20/24px`) absent from `tokens.css`
- **Dimension:** frontend-editor-design-keymap | `frontend/src/tokens/tokens.css` | Spec §7.4: `'headings 20/24'`. No `--perch-fs-heading` or `--perch-fs-h1/h2` token exists. Add `--perch-fs-h1: 24px; --perch-fs-h2: 20px;` to `tokens.css`.

### N-9: Icons are Unicode glyphs, not Lucide/Phosphor SVG
- **Dimension:** frontend-editor-design-keymap | `Sidebar.svelte:18-22`, `DiffView.svelte:22-26` | Spec §7.4: `'Icons: Lucide/Phosphor, 16px chrome, ~1.5–2px stroke'`. Neither `lucide` nor `phosphor` in `package.json`. All icons use Unicode glyphs. The `⚠`/`✓`/`✗` universal-glyph carve-out partially applies, but `◐`/`◯` and `✎` are not universal.

### N-10: `Editor` save has no dirty state indicator
- **Dimension:** frontend-editor-design-keymap | `frontend/src/lib/Editor.svelte:238-239` | `Ctrl-S` save is wired but no visual dirty/unsaved indicator exists.

### N-11: Stale `'tmux window metadata'` / `'tmux options'` comments in `adapter.go`, `model.go`
- **Dimension:** deletions-deps-config | `internal/agent/adapter.go:27`, `internal/model/model.go:8` | References to deleted tmux/TUI concepts.

### N-12: Stale comment in `internal/worktree/hooks.go` references deleted `internal/tmux/cleanup.go`
- **Dimension:** deletions-deps-config | `internal/worktree/hooks.go:53` | File `tmux/cleanup.go` no longer exists.

### N-13: Stale tmux comment in `internal/git/worktree.go:37`
- **Dimension:** deletions-deps-config (critic-surfaced) | `internal/git/worktree.go:37` | `'// This slugifier is distinct from the tmux name sanitizer — do not unify them;'`

### N-14: `frontend/dist/index.html` tracked as build stub while `frontend/dist/` is in `.gitignore`
- **Dimension:** deletions-deps-config | `frontend/dist/index.html`, `.gitignore` | Intentional pattern; add a `.gitignore` comment explaining the tracked stub exception.

### N-15: Makefile `build` target does not rebuild frontend — misleading for fresh clones
- **Dimension:** deletions-deps-config | `Makefile` | Add a comment to `build` target noting it embeds the dist/ stub, not a fresh frontend build. Or rename to `backend-build`.

### N-16: Node.js version not pinned in `.tool-versions`
- **Dimension:** deletions-deps-config | `.tool-versions` | `CONTRIBUTING.md` specifies `v22.x` loosely. Add `nodejs 22.15.1` to `.tool-versions`.

### N-17: `notify.FakeNotifier` defined in production package, not a test file
- **Dimension:** dead-code | `internal/notify/notify.go:11-16` | Move to `notify_test` helper or `internal/notify/testnotify` package.

### N-18: `agent.NewClaudeMonitorWithListener` / `NewOpencodeMonitorWithServer` — test-only constructors in production package
- **Dimension:** dead-code | `claude_monitor.go:33`, `opencode_monitor.go:30` | Neither is called in non-test production code. Unexport these constructors.

### N-19: `gutter.ts` `changedLinesFromHunks` — dead export kept for fictitious backward compat
- **Dimension:** dead-code | `frontend/src/lib/gutter.ts:13` | No production importer; `'kept for backward compatibility'` comment is aspirational. Remove.

### N-20: `DragDrop.svelte` `MIME_TEXT`/`MIME_SESSION` — exported constants duplicated in sibling components
- **Dimension:** dead-code (critic-surfaced) | `DragDrop.svelte:5-6` vs `DiffView.svelte:5`, `FileTree.svelte:5`, `Sidebar.svelte:5` | No production importer uses the exports; each consumer redefines the string literal independently.

### N-21: `agent.ErrForkUnsupported` — exported sentinel error with 0 production importers
- **Dimension:** dead-code (critic-surfaced) | `opencode.go:91-96` | Only `opencode_test.go` references it.

### N-22: `config.Config.AgentBinary()` — 0 production call sites
- **Dimension:** dead-code (critic-surfaced) | `config.go:160-165` | Belongs with M-23 (config fields unread); `agentBins` map and `AgentBinary` method are also dead.

### N-23: Command palette inline keybindings sparsely populated — 14 of 18 commands show no badge
- **Dimension:** frontend-wiring (critic-surfaced) | `App.svelte:279-307` | Only 4 commands (`view:agent/code/diff/split`) carry a `keybinding` value; all `Session`, `Worktree`, `Agent`, `Notifications`, and `Help` commands have none.

### N-24: Editor selection drag (`selection → @mention`) is absent — button-only path
- **Dimension:** frontend-editor-design-keymap (critic-surfaced) | `frontend/src/lib/Editor.svelte` | Spec §7.7 groups selection drag with file and diff drag as the same plumbing; only the button path exists. `Editor.svelte` has no `draggable` attribute and no `ondragstart`/`setData`.

### N-25: `wails.ts` `AgentEvent.kind` union includes `'tool'` literal but Go never emits it
- **Dimension:** binding-drift (critic-surfaced) | `frontend/src/lib/wails.ts:11` | Frontend `switch` statements with a `'tool'` case silently dead-branch. Remove `'tool'` from the union until `tool-start/end` events are implemented (see M-8).

---

## Critic-Surfaced Gaps / couldNotFind

The following items were flagged by the Completeness Critics as unverifiable from source alone or as missing from reviewer checklists:

1. **Spike 3 never run**: `docs/superpowers/spikes/3-opencode-sse-approval.md` does not exist. The real opencode `/event` SSE envelope shape, field names, and `/permission` endpoint routing are all unverified. Internal corroboration from `resources/perch-status.ts:38–40` and plan lines 1527–1536 suggests the real envelope is nested `{type, properties:{…}}`, not the flat format assumed by `sseFrame`.

2. **SSE reconnect/reconcile GET /question**: No code in `internal/agent/` implements a reconnect loop or calls `GET /question` for outstanding permissions. No SQLite schema for question state was inspected.

3. **OpencodeMonitor runtime URL discovery**: The production code has no mechanism to discover the opencode serve URL after the `opencode serve` process starts (no env-var read, no pty-output parsing, no default-port fallback in the Go monitor goroutine).

4. **Session-idle or session-done SSE event type from opencode**: Whether real opencode emits a session-level idle/done event (distinct from `step.ended`) remains unknown because spike 3 was not run.

5. **claude `--continue` flag**: `claude.go:98-100` returns only `['--resume', sessionID]`. The `--continue` flag (resume most recent session without an ID) has no code path.

6. **Integration test for opencode agent path**: `app/app_e2e_test.go` (build tag `integration`) only exercises `agent=claude` via the fake-agent binary. No integration test exercises `agent=opencode` through the full `OpenWorkspace→newMonitor→Prepare→Start` chain. The broken `serverURL` would not be caught by any currently wired test.

7. **`App.test.ts` coverage**: `frontend/src/App.test.ts` (2155 lines) was not read by the test-quality reviewer. It contains describe blocks for approval batching (§8), keymap coverage (j/k, gd, ge, `Ctrl-\ Ctrl-n`, `Ctrl-\``), split-pane routing, drag-to-split, session-reorder, `ConfirmDialog` + undo, and `TokenMeter` cost display. Several reviewer `couldNotFind` claims may be invalidated by this file.

8. **`vendor/modules.txt` cross-check**: The completeness of `go.sum` relative to the 66 vendor modules was not systematically verified (reviewer reported `'go.sum 91 lines, full'` as the completeness signal without a module-by-module cross-check).

9. **Approve() parameter validation**: `Approve(reqID, decision string)` in `app.go:875` does no charset validation on the `reqID` components before using them as map lookup keys. `validateSessionID` is defined but not confirmed called on `Approve`'s inputs.

---

## Known-Not-Bugs / Confirmed Green Gates

The following items were audited and confirmed correctly implemented — listed here to prevent re-investigation:

| Item | Evidence |
|---|---|
| All 25+ bound method names and arg counts align across Go/TS/mock | `app/app.go`, `wails.ts`, `_mock.ts` — all surfaces consistent |
| Event names align: `pty:data:<paneId>`, `pty:exit:<paneId>`, `agent:event`, `fs:changed`, `notify` | `app/app.go:410-411,466,556,601,629`, `bridge.go:148,123`, `wails.ts:102-118` |
| `pty:exit` payload shape is correct Wails variadic-to-positional mapping | `bridge.go:123`, `wails.ts:106` — ALIGNED |
| `pty:data []int` encoding correct — avoids Wails base64 of `[]byte` | `bridge.go:140-150`, `wails.ts:103` |
| `SetWindowFocus` Go void → TS `Promise<void>` consistent | `app/app.go:138`, `wails.ts:55`, `_mock.ts:156-159` |
| `ListWorkspaces` single non-error return → `Promise<WorkspaceVM[]>` consistent | `app/app.go:288`, `wails.ts:30` |
| `hooklistener` binds `127.0.0.1:0`, 32-byte bearer token, constant-time auth | `hooklistener/listener.go:44,49-50,80` |
| `atomicWrite` forces 0600 on token-bearing workspace `.claude/settings.json` | `claude_monitor.go:311-334` |
| Spike 1 (Claude PreToolUse approval): full chain wired end-to-end | `listener.go`, `claude_monitor.go:186-235`, `app_e2e_test.go` (with `-tags integration`) |
| Spike 4 (Wails `OnFileDrop` #3686): `DisableWebViewDrop=true` + frontend `preventDefault` both present | `app/options.go:17,44`, `DragDrop.svelte:17,39` |
| Spike 5 (Linux OS notification): D-Bus path with `notify-send` fallback wired end-to-end | `notify/notify.go:22-49`, `app/app.go:131,648-649` |
| `Always-allow` exact-match (`==`) with no glob/substring hole | `app/app.go:590` |
| `Approve()` reads tool+input from backend-authoritative `a.pending` map, never from frontend-supplied values | `app/app.go:912-915` |
| Production `Run()` opens no TCP IPC port — hooklistener is the only local listener | `app/options.go:31-47`, `options_dev.go` |
| `internal/tui`, `internal/frame`, `internal/tmux` directories fully absent; no charmbracelet/bubbletea/lipgloss in `go.mod`/`go.sum`/vendor | `find internal/`, `go.mod`, `go.sum` |
| All npm deps are exact-pinned (no `^` or `~` ranges) | `frontend/package.json` |
| `DiscoverRepos` Spawn cwd set to worktree directory | `internal/pty/bridge.go:87: cmd.Dir = cwd` |
| Batch approve-all/deny-all (frontend `decideAll`) is mid-flight safe | `App.svelte:266-277` |
| Workspace-remove undo with 6-second window is wired | `App.svelte:217-239`, `App.svelte:700-710` |
| Svelte CVE 1 (GHSA-pr6f-5x2q-rwfp) and CVE 2 (GHSA-f3cj-j4f6-wq85) are SSR-only — not exploitable in CSR Wails build | `vite.config.ts` uses CSR; `main.ts` uses `mount()` not `hydrate()` |
