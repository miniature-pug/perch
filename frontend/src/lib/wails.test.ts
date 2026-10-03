// frontend/src/lib/wails.test.ts
import { vi, beforeEach, test, expect } from "vitest";

beforeEach(() => { (globalThis as any).window = globalThis; });

test("onPtyData subscribes with colon-separated event name and decodes each base64 event", async () => {
  const eventsOn = vi.fn(() => () => {});
  (globalThis as any).runtime = { EventsOn: eventsOn };
  const mod = await import("./wails");
  const received: Uint8Array[] = [];
  mod.onPtyData("pane1", (b) => received.push(b));
  expect(eventsOn).toHaveBeenCalledWith("pty:data:pane1", expect.any(Function));
  const [, cb] = eventsOn.mock.calls[0] as unknown as [string, (d: string | number[]) => void];
  cb("aGk=");                 // "hi", padded on its own
  cb("IQ==");                 // "!", a second independently padded event
  cb([104, 105]);             // an older backend's number[] still works
  expect(received).toEqual([Uint8Array.from([104, 105]), Uint8Array.from([33]), Uint8Array.from([104, 105])]);
});

test("writeToPty sends one padded standard-base64 string", async () => {
  const WriteToPty = vi.fn(async () => {});
  (globalThis as any).go = { app: { App: { WriteToPty } } };
  const mod = await import("./wails");
  await mod.writeToPty("pane1", new Uint8Array([65, 66]));
  expect(WriteToPty).toHaveBeenCalledWith("pane1", "QUI=");
});

test("FEC-31: bytesToBase64 is padded standard base64 that round-trips for 1..4 bytes and > 0x8000 bytes", async () => {
  const mod = await import("./wails");
  const STD = /^[A-Za-z0-9+/]*={0,2}$/;
  const sizes = [0, 1, 2, 3, 4, 0x8000 - 1, 0x8000, 0x8000 + 1, 0x8000 * 2 + 2];
  for (const n of sizes) {
    const bytes = new Uint8Array(n);
    for (let i = 0; i < n; i++) bytes[i] = (i * 37 + 11) & 0xff;
    const b64 = mod.bytesToBase64(bytes);
    expect(b64).toMatch(STD);
    expect(b64.length % 4).toBe(0);
    // Padding only at the very end, never mid-string.
    expect(b64.replace(/=+$/, "")).not.toContain("=");
    expect(mod.base64ToBytes(b64)).toEqual(bytes);
    expect(Buffer.from(b64, "base64")).toEqual(Buffer.from(bytes));
  }
});

test("writeTextToPty encodes text as UTF-8 first (non-Latin-1 text does not throw)", async () => {
  const WriteToPty = vi.fn(async () => {});
  (globalThis as any).go = { app: { App: { WriteToPty } } };
  const mod = await import("./wails");
  await mod.writeTextToPty("pane1", "héllo ✓");
  const [, b64] = WriteToPty.mock.calls[0] as unknown as [string, string];
  expect(Buffer.from(b64, "base64").toString("utf8")).toBe("héllo ✓");
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
