// frontend/src/lib/stores/mode.test.ts
import { vi, describe, it, expect, beforeEach } from "vitest";

beforeEach(() => { vi.resetModules(); });

describe("mode store", () => {
  it("starts in normal", async () => {
    const { mode } = await import("./mode.svelte");
    expect(mode.current).toBe("normal");
  });
  it("enterTerminal → terminal", async () => {
    const { mode } = await import("./mode.svelte");
    mode.enterTerminal();
    expect(mode.current).toBe("terminal");
  });
  it("leaveTerminal → normal", async () => {
    const { mode } = await import("./mode.svelte");
    mode.enterTerminal(); mode.leaveTerminal();
    expect(mode.current).toBe("normal");
  });
  it("enterCommand → command", async () => {
    const { mode } = await import("./mode.svelte");
    mode.enterCommand();
    expect(mode.current).toBe("command");
  });
  it("leaveCommand → normal", async () => {
    const { mode } = await import("./mode.svelte");
    mode.enterCommand(); mode.leaveCommand();
    expect(mode.current).toBe("normal");
  });
  it("leaveTerminal from normal is a no-op", async () => {
    const { mode } = await import("./mode.svelte");
    mode.leaveTerminal();
    expect(mode.current).toBe("normal");
  });
});
