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

/** Minimal DataTransfer fake — jsdom's built-in doesn't round-trip getData/setData. */
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

test("file drop fires writeToPty with @mention when fileDrop cap is true", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  const w = await import("./wails");
  render(DragDrop, { props: { paneId: "p1", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });
  // Simulate a file with a path property (Electron/Wails drop model)
  const file = Object.assign(new File(["x"], "main.go"), { path: "/wt/src/main.go" });
  await fireEvent.drop(zone, { dataTransfer: fakeDataTransfer({ files: [file] }) });
  await waitFor(() => expect(w.writeToPty).toHaveBeenCalledWith("p1", expect.any(Array)));
});

test("renders paste-path hint and no buttons when fileDrop cap is false", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  render(DragDrop, { props: { paneId: "p1", fileDrop: false } });
  // The old "Open file" / "Copy path" buttons were removed as dead no-ops.
  // When fileDrop is false the component shows a hint paragraph instead.
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
// ends anywhere, even when our own handleDrop never runs. The `drag-active`
// class on the drop-zone gates the overlay (`{#if dragActive}`), so we assert
// on that class as a proxy for the overlay being visible. ---

test("dragend anywhere clears drag-active even when no drop lands on the zone", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  render(DragDrop, { props: { paneId: "p1", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });

  await fireEvent.dragEnter(zone, { dataTransfer: fakeDataTransfer({ files: [] }) });
  expect(zone.classList.contains("drag-active")).toBe(true);

  // Drag is cancelled / dropped elsewhere: no drop on the zone, only a window dragend.
  await fireEvent(window, new Event("dragend"));
  expect(zone.classList.contains("drag-active")).toBe(false);
});

test("dragleave outside the zone clears drag-active", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  render(DragDrop, { props: { paneId: "p1", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });

  await fireEvent.dragEnter(zone, { dataTransfer: fakeDataTransfer({ files: [] }) });
  expect(zone.classList.contains("drag-active")).toBe(true);

  // relatedTarget is outside the zone (e.g. document.body) → leaving entirely.
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

test("real file drop still writes to the pty AND clears drag-active", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  const w = await import("./wails");
  vi.mocked(w.writeToPty).mockClear();
  render(DragDrop, { props: { paneId: "p-drop", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });

  await fireEvent.dragEnter(zone, { dataTransfer: fakeDataTransfer({ files: [] }) });
  expect(zone.classList.contains("drag-active")).toBe(true);

  const file = Object.assign(new File(["x"], "main.go"), { path: "/wt/src/main.go" });
  await fireEvent.drop(zone, { dataTransfer: fakeDataTransfer({ files: [file] }) });

  await waitFor(() => expect(w.writeToPty).toHaveBeenCalledWith("p-drop", expect.any(Array)));
  expect(zone.classList.contains("drag-active")).toBe(false);
});
