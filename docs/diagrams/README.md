# Perch — Mermaid Diagrams

Render any `.mmd` file with a Mermaid-capable viewer (GitHub renders `.mmd` files in fenced code blocks; VS Code with the Mermaid Preview extension also works).

| File | What it shows |
|------|---------------|
| [architecture.mmd](architecture.mmd) | Full package map: `cmd/perch` dispatch, core runtime (`tui`, `frame`), CLI sub-commands, data/config layer, discovery, agent adapters, and the `proc.Runner` shell-out seam through which all tmux and git calls flow. |
| [frame-swap.mmd](frame-swap.mmd) | The persistent tmux frame model: the `perch` session with its sidebar pane (TUI) and main pane slot; per-agent detached sessions; the placeholder pane; the swap-pane operation on select (↵) and the swap-home-then-kill sequence on quit (`q`). |
| [status-sequence.mmd](status-sequence.mmd) | Full status pipeline as a sequence diagram: agent hook fires `perch status set` → writes `@perch_pane_status` on the tmux pane → TUI `statusPoll` reads it every `refresh_ms` → list glyph rendered (🤖 working / 💬 waiting / ✓ done / ● live / ○ idle); includes the auto-clear-on-focus note. |
| [worktree-lifecycle.mmd](worktree-lifecycle.mmd) | Two-path flowchart: CREATE (`w` key) — preflight, trust gate for `post_create` hooks, `git worktree add`, file seed, hooks, state record, agent launch; REMOVE (`d` key) — confirm, trust gate for `pre_remove` hooks, `git worktree remove` (force if dirty), kill window, remove state record. Trust gates are explicit at each decision point. |
| [discovery-state.mmd](discovery-state.mmd) | Discovery pipeline (`roots` → scan → deduplicate → worktrees → frecency sort → TUI list) and the two JSON state stores (`state.json` for mappings + frecency, `windows/<k>.json` per live window); shows how the `BootID` in each window record feeds the resurrect KEEP/PRUNE/RESTORE classifier. |
