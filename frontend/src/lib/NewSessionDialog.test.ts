// frontend/src/lib/NewSessionDialog.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

// ── Helper ───────────────────────────────────────────────────────────────────

function makeBranches(repo: string): Promise<string[]> {
  return Promise.resolve(repo.includes("projB") ? ["feat/y", "dev"] : ["main", "feat/x"]);
}

function defaultProps(overrides: Record<string, unknown> = {}) {
  return {
    open: true,
    repos: ["/home/user/proj"],
    loadBranches: vi.fn(makeBranches),
    onCreate: vi.fn(),
    onClose: vi.fn(),
    ...overrides,
  };
}

// ── 1. Dialog structure ──────────────────────────────────────────────────────

test("dialog renders with aria-modal and correct label", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => expect(screen.getByRole("dialog", { name: /new session/i })).toBeInTheDocument());
  expect(screen.getByRole("dialog").getAttribute("aria-modal")).toBe("true");
});

test("model field is absent entirely", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  expect(screen.queryByLabelText(/model/i)).not.toBeInTheDocument();
  expect(screen.queryByText(/selected in the opencode tui/i)).not.toBeInTheDocument();
});

test("fields appear in order: Repo, Worktree, Starting point, Branch, Agent", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  // Load branches before asserting order
  await waitFor(() => screen.getByLabelText(/starting point/i));
  const labels = screen.getAllByText(/^(Repo|Worktree|Starting point|Branch|Agent)$/)
    .map(el => el.textContent?.trim());
  expect(labels).toEqual(["Repo", "Worktree", "Starting point", "Branch", "Agent"]);
});

// ── 2. Worktree toggle ───────────────────────────────────────────────────────

test("worktree defaults to true; Starting point and Branch text input visible", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  await waitFor(() => screen.getByLabelText(/starting point/i));
  expect(screen.getByLabelText(/starting point/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/^branch name$/i)).toBeInTheDocument();
  expect(screen.queryByLabelText(/^branch$/i)).not.toBeInTheDocument();
});

test("disabling worktree hides Starting point and sub-toggle; shows single branch dropdown", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  // Turn off worktree
  await fireEvent.click(screen.getByLabelText(/^worktree$/i));
  await waitFor(() => {
    expect(screen.queryByLabelText(/starting point/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/^branch name$/i)).not.toBeInTheDocument();
    expect(screen.getByLabelText(/^branch$/i)).toBeInTheDocument();
  });
});

// ── 3. Starting point (base-ref) ─────────────────────────────────────────────

test("Starting point dropdown is populated from loadBranches", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  await waitFor(() => {
    expect(screen.getByRole("option", { name: "main" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "feat/x" })).toBeInTheDocument();
  });
});

test("Starting point hidden when use-existing sub-toggle is on", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByLabelText(/starting point/i));
  await fireEvent.click(screen.getByLabelText(/use existing branch/i));
  await waitFor(() => expect(screen.queryByLabelText(/starting point/i)).not.toBeInTheDocument());
});

// ── 4. Branch field — new-name text input ────────────────────────────────────

test("branch name input is prefilled with agent/work suggestion", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByLabelText(/^branch name$/i));
  const input = screen.getByLabelText(/^branch name$/i) as HTMLInputElement;
  expect(input.value).toBe("claude/work");
});

test("invalid branch name disables Create and shows error message", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByLabelText(/^branch name$/i));
  await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "bad name!" } });
  await waitFor(() => {
    expect(screen.getByText(/invalid characters/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /create/i })).toBeDisabled();
  });
});

test("valid branch name enables Create and shows no error", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByLabelText(/^branch name$/i));
  await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "feat/my-feature" } });
  await waitFor(() => {
    expect(screen.queryByText(/invalid characters/i)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /create/i })).not.toBeDisabled();
  });
});

// ── 5. Use-existing branch sub-toggle ────────────────────────────────────────

test("use-existing sub-toggle switches branch control from text input to dropdown", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByLabelText(/use existing branch/i));
  await fireEvent.click(screen.getByLabelText(/use existing branch/i));
  await waitFor(() => {
    expect(screen.queryByLabelText(/^branch name$/i)).not.toBeInTheDocument();
    expect(screen.getByLabelText(/^branch$/i)).toBeInTheDocument();
  });
});

test("use-existing dropdown is populated from loadBranches", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByLabelText(/use existing branch/i));
  await fireEvent.click(screen.getByLabelText(/use existing branch/i));
  await waitFor(() => {
    expect(screen.getByRole("option", { name: "main" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "feat/x" })).toBeInTheDocument();
  });
});

