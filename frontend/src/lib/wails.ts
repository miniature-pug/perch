// Typed seam over Wails-injected globals. Components import ONLY from here.
// Event names: colon-separated per the frozen Wails event table.

export interface StaleSessionVM {
  id: string;
  title: string;
  branch: string;
  agent: string;
  lastActive: string; // ISO timestamp
  added: number;
  removed: number;
  clean: boolean;
  merged: boolean;
  safe: boolean;
}
export interface WorkspaceVM {
  id: string; worktreePath: string; repoPath: string; agent: string; title: string; branch: string;
  state: AgentState; caps: AgentCaps; paneId: string; lastActive: string;
}
export type AgentState = "running"|"idle"|"awaiting-approval"|"awaiting-input"|"done"|"errored"|"exited";
export interface AgentCaps { approvals: boolean; attention: boolean; }
export interface AgentEvent {
  sessionId?: string; workspaceId: string; kind: "state"|"approval"|"question";
  state?: AgentState; approval?: ApprovalReq; err?: string;
}
export interface ApprovalReq { reqId: string; tool: string; summary: string; input?: string; }
export interface FileDiff { path: string; added: number; removed: number; status: "M"|"A"|"D"|"R"|"?"; }
export interface HunkLine { kind: "ctx"|"add"|"del"; text: string; }
export interface Hunk {
  file: string; index: number; header: string;
  oldStart: number; oldLines: number; newStart: number; newLines: number; lines: HunkLine[];
  staged?: boolean;
}
export interface FsNode { name: string; path: string; isDir: boolean; modified?: boolean; untracked?: boolean; }
export interface AlwaysRule { agent: string; tool: string; pattern: string; hash?: string; }
export interface AppSettings {
  theme: string; density: string; font: string; dnd: boolean; glassDisabled?: boolean; alwaysRules: AlwaysRule[];
  staleThresholdDays?: number;
}
export interface WorktreeInfo { path: string; branch: string; head: string; }
export interface RepoInfo { path: string; name: string; branch: string; worktrees: WorktreeInfo[]; }

interface App {
  ListWorkspaces(): Promise<WorkspaceVM[]>;
  CreateWorkspace(agent: string, repoPath: string, baseRef: string, branch: string, title: string, worktree: boolean): Promise<WorkspaceVM>;
  SetWorkspaceTitle(id: string, title: string): Promise<void>;
  WorkspaceForBranch(repoPath: string, branch: string): Promise<{ id: string; found: boolean }>;
  OpenWorkspace(id: string): Promise<void>;
  CloseWorkspace(id: string): Promise<void>;
  RemoveWorkspace(id: string): Promise<void>;
  ForceRemoveWorkspace(id: string): Promise<void>;
  ListStaleSessions(): Promise<StaleSessionVM[]>;
  CleanupSessions(ids: string[], force: boolean): Promise<void>;
  WriteToPty(paneId: string, data: number[]): Promise<void>;
  ResizePty(paneId: string, cols: number, rows: number): Promise<void>;
  OpenShell(paneId: string, cwd: string): Promise<void>;
  Approve(reqId: string, decision: string): Promise<void>;
  PendingApprovals(): Promise<{ workspaceId: string; req: ApprovalReq }[]>;
  DiffStat(worktree: string): Promise<FileDiff[]>;
  Hunks(worktree: string, file: string): Promise<Hunk[]>;
  StageHunk(worktree: string, file: string, index: number): Promise<void>;
  DiscardHunk(worktree: string, file: string, index: number): Promise<void>;
  UnstageHunk(worktree: string, file: string, index: number): Promise<void>;
  ListDir(absDir: string): Promise<FsNode[]>;
  ReadFile(absPath: string): Promise<string>;
  WriteFile(absPath: string, content: string): Promise<void>;
  RevealInFiles(absPath: string): Promise<void>;
  CopyPath(absPath: string): Promise<void>;
  Branches(repo: string): Promise<string[]>;
  DiscoverRepos(): Promise<RepoInfo[]>;
  GetLayout(): Promise<string>;
  SaveLayout(layoutJSON: string): Promise<void>;
  GetSettings(): Promise<AppSettings>;
  SaveSettings(s: AppSettings): Promise<void>;
  SetWindowFocus(focused: boolean): Promise<void>;
  HomeShellCwd(): Promise<string>;
}

declare global {
  interface Window {
    runtime: {
      EventsOn(event: string, cb: (...data: any[]) => void): () => void;
      // Native OS file drop. Delivers each dropped file's ABSOLUTE path (the DOM
      // drop event carries none on WebKitGTK). useDropTarget=false makes the
      // callback fire for every file drop; we hit-test the coordinates ourselves
      // against [data-drop-pane] in lib/osFileDrop.ts. Only one registration is
      // honored process-wide, so it is registered once in App.svelte.
      OnFileDrop(cb: (x: number, y: number, paths: string[]) => void, useDropTarget: boolean): void;
      OnFileDropOff(): void;
    };
    go: { app: { App: App } };
  }
}

const app = (): App => window.go.app.App;

