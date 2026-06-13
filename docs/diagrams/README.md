[perch](../../README.md) / [Docs](../README.md) / Diagrams

# Diagrams

Four Mermaid diagrams describe how perch fits together. GitHub renders `.mmd`
files, and the Mermaid Preview extension renders them in VS Code.

| File | What it shows |
|------|---------------|
| [architecture.mmd](architecture.mmd) | The component graph: CLI dispatch in `cmd/perch`, the Wails GUI layer, the Monitor seam (ClaudeMonitor to its hooklistener, OpencodeMonitor to serve and SSE), the registry and config, discovery, notifications, and the `proc.Runner` shell-out seam |
| [status-sequence.mmd](status-sequence.mmd) | The approval sequence: Claude posts a blocking `PreToolUse` to its loopback listener, the app emits an `agent:event`, the approval card takes your verdict, `hooklistener.Decide` unblocks the agent, and a state event re-renders the UI. It also shows the lifecycle states and the question signal |
| [worktree-lifecycle.mmd](worktree-lifecycle.mmd) | The three paths: create (validate, derive the worktree path, `git worktree add`, register, open), remove (confirm with an undo window, tear down, `git worktree remove`, keep the branch), and stale cleanup (the banner and the cleanup panel) |
| [discovery-state.mmd](discovery-state.mmd) | The discovery pipeline (roots, scan, worktrees, the session list) and the JSON stores, and how opening a session drives `OpenWorkspace` and how `lastSessionID` feeds resume |

The diagrams track the system described in [ARCHITECTURE.md](../../ARCHITECTURE.md).
