// Typed seam over the Wails-injected globals. Wails populates window.runtime
// (events) and window.go.app.App.* (bound methods) at app launch. Components
// import ONLY from here, so Vitest stubs a single surface.

export interface SessionInfo {
  id: string;
  session: string;
  window: string;
  paneId: string;
  status: string;
  dir: string;
}

export interface DiffResult {
  patch: string;
  files: number;
  added: number;
  removed: number;
}

// App is the bound-method surface mirroring app/App's exported Go methods.
interface App {
  ListSessions(): Promise<SessionInfo[]>;
  KillSession(id: string): Promise<void>;
  CreateAgent(tool: string, projectPath: string, branch: string): Promise<string>;
  OpenTerminal(tabID: string, sessionID: string): Promise<void>;
  WriteToPty(tabID: string, data: number[]): Promise<void>;
  ResizePty(tabID: string, cols: number, rows: number): Promise<void>;
  CloseTerminal(tabID: string): Promise<void>;
  Diff(worktreePath: string): Promise<DiffResult>;
}

declare global {
  interface Window {
    runtime: { EventsOn(event: string, cb: (...data: any[]) => void): () => void };
    go: { app: { App: App } };
  }
}

const app = (): App => window.go.app.App;

/** listSessions returns every live perch agent session. */
export const listSessions = () => app().ListSessions();
/** killSession kills the tmux window backing the agent session id. */
export const killSession = (id: string) => app().KillSession(id);
/** createAgent creates a worktree + launches an agent; returns the session id. */
export const createAgent = (tool: string, projectPath: string, branch: string) =>
  app().CreateAgent(tool, projectPath, branch);
/** openTerminal spawns an attach pty for sessionID, bound to tabID. */
export const openTerminal = (tabID: string, sessionID: string) =>
  app().OpenTerminal(tabID, sessionID);
/** writeToPty forwards keystroke bytes (as number[]) to the tab's pty. */
export const writeToPty = (tabID: string, data: number[]) => app().WriteToPty(tabID, data);
/** resizePty applies terminal dimensions to the tab's pty. */
export const resizePty = (tabID: string, cols: number, rows: number) =>
  app().ResizePty(tabID, cols, rows);
/** closeTerminal tears down the tab's attach pty (the agent session survives). */
export const closeTerminal = (tabID: string) => app().CloseTerminal(tabID);
/** diff returns the uncommitted diff + stat for a validated worktree path. */
export const diff = (worktreePath: string) => app().Diff(worktreePath);

/** onSessionsChanged subscribes to the backend's sessions-changed event; returns an unsubscribe fn. */
export function onSessionsChanged(cb: () => void): () => void {
  return window.runtime.EventsOn("sessions-changed", cb);
}

/**
 * onPtyData subscribes to the tab-scoped pty-data event and reconstructs the
 * byte stream. The backend emits a number[] (NOT a []byte — that would arrive
 * base64; see internal/pty pumpReader), so we rebuild a Uint8Array directly.
 */
export function onPtyData(tabID: string, cb: (bytes: Uint8Array) => void): () => void {
  return window.runtime.EventsOn("pty-data:" + tabID, (data: number[]) =>
    cb(Uint8Array.from(data)),
  );
}
