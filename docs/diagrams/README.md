# Perch — Mermaid Diagrams

Render any `.mmd` file with a Mermaid-capable viewer (GitHub renders `.mmd` files in fenced code blocks; VS Code with the Mermaid Preview extension also works).

| File | What it shows |
|------|---------------|
| [architecture.mmd](architecture.mmd) | Component graph: `cmd/perch` dispatch, the Wails GUI layer (`app/`, `internal/pty` direct-pty bridge, `frontend/`), the agent Monitor seam (ClaudeMonitor → hooklistener, OpencodeMonitor → serve/SSE), CLI sub-commands, the registry/config layer, discovery, and the `proc.Runner` shell-out seam through which git calls flow. |
| [status-sequence.mmd](status-sequence.mmd) | Hook-listener approval sequence: claude agent POSTs `PreToolUse` to its loopback hooklistener (blocks) → `app.App` emits `agent:event` → Svelte ApprovalCard → user clicks Allow → `App.Approve` → `hooklistener.Decide` unblocks the agent → state-update event re-renders the UI. opencode is equivalent over the serve SSE stream. |
| [worktree-lifecycle.mmd](worktree-lifecycle.mmd) | Two-path flowchart: CREATE — preflight, trust gate for `post_create` hooks, `git worktree add`, file seed, hooks, register in `workspaces.json`, open (spawn direct pty + start Monitor); REMOVE — confirm, trust gate for `pre_remove` hooks, tear down pty + Monitor (closes hooklistener, strips hook entries), `git worktree remove` (force if dirty), remove registry record. Trust gates are explicit at each decision point. |
| [discovery-state.mmd](discovery-state.mmd) | Discovery pipeline (`roots` → scan → worktrees → blacklist filter → GUI workspace list) and the `workspaces.json` registry (plus `settings.json` / `layout.json`); shows how selecting a workspace drives `OpenWorkspace` (direct pty + Monitor) and how `lastSessionID` feeds agent resume. |
