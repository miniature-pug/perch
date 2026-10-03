// frontend/src/lib/DragDrop.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({ writeToPty: vi.fn(async () => {}) }));

/** Fire a dragleave whose `relatedTarget` actually round-trips. jsdom drops
 * `relatedTarget` when it is passed through fireEvent's plain init dict, so the
 * handler's `contains(relatedTarget)` guard can only be exercised by building the
 * event and defining the property directly. */
async function fireDragLeave(node: Element, relatedTarget: Node | null) {
  const ev = new MouseEvent("dragleave", { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "relatedTarget", { value: relatedTarget, configurable: true });
  await fireEvent(node, ev);
}

/** Minimal DataTransfer fake. jsdom's built-in version does not round-trip getData or setData. */
function fakeDataTransfer(overrides: { textData?: string; files?: File[] } = {}) {
  const store = new Map<string, string>();
  if (overrides.textData !== undefined) {
    store.set("application/x-perch-text", overrides.textData);
  }
  return {
    files: overrides.files ?? [],
    types: overrides.files?.length ? ["Files"] : (overrides.textData !== undefined ? ["application/x-perch-text"] : []),
    getData: (type: string) => store.get(type) ?? "",
    setData: (type: string, value: string) => { store.set(type, value); },
    effectAllowed: "uninitialized",
    dropEffect: "none",
  };
}

// OS file drops do NOT arrive through the DOM drop event. On WebKitGTK the
// dropped File objects carry no real path. (The old `File.path ?? f.name` read a
// non-standard field that is undefined there, and silently degraded to a basename.)
// The absolute paths arrive out-of-band via Wails' native OnFileDrop, and
// lib/osFileDrop.ts routes them to the pane under the drop point. This test
// exercises that real path by mocking the Wails OnFileDrop callback. The true
// end-to-end case, dragging a file from the OS file manager, needs a real WebKitGTK
// window. It is a manual smoke item (see docs/smoke-checklist.md).
test("OS file drop routes ABSOLUTE paths to the pty via the Wails OnFileDrop callback", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  const { registerOsFileDrop } = await import("./osFileDrop");
  const w = await import("./wails");
  vi.mocked(w.writeToPty).mockClear();

  // A fileDrop-enabled pane tags its drop-zone with data-drop-pane, so the code
  // can route the drop by hit-testing the coordinates.
  render(DragDrop, { props: { paneId: "p1", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });
  expect(zone.getAttribute("data-drop-pane")).toBe("p1");

  // Capture the callback Wails would invoke with (x, y, absolutePaths).
  let dropCb: ((x: number, y: number, paths: string[]) => void) | null = null;
  (globalThis as any).runtime = {
    OnFileDrop: (cb: (x: number, y: number, paths: string[]) => void) => { dropCb = cb; },
    OnFileDropOff: () => {},
  };
  const off = registerOsFileDrop();
  expect(typeof dropCb).toBe("function");

  // The drop point resolves (via elementFromPoint) to the pane's drop-zone.
  const origEFP = document.elementFromPoint;
  document.elementFromPoint = () => zone;
  try {
    dropCb!(10, 20, ["/wt/src/main.go"]);
    await waitFor(() => expect(w.writeToPty).toHaveBeenCalledWith("p1", expect.any(Array)));
    const [, bytes] = vi.mocked(w.writeToPty).mock.calls[0];
    const decoded = new TextDecoder().decode(new Uint8Array(bytes as number[]));
    expect(decoded).toBe("@/wt/src/main.go "); // a plain path stays bare (FEX-27)
  } finally {
    document.elementFromPoint = origEFP;
    off();
    delete (globalThis as any).runtime;
  }
});

test("renders paste-path hint and no buttons when fileDrop cap is false", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  render(DragDrop, { props: { paneId: "p1", fileDrop: false } });
  // The code removed the old "Open file" and "Copy path" buttons as dead no-ops.
  // When fileDrop is false, the component shows a hint paragraph instead.
  expect(screen.getByText(/paste path/i)).toBeInTheDocument();
  expect(screen.queryByRole("button")).toBeNull();
});

// --- Behavior 1: in-app text drop ---

test("in-app text drop calls writeToPty with UTF-8 encoded bytes for paneId", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  const w = await import("./wails");
  // Clear previous call records from the OS file drop test
  vi.mocked(w.writeToPty).mockClear();
  render(DragDrop, { props: { paneId: "pane-42", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });
  const text = "@/x.go ";
  await fireEvent.drop(zone, { dataTransfer: fakeDataTransfer({ textData: text }) });
  const expectedBytes = Array.from(new TextEncoder().encode(text));
  await waitFor(() =>
    expect(w.writeToPty).toHaveBeenCalledWith("pane-42", expectedBytes)
  );
});

