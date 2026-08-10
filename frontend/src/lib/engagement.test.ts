import { describe, it, expect } from "vitest";
import { shouldFocusAwaitingInput } from "./engagement";

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