// ── 6. onCreate emission shapes ──────────────────────────────────────────────

test("worktree=true new-branch: onCreate emits (agent, repo, baseRef, branch, true)", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  const onCreate = vi.fn();
  render(D, { props: defaultProps({ onCreate }) });
  await waitFor(() => screen.getByLabelText(/starting point/i));
  // Set baseRef
  await fireEvent.change(screen.getByLabelText(/starting point/i), { target: { value: "main" } });
  // Set branch name
  await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "feat/my-feature" } });
  await fireEvent.click(screen.getByRole("button", { name: /create/i }));
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "main", "feat/my-feature", true);
});

test("worktree=true existing-branch: onCreate emits baseRef='' and worktree=true", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  const onCreate = vi.fn();
  render(D, { props: defaultProps({ onCreate }) });
  await waitFor(() => screen.getByLabelText(/use existing branch/i));
  await fireEvent.click(screen.getByLabelText(/use existing branch/i));
  await waitFor(() => screen.getByLabelText(/^branch$/i));
  await fireEvent.change(screen.getByLabelText(/^branch$/i), { target: { value: "feat/x" } });
  await fireEvent.click(screen.getByRole("button", { name: /create/i }));
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "", "feat/x", true);
});

test("worktree=false: onCreate emits baseRef='' and worktree=false", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  const onCreate = vi.fn();
  render(D, { props: defaultProps({ onCreate }) });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  await fireEvent.click(screen.getByLabelText(/^worktree$/i));
  await waitFor(() => screen.getByLabelText(/^branch$/i));
  await fireEvent.change(screen.getByLabelText(/^branch$/i), { target: { value: "feat/x" } });
  await fireEvent.click(screen.getByRole("button", { name: /create/i }));
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "", "feat/x", false);
});

test("Agent field change flows through to onCreate", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  const onCreate = vi.fn();
  render(D, { props: defaultProps({ onCreate }) });
  await waitFor(() => screen.getByLabelText(/starting point/i));
  await fireEvent.change(screen.getByLabelText(/^agent$/i), { target: { value: "opencode" } });
  // Branch suggestion updates: opencode/work
  await waitFor(() => {
    const input = screen.getByLabelText(/^branch name$/i) as HTMLInputElement;
    expect(input.value).toBe("opencode/work");
  });
  await fireEvent.click(screen.getByRole("button", { name: /create/i }));
  expect(onCreate.mock.calls[0][0]).toBe("opencode");
  expect(onCreate.mock.calls[0][4]).toBe(true);
});

// ── 7. Repo change reloads branches ──────────────────────────────────────────

test("changing repo triggers loadBranches with the new repo", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  const loadBranches = vi.fn(makeBranches);
  render(D, { props: { open: true, repos: ["/home/user/projA", "/home/user/projB"], loadBranches, onCreate: vi.fn(), onClose: vi.fn() } });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  await waitFor(() => screen.getByRole("option", { name: "main" }));
  await fireEvent.change(screen.getByLabelText(/^repo$/i), { target: { value: "/home/user/projB" } });
  await waitFor(() => expect(screen.getByRole("option", { name: "feat/y" })).toBeInTheDocument());
  expect(loadBranches).toHaveBeenCalledWith("/home/user/projB");
});

// ── 8. Escape closes dialog ───────────────────────────────────────────────────

test("Escape key calls onClose", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  const onClose = vi.fn();
  render(D, { props: defaultProps({ onClose }) });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  await fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
  expect(onClose).toHaveBeenCalled();
});

// ── 9. CSS styling assertions ─────────────────────────────────────────────────

test("style block contains option background token", () => {
  // jsdom cannot resolve CSS custom properties; assert the raw style text instead.
  const styleText = [...document.querySelectorAll("style")].map(s => s.textContent).join("\n");
  // The style must contain option color theming so browser popups are dark.
  expect(styleText).toContain("background: var(--perch-bg)");
  expect(styleText).toContain("color: var(--perch-text)");
});

test("style block contains min-width: 0 for .field-select and .field-input", () => {
  const styleText = [...document.querySelectorAll("style")].map(s => s.textContent).join("\n");
  // Count occurrences — must appear for both .field-select and .field-input
  const count = (styleText.match(/min-width:\s*0/g) ?? []).length;
  expect(count).toBeGreaterThanOrEqual(2);
});
