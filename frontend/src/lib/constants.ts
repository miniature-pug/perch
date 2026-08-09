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
export const LAYOUT_SAVE_DEBOUNCE_MS = 300;
export const UNDO_REMOVE_DELAY_MS = 6000;
// How long a multi-key chord prefix (e.g. the `g` in `g d`) stays armed before it
// auto-clears, so a stray `g` never silently swallows the next unrelated keystroke
// and the transient "g…" indicator does not linger (F54).
export const GCHORD_TIMEOUT_MS = 1500;
// Fallback for the count-up duration when the CSS token --perch-dur-countup
// cannot be read (jsdom / no computed styles). Mirrors that token's value.
export const COUNTUP_FALLBACK_MS = 380;

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
// Canonical agent identifiers sent to the backend (mirror Go's model.ToolClaude
// / model.ToolOpencode). Single-sourced so option values and dispatch never
// drift from bare string literals.
export const AGENT_CLAUDE = "claude";
export const AGENT_OPENCODE = "opencode";
export const DEFAULT_AGENT = AGENT_CLAUDE;
// ── Settings option lists ────────────────────────────────────────────────────
export const THEMES = [
  "gruvbox", "tokyo-night", "catppuccin", "dracula", "nord",
  "rose-pine", "one-dark", "perch-cyan", "light",
] as const;
export const DENSITIES = ["dense", "comfortable", "ultra"] as const;
export const FONTS = ["geist", "ibm-plex", "inter"] as const;
export type Density = (typeof DENSITIES)[number];

// ── Drag-and-drop MIME types ─────────────────────────────────────────────────
export const MIME_SESSION = "application/x-perch-session";
export const MIME_TEXT = "application/x-perch-text";

// ── Editor → agent mention protocol ──────────────────────────────────────────
export const MENTION_PREFIX = "@mention:";

// ── localStorage keys ────────────────────────────────────────────────────────
export const STORAGE_CMD_RECENTS = "perch:cmd-recents";

// ── Per-worktree color identity palette ──────────────────────────────────────
// Theme-agnostic accent hues (not for text) — moderately saturated, mid-
// lightness HSL values that remain visually distinct on all 9 app themes
// (dark and light).  Used for sidebar accent dots/borders and notification
// accents.  Hues spaced 45° apart; saturation 55%, lightness 60%.
export const WORKTREE_COLORS = [
  "hsl(  0, 55%, 60%)", // red
  "hsl( 45, 55%, 60%)", // amber
  "hsl( 90, 55%, 60%)", // lime
  "hsl(135, 55%, 60%)", // teal-green
  "hsl(180, 55%, 60%)", // cyan
  "hsl(225, 55%, 60%)", // cornflower blue
  "hsl(270, 55%, 60%)", // violet
  "hsl(315, 55%, 60%)", // rose
] as const;

/**
 * Worktree accent color.
 *
 * Prefer a position-based assignment: when the caller supplies the session's
 * index in the list, the first WORKTREE_COLORS.length sessions each get a
 * distinct palette entry (round-robin, wrapping past the palette size). This
 * eliminates the djb2 hash's birthday-paradox collisions (~59% chance two of
 * four sessions share a color when mapping arbitrary ids into 8 buckets), so
 * the common case of a handful of open sessions is always visually distinct.
 *
 * When no index is supplied (e.g. a notification that only knows a workspace
 * id, or a caller that has no positional context) it falls back to a stable
 * djb2 hash (hash = hash * 33 ^ charCode) over every character of `id`, mapping
 * the unsigned result into WORKTREE_COLORS.  That fallback is deterministic:
 * the same `id` always produces the same color across reloads and processes;
 * empty string maps to index 0.
 */
export function worktreeColor(id: string, index?: number): string {
  // Position-based (round-robin) assignment — guarantees the first
  // WORKTREE_COLORS.length sessions are pairwise distinct.
  if (index !== undefined && Number.isInteger(index) && index >= 0) {
    return WORKTREE_COLORS[index % WORKTREE_COLORS.length];
  }
  let hash = 5381;
  for (let i = 0; i < id.length; i++) {
    hash = (hash * 33) ^ id.charCodeAt(i);
  }
  // Force unsigned 32-bit before modulo so negative values are handled safely.
  return WORKTREE_COLORS[(hash >>> 0) % WORKTREE_COLORS.length];
}

/**
 * Human-readable relative age of an ISO-8601 timestamp: "today", "1d ago",
 * "5d ago".  Returns "" for an empty string or an unparseable timestamp (the
 * isNaN/empty guard).
 *
 * Single source of truth for the sidebar session-age label and the cleanup
 * panel's last-active column, which previously diverged (the sidebar rendered
 * "1d ago" while the cleanup panel rendered "yesterday", and the cleanup panel
 * lacked the empty/invalid guard).  Standardized on the numeric "1d ago" form
 * so every bucket reads the same way (today, 1d ago, 2d ago, …).
 */
export function formatRelativeAge(isoOrEmpty: string): string {
  if (!isoOrEmpty) return "";
  const d = new Date(isoOrEmpty);
  if (isNaN(d.getTime())) return "";
  const days = Math.floor((Date.now() - d.getTime()) / 86400000);
  if (days < 1) return "today";
  if (days === 1) return "1d ago";
  return `${days}d ago`;
}
