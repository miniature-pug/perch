// frontend/src/lib/stores/notifications.test.ts
import { describe, it, expect, beforeEach } from "vitest";

describe("notification store", () => {
  beforeEach(() => { vi.resetModules(); });

  it("addBlocking adds a tier-1 item", async () => {
    const { addBlocking, getItems } = await import("./notifications.svelte");
    addBlocking("ws_a", "Approval needed", "Claude wants to run bash");
    const items = getItems();
    expect(items[0].tier).toBe("blocking");
    expect(items[0].read).toBe(false);
  });

  it("DND silences tiers 2-3 (logged as read) but never tier 1", async () => {
    const { addBlocking, addAmbient, addRoutine, setDnd, getItems } =
      await import("./notifications.svelte");
    setDnd(true);
    addBlocking("ws_a", "Approval", "urgent");
    addAmbient("ws_b", "Done", "quiet");
    addRoutine("ws_c", "File", "bg");
    const items = getItems();
    // All three are still logged to the hub. DND silences notifications, but does not
    // drop them, so the away catch-up stays complete.
    expect(items.filter((i) => i.tier === "blocking")).toHaveLength(1);
    expect(items.filter((i) => i.tier === "ambient")).toHaveLength(1);
    expect(items.filter((i) => i.tier === "routine")).toHaveLength(1);
    // The code records silenced tiers 2-3 as already-read (no unread-badge bump).
    // Blocking stays unread (it always surfaces).
    expect(items.find((i) => i.tier === "blocking")!.read).toBe(false);
    expect(items.find((i) => i.tier === "ambient")!.read).toBe(true);
    expect(items.find((i) => i.tier === "routine")!.read).toBe(true);
  });

  it("markRead + clearRead remove read items", async () => {
    const { addBlocking, addAmbient, markRead, clearRead, getItems } =
      await import("./notifications.svelte");
    addBlocking("ws_a", "X1", "Y1");
    addAmbient("ws_a",  "X2", "Y2");
    const id = getItems()[1].id;   // oldest (blocking was second push)
    markRead(id);
    clearRead();
    expect(getItems().every((n) => !n.read)).toBe(true);
  });

  it("markAllRead marks every item read (unread count → 0), keeping the items", async () => {
    const { addBlocking, addAmbient, markAllRead, getItems } =
      await import("./notifications.svelte");
    addBlocking("ws_a", "Approve", "urgent");
    addAmbient("ws_b",  "Done",    "quiet");
    // Both unread before.
    expect(getItems().filter((n) => !n.read)).toHaveLength(2);
    markAllRead();
    // All read now, none dropped.
    expect(getItems()).toHaveLength(2);
    expect(getItems().every((n) => n.read)).toBe(true);
    expect(getItems().filter((n) => !n.read)).toHaveLength(0);
  });

  it("caps retained notifications at MAX, keeping the newest", async () => {
    const { addAmbient, getItems, MAX_NOTIFICATIONS } =
      await import("./notifications.svelte");
    // This pushes well past the cap. The code drops the oldest and keeps the newest.
    const total = MAX_NOTIFICATIONS + 50;
    for (let i = 0; i < total; i++) addAmbient("ws_a", `A${i}`, "b");

    const items = getItems();
    // The array is bounded to the cap (it drops the oldest and keeps the newest).
    expect(items).toHaveLength(MAX_NOTIFICATIONS);
    // Newest-first: the most recent push sits at the front.
    expect(items[0].title).toBe(`A${total - 1}`);
  });

  it("dropForWorkspace removes only items for that workspace", async () => {
    const { addBlocking, addAmbient, dropForWorkspace, getItems } =
      await import("./notifications.svelte");
    addBlocking("ws_a", "A1", "b");
    addAmbient("ws_b",  "B1", "b");
    addBlocking("ws_a", "A2", "b");
    expect(getItems()).toHaveLength(3);
    dropForWorkspace("ws_a");
    const left = getItems();
    expect(left).toHaveLength(1);
    expect(left[0].workspaceId).toBe("ws_b");
  });

  // -----------------------------------------------------------------------
  // F41: every notification gets an explicit kind at creation.
  // -----------------------------------------------------------------------

  it("F41: addBlocking tags an error title as kind 'error', not 'approval'", async () => {
    const { addBlocking, getItems } = await import("./notifications.svelte");
    addBlocking("ws_a", "Stage failed", "Could not stage hunk in main.go");
    expect(getItems()[0].kind).toBe("error");
  });

  it("F41: addBlocking tags an approval-style notice as kind 'approval'", async () => {
    const { addBlocking, getItems } = await import("./notifications.svelte");
    addBlocking("ws_a", "Approval needed", "Claude wants to run bash");
    expect(getItems()[0].kind).toBe("approval");
  });

  it("F41: addAmbient defaults to kind 'done'; an explicit kind overrides it", async () => {
    const { addAmbient, getItems } = await import("./notifications.svelte");
    addAmbient("ws_a", "Turn done", "finished");
    expect(getItems()[0].kind).toBe("done");
    addAmbient("ws_b", "Synced", "background", "info");
    expect(getItems()[0].kind).toBe("info");
  });

  // -----------------------------------------------------------------------
  // F42b: ambient and routine notifications stay unread until the user opens the hub
  // (no auto-dismiss timer). The docked hub is not a transient toast, so an unseen event
  // must not silently tick the unread badge down while the hub stays closed.
  // -----------------------------------------------------------------------

  it("F42b: an ambient notification stays unread past its old dismiss window while the hub is closed", async () => {
    vi.useFakeTimers();
    try {
      const { addAmbient, getItems } = await import("./notifications.svelte");
      addAmbient("ws_a", "Turn done", "finished");
      expect(getItems()[0].read).toBe(false);
      // Advance far beyond the former 6s ambient (and 3s routine) dismiss window.
      vi.advanceTimersByTime(60_000);
      expect(getItems()[0].read).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it("F42b: opening the hub (markAllRead) is what clears the ambient unread", async () => {
    const { addAmbient, markAllRead, getItems } = await import("./notifications.svelte");
    addAmbient("ws_a", "Turn done", "finished");
    expect(getItems()[0].read).toBe(false);
    markAllRead();
    expect(getItems()[0].read).toBe(true);
  });
});

describe("notification store: audit regressions", () => {
  beforeEach(() => { vi.resetModules(); });

  it("FEC-6: dropBlockingForWorkspace drops only unread blocking items of the given states", async () => {
    const { addBlocking, addRoutine, markRead, dropBlockingForWorkspace, getItems } = await import("./notifications.svelte");
    addRoutine("ws", "Auto-approved", "Read x");
    addBlocking("ws", "Agent error", "boom", undefined, "errored");
    addBlocking("ws", "Approval needed", "Bash", undefined, "awaiting-approval");
    addBlocking("ws", "Approval needed (seen)", "Bash", undefined, "awaiting-approval");
    markRead(getItems()[0].id);
    addBlocking("other", "Approval needed", "Bash", undefined, "awaiting-approval");
    dropBlockingForWorkspace("ws", ["awaiting-approval"]);
    const left = getItems().map((n) => `${n.workspaceId}:${n.title}`);
    expect(left).toEqual([
      "other:Approval needed",
      "ws:Approval needed (seen)",
      "ws:Agent error",
      "ws:Auto-approved",
    ]);
  });

  it("FEC-23: the kind follows the source state", async () => {
    const { addBlocking, getItems } = await import("./notifications.svelte");
    addBlocking("ws", "Agent exited", "", undefined, "exited");
    addBlocking("ws", "Question", "", undefined, "awaiting-input");
    addBlocking("ws", "Approval needed", "", undefined, "awaiting-approval");
    const kinds = Object.fromEntries(getItems().map((n) => [n.title, n.kind]));
    expect(kinds).toEqual({ "Agent exited": "error", "Question": "info", "Approval needed": "approval" });
  });

  it("FEC-33: marking read with nothing unread keeps the same array", async () => {
    const { addRoutine, markAllRead, markReadForWorkspace, getItems } = await import("./notifications.svelte");
    addRoutine("ws", "x", "y");
    markAllRead();
    const before = getItems();
    markAllRead();
    markReadForWorkspace("ws");
    expect(getItems()).toBe(before);
  });
});
