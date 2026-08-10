// frontend/src/lib/Stage.test.ts
import { render, screen, fireEvent } from "@testing-library/svelte";
import { vi, describe, it, expect } from "vitest";
import type { View } from "./stores/layout.svelte";

const base = { view: "agent" as View, split: false, onView: vi.fn(), onSplit: vi.fn() };

describe("Stage", () => {
  it("renders Agent / Code / Diff buttons", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    render(Stage, { props: base });
    expect(screen.getByRole("button", { name: /agent/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /code/i  })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /diff/i  })).toBeInTheDocument();
  });
  it("calls onView('code') when Code is clicked", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    const onView = vi.fn();
    render(Stage, { props: { ...base, onView } });
    await fireEvent.click(screen.getByRole("button", { name: /code/i }));
    expect(onView).toHaveBeenCalledWith("code");
  });
  it("calls onSplit when Split button is clicked", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    const onSplit = vi.fn();
    render(Stage, { props: { ...base, onSplit } });
    await fireEvent.click(screen.getByRole("button", { name: /split/i }));
    expect(onSplit).toHaveBeenCalled();
  });
  it("renders two [data-pane] regions when split=true", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    render(Stage, { props: { ...base, split: true } });
    expect(document.querySelectorAll("[data-pane]").length).toBe(2);
  });
  it("renders one [data-pane] region when split=false", async () => {
    const { default: Stage } = await import("./Stage.svelte");
    render(Stage, { props: base });
    expect(document.querySelectorAll("[data-pane]").length).toBe(1);
  });
});
