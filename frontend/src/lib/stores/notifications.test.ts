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
    // All three are still logged to the hub — DND silences, it does not drop —
    // so the away catch-up stays complete.
    expect(items.filter((i) => i.tier === "blocking")).toHaveLength(1);
    expect(items.filter((i) => i.tier === "ambient")).toHaveLength(1);
    expect(items.filter((i) => i.tier === "routine")).toHaveLength(1);
    // Silenced tiers 2-3 are recorded already-read (no unread-badge bump);
    // blocking stays unread (always surfaces).
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
    // Push well past the cap; the oldest are dropped, the newest are kept.
    const total = MAX_NOTIFICATIONS + 50;
    for (let i = 0; i < total; i++) addAmbient("ws_a", `A${i}`, "b");

    const items = getItems();
    // Array is bounded to the cap (oldest dropped, newest kept).
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
  // F41 — every notification is tagged with an explicit kind at creation.
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
  // F42b — ambient/routine stay unread until the hub is opened (no auto-dismiss
  // timer): the docked hub is not a transient toast, so an unseen event must not
  // silently tick the unread badge down while the hub is closed.
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
