// frontend/src/lib/shellPanes.test.ts
import { describe, it, expect } from "vitest";
import {
  shellPaneId, shellTabTitle, initShellState, addShell, addSplitPartner,
  selectShell, removeShell, planToggleSplit, reloadMenuItems, activeShellTitle,
  type ShellState,
} from "./shellPanes";

describe("shellPaneId", () => {
  it("seq 0 is the backward-compatible default id (no suffix)", () => {
    expect(shellPaneId("ws1", 0)).toBe("shell-ws1");
  });
  it("seq >= 1 appends _<seq> (underscore delimiter, backend-recoverable)", () => {
    expect(shellPaneId("ws1", 1)).toBe("shell-ws1_1");
    expect(shellPaneId("ws1", 7)).toBe("shell-ws1_7");
  });
});

describe("shellTabTitle", () => {
  it("titles by position", () => {
    expect(shellTabTitle(0)).toBe("shell");
    expect(shellTabTitle(1)).toBe("shell 2");
    expect(shellTabTitle(3)).toBe("shell 4");
  });
});

describe("initShellState", () => {
  it("starts with one default shell, active, no split, seq past the default", () => {
    const st = initShellState("ws1");
    expect(st.panes).toEqual([{ id: "shell-ws1" }]);
    expect(st.activeId).toBe("shell-ws1");
    expect(st.splitId).toBeNull();
    expect(st.seq).toBe(1);
  });
});

describe("addShell", () => {
  it("appends a fresh unique id and makes it active; seq is monotonic", () => {
    const st0 = initShellState("ws1");
    const { state: st1, id: id1 } = addShell(st0, "ws1");
    expect(id1).toBe("shell-ws1_1");
    expect(st1.panes.map((p) => p.id)).toEqual(["shell-ws1", "shell-ws1_1"]);
    expect(st1.activeId).toBe("shell-ws1_1");
    const { id: id2 } = addShell(st1, "ws1");
    expect(id2).toBe("shell-ws1_2"); // never reuses seq 1 even after churn
  });
});

describe("selectShell", () => {
  it("selects a background tab as primary", () => {
    let st = initShellState("ws1");
    st = addShell(st, "ws1").state; // active shell-ws1_1
    st = selectShell(st, "shell-ws1");
    expect(st.activeId).toBe("shell-ws1");
  });
  it("selecting the split (right) tab swaps sides, keeping active != split", () => {
    let st = initShellState("ws1");
    st = addShell(st, "ws1").state; // panes: default, _1 (active _1)
    st = { ...st, activeId: "shell-ws1", splitId: "shell-ws1_1" };
    st = selectShell(st, "shell-ws1_1"); // pick the split tab
    expect(st.activeId).toBe("shell-ws1_1");
    expect(st.splitId).toBe("shell-ws1");
    expect(st.activeId).not.toBe(st.splitId);
  });
  it("is a no-op for an unknown id", () => {
    const st = initShellState("ws1");
    expect(selectShell(st, "nope")).toBe(st);
  });
});

describe("removeShell", () => {
  it("reports empty when the last shell is closed (caller must replace)", () => {
    const st = initShellState("ws1");
    const { state, empty } = removeShell(st, "shell-ws1");
    expect(empty).toBe(true);
    expect(state.panes).toEqual([]);
    expect(state.activeId).toBeNull();
  });
  it("activates an adjacent tab when the active one is closed", () => {
    let st = initShellState("ws1");
    st = addShell(st, "ws1").state; // active _1
    st = addShell(st, "ws1").state; // active _2, panes: default,_1,_2
    const { state, empty } = removeShell(st, "shell-ws1_2");
    expect(empty).toBe(false);
    expect(state.panes.map((p) => p.id)).toEqual(["shell-ws1", "shell-ws1_1"]);
    expect(state.activeId).toBe("shell-ws1_1"); // neighbour at clamped index
  });
  it("clears split when the partner is closed", () => {
    let st = initShellState("ws1");
    st = addShell(st, "ws1").state;
    st = { ...st, activeId: "shell-ws1", splitId: "shell-ws1_1" };
    const { state } = removeShell(st, "shell-ws1_1");
    expect(state.splitId).toBeNull();
  });
  it("clears split when fewer than two shells remain", () => {
    let st = initShellState("ws1");
    st = addShell(st, "ws1").state; // default,_1
    st = { ...st, activeId: "shell-ws1_1", splitId: "shell-ws1" };
    const { state } = removeShell(st, "shell-ws1_1"); // one left -> no split possible
    expect(state.panes.map((p) => p.id)).toEqual(["shell-ws1"]);
    expect(state.splitId).toBeNull();
  });
});

describe("planToggleSplit", () => {
  it("turns split off when it is on", () => {
    const st: ShellState = { panes: [{ id: "a" }, { id: "b" }], activeId: "a", splitId: "b", seq: 2 };
    expect(planToggleSplit(st)).toEqual({ off: true, partnerId: null, needNew: false });
  });
  it("uses an existing partner when two+ shells exist and split is off", () => {
    const st: ShellState = { panes: [{ id: "a" }, { id: "b" }], activeId: "a", splitId: null, seq: 2 };
    expect(planToggleSplit(st)).toEqual({ off: false, partnerId: "b", needNew: false });
  });
  it("needs a new shell when only one exists", () => {
    const st = initShellState("ws1");
    expect(planToggleSplit(st)).toEqual({ off: false, partnerId: null, needNew: true });
  });
});

describe("addSplitPartner", () => {
  it("appends a partner and sets split WITHOUT changing the active tab", () => {
    const st = initShellState("ws1");
    const { state, id } = addSplitPartner(st, "ws1");
    expect(id).toBe("shell-ws1_1");
    expect(state.activeId).toBe("shell-ws1"); // active unchanged
    expect(state.splitId).toBe("shell-ws1_1");
  });
});

describe("reloadMenuItems", () => {
  it("lists every pane in tab order with its title and active flag", () => {
    let st = initShellState("ws1");
    st = addShell(st, "ws1").state; // panes: default, _1 (active _1 at index 1)
    expect(reloadMenuItems(st.panes, st.activeId)).toEqual([
      { id: "shell-ws1", title: "shell", active: false },
      { id: "shell-ws1_1", title: "shell 2", active: true },
    ]);
  });
  it("marks no item active when activeId is null", () => {
    const items = reloadMenuItems([{ id: "a" }, { id: "b" }], null);
    expect(items.every((i) => !i.active)).toBe(true);
  });
});

describe("activeShellTitle", () => {
  it("returns the position-based title of the active pane", () => {
    let st = initShellState("ws1");
    st = addShell(st, "ws1").state; // active _1 at index 1
    expect(activeShellTitle(st.panes, st.activeId)).toBe("shell 2");
  });
  it("returns the first title when the default shell is active", () => {
    const st = initShellState("ws1");
    expect(activeShellTitle(st.panes, st.activeId)).toBe("shell");
  });
  it("returns empty string when no pane is active", () => {
    expect(activeShellTitle([{ id: "a" }], null)).toBe("");
  });
});
