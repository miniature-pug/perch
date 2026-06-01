# perch Core Model Design Spike

**Date:** 2026-06-01  
**Branch:** feat/perch-v1  
**tmux version under test:** 3.6  
**Socket used:** `tmux -L perch-spike` (isolated, killed at end)

---

## Executive Summary (15 lines)

Topology recommended: **swap-pane with per-agent sessions** — each managed agent lives in its own detached tmux session, the perch frame is a separate `perch` session with a sidebar pane (the TUI) and a main-display pane (the placeholder), and switching an agent into view is a two-command swap-pane exchange. Verdict: **SOUND AND BUILDABLE on tmux 3.6**.

Key risks:

1. **Crash recovery gap**: if perch crashes while an agent pane is displayed in the frame, that pane is stranded in the dead frame session and must be recovered by `resurrect` — new reconcile logic required.
2. **No-prefix keybinding isolation requires a private socket** (FD): staying on the default server avoids breaking the status hook and switch-client bootstrap, but means no slick Alt+h/l focus keys — prefix-based pane navigation (`prefix + Left/Right`) is the pragmatic default.
3. **Live concurrent attach to a displayed agent shows the placeholder, not the agent** — correct post-`q`, but a limitation while perch is running.

Written doc: `docs/superpowers/plans/2026-06-01-perch-core-model-spike.md`

---

## 1. Empirical Findings

### Q1 — Does moving a session's last pane out via join-pane destroy the session?

**Setup:**

```
tmux -L perch-spike new-session -d -s sessionA -n main -c /tmp
tmux -L perch-spike send-keys -t sessionA:main 'cat' Enter
tmux -L perch-spike new-session -d -s frameB -n frame -c /tmp
```

**Before join-pane:**
```
frameB: 1 windows (created Mon Jun  1 01:06:19 2026)
sessionA: 1 windows (created Mon Jun  1 01:06:05 2026)
---
frameB:0.0 %1 bash 201112
sessionA:0.0 %0 cat 199985
```

**Command:**
```
tmux -L perch-spike join-pane -s sessionA:0.0 -t frameB:0.0
```

**After join-pane:**
```
=== list-sessions AFTER join-pane ===
frameB: 1 windows (created Mon Jun  1 01:06:19 2026)
=== list-panes -a AFTER join-pane ===
frameB:0.0 %1 bash 201112
frameB:0.1 %0 cat 199985
```

**Answer: YES — sessionA is destroyed.** Moving the last pane out via join-pane eliminates the source session. The `cat` process (pane_id `%0`, pane_pid `199985`) survives in frameB, but the source session is gone entirely. This disqualifies the join/break topology for the "each agent is its own session" requirement.

---

### Q2 — break-pane round trip to a named standalone session

**Finding:** `break-pane` in tmux 3.6 cannot directly create a named standalone session. Without `-t`, it creates a new window in the current session:

```
tmux -L perch-spike break-pane -d -P -F '#{session_name}:#{window_name}:#{pane_id}:#{pane_pid}' -s frameB:0.1
# Output: frameB:cat:%2:203790
# → Created window 1 in frameB, NOT a new session

list-sessions: frameB: 2 windows (still only frameB)
```

Attempting `break-pane -t agentA:0` fails with `can't find session: agentA` unless that session already exists. The `-t dst-window` argument targets an existing window or session; it cannot conjure new sessions.

**Answer: break-pane cannot round-trip to a standalone named session directly.** To park a pane as a named session requires `new-session -d -s name 'placeholder'` first. This further confirms the join/break model is unworkable — use swap-pane instead.

---

### Q3 — swap-pane across sessions: do both sessions survive?

**Setup:**
```
agentA:0.0 %3 cat 204368
frameB:0.0 %1 bash 201112
frameB:0.1 %4 sleep 205387   ← main display placeholder
```

**Command:**
```
tmux -L perch-spike swap-pane -s %3 -t %4
# exit=0
```

