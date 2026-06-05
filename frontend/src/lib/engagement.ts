// Engagement logic — pure, framework-free helpers for the chunk-2 "keep you in
// flow" features. Kept out of App.svelte so the edge-detection (the part most
// prone to silent regressions) is unit-testable in isolation.
import type { AgentState } from "./wails";

// States in which the run is NOT settled: the agent is working or needs the human.
// "idle", "done" and "errored" are settled (the turn is over, one way or another).
const ACTIVE_STATES: ReadonlySet<AgentState> = new Set<AgentState>([
  "running",
  "awaiting-approval",
  "awaiting-input",
]);

export function isActiveState(s: AgentState): boolean {
  return ACTIVE_STATES.has(s);
}

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

// #6 — closing ritual. Fires on the EDGE from "some workspace active" to "all
// settled". `armed` is set true by the caller once activity has been seen, so the
// ritual never pops on first load (everything idle) and only celebrates after
// real work. Requires at least one workspace that ended in `done` so a run that
// only errored out doesn't trigger a "session complete" card.
export function ritualShouldFire(
  anyActive: boolean,
  armed: boolean,
  anyDone: boolean,
): boolean {
  return !anyActive && armed && anyDone;
}

export interface RitualStats {
  lines: number;
  files: number;
  sessions: number;
}

export function computeRitualStats(
  diffStats: Record<string, { added: number; removed: number; files?: number }>,
  sessions: number,
): RitualStats {
  let lines = 0;
  let files = 0;
  for (const d of Object.values(diffStats)) {
    lines += d.added + d.removed;
    files += d.files ?? 0;
  }
  return { lines, files, sessions };
}
