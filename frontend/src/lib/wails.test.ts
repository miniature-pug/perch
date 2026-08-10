// frontend/src/lib/wails.test.ts
import { vi, beforeEach, test, expect } from "vitest";

beforeEach(() => { (globalThis as any).window = globalThis; });

test("onPtyData subscribes with colon-separated event name and decodes bytes", async () => {
  const eventsOn = vi.fn(() => () => {});
  (globalThis as any).runtime = { EventsOn: eventsOn };
  const mod = await import("./wails");
  let received: Uint8Array | undefined;
  mod.onPtyData("pane1", (b) => (received = b));
  expect(eventsOn).toHaveBeenCalledWith("pty:data:pane1", expect.any(Function));
  const [, cb] = eventsOn.mock.calls[0] as unknown as [string, (d: number[]) => void];
  cb([104, 105]);
  expect(received).toEqual(Uint8Array.from([104, 105]));
});

test("writeToPty dispatches to window.go.app.App.WriteToPty", async () => {
  const WriteToPty = vi.fn(async () => {});
  (globalThis as any).go = { app: { App: { WriteToPty } } };
  const mod = await import("./wails");
  await mod.writeToPty("pane1", [65, 66]);
  expect(WriteToPty).toHaveBeenCalledWith("pane1", [65, 66]);
});

test("resizePty dispatches to ResizePty", async () => {
  const ResizePty = vi.fn(async () => {});
  (globalThis as any).go = { app: { App: { ResizePty } } };
  const mod = await import("./wails");
  await mod.resizePty("pane1", 120, 40);
  expect(ResizePty).toHaveBeenCalledWith("pane1", 120, 40);
});

test("onAgentEvent subscribes to agent:event", async () => {
  const eventsOn = vi.fn(() => () => {});
  (globalThis as any).runtime = { EventsOn: eventsOn };
  const mod = await import("./wails");
  const received: any[] = [];
  mod.onAgentEvent((ev) => received.push(ev));
  expect(eventsOn).toHaveBeenCalledWith("agent:event", expect.any(Function));
  const [, cb] = eventsOn.mock.calls[eventsOn.mock.calls.length - 1] as unknown as [string, Function];
  cb({ workspaceId: "ws1", kind: "state", state: "running" });
  expect(received[0]).toMatchObject({ workspaceId: "ws1", kind: "state" });
});

test("onFsChanged subscribes to fs:changed", async () => {
  const eventsOn = vi.fn(() => () => {});
  (globalThis as any).runtime = { EventsOn: eventsOn };
  const mod = await import("./wails");
  mod.onFsChanged(() => {});
  expect(eventsOn).toHaveBeenCalledWith("fs:changed", expect.any(Function));
});

test("reloadAgentEnv dispatches to window.go.app.App.ReloadAgentEnv", async () => {
  const ReloadAgentEnv = vi.fn(async () => {});
  (globalThis as any).go = { app: { App: { ReloadAgentEnv } } };
  const mod = await import("./wails");
  await mod.reloadAgentEnv("shell-ws1");
  expect(ReloadAgentEnv).toHaveBeenCalledWith("shell-ws1");
});

test("listWorkspaces dispatches to ListWorkspaces and propagates return value", async () => {
  const KNOWN_WORKSPACES = [{ id: "ws-1", title: "my session" }, { id: "ws-2", title: "other" }];
  const ListWorkspaces = vi.fn(async () => KNOWN_WORKSPACES);
  (globalThis as any).go = { app: { App: { ListWorkspaces } } };
  const mod = await import("./wails");
  const result = await mod.listWorkspaces();
  expect(ListWorkspaces).toHaveBeenCalled();
  // This asserts the wrapper propagates the IPC return value to the caller, not just that the caller called it.
  expect(result).toEqual(KNOWN_WORKSPACES);
});
