// frontend/src/lib/stores/notifications.svelte.ts
import { AMBIENT_DISMISS_MS, ROUTINE_DISMISS_MS } from "../constants";
export type Tier = "blocking" | "ambient" | "routine";
export interface Notification {
  id: string; workspaceId: string; tier: Tier;
  title: string; body: string; read: boolean; ts: number;
}

// Hard cap on retained notifications. The hub is prepended to on every agent
// event, so without a ceiling a long-running cockpit session grows the array
// (and the docked list it feeds) without bound. When we exceed the cap we drop
// the OLDEST entries (the array is newest-first), always clearing each dropped
// id's pending auto-dismiss timer so trimming can never leak a timer.
export const MAX_NOTIFICATIONS = 500;

let items = $state<Notification[]>([]);
let dnd   = $state(false);
let _seq  = 0;

// Auto-dismiss timeouts keyed by notification id.
// Ambient notifications dismiss after 6 s; routine after 3 s.
// Blocking notifications NEVER auto-dismiss.
const _timers = new Map<string, ReturnType<typeof setTimeout>>();

export function getItems(): Notification[] { return items; }
export function getDnd():   boolean         { return dnd; }
export function setDnd(v: boolean)          { dnd = v; }

function add(tier: Tier, workspaceId: string, title: string, body: string) {
  const id = `notif-${++_seq}`;
  // DND silences tiers 2-3: it does NOT drop them. They are still logged to the
  // hub so the away catch-up stays complete, but recorded as already-read so they
  // never bump the unread bell badge (the only interruption these tiers have; OS
  // notifications fire for blocking only). Blocking (tier 1) is never silenced.
  // DND mutes tiers 2-3: mute = silence the interruption, keep the record.
  const silenced = dnd && tier !== "blocking";
  items = [{ id, workspaceId, tier, title, body, read: silenced, ts: Date.now() }, ...items];
  trimToCap();

  // Auto-dismiss for non-blocking tiers that were actually surfaced.
  // Silenced items are already read, so no timer is needed.
  if (tier !== "blocking" && !silenced) {
    const delay = tier === "ambient" ? AMBIENT_DISMISS_MS : ROUTINE_DISMISS_MS;
    const t = setTimeout(() => {
      _timers.delete(id);
      markRead(id);
    }, delay);
    _timers.set(id, t);
  }
}

// Enforce the MAX_NOTIFICATIONS ceiling after a prepend. The array is
// newest-first, so a straight cap keeps the first N (newest) and drops the
// tail (oldest). We keep it minimal but avoid silently discarding an
// unresolved (unread) blocking notification just because it aged past the cap:
// those are partitioned to the front so they survive; everything else obeys
// the newest-N rule. For every entry we drop we clear its pending
// auto-dismiss timer (mirrors markRead / dropForWorkspace) so no timer leaks.
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

  // Clear timers for everything being dropped so trimming cannot leak a timer.
  for (const n of items) {
    if (!keep.has(n.id)) {
      const t = _timers.get(n.id);
      if (t !== undefined) { clearTimeout(t); _timers.delete(n.id); }
    }
  }

  // Rebuild preserving newest-first order (filter keeps original order).
  items = items.filter((n) => keep.has(n.id));
}

export function addBlocking(w: string, t: string, b: string) { add("blocking", w, t, b); }
export function addAmbient (w: string, t: string, b: string) { add("ambient",  w, t, b); }
export function addRoutine (w: string, t: string, b: string) { add("routine",  w, t, b); }

export function markRead(id: string) {
  // Cancel any pending auto-dismiss timer before manual dismiss
  const t = _timers.get(id);
  if (t !== undefined) { clearTimeout(t); _timers.delete(id); }
  items = items.map((n) => n.id === id ? { ...n, read: true } : n);
}

// Mark every item read and cancel all pending auto-dismiss timers.
// Called when the hub is OPENED — seeing the hub is the catch-up, so the
// unread badge clears. Items stay in the list (read), they are not dropped.
export function markAllRead() {
  for (const t of _timers.values()) clearTimeout(t);
  _timers.clear();
  items = items.map((n) => n.read ? n : { ...n, read: true });
}

// Drop every notification belonging to a removed workspace, cancelling any
// pending auto-dismiss timer for the dropped items so they never fire late.
export function dropForWorkspace(wsId: string) {
  for (const n of items) {
    if (n.workspaceId === wsId) {
      const t = _timers.get(n.id);
      if (t !== undefined) { clearTimeout(t); _timers.delete(n.id); }
    }
  }
  items = items.filter((n) => n.workspaceId !== wsId);
}

export function clearRead() { items = items.filter((n) => !n.read); }

// Test-only: snapshot of the pending auto-dismiss timer ids. Used to assert
// that trimming/dropping never leaks a timer for a notification no longer held.
export function _pendingTimerIds(): string[] { return [..._timers.keys()]; }