**After swap-pane:**
```
=== list-sessions AFTER swap-pane ===
agentA: 1 windows (created Mon Jun  1 01:08:05 2026)
frameB: 1 windows (created Mon Jun  1 01:06:19 2026)
=== list-panes -a AFTER swap-pane ===
agentA:0.0 %4 sleep 205387
frameB:0.0 %1 bash 201112
frameB:0.1 %3 cat 204368
```

**Answer: YES — both sessions survive.** swap-pane is a true exchange:
- agentA still exists and now holds the placeholder pane (`%4 sleep`)
- frameB's main display slot now holds agentA's cat process (`%3 cat pid=204368`)
- The placeholder **auto-migrates** to the agent session — no explicit parking management required

**Multi-agent switch (two-step):**
```
# Switch from agentA to agentB (agentB:%5 cat 206171)
# Step 1: return agentA's pane home
tmux -L perch-spike swap-pane -s %3 -t %4   # %3=cat in frame, %4=sleep in agentA
# Step 2: bring agentB in
tmux -L perch-spike swap-pane -s %5 -t %4   # %5=cat in agentB, %4=sleep now in frame
```

After two-step switch:
```
agentA:0.0 %3 cat 204368   ← home, alive
agentB:0.0 %4 sleep 205387  ← has placeholder now
frameB:0.0 %1 bash 201112
frameB:0.1 %5 cat 206171   ← agentB displayed
```

All three sessions survive. All PIDs survive.

**Also tested:** swapping a pane from a detached session (`agentC` never attached) into an active frame works identically — detached vs attached status of the source session is irrelevant to swap-pane.

---

### Q4 — Interactivity after move

After swap-pane, sent input to the moved pane and captured output:

```
tmux -L perch-spike send-keys -t %3 -l "hello perch"
tmux -L perch-spike send-keys -t %3 Enter
tmux -L perch-spike capture-pane -t %3 -p
```

Output:
```
cat

/tmp [🐹 v1.26.3]
❯ cat
hello perch
hello perch
```

After multi-agent switch, agentB's pane also responds:
```
tmux -L perch-spike capture-pane -t %5 -p
# Output: cat / from agentB / from agentB
```

**Answer: YES — fully interactive.** send-keys and capture-pane both work on panes after swap. Addressed by pane_id (`%N`), not coordinate, so targeting is stable across moves.

---

### Q5 — Resize and redraw after move

**Before resize:**
```
frameB: %1 40x24 bash,  %3 39x24 cat
```

**After `resize-pane -t %3 -x 120 -y 40` and `resize-window -t frameB -x 160 -y 50`:**
```
frameB: %1 41x50 bash,  %3 118x50 cat
agentA parked: %4 80x24 sleep   ← detached, stays at detached session size
```

**Swap agentA cat back (from 118x50 frame slot to 80x24 detached agentA session):**
```
agentA %3 80x24 cat   ← SHRANK to agentA's 80x24 window
frameB %4 118x50 sleep ← grew to frame's slot size
```

**Key findings:**
- Pane adopts the destination window's dimensions on swap — no manual resize-pane needed
- Parked session panes (detached) retain their own session's dimensions until swapped
- Default detached session dimensions: 80x24 (tmux default when no client attached)
- Agent process receives SIGWINCH on resize/swap; a real TUI agent (claude/opencode) will reflow — not testable with `cat`/`sleep` but follows standard pty semantics
- **Recommendation:** keep parked sessions at 80x24 (default). On swap-in the frame layout takes over. On swap-out the agent shrinks to 80x24 — agent redraws on SIGWINCH. Not ideal but acceptable.
- **Alternative:** resize parked session to match frame dimensions before swap-in to avoid resize shock. This requires one extra `resize-window -t agentX -x W -y H` per switch, where W×H is the current frame main pane size.

---

### Q6 — Bootstrap

**Construction verified empirically:**

```bash
# Create perch frame session (detached)
tmux -L perch new-session -d -s perch -n frame -c /tmp 'sleep infinity'
# Add main display pane (horizontal split, right side)
tmux -L perch split-window -d -h -t perch:frame -c /tmp 'sleep infinity'
# Resize sidebar to desired width (e.g., 50 cols)
tmux -L perch resize-pane -t perch:frame.0 -x 50
```

