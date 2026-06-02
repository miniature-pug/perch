# Phase-1 Validation Spikes — Runbook

These five harnesses are **throwaway manual experiments** run on a real dev machine — never in CI.
They answer binary questions (PASS/FAIL) that gate Caps bits in the Phase-2+ implementation.
The entire `cmd/spike-*/` tree is deleted in Phase-5 task 5.6.

Record findings under `docs/superpowers/spikes/N-<name>.md` after each run (see "Record" step per spike).
If a spike FAILs, the Caps fallback listed below is already wired into the contracts — engage it and continue.

---

## Spike 1 — Claude `PreToolUse` HTTP interception

**Caps bit:** `Approvals` · **Fallback:** in-terminal approval (no GUI approval card for Claude sessions)

**Goal:** Confirm a `PreToolUse` hook of type `http` posting to a localhost bearer-authed endpoint
can block Claude's tool invocation by returning `permissionDecision: "deny"`, and that the hook
payload carries `session_id`, `transcript_path`, `cwd`, `tool_name`, and `tool_input`.

**Run:**
```
go run ./cmd/spike-1/
# When claude launches, type: "list files in current dir"
# Watch for === HOOK EVENT === output; Claude should be denied the tool.
# Ctrl-C to exit.
```

**PASS:** Hook fires; all payload fields present (`session_id`, `transcript_path`, `cwd`, `tool_name`, `tool_input`); `permissionDecision: "deny"` blocks the tool.
**FAIL:** Hook never fires, payload missing key fields, or deny response has no effect.

**Record:** `docs/superpowers/spikes/1-pretooluse-interception.md` — Outcome, evidence (paste hook event output), Caps decision (`Approvals=true/false`), exact payload field paths.

---

## Spike 2 — Claude transcript token/cost + project slug

**Caps bit:** `Tokens` · **Fallback:** hide token/cost meter in status bar

**Goal:** Confirm (a) `transcript_path` from the hook payload is an absolute path to the live JSONL;
(b) per-turn JSONL records carry `usage` fields with token counts and cost;
(c) the cold-open slug scheme (`/home/u/p` → `-home-u-p`) matches real `~/.claude/projects/` dir names.

**Prereqs:** Spike 1 must have confirmed hook delivery (or equivalent).

**Run:**
```
go run ./cmd/spike-2/
# Complete one full turn (ask a question, let claude finish responding).
# Harness prints: captured transcript_path, expected slug, and any USAGE records found.
# After run: ls ~/.claude/projects/ to verify slug matches.
# Ctrl-C to exit.
```

**PASS:** `transcript_path` non-empty and absolute; at least one USAGE record with `input_tokens`, `output_tokens`, and a cost field; slug derivation rule `/foo/bar` → `-foo-bar` confirmed.
**FAIL:** `transcript_path` empty/absent, or no USAGE records after a completed turn.

**Record:** `docs/superpowers/spikes/2-transcript-tokens.md` — Outcome, evidence (paste USAGE record JSON), Caps decision (`Tokens=true/false`), field path for tokens and cost.

---

## Spike 3 — opencode `serve` + SSE + REST approval

**Caps bit:** opencode `Approvals` + `Tokens` · **Fallback:** opencode degrades to pty-only (no ApprovalCard, no token meter for opencode sessions)

**Goal:** Confirm `opencode serve` exposes `GET /event` as a proper SSE stream emitting
`session.next.step.started/ended/failed` frames (with token+cost) and `permission.v2.asked`;
and that `POST /permission/{id}/respond` with `{"decision":"reject"}` resolves a pending permission.

**Prereqs:** `opencode` binary on PATH; `OPENCODE_SERVER_PASSWORD` set (or harness uses `spike3secret`).

**Run:**
```
OPENCODE_SERVER_PASSWORD=spike3secret go run ./cmd/spike-3/
# In a second terminal:
opencode attach http://127.0.0.1:4096
# Trigger a permissioned tool call (e.g. "write a hello.txt file").
# Harness prints all SSE frames and auto-rejects the permission after 1s.
# Press Enter in the first terminal to stop.
```

