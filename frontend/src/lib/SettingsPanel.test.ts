// frontend/src/lib/SettingsPanel.test.ts
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { vi, describe, it, expect, beforeEach } from "vitest";
import type { AppSettings } from "./wails";

// ---------------------------------------------------------------------------
// Mock wails. getSettings resolves a fixture with 2 alwaysRules.
// saveSettings is a vi.fn() we can assert against.
// ---------------------------------------------------------------------------
const fixture: AppSettings = {
  theme: "gruvbox",
  density: "dense",
  font: "geist",
  dnd: false,
  glassDisabled: false,
  alwaysRules: [
    { agent: "claude", tool: "bash",     pattern: "npm test" },
    { agent: "claude", tool: "readFile", pattern: "/tmp/**" },
  ],
};

vi.mock("./wails", () => ({
  getSettings:  vi.fn(async () => ({ ...fixture, alwaysRules: [...fixture.alwaysRules] })),
  saveSettings: vi.fn(async () => {}),
}));

// Mock the notifications store. The tests use it for the live DND toggle.
vi.mock("./stores/notifications.svelte", () => ({
  getDnd: vi.fn(() => false),
  setDnd: vi.fn(),
}));

beforeEach(() => { vi.clearAllMocks(); });

describe("SettingsPanel — closed", () => {
  it("renders nothing when open=false", async () => {
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: false, onClose: vi.fn() } });
    expect(screen.queryByRole("dialog", { name: "Settings" })).not.toBeInTheDocument();
  });
});

describe("SettingsPanel — open, always-rules", () => {
  it("renders both always-rule rows (tool + pattern text) after async load", async () => {
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });
    // Rule 1
    await waitFor(() => expect(screen.getByText("bash")).toBeInTheDocument());
    expect(screen.getByText("npm test")).toBeInTheDocument();
    // Rule 2
    expect(screen.getByText("readFile")).toBeInTheDocument();
    expect(screen.getByText("/tmp/**")).toBeInTheDocument();
  });

  it("shows empty-state message when alwaysRules is empty", async () => {
    const w = await import("./wails");
    vi.mocked(w.getSettings).mockResolvedValueOnce({
      ...fixture, alwaysRules: [],
    });
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });
    await waitFor(() =>
      expect(screen.getByText(/no always-allow rules/i)).toBeInTheDocument()
    );
  });

  it("revoking first rule calls saveSettings exactly once with one fewer rule", async () => {
    const w = await import("./wails");
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    // Wait for both Revoke buttons to appear
    const revokeBtns = await screen.findAllByRole("button", { name: "Revoke" });
    expect(revokeBtns).toHaveLength(2);

    // Click the first Revoke button.
    await fireEvent.click(revokeBtns[0]);

    await waitFor(() =>
      expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
        expect.objectContaining({
          alwaysRules: [{ agent: "claude", tool: "readFile", pattern: "/tmp/**" }],
        })
      )
    );
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledTimes(1);
  });
});

describe("SettingsPanel — theme change persists", () => {
  it("changing the Theme select calls saveSettings exactly once with the new theme", async () => {
    const w = await import("./wails");
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    const select = await screen.findByLabelText("Theme");
    await fireEvent.change(select, { target: { value: "tokyo-night" } });

    await waitFor(() =>
      expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
        expect.objectContaining({ theme: "tokyo-night" })
      )
    );
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledTimes(1);
  });
});

describe("SettingsPanel — DND toggle persists", () => {
  it("toggling Do Not Disturb calls saveSettings exactly once with dnd=true", async () => {
    const w = await import("./wails");
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    // Wait for the panel to load (the rules appear).
    await screen.findAllByRole("button", { name: "Revoke" });

    const dndBtn = screen.getByRole("switch", { name: "Do not disturb" });
    await fireEvent.click(dndBtn);

    await waitFor(() =>
      expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
        expect.objectContaining({ dnd: true })
      )
    );
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledTimes(1);
  });
});

describe("SettingsPanel — glass toggle persists", () => {
  it("toggling Glass effects calls saveSettings exactly once with glassDisabled=true", async () => {
    const w = await import("./wails");
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    // Wait for the panel to load (the rules appear).
    await screen.findAllByRole("button", { name: "Revoke" });

    const glassBtn = screen.getByRole("switch", { name: "Glass effects" });
    // The default fixture has glassDisabled=false, so glass is ON (aria-checked=true).
    expect(glassBtn).toHaveAttribute("aria-checked", "true");
    await fireEvent.click(glassBtn);

    await waitFor(() =>
      expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
        expect.objectContaining({ glassDisabled: true })
      )
    );
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledTimes(1);
  });
});

describe("SettingsPanel — security caveat and pattern display", () => {
  it("renders the security caveat text when the panel is open", async () => {
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });
    await waitFor(() =>
      expect(
        screen.getByText(/each rule matches one exact tool input/i)
      ).toBeInTheDocument()
    );
  });

  it("renders the pattern for a rule with pattern '/tmp/**' as visible text", async () => {
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });
    await waitFor(() =>
      expect(screen.getByText("/tmp/**")).toBeInTheDocument()
    );
  });

  it("does not render the pattern inside an input or textarea (display-only)", async () => {
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    const { container } = render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });
    await screen.findAllByRole("button", { name: "Revoke" });
    const inputs = container.querySelectorAll("input, textarea");
    // None of the input/textarea elements (if any) should contain the pattern value
    for (const el of Array.from(inputs)) {
      const val = (el as HTMLInputElement | HTMLTextAreaElement).value;
      expect(val).not.toBe("/tmp/**");
      expect(val).not.toBe("npm test");
    }
  });
});

