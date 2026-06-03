// frontend/src/lib/stores/notifications.svelte.ts
export type Tier = "blocking" | "ambient" | "routine";
export interface Notification {
  id: string; workspaceId: string; tier: Tier;
  title: string; body: string; read: boolean; ts: number;
}

let items = $state<Notification[]>([]);
let dnd   = $state(false);
let _seq  = 0;

export function getItems(): Notification[] { return items; }
export function getDnd():   boolean         { return dnd; }
export function setDnd(v: boolean)          { dnd = v; }

function add(tier: Tier, workspaceId: string, title: string, body: string) {
  if (dnd && tier !== "blocking") return;
  items = [{ id: `notif-${++_seq}`, workspaceId, tier, title, body, read: false, ts: Date.now() }, ...items];
}

export function addBlocking(w: string, t: string, b: string) { add("blocking", w, t, b); }
export function addAmbient (w: string, t: string, b: string) { add("ambient",  w, t, b); }
export function addRoutine (w: string, t: string, b: string) { add("routine",  w, t, b); }

export function markRead(id: string) { items = items.map((n) => n.id === id ? { ...n, read: true } : n); }
export function clearRead()          { items = items.filter((n) => !n.read); }