test("FEX-28: in-app text drop goes through the paste prop when given (no raw write)", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  const w = await import("./wails");
  vi.mocked(w.writeToPty).mockClear();
  const paste = vi.fn(() => true);
  render(DragDrop, { props: { paneId: "pane-43", fileDrop: true, paste } });
  const zone = screen.getByRole("region", { name: /drop zone/i });
  await fireEvent.drop(zone, { dataTransfer: fakeDataTransfer({ textData: "line1\nline2" }) });
  await waitFor(() => expect(paste).toHaveBeenCalledWith("line1\nline2"));
  expect(w.writeToPty).not.toHaveBeenCalled();
});

test("in-app text drop is ignored when fileDrop is false", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  const w = await import("./wails");
  vi.mocked(w.writeToPty).mockClear();
  render(DragDrop, { props: { paneId: "p1", fileDrop: false } });
  const zone = screen.getByRole("region", { name: /drop zone/i });
  await fireEvent.drop(zone, { dataTransfer: fakeDataTransfer({ textData: "@/some.go " }) });
  await new Promise(r => setTimeout(r, 50));
  expect(w.writeToPty).not.toHaveBeenCalled();
});

// --- Stuck-overlay backstop: dragActive must return to false whenever a drag
// ends anywhere, even when handleDrop itself never runs. The `drag-active`
// class on the drop-zone gates the overlay (`{#if dragActive}`), so this test asserts
// on that class as a proxy for whether the overlay is visible. ---

test("dragend anywhere clears drag-active even when no drop lands on the zone", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  render(DragDrop, { props: { paneId: "p1", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });

  await fireEvent.dragEnter(zone, { dataTransfer: fakeDataTransfer({ files: [] }) });
  expect(zone.classList.contains("drag-active")).toBe(true);

  // The drag is cancelled or dropped elsewhere: no drop lands on the zone, only a window dragend.
  await fireEvent(window, new Event("dragend"));
  expect(zone.classList.contains("drag-active")).toBe(false);
});

test("dragleave outside the zone clears drag-active", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  render(DragDrop, { props: { paneId: "p1", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });

  await fireEvent.dragEnter(zone, { dataTransfer: fakeDataTransfer({ files: [] }) });
  expect(zone.classList.contains("drag-active")).toBe(true);

  // relatedTarget is outside the zone (for example, document.body), so the drag leaves entirely.
  const outside = document.createElement("div");
  document.body.appendChild(outside);
  await fireDragLeave(zone, outside);
  expect(zone.classList.contains("drag-active")).toBe(false);
});

test("dragleave into a child does NOT clear drag-active", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  render(DragDrop, { props: { paneId: "p1", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });

  // Give the zone a child so a leave-into-child has an in-node relatedTarget.
  const child = document.createElement("div");
  zone.appendChild(child);

  await fireEvent.dragEnter(zone, { dataTransfer: fakeDataTransfer({ files: [] }) });
  expect(zone.classList.contains("drag-active")).toBe(true);

  // Moving onto a child fires dragleave on the zone with relatedTarget inside it.
  await fireDragLeave(zone, child);
  expect(zone.classList.contains("drag-active")).toBe(true);
});

test("a DOM OS-file drop clears drag-active and does NOT itself write to the pty (paths come from Wails, not the DOM)", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  const w = await import("./wails");
  vi.mocked(w.writeToPty).mockClear();
  render(DragDrop, { props: { paneId: "p-drop", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });

  await fireEvent.dragEnter(zone, { dataTransfer: fakeDataTransfer({ files: [] }) });
  expect(zone.classList.contains("drag-active")).toBe(true);

  // A real OS file drop: the DOM event's File carries no usable path, so the DOM
  // handler must ignore the file. The absolute path arrives via Wails OnFileDrop instead,
  // while the handler still clears the drag-active overlay.
  const file = new File(["x"], "main.go");
  await fireEvent.drop(zone, { dataTransfer: fakeDataTransfer({ files: [file] }) });

  expect(zone.classList.contains("drag-active")).toBe(false);
  await new Promise(r => setTimeout(r, 30));
  expect(w.writeToPty).not.toHaveBeenCalled();
});
