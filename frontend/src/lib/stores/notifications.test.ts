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

  it("DND mutes tier 2 and 3 but not tier 1", async () => {
    const { addBlocking, addAmbient, addRoutine, setDnd, getItems } =
      await import("./notifications.svelte");
    setDnd(true);
    addBlocking("ws_a", "Approval", "urgent");
    addAmbient("ws_b", "Done", "quiet");
    addRoutine("ws_c", "File", "bg");
    const items = getItems();
    expect(items.filter((i) => i.tier === "blocking")).toHaveLength(1);
    expect(items.filter((i) => i.tier === "ambient")).toHaveLength(0);
    expect(items.filter((i) => i.tier === "routine")).toHaveLength(0);
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
});
