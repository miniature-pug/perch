// Compile-time contract test: import the real wails module (mocked in other suites)
// and assert the exported function signatures match the pinned contracts.
// These tests pass when the module exports the correct shapes; they fail when
// old signatures or removed exports remain.
import { describe, it, expect } from "vitest";

describe("wails.ts contract (Phase 2)", () => {
  it("createWorkspace export accepts (agent, repoPath, baseRef, branch, worktree)", async () => {
    const mod = await import("./wails");
    // Signature: 5 params. Verify function arity.
    expect(mod.createWorkspace.length).toBe(5);
  });

  it("workspaceForBranch export is a function", async () => {
    const mod = await import("./wails");
    expect(typeof mod.workspaceForBranch).toBe("function");
  });

  it("DEFAULT_MODEL is NOT exported from constants", async () => {
    const mod = await import("./constants");
    expect((mod as Record<string, unknown>)["DEFAULT_MODEL"]).toBeUndefined();
  });
});
