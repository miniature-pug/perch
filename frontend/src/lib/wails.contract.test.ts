// Compile-time contract test: import the real wails module (mocked in other suites)
// and assert the exported function signatures match the pinned contracts.
// These tests pass when the module exports the correct shapes; they fail when
// old signatures or removed exports remain.
import { describe, it, expect, test } from "vitest";
import { forceRemoveWorkspace, listStaleSessions, cleanupSessions, homeShellCwd } from "./wails";
import type { StaleSessionVM } from "./wails";

describe("wails.ts contract", () => {
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

test("cleanup bindings are exported", () => {
  expect(typeof forceRemoveWorkspace).toBe("function");
  expect(typeof listStaleSessions).toBe("function");
  expect(typeof cleanupSessions).toBe("function");
});

test("homeShellCwd is exported", () => {
  expect(typeof homeShellCwd).toBe("function");
});
