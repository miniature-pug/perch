// frontend/src/lib/DiffView.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const fakeStat = [
  { path: "src/main.go", added: 3, removed: 1, status: "M" },
  { path: "README.md",   added: 10, removed: 0, status: "A" },
];
const fakeHunks = [{
  file: "src/main.go", index: 0, header: "@@ -1,3 +1,4 @@",
  oldStart: 1, oldLines: 3, newStart: 1, newLines: 4,
  lines: [{ kind: "ctx", text: "package main" }, { kind: "add", text: `import "fmt"` }],
}];

vi.mock("./wails", () => ({
  diffStat:    vi.fn(async () => fakeStat),
  hunks:       vi.fn(async () => fakeHunks),
  stageHunk:   vi.fn(async () => {}),
  discardHunk: vi.fn(async () => {}),
}));

test("renders file list with status icon+label and +/- counts", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  render(DiffView, { props: { worktree: "/wt" } });
  await waitFor(() => expect(screen.getByText("src/main.go")).toBeInTheDocument());
  expect(screen.getByText("+3")).toBeInTheDocument();
  expect(screen.getByText("-1")).toBeInTheDocument();
  expect(screen.getByText(/modified/i)).toBeInTheDocument();
  expect(screen.getByText(/added/i)).toBeInTheDocument();
});

test("Stage button calls stageHunk", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  render(DiffView, { props: { worktree: "/wt" } });
  await waitFor(() => screen.getByText("src/main.go"));
  await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));
  const w = await import("./wails");
  await waitFor(() => screen.getByRole("button", { name: /stage/i }));
  await fireEvent.click(screen.getByRole("button", { name: /stage/i }));
  await waitFor(() => expect(w.stageHunk).toHaveBeenCalledWith("/wt", "src/main.go", 0));
});
