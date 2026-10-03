// frontend/src/lib/stores/notifications.svelte.ts
export type Tier = "blocking" | "ambient" | "routine";

// The semantic category a notification represents. The code tags this at
// creation, so the hub can filter on intent instead of re-deriving it from
// tier and title heuristics. Errors and approvals are both tier "blocking",
// so tier alone cannot tell an "Approval needed" notification apart from a
// "Stage failed" notification; the explicit kind can. Callers may pass an
// explicit kind. When a caller omits it, the code derives a safe default
// from the tier and title.
export type Kind = "approval" | "error" | "done" | "info";

export interface Notification {
  id: string; workspaceId: string; tier: Tier; kind: Kind;
  title: string; body: string; read: boolean; ts: number;
  // The agent State that produced this notification, for example
  // "awaiting-input" for a "Question", or "awaiting-approval" for an
  // "Approval needed". The code tags this, so a later transition can
  // supersede exactly the notification that its source state made moot,
  // without re-deriving intent from tier and title. This field is
  // optional: locally authored notifications, such as open errors or
  // branch-switch warnings, carry no source state.
  state?: string;
  // An action the notification offers ("retype-launch"). Unknown values
  // render nothing.
  action?: string;
}

// Hard cap on retained notifications. The code prepends to the hub on
// every agent event, so without a ceiling a long-running cockpit session
// would grow the array, and the docked list it feeds, without bound. When
// the count exceeds the cap, the code drops the oldest entries (the array
// is newest-first). The code never drops an unresolved (unread) blocking
// notification just because it aged past the cap.
export const MAX_NOTIFICATIONS = 500;

let items = $state<Notification[]>([]);
let dnd   = $state(false);
let _seq  = 0;

export function getItems(): Notification[] { return items; }
export function getDnd():   boolean         { return dnd; }
export function setDnd(v: boolean)          { dnd = v; }

// Derive a safe default kind when a caller does not tag one explicitly.
// Errors reach the hub through addBlocking or addAmbient with a "failed"
// or "error" title, so they must classify as "error" and never fall under
// the Approvals filter. An untitled blocking notice is an approval, an
// ambient notice is a completed turn, and a routine notice is background
// info.
function defaultKind(tier: Tier, title: string, state?: string): Kind {
  // The source agent state, when known, says what the notification is about
  // (FEC-23): only an awaiting-approval notice belongs under Approvals, so
  // "Agent exited" or "Question" no longer land there.
  switch (state) {
    case "awaiting-approval": return "approval";
    case "errored":
    case "exited":            return "error";
    case "done":              return "done";
    case "awaiting-input":    return "info";
  }
  if (/error|fail/i.test(title)) return "error";
  if (tier === "blocking") return "approval";
  if (tier === "ambient")  return "done";
  return "info";
}

function add(tier: Tier, workspaceId: string, title: string, body: string, kind?: Kind, state?: string, action?: string) {
  const id = `notif-${++_seq}`;
  // DND silences tiers 2 and 3. It does not drop them. The code still logs
  // them to the hub, so the away catch-up stays complete, but marks them
  // already-read, so they never bump the unread bell badge. That badge is
  // the only interruption these tiers have; OS notifications fire for
  // blocking only. DND never silences blocking (tier 1) notifications.
  // Silencing means muting the interruption while keeping the record.
  const silenced = dnd && tier !== "blocking";
  items = [{ id, workspaceId, tier, kind: kind ?? defaultKind(tier, title, state), title, body, read: silenced, ts: Date.now(), state, action }, ...items];
  trimToCap();

  // No auto-dismiss timer. The hub stays docked; it is not a temporary
  // toast. An unseen ambient or routine event must stay unread until the
  // user actually opens the hub (markAllRead fires on open). A timeout
  // would silently tick the unread badge down for events nobody saw.
}

// Enforce the MAX_NOTIFICATIONS ceiling after a prepend. The array is
// newest-first, so a straight cap keeps the first N (newest) entries and
// drops the tail (oldest). The code avoids silently discarding an
// unresolved (unread) blocking notification just because it aged past the
// cap: those entries move to the front so they survive, and everything
// else obeys the newest-N rule.
function trimToCap() {
  if (items.length <= MAX_NOTIFICATIONS) return;

  const keepBlocking = items.filter((n) => n.tier === "blocking" && !n.read);
  const rest         = items.filter((n) => !(n.tier === "blocking" && !n.read));

  // Blocking-unread entries always survive; the rest fill the remaining
  // budget, newest-first. If unresolved blocking entries alone exceed the
  // cap, the code still keeps all of them (it never drops an unresolved
  // approval or question), and it retains no `rest` entries in that case.
  const budget = Math.max(0, MAX_NOTIFICATIONS - keepBlocking.length);
  const keepRest = rest.slice(0, budget);
  const keep = new Set([...keepBlocking, ...keepRest].map((n) => n.id));

  // Rebuild preserving newest-first order (filter keeps original order).
  items = items.filter((n) => keep.has(n.id));
}

