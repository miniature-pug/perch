import { getSettings, saveSettings, type AppSettings } from "../wails";
import { DEFAULT_THEME, DEFAULT_DENSITY, DEFAULT_FONT } from "../constants";

export type { AppSettings };

class SettingsStore {
  theme       = $state<string>(DEFAULT_THEME);
  density     = $state<"dense" | "comfortable" | "ultra">(DEFAULT_DENSITY);
  font        = $state<string>(DEFAULT_FONT);
  dnd         = $state<boolean>(false);
  glass       = $state<boolean>(true);
  alwaysRules = $state<AppSettings["alwaysRules"]>([]);

  async load(): Promise<void> {
    const s = await getSettings();
    this.theme       = s.theme;
    this.density     = s.density as "dense" | "comfortable" | "ultra";
    this.font        = s.font;
    this.dnd         = s.dnd;
    this.glass       = !(s.glassDisabled ?? false);
    this.alwaysRules = s.alwaysRules ?? [];
  }

  private snap(): AppSettings {
    return { theme: this.theme, density: this.density, font: this.font,
             dnd: this.dnd, glassDisabled: !this.glass, alwaysRules: this.alwaysRules };
  }

  async setTheme(v: string): Promise<void>                              { this.theme = v;       await saveSettings(this.snap()); }
  async setDensity(v: "dense"|"comfortable"|"ultra"): Promise<void>     { this.density = v;     await saveSettings(this.snap()); }
  async setFont(v: string): Promise<void>                               { this.font = v;        await saveSettings(this.snap()); }
  async setDnd(v: boolean): Promise<void>                               { this.dnd = v;         await saveSettings(this.snap()); }
  async setGlass(v: boolean): Promise<void>                             { this.glass = v;       await saveSettings(this.snap()); }
  async setAlwaysRules(v: AppSettings["alwaysRules"]): Promise<void>    { this.alwaysRules = v; await saveSettings(this.snap()); }
}

export const settings = new SettingsStore();