**PASS:** SSE connects (HTTP 200); `session.next.step.started/ended` frames appear; `permission.v2.asked` frame appears; `POST /permission/{id}/respond` returns 200 and blocks the tool.
**FAIL:** `opencode serve` not found, SSE fails, step events lack token data, or permission reply has no effect.

**Record:** `docs/superpowers/spikes/3-opencode-sse-approval.md` — Outcome, evidence (paste 2–3 SSE frames + permission round-trip output), Caps decision (`Approvals`/`Tokens` true/false), exact field paths.

---

## Spike 4 — Wails `OnFileDrop` on Linux (issue #3686)

**Caps bit:** n/a (frontend toggle) · **Fallback:** "Open file…" dialog + Copy-path in FileTree context menu

**Goal:** Confirm `DisableWebViewDrop: true` + `preventDefault` on `dragover`/`drop` prevents
WebKitGTK from hijacking the drop, and that `OnFileDrop` delivers the file path to Go.

**Prereqs:** Wails v2.12.0 build env; Linux desktop with Nautilus/Thunar; `npm` and Go available.

**Run:**
```
cd cmd/spike-4
npm create vite@latest frontend -- --template vanilla
# Replace frontend/src/main.js with the content already at cmd/spike-4/frontend/src/main.js
npm --prefix frontend install && npm --prefix frontend run build
wails build -tags production
./build/bin/spike-4
# Open Nautilus/Thunar and drag a file onto the Spike 4 window.
```

**PASS:** Terminal prints `OnFileDrop: x=N y=N paths=[/path/to/file]`; the Wails UI is NOT replaced or hijacked.
**FAIL:** WebKitGTK replaces UI (blank page or file opens elsewhere); `OnFileDrop` never fires.

**Record:** `docs/superpowers/spikes/4-ondrop-linux-3686.md` — Outcome, evidence (terminal output or screenshot description), Fallback-engaged?.

Note: `cmd/spike-4/main.go` carries `//go:build ignore` so it is excluded from `go build ./...`.
The user must run `wails build` (or `go build -tags production`) from within `cmd/spike-4/` after scaffolding the frontend.

---

## Spike 5 — Linux OS desktop notification via D-Bus

**Caps bit:** n/a · **Fallback:** `notify.New()` returns a no-op `Notifier`; in-app `NotificationHub` still works

**Goal:** Confirm sending a notification via `org.freedesktop.Notifications` D-Bus interface
(with `notify-send` fallback) produces a visible desktop banner when the app window is unfocused.

**Prereqs:** Linux desktop with notification daemon (GNOME Shell, KDE Plasma, or `dunst`); `dbus-send` available; Go available.

**Run:**
```
cd cmd/spike-5
go mod tidy          # downloads godbus v5.1.0 (go.mod already has the require line)
go run main.go
# The harness sleeps 3s — click away to unfocus the terminal, then wait.
```

**PASS:** A desktop notification banner appears with title "perch spike 5" via either D-Bus or `notify-send`.
**FAIL:** Neither path produces a visible notification (no daemon, headless, missing socket).

**Record:** `docs/superpowers/spikes/5-linux-notify.md` — Outcome, evidence (which path worked, any errors), Fallback-engaged?.

Note: `cmd/spike-5/` is a separate nested module (`module spike5`) and is excluded from the root
module's `./...` wildcard. The root `go.mod`/`go.sum` are not modified by this spike.
`go mod tidy` inside `cmd/spike-5/` will generate a local `go.sum` for godbus.

---

## Isolation summary

| Spike | Isolation mechanism | Root build impact |
|-------|--------------------|--------------------|
| 1 | stdlib only | none |
| 2 | stdlib only | none |
| 3 | stdlib only | none |
| 4 | `//go:build ignore` (first line of main.go) | excluded from `go build ./...` |
| 5 | separate `go.mod` (`module spike5`) | excluded from root `./...` wildcard |

Root `go.mod` and `go.sum` are UNCHANGED by all five spikes.
