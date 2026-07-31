// Engagement logic — pure, framework-free helpers for the chunk-2 "keep you in
// flow" features. Kept out of App.svelte so the edge-detection (the part most
// prone to silent regressions) is unit-testable in isolation.
import type { AgentState } from "./wails";

// #4 — auto-focus on awaiting-input. Bring the user to the agent pane ONLY when
// THE ACTIVE workspace *newly* enters awaiting-input while the agent view is
// showing. Never for a background workspace (those signal via the sidebar pulse),
// never when the user is in the editor/diff view (would risk yanking them off
// unsaved work), and never re-firing on a state that was already awaiting-input.
export function shouldFocusAwaitingInput(
  prev: AgentState,
  next: AgentState,
  wsId: string,
  activeId: string | null,
  view: string,
): boolean {
  return (
    next === "awaiting-input" &&
    prev !== "awaiting-input" &&
    wsId === activeId &&
    view === "agent"
  );
}

// The "asking you a question" signal is a BACKGROUND cue: it exists to pull the
// user's eye to a session they are not looking at. Once a session is the active
// session AND its agent pane is the visible view, the user has seen it, so the
// signal has done its job and should be acknowledged (suppressed) until a fresh
// question arrives. This mirrors shouldFocusAwaitingInput's "user is looking at
// the agent pane" condition but WITHOUT the edge requirement: simply being the
// active session on the agent view is enough to acknowledge.
export function isViewingAgentPane(
  wsId: string,
  activeId: string | null,
  view: string,
): boolean {
  return wsId === activeId && view === "agent";
}
