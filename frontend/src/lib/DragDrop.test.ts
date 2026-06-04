// frontend/src/lib/DragDrop.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({ writeToPty: vi.fn(async () => {}) }));

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
