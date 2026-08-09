[perch](../README.md) / Backend packages

# internal/

The backend packages. `cmd/perch` wires them together and `app/` exposes them to
the frontend. For how they interact, see [ARCHITECTURE.md](../ARCHITECTURE.md).

| Package | What it owns | Notable exports |
|---------|--------------|-----------------|
| `pty` | One direct pseudo-terminal per pane, with no multiplexer. Runs a login shell, forwards output as Wails events, and routes keystrokes and resize back. | `Bridge`, `Spawn`, `LoginShellArgv` |
| `registry` | The thread-safe, file-backed session registry under the XDG config directory, with atomic writes. | `Store` (`Load`, `List`, `Get`, `Upsert`, `Remove`), `Workspace`, `DefaultConfigDir`, `ConfigDirMode` |
| `git` | git operations behind a `proc.Runner`: ref validation, worktree management, and diff and hunk staging. | `ValidRef`, `AddWorktree`, `AddWorktreeExisting`, `RemoveWorktree`, `WorktreeDirty`, `BranchMerged`, `DiffStat`, `Hunks`, `StageHunk`, `DiscardHunk`, and the `Err*` sentinels |
| `agent` | The Monitor and Adapter seams, with concrete monitors for the two agents. | `Monitor`, `Adapter`, `NewMonitor`, `NewClaude`, `NewOpencode`, `State`, `Caps`, `Event` |
| `hooklistener` | The per-session loopback listener that receives Claude hook posts and blocks `PreToolUse` until a verdict. | `Listener`, `New`, `LoopbackHost`, `HookEvent`, `Decision` |
| `envsync` | The one-per-app loopback listener behind `perch reload`: a per-workspace bearer token, an in-memory environment delta, never persisted. | `Listener`, `New`, `SyncFunc`, `SyncRequest`, `EnvURL`, `EnvToken`, `EnvWS` |
| `notify` | Desktop notifications over D-Bus, with a `notify-send` fallback. | `Notifier`, `New`, `FakeNotifier` |
| `fs` | gitignore-aware directory listing, a recursive change watcher, and atomic file writes. | `Node`, `ListDir`, `Watch`, `ReadFile`, `WriteFile`, `RevealInFiles` |
| `discover` | Finds git repositories under the roots, frecency-ordered, grouped with their worktrees. | `Scan`, `Projects`, `Options` |
| `doctor` | The `perch doctor` health check for `go`, `git`, and the agents, with OS calls injected for testing. | `Run`, `RealSystem`, `ParseToolVersions` |
| `config` | The single-layer global TOML config that exposes the project roots. | `Config`, `Load`, `DefaultGlobalPath` |
| `model` | Shared domain vocabulary as pure data, no I/O. | `Tool`, `ToolClaude`, `ToolOpencode`, `Project`, `Tree` |
| `proc` | The subprocess seam every shell-out goes through. | `Runner`, `ExecRunner`, `FakeRunner` |

## Conventions

- Every shell-out goes through `proc.Runner`, so tests inject `FakeRunner` and
  never spawn a process. See [the Runner principle](../CONTRIBUTING.md#the-runner-principle).
- Slice-returning methods build with `make([]T, 0)` so they marshal to `[]`, not
  `null`, across the bridge.
- Config and registry paths honor `$XDG_CONFIG_HOME`, computed once in
  `registry.DefaultConfigDir()`.