// Workspace
export const listWorkspaces  = ()                                                 => app().ListWorkspaces();
export const createWorkspace      = (agent: string, repoPath: string, baseRef: string, branch: string, title: string, worktree: boolean) => app().CreateWorkspace(agent, repoPath, baseRef, branch, title, worktree);
export const setWorkspaceTitle    = (id: string, title: string)                                                           => app().SetWorkspaceTitle(id, title);
export const workspaceForBranch   = (repoPath: string, branch: string)                                                    => app().WorkspaceForBranch(repoPath, branch);
export const openWorkspace   = (id: string)                                       => app().OpenWorkspace(id);
export const closeWorkspace  = (id: string)                                       => app().CloseWorkspace(id);
export const removeWorkspace      = (id: string)                                  => app().RemoveWorkspace(id);
export const forceRemoveWorkspace = (id: string)                                  => app().ForceRemoveWorkspace(id);
export const listStaleSessions    = ()                                            => app().ListStaleSessions();
export const cleanupSessions      = (ids: string[], force: boolean)               => app().CleanupSessions(ids, force);
// PTY
export const writeToPty = (paneId: string, data: number[])                        => app().WriteToPty(paneId, data);
export const resizePty  = (paneId: string, cols: number, rows: number)            => app().ResizePty(paneId, cols, rows);
export const openShell  = (paneId: string, cwd: string)                           => app().OpenShell(paneId, cwd);
// Approvals
export const approve = (reqId: string, decision: "allow"|"deny"|"always")         => app().Approve(reqId, decision);
// Every approval still awaiting a decision — seeded on mount/open to rebuild the
// queue after a reload or a late open (the agent:event carrying it is one-shot).
export function pendingApprovals(): Promise<{ workspaceId: string; req: ApprovalReq }[]> { return app().PendingApprovals(); }
// Git
export const diffStat    = (worktree: string)                                     => app().DiffStat(worktree);
export const hunks       = (worktree: string, file: string)                       => app().Hunks(worktree, file);
export const stageHunk   = (worktree: string, file: string, index: number)       => app().StageHunk(worktree, file, index);
export const discardHunk = (worktree: string, file: string, index: number)       => app().DiscardHunk(worktree, file, index);
export const unstageHunk = (worktree: string, file: string, index: number)       => app().UnstageHunk(worktree, file, index);
export const branches      = (repo: string)                                       => app().Branches(repo);
export const discoverRepos = ()                                                   => app().DiscoverRepos();
// FS
export const listDir      = (absDir: string)                                      => app().ListDir(absDir);
export const readFile     = (absPath: string)                                     => app().ReadFile(absPath);
export const writeFile    = (absPath: string, content: string)                    => app().WriteFile(absPath, content);
export const revealInFiles = (absPath: string)                                    => app().RevealInFiles(absPath);
export const copyPath      = (absPath: string)                                    => app().CopyPath(absPath);
// Layout & Settings
export const getLayout       = ()                                                    => app().GetLayout();
export const saveLayout      = (layoutJSON: string)                                  => app().SaveLayout(layoutJSON);
export const getSettings     = ()                                                    => app().GetSettings();
export const saveSettings    = (s: AppSettings)                                      => app().SaveSettings(s);
// Window focus reporting
export const setWindowFocus  = (focused: boolean)                                   => app().SetWindowFocus(focused);
export const homeShellCwd    = ()                                                    => app().HomeShellCwd();

// Event name constants — MUST match Go's ptyDataEventPrefix / ptyExitEventPrefix in app/app.go.
// Any rename here requires a matching rename on the Go side.
export const EVT_AGENT            = "agent:event";
export const EVT_FS_CHANGED       = "fs:changed";
export const EVT_NOTIFY           = "notify";
export const EVT_PTY_DATA_PREFIX  = "pty:data:"; // append paneId to form full event name
export const EVT_PTY_EXIT_PREFIX  = "pty:exit:"; // append paneId to form full event name
export const EVT_WORKSPACE_ATTACH = "workspace:attach";

// Event helpers — colon-separated names match the frozen Wails event table.
export function onPtyData(paneId: string, cb: (bytes: Uint8Array) => void): () => void {
  return window.runtime.EventsOn(EVT_PTY_DATA_PREFIX + paneId, (data: number[]) => cb(Uint8Array.from(data)));
}
export function onPtyExit(paneId: string, cb: (code: number) => void): () => void {
  return window.runtime.EventsOn(EVT_PTY_EXIT_PREFIX + paneId, (p: { code: number }) => cb(p.code));
}
export function onAgentEvent(cb: (ev: AgentEvent) => void): () => void {
  return window.runtime.EventsOn(EVT_AGENT, (ev: AgentEvent) => cb(ev));
}
export function onFsChanged(cb: (p: { workspaceId: string; path: string }) => void): () => void {
  return window.runtime.EventsOn(EVT_FS_CHANGED, cb);
}
export function onNotify(
  cb: (p: { tier: "blocking"|"ambient"|"routine"; title: string; body: string; workspaceId: string }) => void,
): () => void {
  return window.runtime.EventsOn(EVT_NOTIFY, cb);
}
export function onWorkspaceAttach(cb: (p: { query: string }) => void): () => void {
  return window.runtime.EventsOn(EVT_WORKSPACE_ATTACH, cb);
}
