/**
 * Shared mock helper for Wails boundary (window.go / window.runtime).
 *
 * Install BEFORE navigation via page.addInitScript(() => installMocks(opts)).
 * Because addInitScript serialises the function body to a string and evaluates
 * it in the page, the helper must be a plain function — no imports or closures
 * over Node module scope.
 *
 * Exposes on window:
 *   window.__calls   – spy log: { method, args }[]
 *   window.__emit(channel, ...args) – fire an EventsOn callback by channel name
 *   window.__evtHandlers – raw map of channel → callback (rarely needed directly)
 */

export interface MockSettings {
  theme?: string;
  density?: string;
  font?: string;
  dnd?: boolean;
  alwaysRules?: { agent: string; tool: string; pattern: string }[];
}

export interface MockWorkspace {
  id: string;
  worktreePath: string;
  agent: string;
  title: string;
  branch: string;
  state: string;
  caps: { approvals: boolean; attention: boolean; tokens: boolean };
  paneId: string;
  lastActive: string;
}

export interface MockOptions {
  settings?: MockSettings;
  /** Empty array → "No session selected" state; provide a workspace to activate views. */
  workspaces?: MockWorkspace[];
  /** Override raw JSON returned by GetLayout (empty string = keep defaults) */
  layoutJSON?: string;
  /** If provided, override ReadFile mock to return this content */
  readFileContent?: string;
  /** If provided, override Hunks mock to return these hunks */
  hunks?: unknown[];
}

/**
 * Default selected workspace shape used by most specs that need an active session.
 * Tests can spread-override individual fields.
 */
export const WORKSPACE_FIXTURE: MockWorkspace = {
  id: "ws-1",
  worktreePath: "/home/user/project",
  agent: "claude",
  title: "test session",
  branch: "main",
  state: "idle",
  caps: { approvals: true, attention: true, tokens: true },
  paneId: "pane-ws-1",
  lastActive: new Date().toISOString(),
};

/**
 * Workspace fixture representing a workspace with no active monitor (zero-value caps).
 * Production ListWorkspaces returns all-false caps when no monitor is running; this
 * fixture exercises the caps-disabled UI path (approval badge hidden, etc.).
 */
export const WORKSPACE_FIXTURE_NO_CAPS: MockWorkspace = {
  id: "ws-2",
  worktreePath: "/home/user/project-no-caps",
  agent: "claude",
  title: "test session (no monitor)",
  branch: "main",
  state: "idle",
  caps: { approvals: false, attention: false, tokens: false },
  paneId: "pane-ws-2",
  lastActive: new Date().toISOString(),
};

/**
 * Produce the init-script function source.
 *
 * addInitScript requires either a path-to-file or a plain function with NO
 * external captures. We therefore serialise opts into the string and embed them.
 */
/**
 * Returns a string of JS to be passed to page.addInitScript({ content: ... }).
 * This avoids the serialisation problem with closures entirely.
 */
