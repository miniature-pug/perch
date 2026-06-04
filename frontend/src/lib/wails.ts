// Typed seam over Wails-injected globals. Components import ONLY from here.
// Event names: colon-separated per the frozen Wails event table.

export interface WorkspaceVM {
  id: string; worktreePath: string; agent: string; title: string; branch: string;
  state: AgentState; caps: AgentCaps; paneId: string; lastActive: string;
}
export type AgentState = "running"|"idle"|"awaiting-approval"|"done"|"errored";
export interface AgentCaps { approvals: boolean; attention: boolean; tokens: boolean; }
export interface AgentEvent {
  workspaceId: string; kind: "state"|"usage"|"approval";
  state?: AgentState; tokens?: number; cost?: number; approval?: ApprovalReq; err?: string;
}
export interface ApprovalReq { reqId: string; tool: string; summary: string; input?: string; }
export interface FileDiff { path: string; added: number; removed: number; status: "M"|"A"|"D"|"R"|"?"; }
export interface HunkLine { kind: "ctx"|"add"|"del"; text: string; }
export interface Hunk {
  file: string; index: number; header: string;
  oldStart: number; oldLines: number; newStart: number; newLines: number; lines: HunkLine[];
}
export interface FsNode { name: string; path: string; isDir: boolean; }
export interface AlwaysRule { agent: string; tool: string; pattern: string; hash?: string; }
export interface AppSettings {
  theme: string; density: string; font: string; dnd: boolean; alwaysRules: AlwaysRule[];
}
export interface WorktreeInfo { path: string; branch: string; head: string; }
export interface RepoInfo { path: string; name: string; branch: string; worktrees: WorktreeInfo[]; }

interface App {
  ListWorkspaces(): Promise<WorkspaceVM[]>;
  CreateWorkspace(agent: string, repoPath: string, branch: string, model: string): Promise<WorkspaceVM>;
  OpenWorkspace(id: string): Promise<void>;
  CloseWorkspace(id: string): Promise<void>;
  RemoveWorkspace(id: string): Promise<void>;
  WriteToPty(paneId: string, data: number[]): Promise<void>;
  ResizePty(paneId: string, cols: number, rows: number): Promise<void>;
  OpenShell(paneId: string, cwd: string): Promise<void>;
  Approve(reqId: string, decision: string): Promise<void>;
  DiffStat(worktree: string): Promise<FileDiff[]>;
  Hunks(worktree: string, file: string): Promise<Hunk[]>;
  StageHunk(worktree: string, file: string, index: number): Promise<void>;
  DiscardHunk(worktree: string, file: string, index: number): Promise<void>;
  ListDir(absDir: string): Promise<FsNode[]>;
  ReadFile(absPath: string): Promise<string>;
  WriteFile(absPath: string, content: string): Promise<void>;
  RevealInFiles(absPath: string): Promise<void>;
  CopyPath(absPath: string): Promise<void>;
  Branches(repo: string): Promise<string[]>;
  Worktrees(repo: string): Promise<WorktreeInfo[]>;
  DiscoverRepos(): Promise<RepoInfo[]>;
  GetLayout(): Promise<string>;
  SaveLayout(layoutJSON: string): Promise<void>;
  GetSettings(): Promise<AppSettings>;
  SaveSettings(s: AppSettings): Promise<void>;
  SetWindowFocus(focused: boolean): Promise<void>;
}

declare global {
  interface Window {
    runtime: { EventsOn(event: string, cb: (...data: any[]) => void): () => void };
    go: { app: { App: App } };
  }
}

const app = (): App => window.go.app.App;

// Workspace
export const listWorkspaces  = ()                                                 => app().ListWorkspaces();
export const createWorkspace = (agent: string, repoPath: string, branch: string, model: string) => app().CreateWorkspace(agent, repoPath, branch, model);
export const openWorkspace   = (id: string)                                       => app().OpenWorkspace(id);
export const closeWorkspace  = (id: string)                                       => app().CloseWorkspace(id);
export const removeWorkspace = (id: string)                                       => app().RemoveWorkspace(id);
// PTY
export const writeToPty = (paneId: string, data: number[])                        => app().WriteToPty(paneId, data);
export const resizePty  = (paneId: string, cols: number, rows: number)            => app().ResizePty(paneId, cols, rows);
export const openShell  = (paneId: string, cwd: string)                           => app().OpenShell(paneId, cwd);
// Approvals
export const approve = (reqId: string, decision: "allow"|"deny"|"always")         => app().Approve(reqId, decision);
// Git
export const diffStat    = (worktree: string)                                     => app().DiffStat(worktree);
export const hunks       = (worktree: string, file: string)                       => app().Hunks(worktree, file);
export const stageHunk   = (worktree: string, file: string, index: number)       => app().StageHunk(worktree, file, index);
export const discardHunk = (worktree: string, file: string, index: number)       => app().DiscardHunk(worktree, file, index);
export const branches      = (repo: string)                                       => app().Branches(repo);
export const worktrees     = (repo: string)                                       => app().Worktrees(repo);
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

// Event helpers — colon-separated names match the frozen Wails event table.
export function onPtyData(paneId: string, cb: (bytes: Uint8Array) => void): () => void {
  return window.runtime.EventsOn("pty:data:" + paneId, (data: number[]) => cb(Uint8Array.from(data)));
}
export function onPtyExit(paneId: string, cb: (code: number) => void): () => void {
  return window.runtime.EventsOn("pty:exit:" + paneId, (p: { code: number }) => cb(p.code));
}
export function onAgentEvent(cb: (ev: AgentEvent) => void): () => void {
  return window.runtime.EventsOn("agent:event", (ev: AgentEvent) => cb(ev));
}
export function onFsChanged(cb: (p: { workspaceId: string; path: string }) => void): () => void {
  return window.runtime.EventsOn("fs:changed", cb);
}
export function onNotify(
  cb: (p: { tier: "blocking"|"ambient"|"routine"; title: string; body: string; workspaceId: string }) => void,
): () => void {
  return window.runtime.EventsOn("notify", cb);
}
