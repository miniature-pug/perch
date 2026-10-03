// frontend/src/lib/ThemeProvider.test.ts
import { render } from "@testing-library/svelte";
import { describe, it, expect, beforeEach } from "vitest";

beforeEach(() => {
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.removeAttribute("data-density");
  document.documentElement.removeAttribute("data-glass");
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
    await rerender({ theme: "dracula" });
    expect(document.documentElement.getAttribute("data-theme")).toBe("dracula");
  });
  it("updates data-density on prop change", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    const { rerender } = render(ThemeProvider, { props: { theme: "gruvbox", density: "comfortable" } });
    await rerender({ theme: "gruvbox", density: "ultra" });
    expect(document.documentElement.getAttribute("data-density")).toBe("ultra");
  });
  it("defaults data-glass to 'on'", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    render(ThemeProvider, { props: { theme: "gruvbox" } });
    expect(document.documentElement.getAttribute("data-glass")).toBe("on");
  });
  it("sets data-glass='off' when glass=false", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    render(ThemeProvider, { props: { theme: "gruvbox", glass: false } });
    expect(document.documentElement.getAttribute("data-glass")).toBe("off");
  });
  it("updates data-glass on prop change", async () => {
    const { default: ThemeProvider } = await import("./ThemeProvider.svelte");
    const { rerender } = render(ThemeProvider, { props: { theme: "gruvbox", glass: true } });
    await rerender({ theme: "gruvbox", glass: false });
    expect(document.documentElement.getAttribute("data-glass")).toBe("off");
  });
});
