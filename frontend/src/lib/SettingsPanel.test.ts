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
  alwaysRules: [
    { agent: "claude", tool: "bash",     pattern: "npm test" },
    { agent: "claude", tool: "readFile", pattern: "/tmp/**" },
  ],
};

vi.mock("./wails", () => ({
  getSettings:  vi.fn(async () => ({ ...fixture, alwaysRules: [...fixture.alwaysRules] })),
  saveSettings: vi.fn(async () => {}),
}));

// Mock the settings store — live-apply calls go here.
vi.mock("./stores/settings.svelte", () => ({
  settings: {
    theme:      "gruvbox",
    density:    "dense",
    font:       "geist",
    dnd:        false,
    alwaysRules: [],
    load:        vi.fn(async () => {}),
    setTheme:    vi.fn(async () => {}),
    setDensity:  vi.fn(async () => {}),
    setFont:     vi.fn(async () => {}),
    setDnd:      vi.fn(async () => {}),
    setAlwaysRules: vi.fn(async () => {}),
  },
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

  it("revoking first rule calls saveSettings with one fewer rule", async () => {
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
  });
});

describe("SettingsPanel — theme change persists", () => {
  it("changing the Theme select calls saveSettings with the new theme", async () => {
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
  });
});

describe("SettingsPanel — DND toggle persists", () => {
  it("toggling Do Not Disturb calls saveSettings with dnd=true", async () => {
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
