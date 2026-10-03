// frontend/src/lib/FileTree.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi, afterEach } from "vitest";
import type { FsNode } from "./wails";

// This is the default listing used by most tests. Tests that need a bespoke tree call
// mockListDir.mockImplementation(...). afterEach then restores this default, so no
// per-test implementation leaks into a later test.
const defaultListDir = async (path: string): Promise<FsNode[]> => {
  if (path === "/wt") return [
    { name: "src",       path: "/wt/src",         isDir: true  },
    { name: "README.md", path: "/wt/README.md",   isDir: false },
  ];
  if (path === "/wt/src") return [{ name: "main.go", path: "/wt/src/main.go", isDir: false }];
  return [];
};

const mockListDir = vi.fn(defaultListDir);

afterEach(() => {
  mockListDir.mockImplementation(defaultListDir);
});

vi.mock("./wails", () => ({
  listDir: mockListDir,
  revealInFiles: vi.fn(async () => {}),
  copyPath: vi.fn((p: string) => p),
}));

test("renders root entries on mount", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => expect(screen.getByText("src")).toBeInTheDocument());
  expect(screen.getByText("README.md")).toBeInTheDocument();
});

test("expand dir fetches children", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  const w = await import("./wails");
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => screen.getByText("src"));
  await fireEvent.click(screen.getByRole("button", { name: /src/ }));
  await waitFor(() => expect(w.listDir).toHaveBeenCalledWith("/wt/src"));
  expect(screen.getByText("main.go")).toBeInTheDocument();
});

test("action menu Open fires onOpen", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  let opened = "";
  render(FileTree, { props: { root: "/wt", onOpen: (p: string) => (opened = p) } });
  await waitFor(() => screen.getByText("README.md"));
  await fireEvent.contextMenu(screen.getByText("README.md"));
  await waitFor(() => screen.getByRole("menuitem", { name: /open/i }));
  await fireEvent.click(screen.getByRole("menuitem", { name: /open/i }));
  expect(opened).toBe("/wt/README.md");
});

test("context menu ArrowDown moves focus to next item, ArrowUp back", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => screen.getByText("README.md"));
  await fireEvent.contextMenu(screen.getByText("README.md"));
  await waitFor(() => screen.getByRole("menuitem", { name: /open/i }));
  const openItem = screen.getByRole("menuitem", { name: /open/i });
  const revealItem = screen.getByRole("menuitem", { name: /reveal/i });
  // ArrowDown from Open should move focus to Reveal
  openItem.focus();
  await fireEvent.keyDown(openItem, { key: "ArrowDown" });
  expect(document.activeElement).toBe(revealItem);
  // ArrowUp from Reveal should return to Open
  await fireEvent.keyDown(revealItem, { key: "ArrowUp" });
  expect(document.activeElement).toBe(openItem);
});

test("context menu Enter on focused item triggers action", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  let opened = "";
  render(FileTree, { props: { root: "/wt", onOpen: (p: string) => (opened = p) } });
  await waitFor(() => screen.getByText("README.md"));
  await fireEvent.contextMenu(screen.getByText("README.md"));
  await waitFor(() => screen.getByRole("menuitem", { name: /open/i }));
  const openItem = screen.getByRole("menuitem", { name: /open/i });
  await fireEvent.keyDown(openItem, { key: "Enter" });
  expect(opened).toBe("/wt/README.md");
});

test("context menu Escape closes the menu", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => screen.getByText("README.md"));
  await fireEvent.contextMenu(screen.getByText("README.md"));
  await waitFor(() => screen.getByRole("menuitem", { name: /open/i }));
  const openItem = screen.getByRole("menuitem", { name: /open/i });
  await fireEvent.keyDown(openItem, { key: "Escape" });
  expect(screen.queryByRole("menuitem", { name: /open/i })).toBeNull();
});

// --- Behavior: git-status coloring classes ---

