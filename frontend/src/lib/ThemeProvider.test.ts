// frontend/src/lib/ThemeProvider.test.ts
import { render } from "@testing-library/svelte";
import { describe, it, expect, beforeEach } from "vitest";

beforeEach(() => {
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.removeAttribute("data-density");
});

describe("ThemeProvider", () => {
  it("sets data-theme on documentElement", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    render(ThemeProvider, { props: { theme: "tokyo-night" } });
    expect(document.documentElement.getAttribute("data-theme")).toBe("tokyo-night");
  });
  it("defaults data-density to dense", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    render(ThemeProvider, { props: { theme: "gruvbox" } });
    expect(document.documentElement.getAttribute("data-density")).toBe("dense");
  });
  it("updates data-theme on prop change", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    const { rerender } = render(ThemeProvider, { props: { theme: "nord" } });
    await rerender({ props: { theme: "dracula" } });
    expect(document.documentElement.getAttribute("data-theme")).toBe("dracula");
  });
  it("updates data-density on prop change", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    const { rerender } = render(ThemeProvider, { props: { theme: "gruvbox", density: "comfortable" } });
    await rerender({ props: { theme: "gruvbox", density: "ultra" } });
    expect(document.documentElement.getAttribute("data-density")).toBe("ultra");
  });
});
