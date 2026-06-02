# Perch — Mermaid Diagrams

Render any `.mmd` file with a Mermaid-capable viewer (GitHub renders `.mmd` files in fenced code blocks; VS Code with the Mermaid Preview extension also works).

| File | What it shows |
|------|---------------|
| [architecture.mmd](architecture.mmd) | Full package map: `cmd/perch` dispatch, the Wails GUI layer (`app/`, `internal/pty`, `frontend/`), CLI sub-commands, data/config layer, discovery, agent adapters, and the `proc.Runner` shell-out seam through which all tmux and git calls flow. |
| [status-sequence.mmd](status-sequence.mmd) | Full status pipeline as a sequence diagram: agent hook fires `perch status set` → writes `@perch_pane_status` on the tmux pane → backend poller reads it every `refresh_ms` → `sessions-changed` event → GUI sidebar glyph rendered (🤖 working / 💬 waiting / ✓ done / ● live / ○ idle); includes the auto-clear-on-tab-open note. |
| [worktree-lifecycle.mmd](worktree-lifecycle.mmd) | Two-path flowchart: CREATE — preflight, trust gate for `post_create` hooks, `git worktree add`, file seed, hooks, state record, agent launch; REMOVE — confirm, trust gate for `pre_remove` hooks, `git worktree remove` (force if dirty), kill window, remove state record. Trust gates are explicit at each decision point. |
| [discovery-state.mmd](discovery-state.mmd) | Discovery pipeline (`roots` → scan → deduplicate → worktrees → frecency sort → GUI session list) and the two JSON state stores (`state.json` for mappings + frecency, `windows/<k>.json` per live window); shows how the `BootID` in each window record feeds the resurrect KEEP/PRUNE/RESTORE classifier. |
