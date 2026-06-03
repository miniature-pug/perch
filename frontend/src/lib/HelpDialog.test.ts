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
