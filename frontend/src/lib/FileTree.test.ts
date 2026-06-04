// frontend/src/lib/FileTree.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";
import type { FsNode } from "./wails";

const mockListDir = vi.fn(async (path: string): Promise<FsNode[]> => {
  if (path === "/wt") return [
    { name: "src",       path: "/wt/src",         isDir: true  },
    { name: "README.md", path: "/wt/README.md",   isDir: false },
  ];
  if (path === "/wt/src") return [{ name: "main.go", path: "/wt/src/main.go", isDir: false }];
  return [];
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