test("modified node gets is-modified class on its button", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  mockListDir.mockResolvedValueOnce([
    { name: "dirty.ts", path: "/wt/dirty.ts", isDir: false, modified: true, untracked: false },
    { name: "clean.ts", path: "/wt/clean.ts", isDir: false, modified: false, untracked: false },
  ]);
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => screen.getByText("dirty.ts"));
  const dirtyBtn = screen.getByRole("button", { name: /dirty\.ts/ });
  const cleanBtn = screen.getByRole("button", { name: /clean\.ts/ });
  expect(dirtyBtn.classList.contains("is-modified")).toBe(true);
  expect(cleanBtn.classList.contains("is-modified")).toBe(false);
});

test("untracked node gets is-untracked class on its button", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  mockListDir.mockResolvedValueOnce([
    { name: "new.ts", path: "/wt/new.ts", isDir: false, modified: false, untracked: true },
    { name: "old.ts", path: "/wt/old.ts", isDir: false, modified: false, untracked: false },
  ]);
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => screen.getByText("new.ts"));
  const newBtn = screen.getByRole("button", { name: /new\.ts/ });
  const oldBtn = screen.getByRole("button", { name: /old\.ts/ });
  expect(newBtn.classList.contains("is-untracked")).toBe(true);
  expect(oldBtn.classList.contains("is-untracked")).toBe(false);
});

// --- Behavior 2: file node dragstart sets @mention payload ---

test("dragstart on a file node sets application/x-perch-text to @<path>+space", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => screen.getByText("README.md"));
  const btn = screen.getByRole("button", { name: /README\.md/ });
  // Build a fake DataTransfer that records setData calls
  const store = new Map<string, string>();
  const dt = {
    setData: vi.fn((type: string, value: string) => { store.set(type, value); }),
    getData: (type: string) => store.get(type) ?? "",
    effectAllowed: "uninitialized" as string,
    files: [],
    types: [] as string[],
  };
  await fireEvent.dragStart(btn, { dataTransfer: dt });
  expect(dt.setData).toHaveBeenCalledWith("application/x-perch-text", "@/wt/README.md ");
  expect(dt.effectAllowed).toBe("copy");
});

test("dragstart on a dir node sets application/x-perch-text to @<path>+space", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  await waitFor(() => screen.getByText("src"));
  const btn = screen.getByRole("button", { name: /src/ });
  const store = new Map<string, string>();
  const dt = {
    setData: vi.fn((type: string, value: string) => { store.set(type, value); }),
    getData: (type: string) => store.get(type) ?? "",
    effectAllowed: "uninitialized" as string,
    files: [],
    types: [] as string[],
  };
  await fireEvent.dragStart(btn, { dataTransfer: dt });
  expect(dt.setData).toHaveBeenCalledWith("application/x-perch-text", "@/wt/src ");
  expect(dt.effectAllowed).toBe("copy");
});

// --- FIX C2: in-place refresh preserves expanded folders (no remount collapse) ---

