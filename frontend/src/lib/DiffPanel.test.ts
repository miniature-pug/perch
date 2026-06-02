import { render, screen, waitFor } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({
  diff: vi.fn(async () => ({ patch: "diff --git a/x b/x", files: 1, added: 3, removed: 2 })),
}));

test("loads and shows the diff stat for a worktree", async () => {
  const { default: DiffPanel } = await import("./DiffPanel.svelte");
  render(DiffPanel, { props: { worktreePath: "/wt/x" } });
  await waitFor(() => expect(screen.getByText(/\+3/)).toBeInTheDocument());
  expect(screen.getByText(/−2/)).toBeInTheDocument();
});