Result:
```
perch:frame:  %6 50x50 sleep (sidebar)
              %7 169x50 sleep (main display placeholder)
```

**Attach handshake (construction verified; live attach not verifiable in this non-tty context):**

- **Outside tmux** (`$TMUX` empty): `tmux -L perch attach-session -t perch` — standard attach. Received `open terminal failed: not a terminal` in test context (no real tty), confirming correct syntax. Would succeed in a real terminal.
- **Inside tmux** (`$TMUX` set, existing client): `tmux -L perch switch-client -t perch` — **this only works when using the SAME server** as the existing client. If perch uses a private socket (`-L perch`) and the user is attached to the default socket, `switch-client` **cannot cross servers** — must use `attach-session` which creates a nested attachment. See FD-01.

**Self-re-exec pattern (recommended):**

```
perch outer (shell):
  1. Check if perch frame session already exists on the target server
  2. If not: tmux new-session -d -s perch 'sleep infinity'; split-window; resize-pane
  3. If $TMUX is set: tmux switch-client -t perch (if same-server) or attach-session
  4. If $TMUX is empty: exec tmux attach-session -t perch
     (exec replaces the outer process; no orphan shell)
  
perch inner (TUI sidebar):
  1. Runs as the command in perch:frame.0 (sidebar pane)
  2. Gets the frame's pane IDs from $TMUX_PANE and tmux list-panes
  3. Manages swap-pane on switch; swap-home on quit
```

**Main pane placeholder before any session selected:** `sleep infinity` or `cat` is sufficient. A welcome message could be sent via `send-keys -l` after the frame is built.

---

### Q7 — Focus model and keybinding isolation

**Directional select-pane works on isolated socket:**

```
tmux -L perch-spike bind-key -n 'M-h' select-pane -L
tmux -L perch-spike bind-key -n 'M-l' select-pane -R
# exit=0
# Verified in list-keys -T root
```

**CRITICAL: Key tables are SERVER-scoped, not session-scoped.** Installing `bind -n M-h` on the default tmux server adds it globally, affecting all sessions and windows the user has. This is a firm constraint.

**Option 1 — Private socket (FD-01):** Run perch on a private socket (`tmux -L perch`). All bindings are scoped to that server; zero pollution of the user's default server. Enables `bind -n M-h`, no-prefix focus keys, `bind -T perch-keys`. Cost: breaks inside-tmux `switch-client` bootstrap and breaks `perch status set` (which calls `tmux.New()` with default socket and targets panes by `$TMUX_PANE`).

**Option 2 — Default socket, no custom bindings (recommended):** Stay on user's default server (`Socket: ""`). Install zero new key bindings. Focus navigation uses **standard tmux prefix keybindings** (`prefix + Left/Right`, `prefix + o`, `prefix + ;`). These already exist in every tmux installation. Zero pollution. This is compatible with the existing `SwitchClient`/`AttachTargetArgs` implementation and `perch status set`.

**Option 3 — Default socket, perch-keys table:** Install bindings only in a custom `perch-keys` table (`bind-key -T perch-keys M-h select-pane -L`) but do NOT activate it with `set -g key-table perch-keys`. Document how to opt in. This adds keybindings to the default server but only activates them when the user's prefix chain enters `perch-keys`.

**Recommendation: Option 2 (default socket, no custom bindings).** Eliminates three cross-cutting concerns (status hook, bootstrap, socket threading) with zero user-visible config pollution. The UX cost is "use your prefix for pane nav" rather than Alt+letter.

---

### Q8 — Closing semantics

**Test: kill frame when agent pane is IN the frame (wrong approach):**
```
# agentB's cat (%5) was in frameB when frameB was killed
# Result: pid 206171 (agentB cat) destroyed

# agentA's cat (%3) was home in agentA when frameB was killed
# Result: agentA survived with cat pid=204368 intact
```

