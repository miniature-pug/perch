// frontend/src/lib/constants.ts
//
// Single source of truth for frontend tuning values: timers, limits, layout
// defaults/clamps, settings defaults, drag-and-drop MIME types, the editor
// mention protocol, and localStorage keys.
//
// Visual tokens (color, spacing, type, motion, shadow, stacking order) live in
// CSS at frontend/src/tokens/*.css — not here. Wails event names live in
// wails.ts (the IPC seam). The settings defaults below MIRROR the Go source of
// truth in app/app.go (the GetSettings absent-file branch); the cross-boundary
// duplication is inherent (no shared module across IPC) — keep them in sync.

// ── Timers (milliseconds) ───────────────────────────────────────────────────
export const AMBIENT_DISMISS_MS = 6000;
export const ROUTINE_DISMISS_MS = 3000;
export const LAYOUT_SAVE_DEBOUNCE_MS = 300;
export const UNDO_REMOVE_DELAY_MS = 6000;

// ── Limits / caps ────────────────────────────────────────────────────────────
export const CMD_RECENCY_MAX = 20;
export const TERMINAL_SCROLLBACK = 10000;
export const PTY_MAX_DIM = 65535; // pty cols/rows hard cap (uint16 max)

// ── Layout defaults & resize clamps (px) ─────────────────────────────────────
export const DEFAULT_SIDEBAR_W = 240;
export const DEFAULT_SHELL_H = 200;
export const SIDEBAR_MIN_W = 160;
export const SIDEBAR_MAX_W = 800;
export const SHELL_MIN_H = 80;
export const SHELL_MAX_H = 800;
export const RESIZE_STEP_PX = 16;

// ── Settings defaults (mirror app/app.go GetSettings absent-file branch) ──────
export const DEFAULT_THEME = "gruvbox";
export const DEFAULT_DENSITY = "dense";
export const DEFAULT_FONT = "geist";
export const DEFAULT_AGENT = "claude";
export const DEFAULT_MODEL = "claude-sonnet-4-5";

// ── Settings option lists ────────────────────────────────────────────────────
export const THEMES = [
  "gruvbox", "tokyo-night", "catppuccin", "dracula", "nord",
  "rose-pine", "one-dark", "perch-cyan", "light",
] as const;
export const DENSITIES = ["dense", "comfortable", "ultra"] as const;
export const FONTS = ["geist", "ibm-plex", "inter"] as const;
export type ThemeName = (typeof THEMES)[number];
export type Density = (typeof DENSITIES)[number];

// ── Drag-and-drop MIME types ─────────────────────────────────────────────────
export const MIME_SESSION = "application/x-perch-session";
export const MIME_TEXT = "application/x-perch-text";

// ── Editor → agent mention protocol ──────────────────────────────────────────
export const MENTION_PREFIX = "@mention:";

// ── localStorage keys ────────────────────────────────────────────────────────
export const STORAGE_CMD_RECENTS = "perch:cmd-recents";
