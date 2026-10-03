// Typed seam over the Wails-injected globals. Components import only from
// here. Event names are colon-separated, per the frozen Wails event table.

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
  // True when reopening this session resumes the prior agent conversation.
  // False starts a fresh conversation. The resume-preview modal shows this
  // value.
  willResume?: boolean;
  // The branch or ref this worktree forked from, when known. Empty or
  // omitted for old records, or for in-repo (non-worktree) records, which
  // have no fork point to show.
  baseRef?: string;
}
export type AgentState = "running"|"idle"|"awaiting-approval"|"awaiting-input"|"done"|"errored"|"exited";
export interface AgentCaps { approvals: boolean; attention: boolean; }
export interface AgentEvent {
  sessionId?: string; workspaceId: string; kind: "state"|"approval"|"question"|"approval-resolved";
  state?: AgentState; approval?: ApprovalReq; err?: string;
  // Set when a pending approval was resolved or withdrawn (answered in perch,
  // auto-approved, answered in the agent's own TUI, or taken away on close,
  // reopen, reload or exit). Composed like ApprovalReq.reqId
  // ("<raw>:<workspaceId>"). May arrive more than once; removal is idempotent.
  resolvedReqId?: string;
}
export interface ApprovalReq { reqId: string; tool: string; summary: string; input?: string; }
// A rename is one entry with status "R", the new path in `path` and the old
// one in `oldPath`. An untracked directory is one entry ending in "/".
export interface FileDiff { path: string; oldPath?: string; added: number; removed: number; status: "M"|"A"|"D"|"R"|"?"; }
export interface HunkLine { kind: "ctx"|"add"|"del"; text: string; }
export interface Hunk {
  // `id` is the content hash of the hunk. Stage/Discard/Unstage send it, and
  // the backend refuses (ErrHunkChanged) when the hunk at `index` no longer
  // has that content.
  file: string; index: number; id: string; header: string;
  oldStart: number; oldLines: number; newStart: number; newLines: number; lines: HunkLine[];
  staged?: boolean;
}
export interface FsNode { name: string; path: string; isDir: boolean; modified?: boolean; untracked?: boolean; }
export interface AlwaysRule { agent: string; tool: string; pattern: string; hash?: string; }
// ApproveAlways result: the rule the grant appended. `added` is false when an
// identical rule already existed, so an Undo must remove nothing.
export interface AlwaysGrant { rule: AlwaysRule; added: boolean; }
// fs:changed payload. `path` is the worktree root. `paths` are the absolute
// files changed in the debounce window (deduplicated, never null); past the
// backend's cap `paths` is [] and `truncated` is true, meaning "anything may
// have changed".
export interface FsChanged { workspaceId: string; path: string; paths?: string[]; truncated?: boolean; }
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
  // Types the agent launch line again into an open session whose login shell
  // was still busy at launch. Rejects once the agent has reported in.
  RetypeLaunch(id: string): Promise<void>;
  CloseWorkspace(id: string): Promise<void>;
  RemoveWorkspace(id: string): Promise<void>;
  ForceRemoveWorkspace(id: string): Promise<void>;
  ListStaleSessions(): Promise<StaleSessionVM[]>;
  CleanupSessions(ids: string[], force: boolean): Promise<void>;
  // data is padded standard base64 (Go decodes a []byte argument from that).
  WriteToPty(paneId: string, data: string): Promise<void>;
  ResizePty(paneId: string, cols: number, rows: number): Promise<void>;
  OpenShell(paneId: string, cwd: string): Promise<void>;
  CloseShell(paneId: string): Promise<void>;
  ReloadAgentEnv(paneID: string): Promise<void>;
  Approve(reqId: string, decision: string): Promise<void>;
  ApproveAlways(reqId: string): Promise<AlwaysGrant>;
  RemoveAlwaysRule(rule: AlwaysRule): Promise<boolean>;
  PendingApprovals(): Promise<{ workspaceId: string; req: ApprovalReq }[]>;
  DiffStat(worktree: string): Promise<FileDiff[]>;
  Hunks(worktree: string, file: string): Promise<Hunk[]>;
  StageHunk(worktree: string, file: string, index: number, id: string): Promise<void>;
  DiscardHunk(worktree: string, file: string, index: number, id: string): Promise<void>;
  UnstageHunk(worktree: string, file: string, index: number, id: string): Promise<void>;
  ListDir(absDir: string): Promise<FsNode[]>;
  ReadFile(absPath: string): Promise<string>;
  WriteFile(absPath: string, content: string): Promise<void>;
  RevealInFiles(absPath: string): Promise<void>;
  CopyPath(absPath: string): Promise<void>;
  ClipboardSetText(s: string): Promise<void>;
  ClipboardText(): Promise<string>;
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
      // Native OS file drop. Delivers each dropped file's absolute path.
      // The DOM drop event carries no path on WebKitGTK. useDropTarget:
      // false makes the callback fire for every file drop. The code
      // hit-tests the coordinates itself against [data-drop-pane] in
      // lib/osFileDrop.ts. The app honors only one registration
      // process-wide, so App.svelte registers it once.
      OnFileDrop(cb: (x: number, y: number, paths: string[]) => void, useDropTarget: boolean): void;
      OnFileDropOff(): void;
      // Opens a URL in the system browser. Wails validates the URL.
      BrowserOpenURL?(url: string): void;
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
export const retypeLaunch    = (workspaceId: string)                              => app().RetypeLaunch(workspaceId);
export const closeWorkspace  = (id: string)                                       => app().CloseWorkspace(id);
export const removeWorkspace      = (id: string)                                  => app().RemoveWorkspace(id);
export const forceRemoveWorkspace = (id: string)                                  => app().ForceRemoveWorkspace(id);
export const listStaleSessions    = ()                                            => app().ListStaleSessions();
export const cleanupSessions      = (ids: string[], force: boolean)               => app().CleanupSessions(ids, force);
// PTY
// Bytes cross the bridge as ONE padded standard-base64 string (Go decodes a
// []byte argument only from that form). The binary string is built in
// chunks, to stay under String.fromCharCode's argument limit, and then
// encoded with a single btoa: base64-encoding each chunk separately would put
// padding in the middle and Go would reject the whole write (FEC-31).
export function bytesToBase64(bytes: Uint8Array): string {
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(bin);
}
// Decode one pty:data event. Each event is padded on its own, so it must be
// decoded on its own, never concatenated with another first.
export function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
export const writeToPty = (paneId: string, data: Uint8Array)                      => app().WriteToPty(paneId, bytesToBase64(data));
// Text to the pty as UTF-8 (btoa alone throws on non-Latin-1 text).
export const writeTextToPty = (paneId: string, text: string)                      => writeToPty(paneId, new TextEncoder().encode(text));
export const resizePty  = (paneId: string, cols: number, rows: number)            => app().ResizePty(paneId, cols, rows);
export const openShell  = (paneId: string, cwd: string)                           => app().OpenShell(paneId, cwd);
export const closeShell = (paneId: string)                                        => app().CloseShell(paneId);
// Captures the drawer shell's current environment, and relaunches the
// agent with it, keeping the conversation (perch reload). paneID is the
// per-session drawer pane, "shell-{workspaceID}". The call is rejected on
// the home drawer, which has no session.
export const reloadAgentEnv = (paneID: string)                                    => app().ReloadAgentEnv(paneID);
// Approvals
export const approve = (reqId: string, decision: "allow"|"deny"|"always")         => app().Approve(reqId, decision);
// "Always allow" that reports the rule it added, and its exact undo.
export const approveAlways    = (reqId: string)                                   => app().ApproveAlways(reqId);
export const removeAlwaysRule = (rule: AlwaysRule)                                => app().RemoveAlwaysRule(rule);
// Every approval still awaiting a decision. This list is seeded on mount
// or open, to rebuild the queue after a reload or a late open, because the
// agent:event that carries it fires only once.
export function pendingApprovals(): Promise<{ workspaceId: string; req: ApprovalReq }[]> { return app().PendingApprovals(); }
// Git
export const diffStat    = (worktree: string)                                     => app().DiffStat(worktree);
export const hunks       = (worktree: string, file: string)                       => app().Hunks(worktree, file);
export const stageHunk   = (worktree: string, file: string, index: number, id: string) => app().StageHunk(worktree, file, index, id);
export const discardHunk = (worktree: string, file: string, index: number, id: string) => app().DiscardHunk(worktree, file, index, id);
export const unstageHunk = (worktree: string, file: string, index: number, id: string) => app().UnstageHunk(worktree, file, index, id);
export const branches      = (repo: string)                                       => app().Branches(repo);
export const discoverRepos = ()                                                   => app().DiscoverRepos();
// FS
export const listDir      = (absDir: string)                                      => app().ListDir(absDir);
export const readFile     = (absPath: string)                                     => app().ReadFile(absPath);
export const writeFile    = (absPath: string, content: string)                    => app().WriteFile(absPath, content);
export const revealInFiles = (absPath: string)                                    => app().RevealInFiles(absPath);
export const copyPath      = (absPath: string)                                    => app().CopyPath(absPath);
export const clipboardSetText = (s: string)                                       => app().ClipboardSetText(s);
export const clipboardText    = ()                                                => app().ClipboardText();
// Layout & Settings
export const getLayout       = ()                                                    => app().GetLayout();
export const saveLayout      = (layoutJSON: string)                                  => app().SaveLayout(layoutJSON);
export const getSettings     = ()                                                    => app().GetSettings();
export const saveSettings    = (s: AppSettings)                                      => app().SaveSettings(s);
// Window focus reporting
export const setWindowFocus  = (focused: boolean)                                   => app().SetWindowFocus(focused);
export const homeShellCwd    = ()                                                    => app().HomeShellCwd();