test("refresh keeps expanded folders open and surfaces newly-added files", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");

  // A deeper fixture with two expandable dirs, so the test can assert BOTH stay open.
  const state = {
    "/wt":      [
      { name: "app",  path: "/wt/app",  isDir: true  },
      { name: "docs", path: "/wt/docs", isDir: true  },
    ],
    "/wt/app":  [{ name: "main.go", path: "/wt/app/main.go", isDir: false }],
    "/wt/docs": [{ name: "guide.md", path: "/wt/docs/guide.md", isDir: false }],
  } as Record<string, FsNode[]>;
  mockListDir.mockImplementation(async (p: string) => state[p] ?? []);

  const { rerender } = render(FileTree, { props: { root: "/wt", onOpen: () => {}, refresh: 0 } });
  await waitFor(() => screen.getByText("app"));

  // Expand both folders.
  await fireEvent.click(screen.getByRole("button", { name: /^app/ }));
  await waitFor(() => screen.getByText("main.go"));
  await fireEvent.click(screen.getByRole("button", { name: /^docs/ }));
  await waitFor(() => screen.getByText("guide.md"));

  // The agent writes a new file under app. The parent bumps refresh.
  state["/wt/app"] = [
    { name: "main.go", path: "/wt/app/main.go", isDir: false },
    { name: "new.go",  path: "/wt/app/new.go",  isDir: false },
  ];
  await rerender({ root: "/wt", onOpen: () => {}, refresh: 1 });

  // Both folders remain open (children still shown) and the new file appears.
  await waitFor(() => expect(screen.getByText("new.go")).toBeInTheDocument());
  expect(screen.getByText("main.go")).toBeInTheDocument();
  expect(screen.getByText("guide.md")).toBeInTheDocument();
  // aria-expanded on both dir buttons is still true.
  expect(screen.getByRole("button", { name: /^app/ }).getAttribute("aria-expanded")).toBe("true");
  expect(screen.getByRole("button", { name: /^docs/ }).getAttribute("aria-expanded")).toBe("true");
});

test("refresh drops an expanded folder that no longer exists", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  const state = {
    "/wt":     [{ name: "gone", path: "/wt/gone", isDir: true }],
    "/wt/gone": [{ name: "inside.ts", path: "/wt/gone/inside.ts", isDir: false }],
  } as Record<string, FsNode[]>;
  mockListDir.mockImplementation(async (p: string) => state[p] ?? []);

  const { rerender } = render(FileTree, { props: { root: "/wt", onOpen: () => {}, refresh: 0 } });
  await waitFor(() => screen.getByText("gone"));
  await fireEvent.click(screen.getByRole("button", { name: /gone/ }));
  await waitFor(() => screen.getByText("inside.ts"));

  // The folder is deleted. refresh bumps, and the root no longer lists it.
  state["/wt"] = [];
  await rerender({ root: "/wt", onOpen: () => {}, refresh: 1 });

  await waitFor(() => expect(screen.queryByText("gone")).toBeNull());
  expect(screen.queryByText("inside.ts")).toBeNull();
});

test("changing root resets the tree (session switch collapses previous expansion)", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  const state = {
    "/wt-a":     [{ name: "adir", path: "/wt-a/adir", isDir: true }],
    "/wt-a/adir": [{ name: "afile.ts", path: "/wt-a/adir/afile.ts", isDir: false }],
    "/wt-b":     [{ name: "bfile.ts", path: "/wt-b/bfile.ts", isDir: false }],
  } as Record<string, FsNode[]>;
  mockListDir.mockImplementation(async (p: string) => state[p] ?? []);

  const { rerender } = render(FileTree, { props: { root: "/wt-a", onOpen: () => {}, refresh: 0 } });
  await waitFor(() => screen.getByText("adir"));
  await fireEvent.click(screen.getByRole("button", { name: /adir/ }));
  await waitFor(() => screen.getByText("afile.ts"));

  // Switch sessions: root changes to a different worktree.
  await rerender({ root: "/wt-b", onOpen: () => {}, refresh: 0 });

  // The previous worktree's entries are gone. The new root's entries show.
  await waitFor(() => expect(screen.getByText("bfile.ts")).toBeInTheDocument());
  expect(screen.queryByText("adir")).toBeNull();
  expect(screen.queryByText("afile.ts")).toBeNull();
});

