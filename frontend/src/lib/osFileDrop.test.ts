// frontend/src/lib/osFileDrop.test.ts
import { vi } from "vitest";
import {
  shellQuote,
  mentionBytes,
  paneIdAt,
  routeOsFileDrop,
  registerOsFileDrop,
  DROP_PANE_ATTR,
} from "./osFileDrop";

vi.mock("./wails", () => ({ writeToPty: vi.fn(async () => {}) }));

const decode = (bytes: number[]) => new TextDecoder().decode(new Uint8Array(bytes));

test("shellQuote wraps in single quotes and escapes embedded single quotes", () => {
  expect(shellQuote("/a/b.go")).toBe("'/a/b.go'");
  expect(shellQuote("/with space/x.go")).toBe("'/with space/x.go'");
  // it's -> 'it'\''s'
  expect(shellQuote("it's")).toBe("'it'\\''s'");
});

test("mentionBytes encodes a shell-quoted @mention with a trailing space", () => {
  expect(decode(mentionBytes("/wt/src/main.go"))).toBe("@'/wt/src/main.go' ");
  expect(decode(mentionBytes("/wt/a b/c.go"))).toBe("@'/wt/a b/c.go' ");
});

/** Build a detached drop-zone element carrying the routing attribute. */
function paneZone(paneId: string): HTMLElement {
  const el = document.createElement("div");
  el.setAttribute(DROP_PANE_ATTR, paneId);
  return el;
}

test("paneIdAt resolves the pane under the point, or null when there is none", () => {
  const zone = paneZone("p1");
  const origEFP = document.elementFromPoint;
  try {
    document.elementFromPoint = () => zone;
    expect(paneIdAt(5, 5)).toBe("p1");
    // A point over a child still resolves up to the drop-zone via closest().
    const child = document.createElement("span");
    zone.appendChild(child);
    document.elementFromPoint = () => child;
    expect(paneIdAt(5, 5)).toBe("p1");
    // No element (a drop outside any pane) resolves to null.
    document.elementFromPoint = () => null;
    expect(paneIdAt(5, 5)).toBeNull();
  } finally {
    document.elementFromPoint = origEFP;
  }
});

test("routeOsFileDrop writes one @mention per path to the pane under the point", async () => {
  const { writeToPty } = await import("./wails");
  vi.mocked(writeToPty).mockClear();
  const zone = paneZone("p9");
  const origEFP = document.elementFromPoint;
  try {
    document.elementFromPoint = () => zone;
    await routeOsFileDrop(1, 2, ["/x/a.go", "/x/b.go"]);
  } finally {
    document.elementFromPoint = origEFP;
  }
  expect(vi.mocked(writeToPty).mock.calls.length).toBe(2);
  expect(vi.mocked(writeToPty).mock.calls[0][0]).toBe("p9");
  expect(decode(vi.mocked(writeToPty).mock.calls[0][1] as number[])).toBe("@'/x/a.go' ");
  expect(decode(vi.mocked(writeToPty).mock.calls[1][1] as number[])).toBe("@'/x/b.go' ");
});

test("routeOsFileDrop is a no-op when the drop lands on no pane", async () => {
  const { writeToPty } = await import("./wails");
  vi.mocked(writeToPty).mockClear();
  const origEFP = document.elementFromPoint;
  try {
    document.elementFromPoint = () => null;
    await routeOsFileDrop(1, 2, ["/x/a.go"]);
  } finally {
    document.elementFromPoint = origEFP;
  }
  expect(writeToPty).not.toHaveBeenCalled();
});

test("registerOsFileDrop is a safe no-op when the Wails runtime is absent", () => {
  expect("runtime" in globalThis).toBe(false);
  const off = registerOsFileDrop();
  expect(typeof off).toBe("function");
  off(); // must not throw
});

test("registerOsFileDrop wires OnFileDrop and its off-fn calls OnFileDropOff", () => {
  let cb: ((x: number, y: number, paths: string[]) => void) | null = null;
  let useDropTarget: boolean | null = null;
  const onFileDropOff = vi.fn();
  (globalThis as any).runtime = {
    OnFileDrop: (c: (x: number, y: number, p: string[]) => void, u: boolean) => { cb = c; useDropTarget = u; },
    OnFileDropOff: onFileDropOff,
  };
  try {
    const off = registerOsFileDrop();
    expect(typeof cb).toBe("function");
    // The code hit-tests coordinates itself, so it disables Wails' CSS drop-target.
    expect(useDropTarget).toBe(false);
    off();
    expect(onFileDropOff).toHaveBeenCalledTimes(1);
  } finally {
    delete (globalThis as any).runtime;
  }
});

test("FEC-26: a failed write for an OS file drop raises a notification instead of an unhandled rejection", async () => {
  const w = await import("./wails");
  const { getItems } = await import("./stores/notifications.svelte");
  vi.mocked(w.writeToPty).mockRejectedValueOnce(new Error("unknown pane"));
  let cb: ((x: number, y: number, paths: string[]) => void) | null = null;
  (globalThis as any).runtime = { OnFileDrop: (c: typeof cb) => { cb = c; }, OnFileDropOff: () => {} };
  const zone = paneZone("pane-err");
  document.body.appendChild(zone);
  const orig = document.elementFromPoint;
  (document as any).elementFromPoint = () => zone;
  try {
    registerOsFileDrop();
    cb!(1, 1, ["/wt/a.go"]);
    await vi.waitFor(() => expect(getItems().some((n) => n.title === "Could not send the dropped file")).toBe(true));
  } finally {
    (document as any).elementFromPoint = orig;
    zone.remove();
    delete (globalThis as any).runtime;
  }
});