**Test: correct quit sequence (swap-home first):**
```
# agentA's cat (%3) was displayed in perch-frame
# Step 1: swap-pane -s %3 -t %7  (swap agent home)
# Step 2: kill-session -t perch-frame
# Result:
agentA: 1 windows — %3 cat 204368 (ALIVE)
agentB: 1 windows — %4 sleep (ALIVE)
```

**Answer: CORRECT quit sequence is swap-home before kill.** If the agent pane is in the frame when the frame is killed, the agent process is destroyed. The `q` quit handler MUST:

1. Identify which agent pane is currently in the frame's main slot (stored in perch TUI state)
2. Swap it home: `swap-pane -s <frame-main-pane-id> -t <agent-home-pane-id>`
3. Then kill-session or let the TUI process exit naturally

The placeholder pane (sleep) that was in the agent session gets destroyed with the frame — that is fine, placeholders are disposable.

**Q1 vs Q8 tension — resolved by swap-pane:** join-pane destroys the source session (Q1); swap-pane keeps both sessions alive (Q3). There is no tension when using swap-pane: agents are always in their own sessions and the frame never owns any agent process.

---

## 2. Recommended Topology

### Object Model

```
tmux server (default socket, -L unset)
├── session: perch          (the frame, one window)
│   └── window: frame
│       ├── pane %SB  (sidebar): perch TUI binary, 40-50 cols wide
│       └── pane %MN  (main):    displayed agent pane OR sleep placeholder
│
├── session: <project>-<branch>-<tool>  (e.g., myrepo-feat-x-claude)
│   └── window: main
│       └── pane %Ax:  agent process (claude/opencode) OR placeholder when displayed
│
├── session: <project>-<branch2>-<tool>
│   └── window: main
│       └── pane %Bx:  agent process OR placeholder
│
└── ... (one session per managed agent)
```

### Session Naming

Agent sessions use existing `tmux.SessionName(projectPath)` + `tmux.WindowName(branch)` logic. For the frame: fixed name `perch` (or user-configurable).

### Switch Sequence (step by step)

Switching from currently-displayed agentA to agentB:

```bash
# TUI state: currentAgentPaneID = %Ax (in frame %MN slot), agentB home pane = %Bx
# All panes addressed by pane_id (stable across moves)

# Step 1: Return current agent pane to its home
tmux swap-pane -s %Ax -t %Bx   # No! Wrong: this puts agentB pane in frame

# CORRECT two-step:
# (Let %PL = the placeholder pane; it is currently in agentA's session from the prior swap-in)
# Step 1: Return agentA's pane home (swap it with its own placeholder)
tmux swap-pane -s %Ax -t %PL   # %Ax (in frame) ↔ %PL (in agentA); result: %Ax back in agentA, %PL in frame
# Step 2: Bring agentB in (swap agentB's pane with the placeholder now in frame)
tmux swap-pane -s %Bx -t %PL   # %Bx (in agentB) ↔ %PL (in frame); result: %Bx in frame, %PL in agentB
```

After step 2: frame has `%Bx` (agentB's process), agentA has `%Ax` (agentA's process), agentB has `%PL` (placeholder). All three sessions alive.

**Implementation note:** The perch TUI must track:
- `currentDisplayedPaneID`: the pane currently in the frame's main slot
- `currentPlaceholderPaneID`: the placeholder pane (migrates between frame and last-viewed agent)
- Per-agent: `agentHomePaneID` (the pane ID when the session was created via `NewSession`)

On first display of any agent, if the placeholder is still in the frame: `swap-pane -s agentHomePaneID -t framePlaceholderPaneID`.

### Quit Sequence

```bash
# q keypress handler:
# 1. If an agent is currently displayed:
tmux swap-pane -s <currentDisplayedPaneID> -t <currentPlaceholderPaneID>
# 2. Kill the perch frame session:
tmux kill-session -t perch
# 3. (tea.Quit already fires; the above runs as the final Cmd before exit)
```

Agent sessions remain: detached, named, independently attachable via `tmux attach-session -t <session-name>`.

