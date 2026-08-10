// frontend/src/lib/shellPanes.ts
// Pure state helpers for many shell terminals per session. ShellPanel
// and App own rendering and pty side effects: openShell on cell mount,
// closeShell on tab close. Every list, active, and split transition lives
// here, so this logic is unit-testable without a DOM. Pane ids follow the
// backend scheme (workspaceIDForShellPane in app.go): the default shell is
// "shell-<wsId>", and additional shells are "shell-<wsId>_<seq>", using
// only [A-Za-z0-9_-]. The mint counter (seq) is monotonic. Ids are never
// reused within a run, so closing then re-adding a shell always yields a
// fresh {#each} key, a new xterm plus a fresh pty, and never a same-key
// collision that would leave a dead terminal.

export interface ShellPane {
  id: string;
}

export interface ShellState {
  panes: ShellPane[];
  activeId: string | null;
  // The right (secondary) shell when split; null means no split. Invariant:
  // this value is never equal to activeId.
  splitId: string | null;
  // Monotonic mint counter: the next seq to hand out. Never decremented.
  seq: number;
}

// shellPaneId builds a pane id for a session at a mint sequence. seq 0 is
// the default "shell-<wsId>", for backward compatibility with the
// pre-multi-terminal single drawer. seq 1 and above is "shell-<wsId>_<seq>".
// The wsId (a UUID) never contains '_', so the backend recovers it by
// cutting on the first '_'.
export function shellPaneId(wsId: string, seq: number): string {
  return seq === 0 ? `shell-${wsId}` : `shell-${wsId}_${seq}`;
}

// shellTabTitle names a tab by its current position (0-based index):
// "shell", "shell 2", and so on. The title is position-based, so it always
// reflects the visible count. Closing a tab renumbers the rest, which is
// fine for an ephemeral terminal list.
export function shellTabTitle(index: number): string {
  return index <= 0 ? "shell" : `shell ${index + 1}`;
}

// A reload target for the multi-shell reload picker: a pane id, its
// current position-based title, and whether it is the active (primary)
// tab.
export interface ShellReloadItem {
  id: string;
  title: string;
  active: boolean;
}

// reloadMenuItems lists every shell as a reload target, in tab order. Each
// entry carries its position-based title (shellTabTitle) and whether it is
// the active tab. Every pane is a valid target: the backend
// (ReloadAgentEnv) resolves any pane id on its own, so the picker can
// reload a shell that is not the focused tab.
export function reloadMenuItems(panes: ShellPane[], activeId: string | null): ShellReloadItem[] {
  return panes.map((p, i) => ({ id: p.id, title: shellTabTitle(i), active: p.id === activeId }));
}

// activeShellTitle is the position-based title of the active pane, or ""
// when there is no active pane. The reload split-button uses this title,
// for example "↻ env → agent · shell 2", so the plain reload's target
// shell stays visible when several shells are open.
export function activeShellTitle(panes: ShellPane[], activeId: string | null): string {
  const i = panes.findIndex((p) => p.id === activeId);
  return i < 0 ? "" : shellTabTitle(i);
}

// initShellState is the state for a freshly opened session: one default
// shell, active, no split. seq starts at 1, because seq 0 (the default id)
// has just been used.
export function initShellState(wsId: string): ShellState {
  const id = shellPaneId(wsId, 0);
  return { panes: [{ id }], activeId: id, splitId: null, seq: 1 };
}

// addShell mints a new shell, appends it, and makes it the active
// (primary) tab. The caller does not spawn it; the newly rendered cell's
// mount calls openShell.
export function addShell(st: ShellState, wsId: string): { state: ShellState; id: string } {
  const id = shellPaneId(wsId, st.seq);
  return {
    state: { panes: [...st.panes, { id }], activeId: id, splitId: st.splitId, seq: st.seq + 1 },
    id,
  };
}

// addSplitPartner mints a new shell as the split partner (right pane)
// without changing the active tab. It is used when the split toggles on
// with only one shell present.
export function addSplitPartner(st: ShellState, wsId: string): { state: ShellState; id: string } {
  const id = shellPaneId(wsId, st.seq);
  return {
    state: { panes: [...st.panes, { id }], activeId: st.activeId, splitId: id, seq: st.seq + 1 },
    id,
  };
}

// selectShell makes a tab the primary. Selecting the current split (right)
// tab swaps the two panes, so the picked one becomes primary, and
// activeId stays different from splitId.
export function selectShell(st: ShellState, id: string): ShellState {
  if (!st.panes.some((p) => p.id === id) || id === st.activeId) return st;
  if (id === st.splitId) return { ...st, activeId: id, splitId: st.activeId };
  return { ...st, activeId: id };
}

// removeShell drops a shell. `empty` true means the drawer is now empty,
// and the caller must add a replacement (the drawer is never empty). If
// the removed shell was active, an adjacent tab becomes active. The split
// clears if the removed shell was the split partner, if fewer than 2
// panes remain, or if the neighbour that becomes active was the partner.
export function removeShell(st: ShellState, id: string): { state: ShellState; empty: boolean } {
  const idx = st.panes.findIndex((p) => p.id === id);
  if (idx < 0) return { state: st, empty: st.panes.length === 0 };
  const panes = st.panes.filter((p) => p.id !== id);
  if (panes.length === 0) {
    return { state: { ...st, panes, activeId: null, splitId: null }, empty: true };
  }
  const activeId = st.activeId === id ? panes[Math.min(idx, panes.length - 1)].id : st.activeId;
  let splitId = st.splitId;
  if (splitId === id || panes.length < 2 || splitId === activeId) splitId = null;
  return { state: { ...st, panes, activeId, splitId }, empty: false };
}

// planToggleSplit describes a split toggle without mutating state:
// off means turn the split off. A set partnerId means use that existing
// pane as the partner. needNew means only one pane exists, so the caller
// must add a split partner: a brand-new shell.
export function planToggleSplit(st: ShellState): { off: boolean; partnerId: string | null; needNew: boolean } {
  if (st.splitId !== null) return { off: true, partnerId: null, needNew: false };
  const other = st.panes.find((p) => p.id !== st.activeId);
  if (other) return { off: false, partnerId: other.id, needNew: false };
  return { off: false, partnerId: null, needNew: true };
}
