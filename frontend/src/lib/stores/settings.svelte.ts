import { getSettings, saveSettings, type AppSettings } from "../wails";
import { DEFAULT_THEME, DEFAULT_DENSITY, DEFAULT_FONT } from "../constants";

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
    this.glass              = !(s.glassDisabled ?? false);
    this.alwaysRules        = s.alwaysRules ?? [];
    this.staleThresholdDays = s.staleThresholdDays;
  }

  private snap(): AppSettings {
    return { theme: this.theme, density: this.density, font: this.font,
             dnd: this.dnd, glassDisabled: !this.glass, alwaysRules: this.alwaysRules,
             staleThresholdDays: this.staleThresholdDays };
  }

  // Persist a UI-pref change without clobbering backend-owned state. alwaysRules
  // is backend-owned: Approve(...,"always") appends to the settings blob with no
  // event, so pull the latest before a UI-pref save or we clobber a rule added
  // since load().
  private async persistPref(): Promise<void> {
    try { const fresh = await getSettings(); this.alwaysRules = fresh.alwaysRules ?? []; } catch { /* keep current on read failure */ }
    await saveSettings(this.snap());
  }

  async setTheme(v: string): Promise<void>                              { this.theme = v;       await this.persistPref(); }
  async setDensity(v: "dense"|"comfortable"|"ultra"): Promise<void>     { this.density = v;     await this.persistPref(); }
  async setFont(v: string): Promise<void>                               { this.font = v;        await this.persistPref(); }
  async setDnd(v: boolean): Promise<void>                               { this.dnd = v;         await this.persistPref(); }
  async setGlass(v: boolean): Promise<void>                             { this.glass = v;       await this.persistPref(); }
  // setAlwaysRules is the authoritative writer of rules: it must NOT reload (that
  // would race its own write). The frontend never appends rules — only overwrites
  // via the settings UI — so its in-memory alwaysRules is authoritative here.
  async setAlwaysRules(v: AppSettings["alwaysRules"]): Promise<void>    { this.alwaysRules = v; await saveSettings(this.snap()); }
}

export const settings = new SettingsStore();
