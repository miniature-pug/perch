import { describe, it, expect } from "vitest";
import {
  isActiveState,
  shouldFocusAwaitingInput,
  ritualShouldFire,
  computeRitualStats,
} from "./engagement";

describe("isActiveState", () => {
  it("treats running / awaiting-approval / awaiting-input as active", () => {
    expect(isActiveState("running")).toBe(true);
    expect(isActiveState("awaiting-approval")).toBe(true);
    expect(isActiveState("awaiting-input")).toBe(true);
  });
  it("treats idle / done / errored as settled", () => {
    expect(isActiveState("idle")).toBe(false);
    expect(isActiveState("done")).toBe(false);
    expect(isActiveState("errored")).toBe(false);
  });
});

describe("shouldFocusAwaitingInput", () => {
  const A = "ws-1";
  it("fires on the edge for the active workspace on the agent view", () => {
    expect(shouldFocusAwaitingInput("running", "awaiting-input", A, A, "agent")).toBe(true);
    expect(shouldFocusAwaitingInput("idle", "awaiting-input", A, A, "agent")).toBe(true);
  });
  it("does NOT re-fire when already awaiting-input (no edge)", () => {
    expect(shouldFocusAwaitingInput("awaiting-input", "awaiting-input", A, A, "agent")).toBe(false);
  });
  it("does NOT fire for a background (non-active) workspace", () => {
    expect(shouldFocusAwaitingInput("running", "awaiting-input", "ws-2", A, "agent")).toBe(false);
  });
  it("does NOT fire when the user is on the code or diff view", () => {
    expect(shouldFocusAwaitingInput("running", "awaiting-input", A, A, "code")).toBe(false);
    expect(shouldFocusAwaitingInput("running", "awaiting-input", A, A, "diff")).toBe(false);
  });
  it("does NOT fire for a transition to any other state", () => {
    expect(shouldFocusAwaitingInput("running", "done", A, A, "agent")).toBe(false);
    expect(shouldFocusAwaitingInput("running", "awaiting-approval", A, A, "agent")).toBe(false);
  });
  it("does NOT fire when there is no active workspace", () => {
    expect(shouldFocusAwaitingInput("running", "awaiting-input", A, null, "agent")).toBe(false);
  });
});

describe("ritualShouldFire", () => {
  it("fires only when settled, armed, and at least one workspace is done", () => {
    expect(ritualShouldFire(false, true, true)).toBe(true);
  });
  it("does not fire while something is still active", () => {
    expect(ritualShouldFire(true, true, true)).toBe(false);
  });
  it("does not fire before activity has been seen (not armed)", () => {
    expect(ritualShouldFire(false, false, true)).toBe(false);
  });
  it("does not fire when nothing finished successfully (no done)", () => {
    expect(ritualShouldFire(false, true, false)).toBe(false);
  });
});

describe("computeRitualStats", () => {
  it("sums lines (added+removed) and files across workspaces, passes sessions through", () => {
    const stats = computeRitualStats(
      {
        a: { added: 10, removed: 4, files: 2 },
        b: { added: 1, removed: 0, files: 1 },
      },
      2,
    );
    expect(stats).toEqual({ lines: 15, files: 3, sessions: 2 });
  });
  it("treats a missing files field as zero", () => {
    const stats = computeRitualStats({ a: { added: 3, removed: 2 } }, 1);
    expect(stats).toEqual({ lines: 5, files: 0, sessions: 1 });
  });
  it("returns zeros for an empty diffstat map", () => {
    expect(computeRitualStats({}, 0)).toEqual({ lines: 0, files: 0, sessions: 0 });
  });
});
