import { vi, beforeEach } from "vitest";

beforeEach(() => {
  (globalThis as any).window = globalThis;
});

test("onPtyData subscribes to the tab-scoped event and decodes bytes", async () => {
  const eventsOn = vi.fn(() => () => {});
  (globalThis as any).runtime = { EventsOn: eventsOn };
  const mod = await import("./wails");

  let received: Uint8Array | undefined;
  const off = mod.onPtyData("tab1", (b) => (received = b));
  expect(eventsOn).toHaveBeenCalledWith("pty-data:tab1", expect.any(Function));
  // Simulate Wails delivering a number[] payload.
  const [, cb] = eventsOn.mock.calls[0] as unknown as [string, (d: number[]) => void];
  cb([104, 105]);
  expect(received).toEqual(Uint8Array.from([104, 105]));
  expect(typeof off).toBe("function");
});

test("listSessions dispatches to window.go bound method", async () => {
  const ListSessions = vi.fn(async () => []);
  (globalThis as any).go = { app: { App: { ListSessions } } };
  const mod = await import("./wails");
  await mod.listSessions();
  expect(ListSessions).toHaveBeenCalled();
});
