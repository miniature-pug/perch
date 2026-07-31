// frontend/src/lib/FileTree.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi, afterEach } from "vitest";
import type { FsNode } from "./wails";

// The default listing used by most tests. Tests that need a bespoke tree call
// mockListDir.mockImplementation(...); afterEach restores this default so no
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

  // A deeper fixture with two expandable dirs, so we can assert BOTH stay open.
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

  // The agent writes a new file under app; the parent bumps refresh.
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

  // The folder is deleted; refresh bumps and the root no longer lists it.
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

  // The previous worktree's entries are gone; the new root's entries show.
  await waitFor(() => expect(screen.getByText("bfile.ts")).toBeInTheDocument());
  expect(screen.queryByText("adir")).toBeNull();
  expect(screen.queryByText("afile.ts")).toBeNull();
});

// FIX C2 (robustness): the component-local `expanded` set must not leak across a
// root change within the SAME instance (no {#key} remount). This exercises the
// un-keyed path directly: a dir under the NEW root that shares a path with a dir
// expanded under the OLD root must NOT auto-expand from stale membership.
test("changing root clears expanded so a same-named dir under the new root stays collapsed", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");
  // Both roots contain a dir literally named the SAME absolute path segment set
  // under distinct roots; use overlapping child paths so a stale `expanded` entry
  // (if not cleared) would auto-expand the new root's dir.
  const state = {
    "/wt-a":       [{ name: "shared", path: "/shared", isDir: true }],
    "/wt-b":       [{ name: "shared", path: "/shared", isDir: true }],
    "/shared":     [{ name: "leaf.ts", path: "/shared/leaf.ts", isDir: false }],
  } as Record<string, FsNode[]>;
  mockListDir.mockImplementation(async (p: string) => state[p] ?? []);

  const { rerender } = render(FileTree, { props: { root: "/wt-a", onOpen: () => {}, refresh: 0 } });
  await waitFor(() => screen.getByText("shared"));
  // Expand the shared dir under root A → /shared enters the `expanded` set.
  await fireEvent.click(screen.getByRole("button", { name: /shared/ }));
  await waitFor(() => screen.getByText("leaf.ts"));
  expect(screen.getByRole("button", { name: /shared/ }).getAttribute("aria-expanded")).toBe("true");

  // Switch to root B. The `expanded` set must be cleared: the identically-pathed
  // dir under root B must render COLLAPSED, and its child must not be shown.
  await rerender({ root: "/wt-b", onOpen: () => {}, refresh: 0 });

  await waitFor(() =>
    expect(screen.getByRole("button", { name: /shared/ }).getAttribute("aria-expanded")).toBe("false")
  );
  expect(screen.queryByText("leaf.ts")).toBeNull();
});

// FIX C2 (race): two rapid back-to-back refresh bumps must not let a stale,
// losing rebuild prune an entry the winning rebuild needs. buildLevel's prune of
// vanished paths mutates the SHARED `expanded` set; it is guarded by the rebuild
// token so only the winning rebuild mutates it. We stall the first (losing)
// listDir so its prune would run AFTER the second rebuild starts — the guard must
// suppress it, keeping the folder expanded.
test("overlapping rebuilds: a stale rebuild must not prune expanded state", async () => {
  const { default: FileTree } = await import("./FileTree.svelte");

  const rootListing: FsNode[] = [{ name: "d", path: "/wt/d", isDir: true }];
  const childListing: FsNode[] = [{ name: "c.ts", path: "/wt/d/c.ts", isDir: false }];

  // Gate the FIRST root re-list (the losing rebuild) so we can order the awaits.
  let releaseFirst: () => void = () => {};
  const firstGate = new Promise<void>((r) => { releaseFirst = r; });
  let rootCall = 0;

  mockListDir.mockImplementation(async (p: string) => {
    if (p === "/wt/d") return childListing;
    if (p === "/wt") {
      rootCall += 1;
      // The 2nd root re-list (from the refresh=1 rebuild that arrives while the
      // 1st is still awaiting) is the LOSING/stale one in this ordering test:
      // hold it until we let the winning rebuild finish first.
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
  // at firstGate; the refresh=2 rebuild starts and, being latest, wins. If the
  // stalled rebuild's prune were NOT token-guarded it could delete "/wt/d" from
  // `expanded` when it finally resumes.
  await rerender({ root: "/wt", onOpen: () => {}, refresh: 1 });
  await rerender({ root: "/wt", onOpen: () => {}, refresh: 2 });

  // Let the stalled (losing) rebuild resume — it must NOT prune the expanded dir.
  releaseFirst();

  // The winning rebuild's result stands and the folder stays expanded.
  await waitFor(() =>
    expect(screen.getByRole("button", { name: /^d/ }).getAttribute("aria-expanded")).toBe("true")
  );
  expect(screen.getByText("c.ts")).toBeInTheDocument();
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
