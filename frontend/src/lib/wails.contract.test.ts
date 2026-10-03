// Compile-time contract test: import the real wails module (mocked in other suites)
// and assert the exported function signatures match the pinned contracts.
// These tests pass when the module exports the correct shapes. They fail when
// old signatures or removed exports remain.
import { describe, it, expect, test } from "vitest";
import { forceRemoveWorkspace, listStaleSessions, cleanupSessions, homeShellCwd } from "./wails";
import type { StaleSessionVM } from "./wails";

describe("wails.ts contract", () => {
  it("createWorkspace export accepts (agent, repoPath, baseRef, branch, title, worktree)", async () => {
    const mod = await import("./wails");
    // Signature: 6 params (title inserted after branch). Verify function arity.
    expect(mod.createWorkspace.length).toBe(6);
  });

  it("setWorkspaceTitle export is a function", async () => {
    const mod = await import("./wails");
    expect(typeof mod.setWorkspaceTitle).toBe("function");
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

// ── Backend contract (branch claude/bold-cray-t6gnl7) ───────────────────────
import type { AgentEvent, AlwaysGrant, FileDiff, FsChanged, Hunk } from "./wails";

describe("wails.ts contract: backend wiring", () => {
  function fakeApp(methods: Record<string, (...args: any[]) => any>) {
    (globalThis as any).window = globalThis;
    (globalThis as any).go = { app: { App: methods } };
  }

  it("hunk mutators send (worktree, file, index, id)", async () => {
    const calls: unknown[][] = [];
    const rec = (name: string) => (...args: unknown[]) => { calls.push([name, ...args]); return Promise.resolve(); };
    fakeApp({ StageHunk: rec("StageHunk"), DiscardHunk: rec("DiscardHunk"), UnstageHunk: rec("UnstageHunk") });
    const mod = await import("./wails");
    await mod.stageHunk("/wt", "a.go", 2, "id-a");
    await mod.discardHunk("/wt", "a.go", 3, "id-b");
    await mod.unstageHunk("/wt", "a.go", 4, "id-c");
    expect(calls).toEqual([
      ["StageHunk", "/wt", "a.go", 2, "id-a"],
      ["DiscardHunk", "/wt", "a.go", 3, "id-b"],
      ["UnstageHunk", "/wt", "a.go", 4, "id-c"],
    ]);
  });

  it("WriteToPty receives padded standard base64", async () => {
    let sent: unknown;
    fakeApp({ WriteToPty: (_p: string, d: unknown) => { sent = d; return Promise.resolve(); } });
    const mod = await import("./wails");
    await mod.writeToPty("p", new Uint8Array([0xff, 0xfe, 0x00, 0x41]));
    expect(sent).toBe("//4AQQ==");
  });

  it("WorkspaceForBranch resolves to {id, found}", async () => {
    fakeApp({ WorkspaceForBranch: () => Promise.resolve({ id: "ws-9", found: true }) });
    const mod = await import("./wails");
    await expect(mod.workspaceForBranch("/r", "b")).resolves.toEqual({ id: "ws-9", found: true });
  });

  it("ApproveAlways resolves to {rule, added}; RemoveAlwaysRule takes the rule", async () => {
    const rule = { agent: "claude", tool: "Bash", pattern: "ls", hash: "h" };
    let removed: unknown;
    fakeApp({
      ApproveAlways: () => Promise.resolve({ rule, added: true } satisfies AlwaysGrant),
      RemoveAlwaysRule: (r: unknown) => { removed = r; return Promise.resolve(true); },
    });
    const mod = await import("./wails");
    const g = await mod.approveAlways("raw:ws");
    expect(g).toEqual({ rule, added: true });
    await expect(mod.removeAlwaysRule(g.rule)).resolves.toBe(true);
    expect(removed).toEqual(rule);
  });

  it("payload shapes compile: Hunk.id, FileDiff.oldPath, fs:changed paths, agent:event resolvedReqId", () => {
    const h: Hunk = { file: "a", index: 0, id: "x", header: "@@", oldStart: 1, oldLines: 1, newStart: 1, newLines: 1, lines: [] };
    const d: FileDiff = { path: "new.go", oldPath: "old.go", added: 0, removed: 0, status: "R" };
    const fs: FsChanged = { workspaceId: "w", path: "/wt", paths: ["/wt/a"], truncated: false };
    const ev: AgentEvent = { workspaceId: "w", kind: "approval-resolved", resolvedReqId: "r:w" };
    expect([h.id, d.oldPath, fs.paths?.length, ev.resolvedReqId]).toEqual(["x", "old.go", 1, "r:w"]);
  });
});

test("retypeLaunch dispatches RetypeLaunch(workspaceId)", async () => {
  let got: unknown;
  (globalThis as any).window = globalThis;
  (globalThis as any).go = { app: { App: { RetypeLaunch: (id: string) => { got = id; return Promise.resolve(); } } } };
  const mod = await import("./wails");
  await mod.retypeLaunch("ws-7");
  expect(got).toBe("ws-7");
});
