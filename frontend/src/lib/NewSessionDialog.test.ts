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
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "main", "feat/my-feature", "", true);
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
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "", "feat/x", "", true);
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
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "", "feat/x", "", false);
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
  // Index 4 is the (empty) title; index 5 is the worktree boolean.
  expect(onCreate.mock.calls[0][4]).toBe("");
  expect(onCreate.mock.calls[0][5]).toBe(true);
});

// ── 6b. Name field (optional user-chosen session title) ──────────────────────

test("Name field renders as the first field with a hint", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  expect(screen.getByLabelText(/^name$/i)).toBeInTheDocument();
  expect(screen.getByText(/optional\. the repo, branch, and agent are shown next to the name\./i)).toBeInTheDocument();
});

test("entered name is passed to onCreate as the title argument", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  const onCreate = vi.fn();
  render(D, { props: defaultProps({ onCreate }) });
  await waitFor(() => screen.getByLabelText(/starting point/i));
  await fireEvent.input(screen.getByLabelText(/^name$/i), { target: { value: "My Session" } });
  await fireEvent.change(screen.getByLabelText(/starting point/i), { target: { value: "main" } });
  await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "feat/x" } });
  await fireEvent.click(screen.getByRole("button", { name: /create/i }));
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "main", "feat/x", "My Session", true);
});

test("empty name still creates (title passed as empty string)", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  const onCreate = vi.fn();
  render(D, { props: defaultProps({ onCreate }) });
  await waitFor(() => screen.getByLabelText(/starting point/i));
  await fireEvent.change(screen.getByLabelText(/starting point/i), { target: { value: "main" } });
  await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "feat/x" } });
  await fireEvent.click(screen.getByRole("button", { name: /create/i }));
  expect(onCreate.mock.calls[0][4]).toBe("");
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

// ── 7b. F#5 — friendly repo names in the Repo select ─────────────────────────

test("repo option shows the friendly name and branch from repoInfo, keeping the path as the value", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, {
    props: defaultProps({
      repos: ["/home/user/proj"],
      repoInfo: {
        "/home/user/proj": { path: "/home/user/proj", name: "proj", branch: "main", worktrees: [] },
      },
    }),
  });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  const repoSelect = screen.getByLabelText(/^repo$/i) as HTMLSelectElement;
  const option = repoSelect.options[0];
  expect(option.value).toBe("/home/user/proj");
  expect(option.textContent).toBe("proj · main");
});

test("repo option falls back to the raw path when no repoInfo entry exists for it (union entry)", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, {
    props: defaultProps({
      repos: ["/home/user/proj"],
      repoInfo: {}, // no entry — e.g. a workspace-derived worktree path
    }),
  });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  const repoSelect = screen.getByLabelText(/^repo$/i) as HTMLSelectElement;
  const option = repoSelect.options[0];
  expect(option.value).toBe("/home/user/proj");
  expect(option.textContent).toBe("/home/user/proj");
});

test("repo select renders correctly when repoInfo prop is omitted entirely", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() }); // no repoInfo prop at all
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  const repoSelect = screen.getByLabelText(/^repo$/i) as HTMLSelectElement;
  expect(repoSelect.options[0].textContent).toBe("/home/user/proj");
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

// ── 10. Null-safety: loadBranches resolves null ───────────────────────────────

test("does not crash and branch list is empty when loadBranches resolves null", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  const loadBranches = vi.fn(async (_repo: string) => null as unknown as string[]);
  render(D, { props: defaultProps({ loadBranches }) });
  // Dialog must render without throwing
  await waitFor(() => expect(screen.getByRole("dialog", { name: /new session/i })).toBeInTheDocument());
  // Wait for the load to settle so the loading placeholder is gone.
  await waitFor(() => expect(screen.queryByText(/loading branches/i)).not.toBeInTheDocument());
  // No branch options should be rendered in the "starting point" select (branches treated as [])
  const startingPointSelect = screen.getByRole("combobox", { name: /starting point/i });
  expect(startingPointSelect.querySelectorAll("option").length).toBe(0);
});

// ── 11. F29 — unique branch suggestion (no per-agent constant collision) ──────

test("typing a Name derives a slugified branch suggestion", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByLabelText(/^branch name$/i));
  await fireEvent.input(screen.getByLabelText(/^name$/i), { target: { value: "fix login" } });
  await waitFor(() => {
    const input = screen.getByLabelText(/^branch name$/i) as HTMLInputElement;
    expect(input.value).toBe("claude/fix-login");
  });
});