// FIX C2 (reliability): the component-local `expanded` set must not leak across a
// root change within the SAME instance (no {#key} remount). This exercises the
// un-keyed path directly. A dir under the NEW root that shares a path with a dir
// expanded under the OLD root must NOT auto-expand from stale membership.
test("changing root clears expanded so a same-named dir under the new root stays collapsed", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  // Both roots contain a dir literally named the SAME absolute path segment set
  // under distinct roots. The test uses overlapping child paths, so a stale `expanded` entry
  // (if not cleared) would auto-expand the new root's dir.
  const state = {
    "/wt-a":       [{ name: "shared", path: "/shared", isDir: true }],
    "/wt-b":       [{ name: "shared", path: "/shared", isDir: true }],
    "/shared":     [{ name: "leaf.ts", path: "/shared/leaf.ts", isDir: false }],
  } as Record<string, FsNode[]>;
  mockListDir.mockImplementation(async (p: string) => state[p] ?? []);

  const { rerender } = render(FileTree, { props: { root: "/wt-a", onOpen: () => {}, refresh: 0 } });
  await waitFor(() => screen.getByText("shared"));
  // Expand the shared dir under root A. /shared then enters the `expanded` set.
  await fireEvent.click(screen.getByRole("button", { name: /shared/ }));
  await waitFor(() => screen.getByText("leaf.ts"));
  expect(screen.getByRole("button", { name: /shared/ }).getAttribute("aria-expanded")).toBe("true");

  // Switch to root B. The component must clear the `expanded` set: the identically-pathed
  // dir under root B must render COLLAPSED, and its child must not appear.
  await rerender({ root: "/wt-b", onOpen: () => {}, refresh: 0 });

  await waitFor(() =>
    expect(screen.getByRole("button", { name: /shared/ }).getAttribute("aria-expanded")).toBe("false")
  );
  expect(screen.queryByText("leaf.ts")).toBeNull();
});

// FIX C2 (race): two rapid back-to-back refresh bumps must not let a stale,
// losing rebuild prune an entry the winning rebuild needs. buildLevel's prune of
// vanished paths mutates the SHARED `expanded` set. The rebuild token guards this set,
// so only the winning rebuild can mutate it. This test stalls the first (losing)
// listDir call, so its prune would run AFTER the second rebuild starts. The guard must
// suppress that prune, so the folder stays expanded.
test("overlapping rebuilds: a stale rebuild must not prune expanded state", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");

  const rootListing: FsNode[] = [{ name: "d", path: "/wt/d", isDir: true }];
  const childListing: FsNode[] = [{ name: "c.ts", path: "/wt/d/c.ts", isDir: false }];

  // Gate the FIRST root re-list (the losing rebuild), so the test can order the awaits.
  let releaseFirst: () => void = () => {};
  const firstGate = new Promise<void>((r) => { releaseFirst = r; });
  let rootCall = 0;

  mockListDir.mockImplementation(async (p: string) => {
    if (p === "/wt/d") return childListing;
    if (p === "/wt") {
      rootCall += 1;
      // The 2nd root re-list (from the refresh=1 rebuild that arrives while the
      // 1st is still awaiting) is the LOSING, stale one in this ordering test.
      // The test holds it until the winning rebuild finishes first.
      if (rootCall === 2) await firstGate;
      return rootListing;
    }
    return [];
  });

  const { rerender } = render(FileTree, { props: { root: "/wt", onOpen: () => {}, refresh: 0 } });
  await waitFor(() => screen.getByText("d"));
  await fireEvent.click(screen.getByRole("button", { name: /^d/ }));
  await waitFor(() => screen.getByText("c.ts"));

  // Bump refresh twice rapidly. The refresh=1 rebuild (2nd root listing) is stalled
  // at firstGate. The refresh=2 rebuild starts and wins, because it is the latest. If the
  // stalled rebuild's prune were NOT token-guarded, it could delete "/wt/d" from
  // `expanded` when it finally resumes.
  await rerender({ root: "/wt", onOpen: () => {}, refresh: 1 });
  await rerender({ root: "/wt", onOpen: () => {}, refresh: 2 });

  // Let the stalled (losing) rebuild resume. It must NOT prune the expanded dir.
  releaseFirst();

  // The winning rebuild's result stands and the folder stays expanded.
  await waitFor(() =>
    expect(screen.getByRole("button", { name: /^d/ }).getAttribute("aria-expanded")).toBe("true")
  );
  expect(screen.getByText("c.ts")).toBeInTheDocument();
});