### perch attach support

After perch quits, each agent session is independently attachable:
```
tmux attach-session -t =myrepo-feat-x-claude
```

**Known limitation:** while perch is running, the displayed agent's pane is physically in the perch frame, so `perch attach <query>` (planned) would find the agent session but its pane would show the placeholder (sleep). Full concurrent-attach is not a clean UX; document as limitation: `perch attach` should tell the user to use perch's own switch-and-detach flow instead.

---

## 3. Bootstrap Design

### Outside tmux (`$TMUX` empty)

```bash
# 1. Ensure frame session exists
tmux new-session -d -s perch -n frame -c "$HOME" "perch --sidebar"
# "perch --sidebar" is the inner TUI process; it occupies the sidebar pane

# 2. Split: add main display pane
tmux split-window -d -h -t perch:frame.0 -c "$HOME" "sleep infinity"

# 3. Resize sidebar to ~50 cols
tmux resize-pane -t perch:frame.0 -x 50

# 4. Attach
exec tmux attach-session -t perch
# exec: replaces outer shell, no orphan process
```

### Inside tmux (`$TMUX` set)

```bash
# Same steps 1-3
tmux new-session -d -s perch -n frame -c "$HOME" "perch --sidebar"
tmux split-window -d -h -t perch:frame.0 -c "$HOME" "sleep infinity"
tmux resize-pane -t perch:frame.0 -x 50

# Step 4: switch-client (same server — default socket, no -L)
tmux switch-client -t perch
```

**Inside-tmux constraint:** `switch-client` requires the client to be on the same server as the target session. Since perch uses the default socket (`Socket: ""`), this works without issue. If a private socket were chosen (FD-01), `switch-client` from an existing client on the default server would fail with `no current client` — a nested attach or terminal multiplexer would be required instead.

### Inner sidebar process

The inner `perch --sidebar` subcommand is the TUI (bubbletea program) scoped to the sidebar pane. It:
1. Gets its own pane ID from `$TMUX_PANE`
2. Detects the frame's main pane ID by listing panes in the current window and picking the other one
3. Manages `swap-pane` on selection change
4. On quit: swaps displayed agent home, then calls `tea.Quit`

The outer process (the one that set up the frame) either `exec`'d into `tmux attach` (outside-tmux) or was a one-shot bootstrapper that ran `switch-client` and exited normally (inside-tmux).

### Main pane placeholder

Before any session is selected, the frame's main display slot runs `sleep infinity`. The sidebar TUI can send a welcome message via `tmux send-keys -t <main-pane-id> -l "Select a session from the sidebar"` on startup. Alternatively: leave it blank (sleep never prints anything).

---

## 4. Focus and Keybindings

**Recommendation: no new tmux key bindings installed.** Use the standard tmux prefix-based pane navigation:
- `prefix + Left/Right`: move focus between sidebar and main
- `prefix + o`: cycle panes
- `prefix + ;`: last active pane

This requires zero additions to the user's tmux config and zero binding installation at perch startup.

**Why not no-prefix Alt keys?** Key tables are server-scoped. Installing `bind -n M-h` on the default server adds it for all the user's sessions, not just the perch frame — a clear violation of "do not mutate user's tmux config." On a private socket (FD-01) this would be clean, but the private socket has three other consequences (see FD-01 below).

**FD-01 — Private socket alternative:**
```
tmux -L perch new-session ...
tmux -L perch bind-key -n 'M-h' select-pane -L
tmux -L perch bind-key -n 'M-l' select-pane -R
```
Verified working empirically (`exit=0`, confirmed in `list-keys -T root`). Costs:
1. `switch-client` from user's existing tmux client (different server) is impossible — nested attach required
2. `perch status set` calls `tmux.New()` (default socket) + `set-option -t $TMUX_PANE`; pane IDs are per-server, so the option lands on wrong server; all M8 status reporting silently breaks
3. All existing call sites (launch, connect, resurrect) must thread the socket name through — O(N) file changes

