// frontend/src/lib/CrashScreen.test.ts
import { render, screen, fireEvent } from "@testing-library/svelte";
import { vi, describe, it, expect } from "vitest";

describe("CrashScreen", () => {
  it("renders the error message and data-testid for an Error object", async () => {
    const { default: CrashScreen } = await import("./CrashScreen.svelte");
    render(CrashScreen, {
      props: { error: new Error("boom"), onRetry: vi.fn(), onReload: vi.fn() },
    });
    expect(screen.getByTestId("crash-screen")).toBeInTheDocument();
    expect(screen.getByText("boom")).toBeInTheDocument();
  });

  it('"Try again" button calls onRetry once', async () => {
    const { default: CrashScreen } = await import("./CrashScreen.svelte");
    const onRetry = vi.fn();
    render(CrashScreen, {
      props: { error: new Error("oops"), onRetry, onReload: vi.fn() },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it('"Reload perch" button calls onReload once', async () => {
    const { default: CrashScreen } = await import("./CrashScreen.svelte");
    const onReload = vi.fn();
    render(CrashScreen, {
      props: { error: new Error("oops"), onRetry: vi.fn(), onReload },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Reload perch" }));
    expect(onReload).toHaveBeenCalledOnce();
  });

  it("renders non-Error string value via String(error) branch", async () => {
    const { default: CrashScreen } = await import("./CrashScreen.svelte");
    render(CrashScreen, {
      props: { error: "stringy failure", onRetry: vi.fn(), onReload: vi.fn() },
    });
    expect(screen.getByText("stringy failure")).toBeInTheDocument();
  });
});
