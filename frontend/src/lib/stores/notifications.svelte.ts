// frontend/src/lib/stores/notifications.svelte.ts
import { AMBIENT_DISMISS_MS, ROUTINE_DISMISS_MS } from "../constants";
export type Tier = "blocking" | "ambient" | "routine";
export interface Notification {
  id: string; workspaceId: string; tier: Tier;
  title: string; body: string; read: boolean; ts: number;
}

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

export function addBlocking(w: string, t: string, b: string) { add("blocking", w, t, b); }
export function addAmbient (w: string, t: string, b: string) { add("ambient",  w, t, b); }
export function addRoutine (w: string, t: string, b: string) { add("routine",  w, t, b); }

export function markRead(id: string) {
  // Cancel any pending auto-dismiss timer before manual dismiss
  const t = _timers.get(id);
  if (t !== undefined) { clearTimeout(t); _timers.delete(id); }
  items = items.map((n) => n.id === id ? { ...n, read: true } : n);
}
export function clearRead() { items = items.filter((n) => !n.read); }
