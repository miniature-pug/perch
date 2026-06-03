// frontend/src/lib/FileTree.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({
  listDir: vi.fn(async (path: string) => {
    if (path === "/wt") return [
      { name: "src",       path: "/wt/src",         isDir: true  },
      { name: "README.md", path: "/wt/README.md",   isDir: false },
    ];
    if (path === "/wt/src") return [{ name: "main.go", path: "/wt/src/main.go", isDir: false }];
    return [];
  }),
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