// --- F11b: the rebuild is gated on `visible` (no listing fires on hide) ---

test("does not re-list while hidden, and re-lists a deferred refresh when shown", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  const w = await import("./wails");
  const { rerender } = render(FileTree, { props: { root: "/wt", onOpen: () => {}, refresh: 0 } });
  await waitFor(() => screen.getByText("src"));
  const callsBefore = vi.mocked(w.listDir).mock.calls.length;

  // Hide the tree AND bump refresh: an off-screen file write must not re-list.
  await rerender({ root: "/wt", onOpen: () => {}, refresh: 1, visible: false });
  await new Promise((r) => setTimeout(r, 30));
  expect(vi.mocked(w.listDir).mock.calls.length).toBe(callsBefore);

  // Reveal the tree: the component picks up the deferred refresh and re-lists the root.
  await rerender({ root: "/wt", onOpen: () => {}, refresh: 1, visible: true });
  await waitFor(() =>
    expect(vi.mocked(w.listDir).mock.calls.length).toBeGreaterThan(callsBefore)
  );
});

test("selectedPath marks the matching file row with is-selected + aria-current", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  mockListDir.mockImplementation(async (p: string) => (p === "/wt"
    ? [{ name: "README.md", path: "/wt/README.md", isDir: false }]
    : []));
  render(FileTree, { props: { root: "/wt", onOpen: () => {}, selectedPath: "/wt/README.md" } });
  await waitFor(() => screen.getByText("README.md"));
  const btn = screen.getByRole("button", { name: /README\.md/ });
  expect(btn.classList.contains("is-selected")).toBe(true);
  expect(btn.getAttribute("aria-current")).toBe("true");
});

// --- Audit regressions (FEX-3, FEX-18, FEX-19, FEX-27) ---

const deepTree = async (path: string): Promise<FsNode[]> => {
  if (path === "/wt") return [
    { name: "a", path: "/wt/a", isDir: true },
    { name: "b", path: "/wt/b", isDir: true },
  ];
  if (path === "/wt/a") return [{ name: "inner", path: "/wt/a/inner", isDir: true }];
  if (path === "/wt/a/inner") return [{ name: "deep.ts", path: "/wt/a/inner/deep.ts", isDir: false }];
  if (path === "/wt/b") return [{ name: "b.ts", path: "/wt/b/b.ts", isDir: false }];
  return [];
};

test("FEX-18: collapsing a folder forgets its expanded descendants", async () => {
  mockListDir.mockImplementation(deepTree);
  const { default: FileTree } = await import("./FileTree.svelte");
  const r = render(FileTree, { props: { root: "/wt", onOpen: () => {}, refresh: 0 } });
  await fireEvent.click(await screen.findByRole("button", { name: /^📁 a$|a$/ }));
  await fireEvent.click(await screen.findByRole("button", { name: /inner/ }));
  await screen.findByText("deep.ts");
  await fireEvent.click(screen.getByRole("button", { name: /^.*\ba$/ }));       // collapse a
  await fireEvent.click(await screen.findByRole("button", { name: /^.*\ba$/ })); // expand a again
  await screen.findByText("inner");
  expect(screen.queryByText("deep.ts")).toBeNull();
  // A refresh does not pop the old descendant open either.
  await r.rerender({ root: "/wt", onOpen: () => {}, refresh: 1 });
  await new Promise((res) => setTimeout(res, 20));
  expect(screen.queryByText("deep.ts")).toBeNull();
});

