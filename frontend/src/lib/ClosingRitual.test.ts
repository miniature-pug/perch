import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

test("renders all three stat values in the DOM", async () => {
  const { default: ClosingRitual } = await import("./ClosingRitual.svelte");
  render(ClosingRitual, {
    props: {
      lines: 142,
      files: 7,
      sessions: 3,
      onDismiss: vi.fn(),
    },
  });
  await waitFor(() => screen.getByRole("dialog"));
  expect(screen.getByText("142")).toBeInTheDocument();
  expect(screen.getByText("7")).toBeInTheDocument();
  expect(screen.getByText("3")).toBeInTheDocument();
});

test("has role=dialog and aria-modal=true", async () => {
  const { default: ClosingRitual } = await import("./ClosingRitual.svelte");
  render(ClosingRitual, {
    props: { lines: 10, files: 2, sessions: 1, onDismiss: vi.fn() },
  });
  await waitFor(() => screen.getByRole("dialog"));
  const dialog = screen.getByRole("dialog");
  expect(dialog).toHaveAttribute("aria-modal", "true");
});

test("clicking the dismiss button calls onDismiss", async () => {
  const { default: ClosingRitual } = await import("./ClosingRitual.svelte");
  const onDismiss = vi.fn();
  render(ClosingRitual, {
    props: { lines: 50, files: 4, sessions: 2, onDismiss },
  });
  await waitFor(() => screen.getByRole("button", { name: "Done" }));
  await fireEvent.click(screen.getByRole("button", { name: "Done" }));
  expect(onDismiss).toHaveBeenCalledTimes(1);
});

test("pressing Escape calls onDismiss", async () => {
  const { default: ClosingRitual } = await import("./ClosingRitual.svelte");
  const onDismiss = vi.fn();
  render(ClosingRitual, {
    props: { lines: 88, files: 5, sessions: 1, onDismiss },
  });
  await waitFor(() => screen.getByRole("dialog"));
  await fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
  expect(onDismiss).toHaveBeenCalledTimes(1);
});
