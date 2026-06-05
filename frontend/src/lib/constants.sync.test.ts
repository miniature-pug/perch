// frontend/src/lib/constants.sync.test.ts
//
// Cross-language sync guard for the values perch mirrors across the Wails IPC seam.
//
// MIRROR: app/app.go defaultTheme/Density/Font + ptyDataEventPrefix/ptyExitEventPrefix
// — Go's defaults_sync_test pins the same values; if you change one side, both
// tests fail until re-synced.
//
// There is no shared module across the IPC boundary, so these literals are
// duplicated by necessity. This test freezes the frontend half against the exact
// strings the Go side freezes, so a one-sided edit can never silently drift.

import { describe, it, expect } from "vitest";
import { DEFAULT_THEME, DEFAULT_DENSITY, DEFAULT_FONT } from "./constants";
import { EVT_PTY_DATA_PREFIX, EVT_PTY_EXIT_PREFIX } from "./wails";

describe("cross-language sync: settings defaults (mirror app/app.go)", () => {
  it("DEFAULT_THEME is pinned to 'gruvbox'", () => {
    expect(DEFAULT_THEME).toBe("gruvbox");
  });
  it("DEFAULT_DENSITY is pinned to 'dense'", () => {
    expect(DEFAULT_DENSITY).toBe("dense");
  });
  it("DEFAULT_FONT is pinned to 'geist'", () => {
    expect(DEFAULT_FONT).toBe("geist");
  });
});

describe("cross-language sync: pty event-name prefixes (mirror app/app.go)", () => {
  it("EVT_PTY_DATA_PREFIX is pinned to 'pty:data:'", () => {
    expect(EVT_PTY_DATA_PREFIX).toBe("pty:data:");
  });
  it("EVT_PTY_EXIT_PREFIX is pinned to 'pty:exit:'", () => {
    expect(EVT_PTY_EXIT_PREFIX).toBe("pty:exit:");
  });
});
