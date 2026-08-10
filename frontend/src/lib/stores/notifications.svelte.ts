// frontend/src/lib/stores/notifications.svelte.ts
export type Tier = "blocking" | "ambient" | "routine";

// The semantic category a notification represents, tagged at CREATION so the
// hub filters on intent rather than re-deriving it from tier/title heuristics.
// Errors and approvals are both tier "blocking", so tier alone cannot tell an
// "Approval needed" apart from a "Stage failed"; the explicit kind can. Callers
// may pass an explicit kind; when omitted we derive a safe default from the
// tier and title.
export type Kind = "approval" | "error" | "done" | "info";

export interface Notification {
  id: string; workspaceId: string; tier: Tier; kind: Kind;
  title: string; body: string; read: boolean; ts: number;
}

// Hard cap on retained notifications. The hub is prepended to on every agent
// event, so without a ceiling a long-running cockpit session grows the array
// (and the docked list it feeds) without bound. When we exceed the cap we drop
// the OLDEST entries (the array is newest-first), while never dropping an
// unresolved (unread) blocking notification just because it aged past the cap.
export const MAX_NOTIFICATIONS = 500;

let items = $state<Notification[]>([]);
let dnd   = $state(false);
let _seq  = 0;

export function getItems(): Notification[] { return items; }
export function getDnd():   boolean         { return dnd; }
export function setDnd(v: boolean)          { dnd = v; }

// Derive a safe default kind when a caller does not tag one explicitly. Errors
// reach the hub through addBlocking/addAmbient with a "failed"/"error" title,
// so they must classify as "error" and never fall under the Approvals filter;
// an untitled blocking notice is an approval, ambient is a completed turn, and
// routine is background info.
function defaultKind(tier: Tier, title: string): Kind {
  if (/error|fail/i.test(title)) return "error";
  if (tier === "blocking") return "approval";
  if (tier === "ambient")  return "done";
  return "info";
}

function add(tier: Tier, workspaceId: string, title: string, body: string, kind?: Kind) {
  const id = `notif-${++_seq}`;
  // DND silences tiers 2-3: it does NOT drop them. They are still logged to the
  // hub so the away catch-up stays complete, but recorded as already-read so they
  // never bump the unread bell badge (the only interruption these tiers have; OS
  // notifications fire for blocking only). Blocking (tier 1) is never silenced.
  // DND mutes tiers 2-3: mute = silence the interruption, keep the record.
  const silenced = dnd && tier !== "blocking";
  items = [{ id, workspaceId, tier, kind: kind ?? defaultKind(tier, title), title, body, read: silenced, ts: Date.now() }, ...items];
  trimToCap();

  // No auto-dismiss timer: the hub is a docked panel, not a transient toast, so
  // an unseen ambient/routine event must stay UNREAD until the user actually
  // opens the hub (markAllRead fires on open). Timing out to read while the hub
  // is closed would silently tick the unread badge down for events nobody saw.
}

// Enforce the MAX_NOTIFICATIONS ceiling after a prepend. The array is
// newest-first, so a straight cap keeps the first N (newest) and drops the
// tail (oldest). We avoid silently discarding an unresolved (unread) blocking
// notification just because it aged past the cap: those are partitioned to the
// front so they survive; everything else obeys the newest-N rule.
function trimToCap() {
  if (items.length <= MAX_NOTIFICATIONS) return;

  const keepBlocking = items.filter((n) => n.tier === "blocking" && !n.read);
  const rest         = items.filter((n) => !(n.tier === "blocking" && !n.read));

  // Blocking-unread always survive; the rest fill the remaining budget,
  // newest-first. If unresolved blocking alone exceed the cap they are all
  // still kept (never drop an unresolved approval/question), and no `rest`
  // entries are retained.
  const budget = Math.max(0, MAX_NOTIFICATIONS - keepBlocking.length);
  const keepRest = rest.slice(0, budget);
  const keep = new Set([...keepBlocking, ...keepRest].map((n) => n.id));

  // Rebuild preserving newest-first order (filter keeps original order).
  items = items.filter((n) => keep.has(n.id));
}

export function addBlocking(w: string, t: string, b: string, kind?: Kind) { add("blocking", w, t, b, kind); }
export function addAmbient (w: string, t: string, b: string, kind?: Kind) { add("ambient",  w, t, b, kind); }
export function addRoutine (w: string, t: string, b: string, kind?: Kind) { add("routine",  w, t, b, kind); }

export function markRead(id: string) {
  items = items.map((n) => n.id === id ? { ...n, read: true } : n);
}

// Mark every item read. Called when the hub is OPENED — seeing the hub is the
// catch-up, so the unread badge clears. Items stay in the list (read), they are
// not dropped.
export function markAllRead() {
  items = items.map((n) => n.read ? n : { ...n, read: true });
}

// Mark read every unread notification for a workspace the user is now looking at.
// Non-destructive (mirrors markAllRead, NOT dropForWorkspace): the entries stay in
// the hub history so the away catch-up remains complete, they just stop bumping the
// unread bell badge — visiting a session IS the catch-up for its events. An
// unresolved approval/error therefore also keeps its state-driven sidebar signal
// (that is keyed off ws.state, not on notification read-state). Callers gate this
// on "the session is active AND the window is focused" so events that arrive while
// the user is away still accumulate (and still OS-toast) until they return.
export function markReadForWorkspace(wsId: string) {
  items = items.map((n) => n.workspaceId === wsId && !n.read ? { ...n, read: true } : n);
}

// Drop every notification belonging to a removed workspace.
export function dropForWorkspace(wsId: string) {
  items = items.filter((n) => n.workspaceId !== wsId);
}

export function clearRead() { items = items.filter((n) => !n.read); }