describe("SettingsPanel — F52 appearance display names", () => {
  it("shows human-readable option labels while keeping slugs as the values", async () => {
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    const themeSelect = (await screen.findByLabelText("Theme")) as HTMLSelectElement;
    const densitySelect = screen.getByLabelText("Density") as HTMLSelectElement;
    const fontSelect = screen.getByLabelText("Font") as HTMLSelectElement;

    const optByText = (sel: HTMLSelectElement, text: string) =>
      Array.from(sel.options).find((o) => o.textContent?.trim() === text);

    // The select shows the display name and keeps the slug as the value.
    expect(optByText(themeSelect, "Tokyo Night")?.value).toBe("tokyo-night");
    expect(optByText(themeSelect, "Rosé Pine")?.value).toBe("rose-pine");
    expect(optByText(densitySelect, "Comfortable")?.value).toBe("comfortable");
    expect(optByText(fontSelect, "IBM Plex")?.value).toBe("ibm-plex");

    // The select no longer renders raw slugs as option text.
    expect(optByText(themeSelect, "tokyo-night")).toBeUndefined();
    expect(optByText(fontSelect, "ibm-plex")).toBeUndefined();
  });
});

describe("SettingsPanel — stale threshold persists", () => {
  it("renders the input with the current staleThresholdDays value", async () => {
    const w = await import("./wails");
    vi.mocked(w.getSettings).mockResolvedValueOnce({ ...fixture, staleThresholdDays: 14 });
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    const input = (await screen.findByLabelText(/stale/i)) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe("14"));
  });

  it("renders the input blank when staleThresholdDays is unset", async () => {
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    const input = (await screen.findByLabelText(/stale/i)) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe(""));
  });

  it("changing the stale threshold to a valid positive integer calls saveSettings exactly once with the new value", async () => {
    const w = await import("./wails");
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    const input = (await screen.findByLabelText(/stale/i)) as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "45" } });

    await waitFor(() =>
      expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
        expect.objectContaining({ staleThresholdDays: 45 })
      )
    );
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledTimes(1);
  });

  it("accepts 0 without raising an error", async () => {
    const w = await import("./wails");
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    const input = (await screen.findByLabelText(/stale/i)) as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "0" } });

    await waitFor(() =>
      expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
        expect.objectContaining({ staleThresholdDays: 0 })
      )
    );
    expect(screen.queryByText(/failed to save settings/i)).not.toBeInTheDocument();
  });

  it("accepts a blank value without raising an error", async () => {
    const w = await import("./wails");
    vi.mocked(w.getSettings).mockResolvedValueOnce({ ...fixture, staleThresholdDays: 14 });
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    const input = (await screen.findByLabelText(/stale/i)) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe("14"));
    await fireEvent.input(input, { target: { value: "" } });

    await waitFor(() =>
      expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
        expect.objectContaining({ staleThresholdDays: undefined })
      )
    );
    expect(screen.queryByText(/failed to save settings/i)).not.toBeInTheDocument();
  });

  it("ignores a negative value (no saveSettings call, input unchanged)", async () => {
    const w = await import("./wails");
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });

    const input = (await screen.findByLabelText(/stale/i)) as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "-5" } });

    expect(vi.mocked(w.saveSettings)).not.toHaveBeenCalled();
  });
});

describe("SettingsPanel — close behaviour", () => {
  it("clicking the close button calls onClose", async () => {
    const onClose = vi.fn();
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose } });
    await screen.findByRole("dialog", { name: "Settings" });
    await fireEvent.click(screen.getByRole("button", { name: "close settings" }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("pressing Escape calls onClose", async () => {
    const onClose = vi.fn();
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose } });
    const dialog = await screen.findByRole("dialog", { name: "Settings" });
    await fireEvent.keyDown(dialog, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });
});

describe("SettingsPanel — audit regressions (FEX-13)", () => {
  it("re-reads settings on every open, so a rule granted since startup is listed", async () => {
    const w = await import("./wails");
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    const r = render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });
    await waitFor(() => expect(screen.getAllByRole("button", { name: "Revoke" })).toHaveLength(2));
    await r.rerender({ open: false, onClose: vi.fn() });
    vi.mocked(w.getSettings).mockResolvedValueOnce({
      ...fixture, alwaysRules: [...fixture.alwaysRules, { agent: "claude", tool: "Edit", pattern: "*" }],
    });
    await r.rerender({ open: true, onClose: vi.fn() });
    await waitFor(() => expect(screen.getAllByRole("button", { name: "Revoke" })).toHaveLength(3));
  });

  it("revoke removes the rule from a fresh read, keeping rules granted after the panel loaded", async () => {
    const w = await import("./wails");
    const { default: SettingsPanel } = await import("./SettingsPanel.svelte");
    render(SettingsPanel, { props: { open: true, onClose: vi.fn() } });
    const revokeBtns = await screen.findAllByRole("button", { name: "Revoke" });
    const late = { agent: "claude", tool: "Edit", pattern: "*" };
    vi.mocked(w.getSettings).mockResolvedValueOnce({ ...fixture, alwaysRules: [...fixture.alwaysRules, late] });
    await fireEvent.click(revokeBtns[0]);
    await waitFor(() => expect(w.saveSettings).toHaveBeenCalled());
    const saved = vi.mocked(w.saveSettings).mock.calls.at(-1)![0];
    expect(saved.alwaysRules).toEqual([fixture.alwaysRules[1], late]);
  });
});
