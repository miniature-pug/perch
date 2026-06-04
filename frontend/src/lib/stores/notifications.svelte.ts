// frontend/src/lib/stores/notifications.svelte.ts
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
// Blocking notifications NEVER auto-dismiss (spec §8).
const _timers = new Map<string, ReturnType<typeof setTimeout>>();

export function getItems(): Notification[] { return items; }
export function getDnd():   boolean         { return dnd; }
export function setDnd(v: boolean)          { dnd = v; }

function add(tier: Tier, workspaceId: string, title: string, body: string) {
  if (dnd && tier !== "blocking") return;
  const id = `notif-${++_seq}`;
  items = [{ id, workspaceId, tier, title, body, read: false, ts: Date.now() }, ...items];

  // Auto-dismiss for non-blocking tiers (spec §8 "ambient → toast 5-7s")
  if (tier !== "blocking") {
    const delay = tier === "ambient" ? 6000 : 3000;
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
