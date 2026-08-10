// frontend/src/lib/DiffView.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";
import type { FileDiff, Hunk } from "./wails";

const fakeStat: FileDiff[] = [
  { path: "src/main.go", added: 3, removed: 1, status: "M" },
  { path: "README.md",   added: 10, removed: 0, status: "A" },
];
const fakeHunks: Hunk[] = [{
  file: "src/main.go", index: 0, header: "@@ -1,3 +1,4 @@",
  oldStart: 1, oldLines: 3, newStart: 1, newLines: 4,
  lines: [{ kind: "ctx", text: "package main" }, { kind: "add", text: `import "fmt"` }],
}];

vi.mock("./wails", () => ({
  diffStat:    vi.fn(async () => fakeStat),
  hunks:       vi.fn(async () => fakeHunks),
  stageHunk:   vi.fn(async () => {}),
  discardHunk: vi.fn(async () => {}),
  unstageHunk: vi.fn(async () => {}),
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

test("stage refreshes file list (diffStat re-called) and fires onDiffChanged", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  const w = await import("./wails");
  vi.mocked(w.diffStat).mockClear();
  const onDiffChanged = vi.fn();

  render(DiffView, { props: { worktree: "/wt", onDiffChanged } });
  await waitFor(() => screen.getByText("src/main.go"));

  // diffStat was already called once, for the initial load.
  const callsBefore = vi.mocked(w.diffStat).mock.calls.length;

  // Expand and stage
  await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));
  await waitFor(() => screen.getByRole("button", { name: /stage/i }));
  await fireEvent.click(screen.getByRole("button", { name: /stage/i }));

  await waitFor(() => {
    expect(vi.mocked(w.diffStat).mock.calls.length).toBeGreaterThan(callsBefore);
  });
  expect(onDiffChanged).toHaveBeenCalledTimes(1);
});

// --- F1: Discard is deferred behind an undo toast (irreversible-action guard) ---

test("discard shows an undo toast and does NOT revert immediately", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  const w = await import("./wails");
  vi.mocked(w.discardHunk).mockClear();

  render(DiffView, { props: { worktree: "/wt" } });
  await waitFor(() => screen.getByText("src/main.go"));

  await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));
  await waitFor(() => screen.getByRole("button", { name: /discard/i }));
  await fireEvent.click(screen.getByRole("button", { name: /discard/i }));

  // The toast appears. The working tree is NOT touched yet.
  await waitFor(() => expect(screen.getByTestId("discard-undo-toast")).toBeInTheDocument());
  expect(screen.getByRole("button", { name: /undo/i })).toBeInTheDocument();
  expect(w.discardHunk).not.toHaveBeenCalled();
});

test("Undo cancels the discard entirely — the backend revert never runs", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  const w = await import("./wails");
  vi.mocked(w.discardHunk).mockClear();
  const onDiffChanged = vi.fn();

  render(DiffView, { props: { worktree: "/wt", onDiffChanged } });
  await waitFor(() => screen.getByText("src/main.go"));

  await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));
  await waitFor(() => screen.getByRole("button", { name: /discard/i }));
  await fireEvent.click(screen.getByRole("button", { name: /discard/i }));

  await waitFor(() => screen.getByRole("button", { name: /undo/i }));
  await fireEvent.click(screen.getByRole("button", { name: /undo/i }));

  // Nothing is lost: no git revert, toast dismissed, hunk restored.
  await waitFor(() => expect(screen.queryByTestId("discard-undo-toast")).toBeNull());
  expect(w.discardHunk).not.toHaveBeenCalled();
  await waitFor(() => screen.getByRole("button", { name: /discard/i }));
});

test("an un-undone discard commits the real revert after the delay, then refreshes", async () => {
  vi.useFakeTimers();
  try {
    const { default: DiffView } = await import("./DiffView.svelte");
    const w = await import("./wails");
    vi.mocked(w.discardHunk).mockClear();
    vi.mocked(w.diffStat).mockClear();
    const onDiffChanged = vi.fn();

    render(DiffView, { props: { worktree: "/wt", onDiffChanged } });
    // Flush the initial diffStat effect (past the 150ms loading-delay timer).
    await vi.advanceTimersByTimeAsync(200);

    await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));
    await vi.advanceTimersByTimeAsync(0); // flush the hunks fetch
    await fireEvent.click(screen.getByRole("button", { name: /discard/i }));
    await vi.advanceTimersByTimeAsync(0); // flush the optimistic-hide state update

    // Deferred: nothing reverted yet.
    expect(w.discardHunk).not.toHaveBeenCalled();
    const statBefore = vi.mocked(w.diffStat).mock.calls.length;

    // Let the 6s undo window elapse with no Undo.
    await vi.advanceTimersByTimeAsync(6000);

    expect(w.discardHunk).toHaveBeenCalledTimes(1);
    expect(w.discardHunk).toHaveBeenCalledWith("/wt", "src/main.go", 0);
    // A committed discard refreshes the file list and notifies the parent.
    expect(vi.mocked(w.diffStat).mock.calls.length).toBeGreaterThan(statBefore);
    expect(onDiffChanged).toHaveBeenCalledTimes(1);
  } finally {
    vi.useRealTimers();
  }
});

