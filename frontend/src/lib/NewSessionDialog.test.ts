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

// ---------------------------------------------------------------------------
// Feature 5: model field hidden for opencode agent
// ---------------------------------------------------------------------------

test("Feature 5: default agent (claude) — model input is present", async () => {
  const { default: NewSessionDialog } = await import("./NewSessionDialog.svelte");
  render(NewSessionDialog, { props: {
    open: true, repos: ["/home/user/proj"],
    loadBranches: vi.fn(async () => ["main"]),
    onCreate: vi.fn(), onClose: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));

  // claude is the default agent — model input must be rendered
  expect(screen.getByRole("textbox", { name: /model/i })).toBeInTheDocument();
  expect(screen.queryByText(/selected in the opencode tui/i)).not.toBeInTheDocument();
});

test("Feature 5: switching agent to opencode hides model input and shows note", async () => {
  const { default: NewSessionDialog } = await import("./NewSessionDialog.svelte");
  render(NewSessionDialog, { props: {
    open: true, repos: ["/home/user/proj"],
    loadBranches: vi.fn(async () => ["main"]),
    onCreate: vi.fn(), onClose: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));

  // Start with claude — model input present
  expect(screen.getByRole("textbox", { name: /model/i })).toBeInTheDocument();

  // Switch agent to opencode
  await fireEvent.change(screen.getByLabelText(/agent/i), { target: { value: "opencode" } });
  await waitFor(() => {
    // Model input must be gone
    expect(screen.queryByRole("textbox", { name: /model/i })).not.toBeInTheDocument();
    // Note explaining why is shown
    expect(screen.getByText(/selected in the opencode tui/i)).toBeInTheDocument();
  });

  // Switching back to claude restores the model input
  await fireEvent.change(screen.getByLabelText(/agent/i), { target: { value: "claude" } });
  await waitFor(() => {
    expect(screen.getByRole("textbox", { name: /model/i })).toBeInTheDocument();
    expect(screen.queryByText(/selected in the opencode tui/i)).not.toBeInTheDocument();
  });
});
