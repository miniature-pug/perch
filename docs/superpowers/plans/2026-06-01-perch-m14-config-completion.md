# M14 — Config Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make every documented, planned config knob actually drive behavior. Audit found 9 parsed-but-dead knobs; the user ruled: wire the unambiguous four, implement the planned remainder, drop the unplanned. This plan wires: `[theme].accent`, `[agents].<name>` binary paths, `[default_session].startup_command`, default agent/tool, `blacklist` (globs), `sort_order` (running+frecency), `[[wildcard]]` (agent-by-path). It REMOVES `pre_merge` (its parent feature §7.3 merge is explicitly optional/v1-slippable per plan.md) and DROPS the `pinned` sort tier (pinning is no planned feature — only a token).

**Architecture:** The keystone is threading the loaded `*config.Config` through `tui.Config → loader → Model` and loading it ONCE at startup — replacing today's per-field cherry-picking (only `RefreshMs`/`Roots` are extracted at scattered `config.Load` sites). This single change centralizes config flow and unblocks every wiring. `**`-glob matching uses `github.com/bmatcuk/doublestar/v4` v4.10.0 (verified: 2026-01-25, ~4mo old, zero transitive deps, MIT).

**Tool-resolution order (new sessions):** `item.tool` (existing session metadata) → `[[wildcard]]` path-glob match → `[default_session].agent` / project `agent` → `"claude"` final fallback.

---

## Decisions (locked)

- **Thread the whole `*config.Config`**, not per-field copies. `tui.Config` gains `Cfg *config.Config`; `loader` gains `Cfg *config.Config`. Load once in `handleTUI`/`sidebar`. RefreshMs continues to work (read from `cfg.RefreshMs`).
- **`pre_merge` removed** from `projectConfig`/`Config` + any reference. §7.3 merge is explicitly optional and plan-sanctioned to slip v1; not building a merge feature at the finish line. (Flag in CHANGELOG/report.)
- **`pinned` sort tier dropped.** `sort_order` supports `running` (live session first) and `frecency`. Default becomes `["running","frecency"]`. Unknown tokens (incl. a user-supplied `pinned`) are ignored gracefully, not errored.
- **doublestar/v4** for globs — verify zero new transitive deps + `make vulncheck` clean after vendoring.
- **Theme is instance-level**, not global mutation: the accent-dependent styles live on the Model (computed from `cfg.Theme.Accent`, default `#EE6FF8`), so tests stay isolated and there's no package-global mutation.

---

## Task 1: Thread `*config.Config` through the TUI + remove `pre_merge`

**Files:** `internal/tui/run.go`, `internal/tui/data.go`, `internal/tui/app.go`, `cmd/perch/main.go`, `internal/config/config.go` (+ tests).

- [ ] **Step 1 (test):** In `internal/tui/run_test.go` (or app_test.go), assert that `Run`/`New` makes the loaded config reachable. Add a test that builds a `tui.Config{Cfg: &config.Config{RefreshMs: 250}}` and confirms `WithRefresh` still derives the tick from `cfg.RefreshMs` (behavior preserved), and that `m.cfg` (new field) is non-nil. Run → fails (no `Cfg` field).

- [ ] **Step 2:** Add `Cfg *config.Config` to `tui.Config` (run.go). In `Run`, set `ldr.Cfg = cfg.Cfg` and keep `WithRefresh(cfg.RefreshMs)` (derive from `cfg.Cfg.RefreshMs` when `cfg.RefreshMs` unset — but keep the existing explicit field to avoid churn; simplest: set `cfg.RefreshMs` from `cfg.Cfg.RefreshMs` in main.go). Add `Cfg *config.Config` to the `loader` struct (data.go) and a `cfg *config.Config` field on `Model` set in `New`/`WithLoader` (mirror how loader is wired). Guard everywhere for `nil` (test/scaffold mode → fall back to zero-value behavior).

- [ ] **Step 3 (main.go):** In `handleTUI` and `sidebar`, replace the `refreshMs`-only extraction with loading the full config ONCE and passing `Cfg: cfg` into `tui.Config` (set `RefreshMs: cfg.RefreshMs` too for the existing path). Degrade gracefully on load error (`Cfg` nil → defaults). Do NOT change `resurrectDeps`/`handleResurrect` (they only need Roots) unless trivial.

- [ ] **Step 4 (remove pre_merge):** In `internal/config/config.go` delete the `PreMerge` field from `projectConfig` and `Config`, and its merge assignment (config.go:476-478). Grep the repo for `PreMerge`/`pre_merge` and remove every reference (struct, merge, any test). Confirm no production code consumed it (audit proved dead). Update any config test that referenced it.