export function buildInitScriptContent(opts: MockOptions = {}): string {
  const settings: Required<MockSettings> = {
    theme: opts.settings?.theme ?? "gruvbox",
    density: opts.settings?.density ?? "dense",
    font: opts.settings?.font ?? "geist",
    dnd: opts.settings?.dnd ?? false,
    alwaysRules: opts.settings?.alwaysRules ?? [],
  };

  const workspaces = opts.workspaces ?? [];
  const layoutJSON = opts.layoutJSON ?? "";
  const readFileContent = opts.readFileContent ?? "// mock file content\nconsole.log('hello');\n";
  const hunks = opts.hunks ?? [];

  return `
(function() {
  // Spy log — all IPC calls recorded here
  window.__calls = [];

  // EventsOn handler registry — channel → callback
  window.__evtHandlers = {};

  // Emit helper — lets tests fire backend events into the app
  window.__emit = function(channel) {
    var args = Array.prototype.slice.call(arguments, 1);
    var handler = window.__evtHandlers[channel];
    if (handler) handler.apply(null, args);
  };

  // Record an IPC call
  function record(method, args) {
    window.__calls.push({ method: method, args: args });
  }

  // window.runtime — EventsOn must return an unsubscribe function
  window.runtime = {
    EventsOn: function(event, cb) {
      window.__evtHandlers[event] = cb;
      return function() {
        delete window.__evtHandlers[event];
      };
    },
    EventsOff: function(event) {
      delete window.__evtHandlers[event];
    },
    EventsEmit: function(event) {
      var args = Array.prototype.slice.call(arguments, 1);
      var handler = window.__evtHandlers[event];
      if (handler) handler.apply(null, args);
    },
  };

  var _settings = ${JSON.stringify(settings)};
  var _workspaces = ${JSON.stringify(workspaces)};
  var _layoutJSON = ${JSON.stringify(layoutJSON)};
  var _readFileContent = ${JSON.stringify(readFileContent)};
  var _hunks = ${JSON.stringify(hunks)};

  window.go = {
    app: {
      App: {
        GetSettings: function() {
          record('GetSettings', []);
          return Promise.resolve(Object.assign({}, _settings));
        },
        GetLayout: function() {
          record('GetLayout', []);
          return Promise.resolve(_layoutJSON);
        },
        ListWorkspaces: function() {
          record('ListWorkspaces', []);
          return Promise.resolve(_workspaces.slice());
        },
        SaveSettings: function(s) {
          record('SaveSettings', [s]);
          // Update internal state so subsequent GetSettings calls reflect change
          _settings = Object.assign({}, s);
          return Promise.resolve();
        },
        SaveLayout: function(json) {
          record('SaveLayout', [json]);
          return Promise.resolve();
        },
        SetWindowFocus: function(focused) {
          record('SetWindowFocus', [focused]);
          return Promise.resolve();
        },
        CreateWorkspace: function(agent, repo, branch, model) {
          record('CreateWorkspace', [agent, repo, branch, model]);
          return Promise.resolve({
            id: 'ws-new', worktreePath: repo, agent: agent, title: 'new session',
            branch: branch, state: 'idle',
            caps: { approvals: true, attention: true, tokens: true },
            paneId: 'pane-ws-new', lastActive: new Date().toISOString(),
          });
        },
        OpenWorkspace: function(id) {
          record('OpenWorkspace', [id]);
          return Promise.resolve();
        },
        CloseWorkspace: function(id) {
          record('CloseWorkspace', [id]);
          return Promise.resolve();
        },
        RemoveWorkspace: function(id) {
          record('RemoveWorkspace', [id]);
          return Promise.resolve();
        },
        WriteToPty: function(paneId, data) {
          record('WriteToPty', [paneId, data]);
          return Promise.resolve();
        },
        ResizePty: function(paneId, cols, rows) {
          record('ResizePty', [paneId, cols, rows]);
          return Promise.resolve();
        },
        OpenShell: function(paneId, cwd) {
          record('OpenShell', [paneId, cwd]);
          return Promise.resolve();
        },
        Approve: function(reqId, decision) {
          record('Approve', [reqId, decision]);
          return Promise.resolve();
        },
        DiffStat: function(worktree) {
          record('DiffStat', [worktree]);
          return Promise.resolve([]);
        },
        Hunks: function(worktree, file) {
          record('Hunks', [worktree, file]);
          return Promise.resolve(_hunks.slice());
        },
        StageHunk: function(worktree, file, index) {
          record('StageHunk', [worktree, file, index]);
          return Promise.resolve();
        },
        DiscardHunk: function(worktree, file, index) {
          record('DiscardHunk', [worktree, file, index]);
          return Promise.resolve();
        },
        ListDir: function(absDir) {
          record('ListDir', [absDir]);
          return Promise.resolve([]);
        },
        ReadFile: function(absPath) {
          record('ReadFile', [absPath]);
          return Promise.resolve(_readFileContent);
        },
        WriteFile: function(absPath, content) {
          record('WriteFile', [absPath, content]);
          return Promise.resolve();
        },
        RevealInFiles: function(absPath) {
          record('RevealInFiles', [absPath]);
          return Promise.resolve();
        },
        CopyPath: function(absPath) {
          record('CopyPath', [absPath]);
          return Promise.resolve();
        },
        Branches: function(repo) {
          record('Branches', [repo]);
          return Promise.resolve(['main', 'dev']);
        },
        Worktrees: function(repo) {
          record('Worktrees', [repo]);
          return Promise.resolve([]);
        },
        DiscoverRepos: function() {
          record('DiscoverRepos', []);
          return Promise.resolve([
            {
              path: '/home/user/perch',
              name: 'perch',
              branch: 'main',
              worktrees: [{ path: '/home/user/perch', branch: 'main', head: '' }],
            },
            {
              path: '/home/user/my-project',
              name: 'my-project',
              branch: 'feat/v2',
              worktrees: [],
            },
          ]);
        },
      },
    },
  };
})();
`;
}