test("FEX-18: an expanded folder that fails to list does not break the rebuild", async () => {
  mockListDir.mockImplementation(deepTree);
  const { default: FileTree } = await import("./FileTree.svelte");
  const r = render(FileTree, { props: { root: "/wt", onOpen: () => {}, refresh: 0 } });
  await fireEvent.click(await screen.findByRole("button", { name: /^.*\ba$/ }));
  await screen.findByText("inner");
  mockListDir.mockImplementation(async (p: string) => {
    if (p === "/wt/a") throw new Error("permission denied");
    if (p === "/wt") return [...(await deepTree(p)), { name: "new.ts", path: "/wt/new.ts", isDir: false }];
    return deepTree(p);
  });
  await r.rerender({ root: "/wt", onOpen: () => {}, refresh: 1 });
  await screen.findByText("new.ts");
  expect(screen.queryByText("inner")).toBeNull();
});

test("FEX-18: an expand that resolves after a rebuild still opens the folder", async () => {
  mockListDir.mockImplementation(deepTree);
  const { default: FileTree } = await import("./FileTree.svelte");
  const r = render(FileTree, { props: { root: "/wt", onOpen: () => {}, refresh: 0 } });
  await screen.findByText("b");
  let release: (n: FsNode[]) => void = () => {};
  mockListDir.mockImplementation((p: string) =>
    p === "/wt/b" ? new Promise<FsNode[]>((res) => { release = res; }) : deepTree(p));
  await fireEvent.click(screen.getByRole("button", { name: /^.*\bb$/ }));
  await r.rerender({ root: "/wt", onOpen: () => {}, refresh: 1 }); // rebuild replaces node objects
  await new Promise((res) => setTimeout(res, 10));
  release([{ name: "b.ts", path: "/wt/b/b.ts", isDir: false }]);
  await screen.findByText("b.ts");
});

test("FEX-19: sibling folders are listed in parallel", async () => {
  const started: string[] = [];
  const pending: Array<() => void> = [];
  mockListDir.mockImplementation(deepTree);
  const { default: FileTree } = await import("./FileTree.svelte");
  const r = render(FileTree, { props: { root: "/wt", onOpen: () => {}, refresh: 0 } });
  await fireEvent.click(await screen.findByRole("button", { name: /^.*\ba$/ }));
  await screen.findByText("inner");
  await fireEvent.click(screen.getByRole("button", { name: /^.*\bb$/ }));
  await screen.findByText("b.ts");
  mockListDir.mockImplementation((p: string) => {
    started.push(p);
    if (p === "/wt") return deepTree(p);
    return new Promise<FsNode[]>((res) => { pending.push(() => res([])); });
  });
  await r.rerender({ root: "/wt", onOpen: () => {}, refresh: 1 });
  await waitFor(() => expect(started).toEqual(expect.arrayContaining(["/wt/a", "/wt/b"])));
  pending.forEach((f) => f());
});

test("FEX-3: context-menu Open on a folder expands it instead of opening it in the editor", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  const onOpen = vi.fn();
  render(FileTree, { props: { root: "/wt", onOpen } });
  await fireEvent.contextMenu(await screen.findByText("src"));
  await fireEvent.click(await screen.findByRole("menuitem", { name: /^open$/i }));
  await screen.findByText("main.go");
  expect(onOpen).not.toHaveBeenCalled();
});

test("FEX-27: dragging a path with a space sends a quoted @mention", async () => {
  mockListDir.mockImplementation(async (p: string) =>
    p === "/wt" ? [{ name: "my notes.md", path: "/wt/my notes.md", isDir: false }] : []);
  const { default: FileTree } = await import("./FileTree.svelte");
  render(FileTree, { props: { root: "/wt", onOpen: () => {} } });
  const btn = (await screen.findByText("my notes.md")).closest("button")!;
  const store = new Map<string, string>();
  const dt = { setData: (t: string, v: string) => { store.set(t, v); }, effectAllowed: "" };
  await fireEvent.dragStart(btn, { dataTransfer: dt });
  expect(store.get("application/x-perch-text")).toBe("@'/wt/my notes.md' ");
});
