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
  const [, cb] = eventsOn.mock.calls[0] as [string, (d: number[]) => void];
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
  const [, cb] = eventsOn.mock.calls[eventsOn.mock.calls.length - 1] as [string, Function];
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

test("listWorkspaces dispatches to ListWorkspaces", async () => {
  const ListWorkspaces = vi.fn(async () => []);
  (globalThis as any).go = { app: { App: { ListWorkspaces } } };
  const mod = await import("./wails");
  await mod.listWorkspaces();
  expect(ListWorkspaces).toHaveBeenCalled();
});
