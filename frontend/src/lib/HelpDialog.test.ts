// frontend/src/lib/HelpDialog.test.ts
import { render, screen, fireEvent } from "@testing-library/svelte";
import { tick } from "svelte";
import { vi, describe, it, expect } from "vitest";
import HelpDialog from "./HelpDialog.svelte";

describe("HelpDialog — closed", () => {
  it("renders nothing when open=false", () => {
    render(HelpDialog, { props: { open: false, onClose: vi.fn() } });
    expect(screen.queryByRole("dialog", { name: "help" })).not.toBeInTheDocument();
  });
});

describe("HelpDialog — open", () => {
  it("renders the dialog with role=dialog aria-label=help when open=true", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    expect(screen.getByRole("dialog", { name: "help" })).toBeInTheDocument();
  });

  it("contains the Toggle split shortcut text", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    expect(screen.getByText(/Toggle split/i)).toBeInTheDocument();
  });

  it("contains the g d / Diff view shortcut row", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    // The table has 'g d' and 'Diff view' as separate cells — check both present
    expect(screen.getByText("g d")).toBeInTheDocument();
    expect(screen.getAllByText(/Diff view/i).length).toBeGreaterThan(0);
  });

  it("contains the About perch section and description", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    expect(screen.getByText(/About perch/i)).toBeInTheDocument();
    expect(screen.getByText(/perch — a worktree-native cockpit for agent-assisted coding\./i)).toBeInTheDocument();
  });

  it("contains the close button", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    expect(screen.getByRole("button", { name: "close help" })).toBeInTheDocument();
  });
});

describe("HelpDialog — F46 complete keymap", () => {
  it("includes the previously-missing shortcut rows (Ctrl-K, Ctrl-b, g t / g T, Ctrl-S)", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    expect(screen.getByText("Ctrl-K")).toBeInTheDocument();
    expect(screen.getByText("Ctrl-b")).toBeInTheDocument();
    expect(screen.getByText("g t")).toBeInTheDocument();
    expect(screen.getByText("g T")).toBeInTheDocument();
    expect(screen.getByText("Ctrl-S")).toBeInTheDocument();
  });
});

describe("HelpDialog — global keymap parity with App.svelte", () => {
  it("lists the n / x / ? / F1 bindings implemented in App.svelte onKeyDown", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    expect(screen.getByText("n")).toBeInTheDocument();  // new session
    expect(screen.getByText("x")).toBeInTheDocument();  // remove selected session
    expect(screen.getByText("?")).toBeInTheDocument();  // open help
    expect(screen.getByText("F1")).toBeInTheDocument(); // open help
    expect(screen.getByText("New session")).toBeInTheDocument();
    expect(screen.getByText(/Remove the selected session/i)).toBeInTheDocument();
  });

  it("labels j/k in the code's real direction — j = next, k = previous", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    // Chips render j then k, so the paired action must read next-then-previous.
    expect(screen.getByText("Next / previous session")).toBeInTheDocument();
    // The old reversed label must be gone, so a future flip fails this test.
    expect(screen.queryByText("Previous / next session")).not.toBeInTheDocument();
  });

  it("documents the approval-card accelerators (a / d / ⇧A) from ApprovalCard.svelte", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    expect(screen.getByText("a")).toBeInTheDocument();  // allow
    expect(screen.getByText("d")).toBeInTheDocument();  // deny
    expect(screen.getByText("⇧A")).toBeInTheDocument(); // always allow
  });
});

describe("HelpDialog — F53 section views", () => {
  it("section='about' shows the About blurb but not the shortcuts table", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn(), section: "about" } });
    expect(screen.getByText(/a worktree-native cockpit/i)).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.queryByText("Keyboard Shortcuts")).not.toBeInTheDocument();
  });

  it("section='shortcuts' shows the table but not the About blurb", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn(), section: "shortcuts" } });
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.queryByText(/a worktree-native cockpit/i)).not.toBeInTheDocument();
  });

  it("default (no section) renders both the table and the About blurb", () => {
    render(HelpDialog, { props: { open: true, onClose: vi.fn() } });
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByText(/a worktree-native cockpit/i)).toBeInTheDocument();
  });
});

describe("HelpDialog — interactions", () => {
  it("clicking the close button calls onClose", async () => {
    const onClose = vi.fn();
    render(HelpDialog, { props: { open: true, onClose } });
    await fireEvent.click(screen.getByRole("button", { name: "close help" }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("pressing Escape calls onClose", async () => {
    const onClose = vi.fn();
    render(HelpDialog, { props: { open: true, onClose } });
    const dialog = screen.getByRole("dialog", { name: "help" });
    await fireEvent.keyDown(dialog, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });
});
