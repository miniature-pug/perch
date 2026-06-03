// frontend/src/lib/NewSessionDialog.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

test("onCreate fires with agent, repo, branch, model", async () => {
  const { default: NewSessionDialog } = await import("./NewSessionDialog.svelte");
  const onCreate = vi.fn();
  const loadBranches = vi.fn(async (_repo: string) => ["main", "feat/x"]);
  render(NewSessionDialog, { props: {
    open: true, repos: ["/home/user/proj"],
    loadBranches,
    onCreate, onClose: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  // Wait for branches to load asynchronously — assert the specific mocked values are present.
  await waitFor(() => expect(screen.getByLabelText(/branch/i)).toBeInTheDocument());
  await waitFor(() => {
    expect(screen.getByRole("option", { name: "main" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "feat/x" })).toBeInTheDocument();
  });
  await fireEvent.change(screen.getByLabelText(/repo/i),   { target: { value: "/home/user/proj" } });
  // After repo change, wait for branch options to reload with specific values.
  await waitFor(() => {
    expect(screen.getByRole("option", { name: "main" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "feat/x" })).toBeInTheDocument();
  });
  await fireEvent.change(screen.getByLabelText(/branch/i), { target: { value: "feat/x" } });
  await fireEvent.change(screen.getByLabelText(/model/i),  { target: { value: "claude-opus-4-5" } });
  await fireEvent.click(screen.getByRole("button", { name: /create/i }));
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "feat/x", "claude-opus-4-5");
});

test("branches load from loadBranches when dialog opens", async () => {
  const { default: NewSessionDialog } = await import("./NewSessionDialog.svelte");
  const loadBranches = vi.fn(async (_repo: string) => ["main", "dev", "release"]);
  render(NewSessionDialog, { props: {
    open: true, repos: ["/home/user/proj"],
    loadBranches,
    onCreate: vi.fn(), onClose: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  // All branches returned by loadBranches should render as options
  await waitFor(() => {
    expect(screen.getByRole("option", { name: "main" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "dev" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "release" })).toBeInTheDocument();
  });
  expect(loadBranches).toHaveBeenCalledWith("/home/user/proj");
});

test("changing repo triggers loadBranches with the new repo", async () => {
  const { default: NewSessionDialog } = await import("./NewSessionDialog.svelte");
  const loadBranches = vi.fn()
    .mockImplementation(async (repo: string) => repo === "/home/user/projA" ? ["main"] : ["feat/y"]);
  render(NewSessionDialog, { props: {
    open: true, repos: ["/home/user/projA", "/home/user/projB"],
    loadBranches,
    onCreate: vi.fn(), onClose: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  // Initially projA branches
  await waitFor(() => expect(screen.getByRole("option", { name: "main" })).toBeInTheDocument());

  // Change to projB
  await fireEvent.change(screen.getByLabelText(/repo/i), { target: { value: "/home/user/projB" } });
  // projB branches should now be shown
  await waitFor(() => expect(screen.getByRole("option", { name: "feat/y" })).toBeInTheDocument());
  expect(loadBranches).toHaveBeenCalledWith("/home/user/projB");
});