**FD-02 — perch-keys table opt-in:**
```
tmux bind-key -T perch-keys 'h' select-pane -L
tmux bind-key -T perch-keys 'l' select-pane -R
# User activates with: bind-key -n 'M-p' switch-client -T perch-keys
```
Adds to default server but only activates when user manually switches to `perch-keys` table. Clean enough for power users, awkward as default.

**Verdict: Option 2 (default socket + prefix nav) for v0.1; FD-01 as future opt-in.**

---

## 5. Resize and Redraw Plan

### Layout strategy

The frame window has two panes side-by-side (horizontal split). The layout is `even-horizontal` or explicit:
- Sidebar: fixed 50-column width, full height
- Main: remaining width, full height

On terminal resize, tmux sends `SIGWINCH` to both panes. The perch TUI sidebar receives `tea.WindowSizeMsg` and should re-layout the list/viewport model. The agent in the main pane receives SIGWINCH and redraws its own TUI (claude/opencode handle this natively).

### Resize on swap

When a pane is swapped from a parked agent session (80x24) into the frame's main slot:
- The pane immediately adopts the frame main slot's dimensions (verified: `%3 39x24 cat` in frame)
- A SIGWINCH is delivered to the process — agent reflows
- No explicit `resize-pane` needed after swap

When a pane is swapped out (back to its home session):
- The pane shrinks to the home session's dimensions (typically 80x24 for detached sessions)
- SIGWINCH is delivered; agent should handle gracefully (render into smaller size)

**Optional optimization:** before swap-in, resize the parked agent's session to match the frame main dimensions:
```bash
# Get frame main dimensions
W=$(tmux display-message -p -t <main-pane-id> '#{pane_width}')
H=$(tmux display-message -p -t <main-pane-id> '#{pane_height}')
tmux resize-window -t <agent-session> -x $W -y $H
# Then swap-pane
```
This eliminates the resize shock. Worth doing if agents show layout artifacts on swap-in.

### perch TUI self-resize

The sidebar TUI must handle `tea.WindowSizeMsg` normally (already does via `m.width`/`m.height` in `app.go`). After the frame is split, the sidebar's pane dimensions will be the sidebar column width, not the full terminal width — the TUI must not assume `tea.WindowSizeMsg` gives the full terminal width. Existing `WithAltScreen()` in `run.go` should handle this correctly since bubbletea reads the actual pty size.

---

## 6. Port Analysis — Milestone Mapping

### M5 — Launch and attach (`internal/tui/launch.go`, `internal/tmux/launch.go`)

**`launchCmd` — minimal change.** Agent sessions are still created via `tmux.Launch()` → `Connect()` → `NewSession()`. The returned `paneID` is now stored in TUI state as the agent's `homePaneID` for use in swap operations. No structural change to `Launch`.

**`attachTo` — **major change.** Today, `attachTo` runs `switch-client` or `attach-session` to hand the entire terminal to the agent session. In the new model, `attachTo` is replaced by a `swapCmd` that runs `swap-pane -s <agentHomePaneID> -t <framePlaceholderPaneID>`. The `AttachArgs`/`AttachTargetArgs` logic becomes **bootstrap-only** (used once to attach the user to the frame session at startup). The per-selection Enter key action becomes `swapIn(selectedItem)`.

**`launchedMsg` handling:** after a new session is launched, the agent's pane ID is recorded. If `switchOnLaunch` is true (default), immediately `swapIn` it.

### M5 — `@perch_session` stamping

Unchanged. `SetPaneOption` stamps `@perch_session` on the pane after `Launch`. Since pane IDs are stable across swap (only the window/session ownership changes, not the pane_id), `ListPanesAll` + `buildLiveIndex` will find the correct pane regardless of which session it's currently parked in.

**Important:** `CapturePane` in `data.go` (`previewCmd`) uses `pane.ID` as the target. After swap-in, the agent pane is in the frame — `capture-pane` still works by pane_id. After swap-out, the pane is in its home session — still works. No change needed.

### M6 — Worktree create/remove