export function addBlocking(w: string, t: string, b: string, kind?: Kind, state?: string, action?: string) { add("blocking", w, t, b, kind, state, action); }
export function addAmbient (w: string, t: string, b: string, kind?: Kind, state?: string) { add("ambient",  w, t, b, kind, state); }
export function addRoutine (w: string, t: string, b: string, kind?: Kind, state?: string) { add("routine",  w, t, b, kind, state); }

export function markRead(id: string) {
  if (!items.some((n) => n.id === id && !n.read)) return;
  items = items.map((n) => n.id === id ? { ...n, read: true } : n);
}

// Mark every item read. The code calls this when the user opens the hub:
// seeing the hub is the catch-up, so the unread badge clears. Items stay
// in the list, marked read; the code does not drop them.
export function markAllRead() {
  // No-op when nothing is unread, so the hub and the badge are not
  // invalidated on every session switch or window focus (FEC-33).
  if (!items.some((n) => !n.read)) return;
  items = items.map((n) => n.read ? n : { ...n, read: true });
}

// Mark read every unread notification for a session the user is now
// looking at. This is non-destructive, like markAllRead and unlike
// dropForWorkspace: the entries stay in the hub history, so the away
// catch-up stays complete. They only stop bumping the unread bell badge,
// because visiting a session is the catch-up for its events. An unresolved
// approval or error therefore also keeps its state-driven sidebar signal,
// which is keyed off ws.state, not off notification read-state. Callers
// gate this call on "the session is active and the window is focused", so
// events that arrive while the user is away still accumulate, and still
// trigger an OS toast, until the user returns.
export function markReadForWorkspace(wsId: string) {
  if (!items.some((n) => n.workspaceId === wsId && !n.read)) return; // FEC-33
  items = items.map((n) => n.workspaceId === wsId && !n.read ? { ...n, read: true } : n);
}

// Drop every notification that belongs to a removed session. Only for a
// genuine removal; a state change uses dropBlockingForWorkspace.
export function dropForWorkspace(wsId: string) {
  if (!items.some((n) => n.workspaceId === wsId)) return;
  items = items.filter((n) => n.workspaceId !== wsId);
}

// Drop a session's still-unread "Question" notification once the agent
// leaves awaiting-input. This function is scoped to blocking notifications
// whose source state was awaiting-input, so a pending "Approval needed"
// notification, of which claude may have several queued for one session
// (state "awaiting-approval"), is never touched. The function is gated on
// !read, so a question the user already saw stays in the hub history. This
// is the targeted cousin of dropForWorkspace, used on the
// awaiting-input-to-other-state edge that the resolved-state
// dropForWorkspace clear does not cover.
export function dropAwaitingInputForWorkspace(wsId: string) {
  dropBlockingForWorkspace(wsId, ["awaiting-input"]);
}

// Drop a session's still-unread blocking notifications whose source state is
// one of `states`, once the condition they asked about is resolved. Use this,
// not dropForWorkspace, on a state edge: everything else (auto-approval
// audit entries, errors, completed turns, the read history) stays in the hub
// for the away catch-up (FEC-6).
export function dropBlockingForWorkspace(wsId: string, states: string[]) {
  const drop = (n: Notification) =>
    n.workspaceId === wsId && n.tier === "blocking" && !n.read && n.state !== undefined && states.includes(n.state);
  if (!items.some(drop)) return;
  items = items.filter((n) => !drop(n));
}

// Withdraw an offered action from a session's notifications, for example
// "retype-launch" once the agent has reported in.
export function clearActionForWorkspace(wsId: string, action: string) {
  if (!items.some((n) => n.workspaceId === wsId && n.action === action)) return;
  items = items.map((n) => n.workspaceId === wsId && n.action === action ? { ...n, action: undefined } : n);
}

export function clearRead() { items = items.filter((n) => !n.read); }
