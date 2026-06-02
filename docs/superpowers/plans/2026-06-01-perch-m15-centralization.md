# M15 — Centralization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Per the user directive "anything that can be hardcoded needs to be centralized" — replace scattered magic literals (protocol identifiers, status strings, file names, tunable numbers, theme colors) with single-source named constants. **Behavior-preserving: every value stays identical; only its definition moves.** This is NOT about exposing new user-config knobs — it's about one source of truth per value so a typo can't silently diverge across call sites.

**Architecture:** Each value gets ONE named const in its owning package; all sites reference the const. The existing green test suite is the correctness proof — values are unchanged, so all tests must still pass without edits (except tests that hard-coded a literal we're now sharing, which should switch to the const too). The highest-value target (silent-bug risk) is the tmux `@perch_*` option names, repeated as raw literals across `tmux`, `tui`, `resurrect`, `status`.

---

## Ground rule for every task

- The const VALUE must be byte-identical to the literal it replaces. Do not "improve" any value.
- After extracting, `grep` the repo to confirm NO raw literal of that value remains (outside the const definition + vendored code).
- Run the full affected-package tests; they must pass UNCHANGED (proves no value drifted).
- Centralize as named consts/vars only. Do NOT add new fields to `config.toml` or expose anything new to users.

---

## Task 1: Protocol / identifier constants (highest value)

**Files:** `internal/tmux/tmux.go` (+ wherever the literals live), `internal/tui/launch.go`, `internal/resurrect/resurrect.go`, `internal/status/status.go`, `cmd/perch/main.go`, `internal/frame/frame.go`, `internal/tmux/frame.go`, `internal/tmux/connect.go`.

- [ ] **Step 1:** In `internal/tmux`, define EXPORTED consts (these cross package boundaries):
```go
const (
	OptionPerchSession    = "@perch_session"
	OptionPerchPaneStatus = "@perch_pane_status"
)
const fieldDelim = "\x1f" // unexported; the list-panes / format field separator
```
- [ ] **Step 2:** Replace every raw `"@perch_session"` literal with `tmux.OptionPerchSession`:
  - `internal/tmux/tmux.go` (the `paneFormat` const embed — keep the format string but build it from the const, OR leave the format string literal and ADD the const for the SetPaneOption/lookup sites; if the format string can't cleanly interpolate, leave that ONE embed and convert the SetPaneOption/Get sites). `internal/tui/launch.go:144,223`, `internal/resurrect/resurrect.go:284`, `cmd/perch/main.go` status-set/lookup sites.
  - Same for `"@perch_pane_status"` → `tmux.OptionPerchPaneStatus`: `internal/tui/launch.go`, `internal/status/status.go`.
- [ ] **Step 3:** Replace the `"\x1f"` delimiter literals with `fieldDelim` within package `tmux` (tmux.go SplitN, frame.go PaneSize, connect.go). For `internal/resurrect/resurrect.go:181` (windowKey join) — it reuses the delimiter cross-package; either export it as `tmux.FieldDelim` and use it there, or define a local `const windowKeyDelim = "\x1f"` with a comment noting it mirrors tmux's. Prefer exporting `tmux.FieldDelim` and using it in resurrect so the coupling is explicit.
- [ ] **Step 4:** Fix the `frame.go:119` fallback that uses literal `"perch"` instead of the existing `DefaultFrameSession` const — replace with `DefaultFrameSession`.
- [ ] **Step 5:** `grep -rn '"@perch_session"\|"@perch_pane_status"'` over non-test non-vendor code → only the const definitions (and possibly the one paneFormat embed) remain. `go build ./... && go test ./... && go vet ./... && golangci-lint run ./...` all green (unchanged behavior).
- [ ] **Step 6 (commit):**
```bash
git add -A && git commit -m "refactor: centralize tmux @perch_* option names + field delimiter as consts (M15-1)"
```

---

## Task 2: Status strings + agent binary-name literals

**Files:** `internal/tui/list.go`, `cmd/perch/main.go`, `internal/agent/claude.go`, `internal/agent/opencode.go`, `internal/tui/data.go`, `internal/attach/attach.go`, `internal/doctor/doctor.go`.

- [ ] **Step 1:** The `internal/status` package already defines `StateWorking="working"`, `StateWaiting="waiting"`, `StateDone="done"`. Replace raw `"working"/"waiting"/"done"` literals with these consts at: `internal/tui/list.go:141,143,145` (statusFromOption mapping), `cmd/perch/main.go:574` (the status-set arg validation switch — but KEEP accepting the user's typed string; just compare against the consts), and `internal/agent/claude.go:193-196` (the perch-hook command strings — build `"perch status set " + status.StateWorking` etc. so the hook command and the const can't drift). If importing `internal/status` into `internal/agent` creates an import cycle, STOP and report (then leave claude.go literals with a TODO comment instead).
- [ ] **Step 2:** Agent binary-name literals: `internal/tui/data.go:94` and `internal/attach/attach.go:116` build `agent.Opencode{Bin: "opencode"}` inline — replace with `agent.NewOpencode()` (the constructor that already sets the bin), OR use `string(model.ToolOpencode)`. In `internal/agent/claude.go:54,84` and `opencode.go:45,68` the bare `"claude"`/`"opencode"` fallback in `bin()` should reference `string(model.ToolClaude)`/`string(model.ToolOpencode)`. In `internal/doctor/doctor.go` tool-table literals, reference `string(model.ToolClaude)`/`string(model.ToolOpencode)`.
- [ ] **Step 2b:** Verify no import cycle (`agent`→`model` and `status` already exist? check). If `tui/list.go` importing `status` is new, confirm it's acyclic.
- [ ] **Step 3:** `grep` confirms the literals are gone from those sites. Build/test/vet/lint green. Commit:
```bash
git add -A && git commit -m "refactor: use status.State* consts and model.Tool* for binary names (M15-2)"
```

---

## Task 3: File / directory name constants

**Files:** `internal/state/state.go`, `internal/trust/trust.go` (or state), `internal/tui/worktree_actions.go`, `internal/tui/app.go`, `internal/config/config.go`, `internal/agent/*.go`, `internal/doctor/doctor.go`, `internal/git/worktree.go`.

- [ ] **Step 1:** Define a single named const for each, in the owning package, and replace all literal uses:
  - `state` pkg: `const stateFile = "state.json"` (state.go:114,150); `const windowsDirName = "windows"` (state.go:195 + the windowsDir() helper; also cmd/perch/main.go:833 debug — use an exported accessor or `state.WindowsDirName`).
  - `trust` pkg: export `const TrustFile = "trust.json"`; replace the 3 literals in `internal/tui` (worktree_actions.go:108,249, app.go:730) with `trust.TrustFile`.
  - `config` pkg: `const configFileName = "config.toml"` (config.go:252), `const projectConfigName = ".perch.toml"` (config.go:307).
  - `agent` pkg: export `const PerchStatusPlugin = "perch-status.ts"` (opencode.go:138) + use in `doctor.go:289`; export `const ClaudeSettingsFile = "settings.json"` (claude.go:141,145) + use in `doctor.go:239,244`.
  - `git` pkg: `const worktreeDirSuffix = "__worktrees"` (worktree.go:78).
  - `tui`/`git`: `const worktreeBranchPrefix = "perch/"` (worktree_actions.go:274) — put it where the branch handle is built.
- [ ] **Step 2:** For each, `grep` confirms one definition + const-only references. Build/test/vet/lint green (paths unchanged → all FS-touching tests still pass). Commit:
```bash
git add -A && git commit -m "refactor: centralize file/dir name literals as named consts (M15-3)"
```

---

## Task 4: Numeric, layout, and color tunables

**Files:** `internal/tui/styles.go`, `internal/tui/app.go`, `internal/tui/list.go`, `internal/tui/screen.go`, `internal/state/state.go`, `internal/tmux/tmux.go`, `internal/tmux/cleanup.go`, `internal/frame/frame.go`, `internal/discover/discover.go`, `internal/agent/claude.go`.

- [ ] **Step 1:** Extract these literals to named consts/vars (value identical), in the owning package:
  - **Status colors** (styles.go statusStyle inline AdaptiveColor literals) → named vars `colorStatusWorking/Waiting/Done/Live` at package level alongside the palette.
  - **Layout:** `const listPanePercent = 30` (app.go:840 `m.width*30/100`); `const cmdBarCharLimit = 256` (app.go New). (`minWideWidth`, `sidebarWidth` are ALREADY consts — leave; just confirm `frame.go:146` `h = 24` → `const defaultPaneHeight = 24`.)
  - **state frecency:** `const frecencyHour = 3_600`, `frecencyDay = 86_400`, `frecencyWeek = 604_800`, and multipliers `frecencyMul1h=4.0, frecencyMul1d=2.0, frecencyMul1w=0.5, frecencyMulOld=0.25`, `frecencyAgeFactor=0.9` (state.go:272-304), `rankBumpDelta=1.0` (state.go:288).
  - **tmux:** `const maxSessionIDLen = 128` (tmux.go:130 validPerchSessionID).
  - **cleanup:** `const cleanupDelaySecs = 0.3` (cleanup.go:59) — substitute into the `sleep` step (format it back to the same string `"sleep 0.3"`).
  - **discover:** `const DefaultMaxDepth = 8` (discover.go:65) — exported if it's a tunable default.
  - **agent:** `const scannerInitBuf = 64 * 1024` (claude.go:420).
  - **list:** `const rowFmt = "%-9s %-40s %s"` (list.go:116).
- [ ] **Step 2:** `grep` confirms each literal replaced. Build/test/vet/lint green. The cleanup `sleep 0.3` string must be byte-identical after formatting (test: `internal/tmux/cleanup_test.go` asserts the script — must still pass). Commit:
```bash
git add -A && git commit -m "refactor: centralize numeric/layout/color tunables as named consts (M15-4)"
```

---

## Closing gate

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l internal/ cmd/)"
go test -race -tags=integration ./...
make lint && make vulncheck && go mod verify
make build && ls -lh bin/perch
```

## Self-review checklist

1. Every extracted const VALUE is byte-identical to the literal it replaced (no value drift). Proven by the unchanged test suite passing. ✅
2. `@perch_session`/`@perch_pane_status`/`\x1f` exist as single consts; no raw literal remains outside the definition (+ the one paneFormat embed if unavoidable). ✅
3. status.State* and model.Tool* used everywhere; no import cycles introduced. ✅
4. No NEW user-config knobs added (centralize ≠ expose). ✅
5. cleanup `sleep 0.3` script string unchanged; cleanup_test still green. ✅