No structural change. `internal/tui/launch.go`'s worktree launch path creates a new session for the new worktree. The swap-pane topology handles it identically to any other agent launch.

### M8 — Status pipeline (`@perch_pane_status`, `internal/status`)

**`perch status set`** (in `cmd/perch/main.go`) reads `$TMUX_PANE` and calls `tmux.New().SetPaneOption(pane, "@perch_pane_status", state)`. This works on the **default socket** regardless of which session the pane is currently in — pane IDs are server-global. No change required.

**`statusPoll` in `data.go`** calls `ListPanesAll` and builds the `statuses` map keyed by `PerchSession`. Since pane_ids are stable across swap, `@perch_pane_status` remains on the correct pane whether it's parked or displayed. No change required.

**Socket concern:** if FD-01 (private socket) were adopted, `perch status set` uses `tmux.New()` which has `Socket: ""` — it would target the wrong server and silently fail. This is the primary technical reason FD-01 is not the default recommendation.

### M9 — UX overlays (toast/modal/help/screen-modes, `internal/tui`)

No structural change. Toast, modal, help overlay, and screen-modes (`screenNormal`, `screenFullList`, `screenFullPreview`) are entirely within the perch TUI sidebar process. The sidebar pane dimensions change (narrower than the full terminal), but `tea.WindowSizeMsg` handles this. The `screenFullPreview` mode (viewport-only) now becomes less relevant since the main pane shows the live agent — consider repurposing or removing it.

### Preview strategy: `capture-pane` vs live display

**Today:** `internal/tui/data.go` fires `previewCmd` → `CapturePane` → static snapshot rendered in sidebar's viewport.

**Recommended approach in the new model:**
- **Non-focused rows (not the current selection):** keep `capture-pane -e -p` static snapshot in the sidebar's preview viewport (right panel of the sidebar when `screenNormal`) — useful for glancing at other sessions without switching
- **Selected/focused row:** the agent's live pane IS visible in the main display (after swap-in) — no preview needed; the sidebar can show metadata (session title, branch, status badge, timing) instead of a duplicate capture
- **Before first selection (no swap-in yet):** still show `capture-pane` snapshot for the highlighted row

This means `previewCmd` is retained for non-active rows and the pre-selection state. Once an agent is swapped into the main pane, the preview pane in the sidebar TUI can be hidden or repurposed (show session info / diff stats / token count from the agent's status).

---

## 7. Risks and Open Questions

### Risk 1 — Crash recovery (agent pane stranded in dead frame)

If perch crashes (signal, OOM, bug) while agent pane `%Ax` is displayed in the frame, the frame session becomes orphaned. The next time tmux garbage-collects or the frame is manually killed, `%Ax` is destroyed.

**Mitigation:** `resurrect` (`internal/resurrect`) must detect this case: a shadow-record pane (`PaneKey = "%Ax"`) is absent from `list-panes -a` but the session it was supposed to be in (`TmuxSession`) is absent from `list-sessions`. Currently resurrect checks "boot ID changed" — this is a different failure mode. New reconcile rule: "pane not found in any session, session gone → stranded in dead frame → attempt re-launch."

**Safe window:** the two-step switch puts the agent home first (step 1) before bringing the next agent in (step 2). A crash between steps 1 and 2 leaves both agents home — safe. Only a crash while an agent is displayed (between switches) causes the stranded-pane failure.

### Risk 2 — Race: swap-pane while agent status poll is in flight

`statusPoll` calls `ListPanesAll` while a swap is executing. The pane_id is stable but the `session_name`/`window_name` fields in the snapshot may be momentarily stale (the pane is in transit). The `liveTarget` field in `data.go` (`tmux.WindowTarget(pane.Session, pane.Window)`) would point to the wrong location.

**Mitigation:** address all post-swap tmux commands by `pane_id` (`%N`), never by session:window coordinate. The existing `captureTarget = pane.ID` in `buildItemFromSession` is already correct. `liveTarget` (used for `switch-client` in the old model) becomes unused after the refactor.

### Risk 3 — tmux 3.6 swap-pane cross-session

Verified working on tmux 3.6. The cross-session swap was a documented tmux 2.x feature; it is stable in 3.x. No known regressions in 3.6 changelog for swap-pane.

### Risk 4 — Placeholder pane leak on launch without display

If perch launches an agent (`NewSession`) but the user never selects it, the agent session has its own pane and NO placeholder has ever been swapped in. The first `swapIn` brings the placeholder from the frame — this is fine. But if perch quits without ever displaying that agent, the quit handler finds no placeholder in that agent's session and has nothing to swap — also fine (the agent's process pane is already home). No leak.

