import { getLayout, saveLayout } from "../wails";

export type View = "agent" | "code" | "diff";

class LayoutStore {
  sidebarW  = $state<number>(240);
  shellH    = $state<number>(200);
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
        this.sidebarW  = s.sidebarW  ?? 240;
        this.shellH    = s.shellH    ?? 200;
        this.view      = s.view      ?? "agent";
        this.split     = s.split     ?? false;
        this.splitId   = s.splitId   ?? null;
        this.collapsed = s.collapsed ?? {};
        this.order     = Array.isArray(s.order) ? s.order : [];
      }
    } catch { /* corrupt — keep defaults */ }
  }

  private save(): void {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = setTimeout(() => saveLayout(JSON.stringify({
      sidebarW: this.sidebarW, shellH: this.shellH,
      view: this.view, split: this.split, splitId: this.splitId,
      collapsed: this.collapsed, order: this.order,
    })), 300);
  }

  setSidebarW(v: number):  void { this.sidebarW = v;          this.save(); }
  setShellH(v: number):    void { this.shellH = v;            this.save(); }
  setView(v: View):        void { this.view = v;              this.save(); }
  toggleSplit():           void { this.split = !this.split;   this.save(); }
  setSplitId(id: string | null): void { this.splitId = id;   this.save(); }
  setCollapsed(id: string, v: boolean): void { this.collapsed = { ...this.collapsed, [id]: v }; this.save(); }
  setOrder(ids: string[]): void { this.order = ids;           this.save(); }
}

export const layout = new LayoutStore();