// Event name constants. These must match Go's ptyDataEventPrefix and
// ptyExitEventPrefix in app/app.go. Any rename here requires a matching
// rename on the Go side.
export const EVT_AGENT            = "agent:event";
export const EVT_FS_CHANGED       = "fs:changed";
export const EVT_NOTIFY           = "notify";
export const EVT_PTY_DATA_PREFIX  = "pty:data:"; // append paneId to form full event name
export const EVT_PTY_EXIT_PREFIX  = "pty:exit:"; // append paneId to form full event name
export const EVT_WORKSPACE_ATTACH = "workspace:attach";
export const EVT_WORKSPACE_RELAUNCH = "workspace:relaunch";

// Event helpers. The colon-separated names match the frozen Wails event table.
export function onPtyData(paneId: string, cb: (bytes: Uint8Array) => void): () => void {
  // A base64 string per event; a number[] from an older backend still works.
  return window.runtime.EventsOn(EVT_PTY_DATA_PREFIX + paneId, (data: string | number[]) =>
    cb(typeof data === "string" ? base64ToBytes(data) : Uint8Array.from(data)));
}
export function onPtyExit(paneId: string, cb: (code: number) => void): () => void {
  return window.runtime.EventsOn(EVT_PTY_EXIT_PREFIX + paneId, (p: { code: number }) => cb(p.code));
}
export function onAgentEvent(cb: (ev: AgentEvent) => void): () => void {
  return window.runtime.EventsOn(EVT_AGENT, (ev: AgentEvent) => cb(ev));
}
export function onFsChanged(cb: (p: FsChanged) => void): () => void {
  return window.runtime.EventsOn(EVT_FS_CHANGED, cb);
}
export function onNotify(
  // `action` names an action the notification offers; "retype-launch" is the
  // only one today. Unknown values are ignored.
  cb: (p: { tier: "blocking"|"ambient"|"routine"; title: string; body: string; workspaceId: string; state?: string; action?: string }) => void,
): () => void {
  return window.runtime.EventsOn(EVT_NOTIFY, cb);
}
export function onWorkspaceAttach(cb: (p: { query: string }) => void): () => void {
  return window.runtime.EventsOn(EVT_WORKSPACE_ATTACH, cb);
}
// A conversation-preserving relaunch (perch reload, or the drawer's
// env-to-agent button) respawns the agent pty under the same paneId. The
// app remounts that session's agent terminal, so the new agent redraws
// into a fresh xterm instead of drawing over the stale buffer.
export function onWorkspaceRelaunch(cb: (p: { workspaceId: string }) => void): () => void {
  return window.runtime.EventsOn(EVT_WORKSPACE_RELAUNCH, cb);
}