test("renders an error message (not 'No changes') when the initial diffStat rejects", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  const w = await import("./wails");
  vi.mocked(w.diffStat).mockRejectedValueOnce(new Error("git failed"));

  render(DiffView, { props: { worktree: "/wt-err" } });

  await waitFor(() => expect(screen.getByText(/could not load diff/i)).toBeInTheDocument());
  // A git error must NOT masquerade as an empty diff.
  expect(screen.queryByText(/^no changes$/i)).toBeNull();
});

// --- per-hunk send-to-agent button ---

test("send hunk to agent button not rendered when onSendToAgent prop is absent", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  render(DiffView, { props: { worktree: "/wt" } });
  await waitFor(() => screen.getByText("src/main.go"));
  // Expand src/main.go
  await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));
  await waitFor(() => screen.getByRole("button", { name: /stage/i }));
  // No send button appears without the prop.
  expect(screen.queryByRole("button", { name: /send hunk to agent/i })).toBeNull();
});

test("send hunk to agent button calls onSendToAgent with hunk lines joined", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  const spy = vi.fn();

  render(DiffView, { props: { worktree: "/wt", onSendToAgent: spy } });
  await waitFor(() => screen.getByText("src/main.go"));

  // Expand src/main.go to show its hunk
  await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));
  await waitFor(() => screen.getByRole("button", { name: /send hunk to agent/i }));

  // Click the send button
  await fireEvent.click(screen.getByRole("button", { name: /send hunk to agent/i }));

  // The spy must receive the hunk's lines joined by "\n".
  const expectedText = fakeHunks[0].lines.map((l) => l.text).join("\n");
  expect(spy).toHaveBeenCalledWith(expectedText);
  expect(spy).toHaveBeenCalledTimes(1);
});

// --- Behavior 3: hunk row dragstart sets application/x-perch-text ---

test("dragstart on a hunk row sets application/x-perch-text to the hunk text", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  render(DiffView, { props: { worktree: "/wt" } });
  await waitFor(() => screen.getByText("src/main.go"));

  // Expand src/main.go to reveal the hunk
  await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));
  await waitFor(() => screen.getByText("@@ -1,3 +1,4 @@"));

  // The hunk div is the nearest draggable ancestor of the header text
  const hunkHeader = screen.getByText("@@ -1,3 +1,4 @@");
  const hunkEl = hunkHeader.closest("[draggable]") as HTMLElement;
  expect(hunkEl).toBeTruthy();

  const store = new Map<string, string>();
  const dt = {
    setData: vi.fn((type: string, value: string) => { store.set(type, value); }),
    getData: (type: string) => store.get(type) ?? "",
    effectAllowed: "uninitialized" as string,
    files: [],
    types: [] as string[],
  };
  await fireEvent.dragStart(hunkEl, { dataTransfer: dt });

  const expectedText = fakeHunks[0].lines.map((l) => l.text).join("\n");
  expect(dt.setData).toHaveBeenCalledWith("application/x-perch-text", expectedText);
  expect(dt.effectAllowed).toBe("copy");
});

// --- F5: fast fetches must not flash the "Loading…" placeholder ---

test("a fast diffStat resolve does not flash the 'Loading…' placeholder", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  render(DiffView, { props: { worktree: "/wt-fast" } });
  // The component arms the placeholder after a delay, so the placeholder stays absent even synchronously.
  expect(screen.queryByText("Loading…")).toBeNull();
  // The list arrives without the placeholder ever appearing.
  await waitFor(() => screen.getByText("src/main.go"));
  expect(screen.queryByText("Loading…")).toBeNull();
});

// --- F2-fe: staged hunks are no longer a one-way trap. Unstage is offered. ---

test("a staged hunk offers Unstage in place of Stage/Discard, and Unstage calls unstageHunk", async () => {
  const { default: DiffView } = await import("./DiffView.svelte");
  const w = await import("./wails");
  vi.mocked(w.unstageHunk).mockClear();
  // The first expand returns a STAGED hunk.
  vi.mocked(w.hunks).mockResolvedValueOnce([{ ...fakeHunks[0], staged: true }]);

  render(DiffView, { props: { worktree: "/wt" } });
  await waitFor(() => screen.getByText("src/main.go"));
  await fireEvent.click(screen.getByRole("button", { name: /src\/main\.go/ }));

  // When staged, the component offers only Unstage. The one-way-trap Stage and Discard buttons disappear.
  await waitFor(() => screen.getByRole("button", { name: /unstage/i }));
  expect(screen.queryByRole("button", { name: /^stage$/i })).toBeNull();
  expect(screen.queryByRole("button", { name: /discard/i })).toBeNull();

  await fireEvent.click(screen.getByRole("button", { name: /unstage/i }));
  expect(w.unstageHunk).toHaveBeenCalledWith("/wt", "src/main.go", 0);

  // The refetched (now unstaged) hunk toggles the action back to Stage and Discard.
  await waitFor(() => screen.getByRole("button", { name: /^stage$/i }));
  expect(screen.queryByRole("button", { name: /unstage/i })).toBeNull();
});
