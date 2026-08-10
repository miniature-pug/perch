[perch](../README.md) / Frontend

# frontend/

The cockpit's interface: a Svelte 5 SPA, built by Vite, embedded in the binary
and rendered by WebKit2GTK. It draws agent terminals with xterm.js and the
editor with CodeMirror 6. For how the frontend talks to the backend, see
[ARCHITECTURE.md](../ARCHITECTURE.md). For what the frontend does, see the
[usage guide](../docs/usage.md).

## Layout

```
frontend/
|-- src/
|   |-- main.ts            mount Root into #app, load the stylesheets
|   |-- Root.svelte        the error boundary; falls back to CrashScreen
|   |-- App.svelte         the shell: layout, the keymap, the command registry, all wiring
|   |-- lib/               components, stores, and the backend seam
|   |-- tokens/            tokens.css, themes.css, glass.css
|   `-- reset.css
|-- vite.config.ts
|-- playwright.config.ts
`-- package.json
```

`src/lib/` holds the components (`*.svelte`), the runes stores in `lib/stores/`,
`wails.ts` (the typed seam over the backend bindings and events), `constants.ts`
(timers, limits, defaults, and the theme, density, and font lists), `actions.ts`
(`focusOnMount`, `countUp`), and helpers for previews and the editor.

## Components

| Component | Role |
|-----------|------|
| `Sidebar` | The session list and the new-session call to action |
| `Stage` | The agent, code, and diff view selector and the split-pane host |
| `Terminal` | An xterm.js pane over a pty |
| `ShellDrawer` | A collapsible drawer wrapping a Terminal |
| `Editor` | The CodeMirror editor, with a git gutter, search, and save |
| `FileTree` | The worktree file navigator and its context menu |
| `Preview` | Read-only markdown, Mermaid, and image preview |
| `DiffView` | The changed-file list and per-hunk stage, discard, and send |
| `DragDrop` | A drop zone that writes files or text into a pty |
| `ApprovalCard` | The docked approval card, with Allow, Deny, Always, and Approve all/Deny all |
| `NotificationHub` | The notification panel, with filters and do-not-disturb |
| `NewSessionDialog` | The create-session modal |
| `ConfirmDialog` | The generic confirm modal |
| `HelpDialog` | The shortcut and about modal |
| `SettingsPanel` | Appearance, notifications, and always-allow rules |
| `CleanupPanel` | The stale-session table |
| `CommandPalette` | The fuzzy command palette |
| `MenuBar` | The top menu bar and the notification bell |
| `ThemeProvider` | Applies the theme, density, glass, and font to the document |
| `CrashScreen` | The full-screen error fallback |

## State

The app holds its local state in `App.svelte` with Svelte 5 runes. Four stores
in `lib/stores/` hold the rest:

- `layout` keeps the pane sizes, the active view, and collapse state, and saves a
  debounced JSON blob through `SaveLayout`.
- `mode` holds the current keyboard mode (normal, terminal, or command).
  `mode` is not persisted.
- `settings` holds the theme, density, font, do-not-disturb, glass, and
  always-allow rules, and writes through `SaveSettings`.
- `notifications` holds the hub items and the do-not-disturb flag, with
  auto-dismiss timers for the lower tiers.

The settings defaults here mirror the Go defaults in `app/app.go`. The two
sides share no module, so contributors keep them in sync by hand.

## Build

| Command | What it runs |
|---------|--------------|
| `npm run dev` | The Vite dev server |
| `npm run build` | `vite build` into `dist/` |
| `npm test` | The vitest unit suites |
| `npm run check` | `tsc --noEmit` |
| `npm run test:e2e` | Build, then the Playwright suite |

Pinned versions: Svelte `5.56.1`, Vite `8.0.10`, vitest `4.1.5`, Playwright
`1.60.0`. The Go build embeds `dist/` through `//go:embed all:frontend/dist`, so
`make gui-build` runs this build before compiling the binary. Run the suites
through `make test-front` and `make test-e2e` so they use the pinned container
toolchain.