test("empty Name twice suggests claude/work then claude/work-2 (auto-increment)", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  // First default session: claude/work is free.
  const first = render(D, { props: defaultProps({ loadBranches: vi.fn(async () => ["main"]) }) });
  await waitFor(() => {
    const input = screen.getByLabelText(/^branch name$/i) as HTMLInputElement;
    expect(input.value).toBe("claude/work");
  });
  first.unmount();
  // Second default session: claude/work now exists as a branch → auto-increment.
  render(D, { props: defaultProps({ loadBranches: vi.fn(async () => ["main", "claude/work"]) }) });
  await waitFor(() => {
    const input = screen.getByLabelText(/^branch name$/i) as HTMLInputElement;
    expect(input.value).toBe("claude/work-2");
  });
});

test("editing the branch field stops auto-suggestion from clobbering it", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByLabelText(/^branch name$/i));
  await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "my/custom" } });
  // Changing the Name afterwards must NOT overwrite the user's branch choice.
  await fireEvent.input(screen.getByLabelText(/^name$/i), { target: { value: "something else" } });
  const input = screen.getByLabelText(/^branch name$/i) as HTMLInputElement;
  expect(input.value).toBe("my/custom");
});

// ── 12. F30 — base ref defaults to the repo's default branch ──────────────────

test("default baseRef is the repo's default branch, not the alphabetically-first branch", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps({ loadBranches: vi.fn(async () => ["dev", "feature-x", "main"]) }) });
  await waitFor(() => screen.getByLabelText(/starting point/i));
  await waitFor(() => {
    const sel = screen.getByLabelText(/starting point/i) as HTMLSelectElement;
    expect(sel.value).toBe("main");
  });
  // ...and the default branch is pinned to the top of the list.
  const options = Array.from((screen.getByLabelText(/starting point/i) as HTMLSelectElement).options).map((o) => o.value);
  expect(options[0]).toBe("main");
});

// ── 13. F43 — in-flight feedback (Creating… + Loading branches…) ──────────────

test("Create button reads 'Creating…' while onCreate is pending", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  let resolveCreate!: () => void;
  const onCreate = vi.fn(() => new Promise<void>((r) => { resolveCreate = r; }));
  render(D, { props: defaultProps({ onCreate }) });
  await waitFor(() => screen.getByLabelText(/starting point/i));
  await fireEvent.change(screen.getByLabelText(/starting point/i), { target: { value: "main" } });
  await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "feat/x" } });
  await fireEvent.click(screen.getByRole("button", { name: /^create$/i }));
  await waitFor(() => expect(screen.getByRole("button", { name: /creating/i })).toBeInTheDocument());
  resolveCreate();
  await waitFor(() => expect(screen.getByRole("button", { name: /^create$/i })).toBeInTheDocument());
});

test("selects show a 'Loading branches…' placeholder while the branch list is in flight", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  let resolveBranches!: (v: string[]) => void;
  const loadBranches = vi.fn(() => new Promise<string[]>((r) => { resolveBranches = r; }));
  render(D, { props: defaultProps({ loadBranches }) });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  expect(screen.getByText(/loading branches/i)).toBeInTheDocument();
  resolveBranches(["main", "feat/x"]);
  await waitFor(() => expect(screen.queryByText(/loading branches/i)).not.toBeInTheDocument());
});

// ── 14. F26b — inline error prop ──────────────────────────────────────────────

test("error prop renders inline as an alert above the actions", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps({ error: "A branch named 'feat/x' already exists." }) });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  const alert = screen.getByRole("alert");
  expect(alert).toHaveTextContent(/a branch named 'feat\/x' already exists\./i);
});

test("no alert is rendered when the error prop is absent", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

// ── 15. F57 — worktree hint reflects checked state ────────────────────────────

test("worktree checkbox shows a state-dependent hint", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  // Checked by default → isolated-worktree hint.
  expect(screen.getByText(/isolated git worktree/i)).toBeInTheDocument();
  // Uncheck → repo-mode hint mentioning a clean working tree.
  await fireEvent.click(screen.getByLabelText(/^worktree$/i));
  await waitFor(() => {
    expect(screen.getByText(/clean working tree/i)).toBeInTheDocument();
    expect(screen.queryByText(/isolated git worktree/i)).not.toBeInTheDocument();
  });
});

// ── 16. F50 — sentence-case title ─────────────────────────────────────────────

test("dialog heading uses sentence case", async () => {
  const { default: D } = await import("./NewSessionDialog.svelte");
  render(D, { props: defaultProps() });
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  expect(screen.getByRole("heading", { name: "New session" })).toBeInTheDocument();
});
