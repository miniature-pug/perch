import { getSettings, saveSettings, type AppSettings, type AlwaysRule } from "../wails";
import { DEFAULT_THEME, DEFAULT_DENSITY, DEFAULT_FONT } from "../constants";
import { setDnd as applyDnd } from "./notifications.svelte";

export type { AppSettings };

class SettingsStore {
  theme               = $state<string>(DEFAULT_THEME);
  density             = $state<"dense" | "comfortable" | "ultra">(DEFAULT_DENSITY);
  font                = $state<string>(DEFAULT_FONT);
  dnd                 = $state<boolean>(false);
  glass               = $state<boolean>(true);
  alwaysRules         = $state<AppSettings["alwaysRules"]>([]);
  staleThresholdDays  = $state<number | undefined>(undefined);

  async load(): Promise<void> {
    const s = await getSettings();
    this.theme              = s.theme;
    this.density            = s.density as "dense" | "comfortable" | "ultra";
    this.font               = s.font;
    this.dnd                = s.dnd;
    // The persisted value is the single source of truth for Do Not Disturb;
    // the notification store only applies it (FEC-7).
    applyDnd(!!s.dnd);
    this.glass              = !(s.glassDisabled ?? false);
    this.alwaysRules        = s.alwaysRules ?? [];
    this.staleThresholdDays = s.staleThresholdDays;
  }

  private snap(): AppSettings {
    return { theme: this.theme, density: this.density, font: this.font,
             dnd: this.dnd, glassDisabled: !this.glass, alwaysRules: this.alwaysRules,
             staleThresholdDays: this.staleThresholdDays };
  }

  // Persist a UI-pref change without overwriting backend-owned state.
  // alwaysRules is backend-owned: Approve(...,"always") appends to the
  // settings blob with no event. Pull the latest value before a UI-pref
  // save, or the save would overwrite a rule added since load().
  private async persistPref(): Promise<void> {
    try { const fresh = await getSettings(); this.alwaysRules = fresh.alwaysRules ?? []; } catch { /* keep current on read failure */ }
    await saveSettings(this.snap());
  }

  async setTheme(v: string): Promise<void>                              { this.theme = v;       await this.persistPref(); }
  async setDensity(v: "dense"|"comfortable"|"ultra"): Promise<void>     { this.density = v;     await this.persistPref(); }
  async setFont(v: string): Promise<void>                               { this.font = v;        await this.persistPref(); }
  // Applies DND to the notification store at once, then persists it, so the
  // hub toggle, the command and the Settings panel all share one value (FEC-7).
  async setDnd(v: boolean): Promise<void>                               { this.dnd = v; applyDnd(v); await this.persistPref(); }
  async setGlass(v: boolean): Promise<void>                             { this.glass = v;       await this.persistPref(); }
  async setStaleThresholdDays(v: number | undefined): Promise<void>     { this.staleThresholdDays = v; await this.persistPref(); }
  // setAlwaysRules is the authoritative writer of rules. It must not
  // reload, because a reload would race its own write. The frontend never
  // appends rules; it only overwrites them, through the settings UI. Its
  // in-memory alwaysRules value is therefore authoritative here.
  async setAlwaysRules(v: AppSettings["alwaysRules"]): Promise<void>    { this.alwaysRules = v; await saveSettings(this.snap()); }

  // Remove specific rules, matched by identity, from the backend's CURRENT
  // list. A rule granted after this store (or a settings panel) last read the
  // list survives, because the filter runs on a fresh read, not on a stale
  // copy (FEX-13, FEC-15). Returns the list that was saved.
  async removeAlwaysRules(remove: AlwaysRule[]): Promise<AlwaysRule[]> {
    const fresh = await getSettings();
    const keep = (fresh.alwaysRules ?? []).filter((r) => !remove.some((x) => sameAlwaysRule(x, r)));
    this.alwaysRules = keep;
    await saveSettings(this.snap());
    return keep;
  }
}

// Rule identity. The backend's hash covers the same fields when present.
export function sameAlwaysRule(a: AlwaysRule, b: AlwaysRule): boolean {
  if (a.hash && b.hash) return a.hash === b.hash;
  return a.agent === b.agent && a.tool === b.tool && a.pattern === b.pattern;
}

export const settings = new SettingsStore();