- [ ] **Step 5:** `go build ./... && go test ./internal/tui/ ./internal/config/ && go vet ./...` — green. Existing behavior unchanged (RefreshMs still drives the tick).

- [ ] **Step 6 (commit):**
```bash
git add internal/tui/run.go internal/tui/data.go internal/tui/app.go cmd/perch/main.go internal/config/config.go internal/tui/*_test.go internal/config/*_test.go
git commit -m "refactor(config): thread *config.Config through the TUI; remove dead pre_merge (M14-1)"
```

---

## Task 2: `**`-glob helper (doublestar)

**Files:** `internal/match/match.go` (new), `internal/match/match_test.go` (new), `go.mod`/`go.sum`/`vendor/`.

- [ ] **Step 1 (dep):** `go get github.com/bmatcuk/doublestar/v4@v4.10.0 && go mod tidy && go mod vendor`. Then `go mod why github.com/bmatcuk/doublestar/v4` and confirm `go.mod` gained ONLY doublestar (no unexpected transitive deps). Run `make vulncheck` → must stay "No vulnerabilities found".

- [ ] **Step 2 (test):** Create `internal/match/match_test.go` covering `MatchAny(patterns []string, path string) bool`:
```go
{"**/archive/** matches nested", []string{"**/archive/**"}, "/home/u/proj/archive/old/x", true},
{"**/archive/** no match", []string{"**/archive/**"}, "/home/u/proj/src/x", false},
{"* single segment", []string{"*.tmp"}, "foo.tmp", true},  // basename semantics — see note
{"empty patterns never match", nil, "/anything", false},
{"invalid pattern is ignored (not a match, not a panic)", []string{"[", "**/x/**"}, "/a/x/b", true},
```
Decide and document the matching semantics: match each pattern against the FULL path with `doublestar.Match` (which supports `**` crossing separators), and ALSO against the base name, so both `**/archive/**` (path) and `*.tmp` (basename) styles work. An invalid pattern (doublestar returns an error) is skipped, never a match.

