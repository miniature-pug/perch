// frontend/src/lib/SettingsPanel.test.ts
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { vi, describe, it, expect, beforeEach } from "vitest";
import type { AppSettings } from "./wails";

// ---------------------------------------------------------------------------
// Mock wails — getSettings resolves a fixture with 2 alwaysRules;
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

// Mock the notifications store — used for live DND toggle.
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

    // Click first revoke
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

    // Wait for panel to load (rules appear)
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

    // Wait for panel to load (rules appear)
    await screen.findAllByRole("button", { name: "Revoke" });

    const glassBtn = screen.getByRole("switch", { name: "Glass effects" });
    // Default fixture has glassDisabled=false → glass is ON (aria-checked=true)
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

    // Display name shown, slug preserved as the value
    expect(optByText(themeSelect, "Tokyo Night")?.value).toBe("tokyo-night");
    expect(optByText(themeSelect, "Rosé Pine")?.value).toBe("rose-pine");
    expect(optByText(densitySelect, "Comfortable")?.value).toBe("comfortable");
    expect(optByText(fontSelect, "IBM Plex")?.value).toBe("ibm-plex");

    // Raw slugs are no longer rendered as option text
    expect(optByText(themeSelect, "tokyo-night")).toBeUndefined();
    expect(optByText(fontSelect, "ibm-plex")).toBeUndefined();
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
