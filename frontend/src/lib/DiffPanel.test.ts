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

test("stale diff resolution is ignored when worktreePath changes", async () => {
  const { default: DiffPanel } = await import("./DiffPanel.svelte");
  const w = await import("./wails");

  // Track deferred resolvers keyed by path.
  const resolvers: Record<string, (r: import("./wails").DiffResult) => void> = {};
  vi.mocked(w.diff).mockImplementation(
    (p: string) =>
      new Promise<import("./wails").DiffResult>((res) => {
        resolvers[p] = res;
      }),
  );

  const { rerender } = render(DiffPanel, { props: { worktreePath: "/a" } });

  // Switch to "/b" — the $effect cleanup for "/a" should cancel that resolution.
  await rerender({ worktreePath: "/b" });

  // Resolve "/b" first (the current path).
  resolvers["/b"]({ patch: "b-patch", files: 9, added: 9, removed: 1 });
  await waitFor(() => expect(screen.getByText(/\+9/)).toBeInTheDocument());

  // Now resolve "/a" (stale) — must NOT overwrite the displayed "/b" result.
  resolvers["/a"]({ patch: "a-patch", files: 1, added: 1, removed: 0 });
  // Wait a tick for any potential microtask to run.
  await new Promise<void>((res) => setTimeout(res, 0));

  // "/b"'s stat must still be displayed.
  expect(screen.getByText(/\+9/)).toBeInTheDocument();
  // "/a"'s +1 must NOT appear.
  expect(screen.queryByText(/\+1/)).not.toBeInTheDocument();

  // Restore default mock for any subsequent tests.
  vi.mocked(w.diff).mockImplementation(
    async () => ({ patch: "diff --git a/x b/x", files: 1, added: 3, removed: 2 }),
  );
});