### Risk 5 — Multiple simultaneous swaps

The perch TUI is single-threaded (bubbletea event loop). Session switches are dispatched as `tea.Cmd` (goroutines) but selection changes should be serialized in the Update loop via the existing `capturing` guard pattern. A `swapping bool` guard (mirrors `capturing`/`polling`) must prevent concurrent swap sequences from crossing each other's state.

### Risk 6 — `perch attach <query>` concurrent with perch running

When perch is running and an agent is displayed in the frame (its pane is physically in the perch session), a concurrent `perch attach agentA` would attach to agentA's session — which currently holds the placeholder (`sleep`), not the agent process. The user would see a blank sleep pane.

**Resolution:** `perch attach` in M-future should detect this: check if the target agent's pane is in the perch session (not home). If so, either: (a) tell the user to use perch, (b) tell perch to swap it home first, (c) attach to the frame and switch-select. Document as limitation for now.

### FD-01 — Private socket

See §4 (Focus and Keybindings). Decision: default socket in v0.1; private socket as a future opt-in for users who want no-prefix keybindings and are willing to accept the nested-attach UX on inside-tmux launch.

### FD-02 — Placeholder process choice

`sleep infinity` vs `cat` vs `sh -c 'while :; do read; done'` as the placeholder. `sleep infinity` is cleanest (zero CPU, no stdin handling). On some systems `sleep infinity` is unavailable — `sleep 2147483647` is a safe fallback. Alternatively `true` loops would respawn on exit under `repeat-time` tmux option — unnecessary complexity. Use `sleep infinity`.

### FD-03 — Frame session name collision

If the user already has a session named `perch`, startup will fail or reuse it incorrectly. Mitigation: check `HasSession("perch")` on startup and verify it looks like a perch frame (e.g., window named `frame`, sidebar pane has `@perch_frame = "1"` option set). If it's a perch frame, reuse it (resume path). If it's something else, error with a clear message.

---

## 8. Verdict

**The tmux-pane persistent-frame approach is SOUND and buildable on tmux 3.6.**

The `swap-pane` primitive:
- Works across sessions (verified, exit=0)
- Keeps both sessions alive (verified via `list-sessions`)
- Keeps processes alive by pid (verified via `list-panes #{pane_pid}`)
- Is fully interactive after swap (verified via `send-keys` + `capture-pane`)
- Resizes panes correctly on swap (verified dimensions update)
- Auto-migrates the placeholder between frame and agent session (verified — no explicit parking management)

The Q1 vs Q8 tension is **resolved by using swap-pane exclusively** — join-pane is disqualified by Q1, swap-pane satisfies both Q3 (both sessions live) and Q8 (agents survive frame kill, given correct quit sequence).

The primary non-trivial engineering requirements added by this model:
1. TUI must track `currentDisplayedPaneID` and `currentPlaceholderPaneID` in Model state
2. Enter key dispatches `swapIn` (two-step swap) instead of `attachTo` (switch-client)
3. Quit handler must swap displayed agent home before killing frame
4. Resize handling: optionally pre-size parked sessions to avoid SIGWINCH shock
5. `resurrect` needs a new reconcile rule for crash-stranded panes

No new dependencies, no daemon process, no protocol changes. The tmux IS the engine, as specified.

---

*Isolated tmux server `perch-spike` was killed at the end of all experiments: `tmux -L perch-spike kill-server`.*
