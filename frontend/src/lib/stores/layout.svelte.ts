import { getLayout, saveLayout } from "../wails";
import {
  DEFAULT_SIDEBAR_W, DEFAULT_SHELL_H, LAYOUT_SAVE_DEBOUNCE_MS,
  SIDEBAR_MIN_W, SIDEBAR_MAX_W, SHELL_MIN_H, SHELL_MAX_H,
} from "../constants";

export type View = "agent" | "code" | "diff";

// Clamp a live value into [min, max]. The function coerces a non-finite
// value to min.
function clamp(v: number, min: number, max: number): number {
  if (!Number.isFinite(v)) return min;
  return Math.min(Math.max(v, min), max);
}

// Validate a persisted value. The function trusts only a finite number
// already in [min, max]. Anything else, such as NaN, plus or minus
// Infinity, an out-of-range value, or the wrong type, falls back to the
// default, so a corrupt-but-valid layout.json can never wedge the UI.
function validRange(v: unknown, min: number, max: number, fallback: number): number {
  return typeof v === "number" && Number.isFinite(v) && v >= min && v <= max ? v : fallback;
}

// Accept only a plain object of boolean values. A corrupt persisted
// `collapsed` value (a string, an array, or null) would otherwise get
// spread by setCollapsed into a malformed object. Reject anything that is
// not a plain object, and coerce each entry to boolean, dropping
// non-boolean values.
function validCollapsed(v: unknown): Record<string, boolean> {
  if (!v || typeof v !== "object" || Array.isArray(v)) return {};
  const out: Record<string, boolean> = {};
  for (const [key, val] of Object.entries(v as Record<string, unknown>)) {
    if (typeof val === "boolean") out[key] = val;
  }
  return out;
}

class LayoutStore {
  sidebarW  = $state<number>(DEFAULT_SIDEBAR_W);
  shellH    = $state<number>(DEFAULT_SHELL_H);
  view      = $state<View>("agent");
  split     = $state<boolean>(false);
  splitId   = $state<string | null>(null);
  collapsed = $state<Record<string, boolean>>({});
  order     = $state<string[]>([]);

  private timer: ReturnType<typeof setTimeout> | null = null;

  async restore(): Promise<void> {
    try {
      const raw = await getLayout();
      if (raw) {
        const s = JSON.parse(raw);
        this.sidebarW  = validRange(s.sidebarW, SIDEBAR_MIN_W, SIDEBAR_MAX_W, DEFAULT_SIDEBAR_W);
        this.shellH    = validRange(s.shellH,   SHELL_MIN_H,   SHELL_MAX_H,   DEFAULT_SHELL_H);
        this.view      = s.view      ?? "agent";
        this.split     = s.split     ?? false;
        this.splitId   = s.splitId   ?? null;
        this.collapsed = validCollapsed(s.collapsed);
        this.order     = Array.isArray(s.order) ? s.order : [];
      }
    } catch { /* Corrupt data. Keep the defaults. */ }
  }

  private save(): void {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = setTimeout(() => saveLayout(JSON.stringify({
      sidebarW: this.sidebarW, shellH: this.shellH,
      view: this.view, split: this.split, splitId: this.splitId,
      collapsed: this.collapsed, order: this.order,
    })), LAYOUT_SAVE_DEBOUNCE_MS);
  }

  setSidebarW(v: number):  void { this.sidebarW = clamp(v, SIDEBAR_MIN_W, SIDEBAR_MAX_W); this.save(); }
  setShellH(v: number):    void { this.shellH = clamp(v, SHELL_MIN_H, SHELL_MAX_H);       this.save(); }
  setView(v: View):        void { this.view = v;              this.save(); }
  toggleSplit():           void { this.split = !this.split;   this.save(); }
  setSplit(v: boolean):    void { this.split = v;             this.save(); }
  setSplitId(id: string | null): void { this.splitId = id;   this.save(); }
  setCollapsed(id: string, v: boolean): void { this.collapsed = { ...this.collapsed, [id]: v }; this.save(); }
  setOrder(ids: string[]): void { this.order = ids;           this.save(); }
}

export const layout = new LayoutStore();
