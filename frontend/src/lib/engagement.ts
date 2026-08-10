// Engagement logic. These are pure, framework-free helpers for the chunk-2
// "keep you in flow" features. They stay out of App.svelte so the
// edge-detection code, the part most prone to silent regressions, is
// unit-testable in isolation.
import type { AgentState } from "./wails";

// #4, auto-focus on awaiting-input. Bring the user to the agent pane only
// when the active session newly enters awaiting-input while the agent view
// is showing. Do not focus for a background session; those signal through
// the sidebar pulse instead. Do not focus when the user is in the editor or
// diff view, because that could pull them off unsaved work. Do not re-fire
// on a state that was already awaiting-input.
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

// The "asking you a question" attention signal is a background cue. It
// exists to pull the user's eye to a session they are not looking at. Once
// a session is the active session and its agent pane is the visible view,
// the user has seen the signal. The signal has then done its job, and the
// code should acknowledge (suppress) it until a fresh question arrives.
// This mirrors shouldFocusAwaitingInput's "user is looking at the agent
// pane" condition, but without the edge requirement: simply being the
// active session on the agent view is enough to acknowledge the signal.
export function isViewingAgentPane(
  wsId: string,
  activeId: string | null,
  view: string,
): boolean {
  return wsId === activeId && view === "agent";
}