- [ ] **Step 3:** Implement `internal/match/match.go`:
```go
// Package match provides **-aware glob matching for config blacklist and
// wildcard rules, backed by doublestar (path globbing that supports ** across
// separators). A malformed pattern is skipped (never matches), never panics.
package match

import (
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// MatchAny reports whether path matches any of the glob patterns. Each pattern
// is tried against the full path and against filepath.Base(path), so both
// path-style ("**/archive/**") and basename-style ("*.tmp") patterns work.
// Patterns that fail to compile are skipped.
func MatchAny(patterns []string, path string) bool {
	base := filepath.Base(path)
	for _, p := range patterns {
		if ok, err := doublestar.Match(p, path); err == nil && ok {
			return true
		}
		if ok, err := doublestar.Match(p, base); err == nil && ok {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4:** `go test ./internal/match/ && golangci-lint run ./internal/match/...` green.

- [ ] **Step 5 (commit):**
```bash
git add internal/match/ go.mod go.sum vendor/
git commit -m "feat(match): **-aware glob helper via doublestar v4.10.0 (M14-2)"
```

---

## Task 3: Wire `[theme].accent`

**Files:** `internal/tui/styles.go`, `internal/tui/app.go`, `internal/tui/*_test.go`.

- [ ] **Step 1 (test):** Assert that a Model built with `cfg.Theme.Accent = "#00FF00"` renders the selected row / modal border using that color (assert the ANSI/style carries the configured color, e.g. via a small helper that builds the themed selectedRow and checks its foreground). Default (nil cfg / empty accent) keeps `#EE6FF8`.

- [ ] **Step 2:** Refactor the accent-dependent styles to be INSTANCE-level. Add a `theme` struct to the Model holding the accent-derived styles (`selectedRow`, `helpOverlay`, modal border color) computed from the configured accent (default `#EE6FF8` when unset). Build it in `New`/`WithLoader` from `m.cfg.Theme.Accent`. Replace the global `styles.selectedRow`/`styles.helpOverlay`/`modalStyle` *accent uses* at render sites with the Model's themed styles. Leave non-accent styles (borders=colorSubtle, status colors, footer=colorMuted) global. (Note: `colorAccent` default moves to read from `config.defaultAccent` so the default is single-sourced — see M15.)

- [ ] **Step 3:** `go test ./internal/tui/` green; `make build` (binary size unchanged). Commit:
```bash
git commit -am "feat(tui): wire [theme].accent to the UI accent (M14-3)"
```

---

## Task 4: Launch-path config — agent binary, startup_command, tool resolution

**Files:** `internal/tui/launch.go`, `internal/tui/app.go`, `internal/tui/cmdbar.go`, `internal/tui/worktree_actions.go` (+ tests). Uses `internal/match`.

- [ ] **Step 1 (tests, FakeRunner):** In launch tests assert, via `FakeRunner.Calls`:
  - **Agent binary:** with `cfg.Agents["claude"]="/opt/claude"`, a launch uses argv[0]==`/opt/claude` (not bare `claude`). With no config, argv[0]==`claude` (fallback).
  - **startup_command:** with `cfg.StartupCommand="echo hi"`, after Launch a `send-keys` carrying `echo hi` is issued to the new pane. Empty → no extra send-keys.
  - **Tool resolution:** a new session where `item.tool==""` and `cfg.Agent=="opencode"` launches opencode. With a `[[wildcard]]` `{pattern:"**/experiments/*", agent:"claude"}` and a treePath under `…/experiments/x`, the wildcard wins over `cfg.Agent`. Existing `item.tool` always wins over both.

- [ ] **Step 2:** Add a `resolveTool(treePath, itemTool string) string` helper on Model: returns `itemTool` if non-empty; else the first `[[wildcard]]` whose `pattern` matches `treePath` via `match.MatchAny([]string{w.Pattern}, treePath)`; else `cfg.Agent`; else `"claude"`. Use it in the `n` handler, `:new` (cmdbar), and `worktreeCreateCmd`/`runHere`/`runMain` where `tool` is currently `it.tool`.

- [ ] **Step 3:** In `launchCmd`, resolve argv[0] via `m.cfg.AgentBinary(model.Tool(spec.tool))` (falls back to the bare name when unconfigured) instead of `adapter.Name()`. Thread the resolved binary into the argv build (keep `adapter.Name()` as the fallback inside `AgentBinary`). After a successful `Launch`, if `m.cfg.StartupCommand != ""`, send it to the new pane via the tmux send-keys seam (mirror how the agent command is delivered; ensure it runs in the agent pane). Guard all `m.cfg`-nil cases.

- [ ] **Step 4:** `go test -race ./internal/tui/`, vet, lint green. Commit:
```bash
git commit -am "feat(tui): wire agent binary paths, startup_command, default-agent+wildcard tool resolution (M14-4)"
```

---

## Task 5: Discovery config — `blacklist` filter + `sort_order`

**Files:** `internal/tui/data.go`, `internal/discover/` or a TUI-side filter, `internal/state/state.go` (sort), `internal/config/config.go` (drop `pinned` from default) (+ tests). Uses `internal/match`.

- [ ] **Step 1 (tests):**
  - **blacklist:** with `cfg.Blacklist=["**/archive/**"]`, a discovered project/tree whose path is under `…/archive/…` is absent from the assembled list; non-matching ones remain. Empty blacklist → no filtering.
  - **sort_order:** with `["running","frecency"]`, live-session rows sort before idle rows; within each group, frecency order holds. With `["frecency"]`, pure frecency (current behavior). Unknown token `"pinned"` is ignored (no panic, no effect).

- [ ] **Step 2:** **blacklist** — filter in the loader after discovery returns project/tree paths: drop any whose path matches `match.MatchAny(cfg.Blacklist, path)`. Apply to both projects and their trees. (Keep `discover`'s own `DefaultPrune` walk-pruning unchanged — blacklist is a post-discovery UI filter, matching the plan's "hide matching paths from the UI".)

- [ ] **Step 3:** **sort_order** — implement an ordering that honors the configured priority list. Tier functions: `running` = has a live tmux session (item.live), `frecency` = the existing `state.FrecencyScore`. Order rows by the priority list (stable within ties; unknown tokens skipped). Default `sort_order` in config.go becomes `["running","frecency"]` (drop `"pinned"`). The cold-start alphabetical fallback (plan §1143) stays as the final tiebreak.

- [ ] **Step 4:** `go test -race ./internal/tui/ ./internal/state/ ./internal/config/`, vet, lint green. Commit:
```bash
git commit -am "feat: wire blacklist UI filter and sort_order (running+frecency); drop unplanned pinned tier (M14-5)"
```

---

## Closing gate (after all tasks)

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l internal/ cmd/)"
go test -race -tags=integration ./...
make lint && make vulncheck && go mod verify
make build && ls -lh bin/perch   # confirm still ~5 MB (+doublestar is tiny)
```

## Self-review checklist

1. Every wired knob has a FakeRunner/loader test proving it changes behavior. ✅
2. Tool-resolution order is exactly item.tool → wildcard → default agent → "claude". ✅
3. `pre_merge` fully removed (no struct field, no reference); `pinned` tier dropped (default updated, unknown tokens ignored). ✅
4. doublestar adds no unexpected transitive deps; vulncheck clean; binary ~5 MB. ✅
5. All `m.cfg`-nil paths guarded (scaffold/test mode). ✅
6. blacklist is a post-discovery UI filter (DefaultPrune walk-pruning untouched). ✅
