// frontend/src/lib/DragDrop.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({ writeToPty: vi.fn(async () => {}) }));

test("file drop fires writeToPty with @mention when fileDrop cap is true", async () => {
  const { default: DragDrop } = await import("./DragDrop.svelte");
  const w = await import("./wails");
  render(DragDrop, { props: { paneId: "p1", fileDrop: true } });
  const zone = screen.getByRole("region", { name: /drop zone/i });
  // Simulate a file with a path property (Electron/Wails drop model)
  const file = Object.assign(new File(["x"], "main.go"), { path: "/wt/src/main.go" });
  await fireEvent.drop(zone, { dataTransfer: { files: [file], types: ["Files"] } });
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
