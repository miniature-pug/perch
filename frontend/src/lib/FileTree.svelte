<!-- frontend/src/lib/FileTree.svelte -->
<script lang="ts">
  import { listDir, revealInFiles, copyPath, type FsNode } from "./wails";
  import { mentionText } from "./osFileDrop";
  import { MIME_TEXT } from "./constants";
  import { SvelteSet } from "svelte/reactivity";
  import { untrack } from "svelte";

  function handleDragStart(e: DragEvent, node: TreeNode) {
    if (!e.dataTransfer) return;
    // The same @mention format as an OS file drop and "Send to agent", so a
    // path with spaces survives every entry point (FEX-27, FEC-35).
    e.dataTransfer.setData(MIME_TEXT, mentionText(node.path));
    e.dataTransfer.effectAllowed = "copy";
  }

  let {
    root,
    onOpen,
    // A bumped signal (fs version) that tells the tree to re-list its
    // directories in place. When refresh changes, the tree re-fetches the
    // directories that are visible now. Every open folder stays open,
    // because a file write must not collapse the tree. Refresh never
    // remounts the component, so the tree keeps its scroll position and
    // expansion state.
    refresh = 0,
    // The path of the file that is open now. The matching row shows a
    // selected cue. The cue stays visible across refreshes.
    selectedPath = null,
    // False when the tree is mounted but hidden, for example on the agent
    // or diff view. This flag guards the rebuild: no directory listing
    // runs on a hide, and none runs on a background file write while
    // hidden. The effect re-lists the tree when visible becomes true again.
    visible = true,
  }: {
    root: string;
    onOpen: (path: string) => void;
    refresh?: number;
    selectedPath?: string | null;
    visible?: boolean;
  } = $props();

  type TreeNode = FsNode & { children?: TreeNode[]; expanded?: boolean };

  let nodes = $state<TreeNode[]>([]);
  let menu  = $state<{ node: TreeNode; x: number; y: number } | null>(null);

  // Absolute paths of every directory that is expanded now. This set is
  // the lasting source of truth for expansion. The set survives an
  // in-place refresh: the node objects rebuild on each re-list, but the
  // set does not rebuild. Open folders stay open when files change. The
  // type is SvelteSet, so membership reads are reactive.
  const expanded = new SvelteSet<string>();

  // Re-lists `dir` and rebuilds its child nodes. The function expands any
  // child directory that is still in the expanded set. A directory that
  // no longer exists drops out of the list on its own, because it is
  // absent from the fresh listing. The function then removes that
  // directory from the set.
  //
  // `token` identifies the rebuild that owns this call. The prune below
  // changes the shared `expanded` set. A stale rebuild still in flight (a
  // slower, losing rebuild whose token no longer matches `rebuildToken`)
  // must not prune. A stale rebuild could remove an entry that the
  // winning rebuild still needs. Only the current rebuild can change
  // `expanded`.
  //
  // Sibling directories are listed in parallel, not one after another
  // (FEX-19). A child directory that fails to list (deleted mid-rebuild,
  // permissions) is shown collapsed instead of failing the whole rebuild
  // (FEX-18).
  async function buildLevel(dir: string, token: number): Promise<TreeNode[]> {
    const listing = await listDir(dir);
    const present = new Set(listing.map((n) => n.path));
    // Drop expanded paths under this directory that vanished from the
    // listing. Do this only when this call still owns the latest rebuild.
    if (token === rebuildToken) {
      for (const p of expanded) {
        if (isChildOf(dir, p) && !present.has(p)) expanded.delete(p);
      }
    }
    return Promise.all(listing.map(async (n): Promise<TreeNode> => {
      const node: TreeNode = { ...n };
      if (n.isDir && expanded.has(n.path)) {
        try {
          node.children = await buildLevel(n.path, token);
          node.expanded = true;
        } catch {
          node.expanded = false;
          if (token === rebuildToken) expanded.delete(n.path);
        }
      } else if (n.isDir) {
        node.expanded = false;
      }
      return node;
    }));
  }

  // The live node for `path` in the current tree. A rebuild replaces the node
  // objects, so an action that awaited must re-find its node by path instead
  // of mutating a detached one (FEX-18).
  function findNode(list: TreeNode[], path: string): TreeNode | null {
    for (const n of list) {
      if (n.path === path) return n;
      if (n.children && path.startsWith(n.path + "/")) {
        const hit = findNode(n.children, path);
        if (hit) return hit;
      }
    }
    return null;
  }

  // True when `p` is a direct child path of `dir` (one segment deeper).
  function isChildOf(dir: string, p: string): boolean {
    const prefix = dir.endsWith("/") ? dir : dir + "/";
    if (!p.startsWith(prefix)) return false;
    return !p.slice(prefix.length).includes("/");
  }

  // Rebuilds the whole visible tree from the root. The rebuild keeps the
  // expanded set. It runs on mount, on a root change, and on every
  // refresh bump. A guard stops a stale async rebuild (root or refresh
  // changed mid-flight) from overwriting newer content.
  let rebuildToken = 0;
  async function rebuild() {
    const mine = ++rebuildToken;
    try {
      const next = await buildLevel(root, mine);
      if (mine === rebuildToken) nodes = next;
    } catch {
      // The root could not be listed (removed worktree, permissions). Keep
      // what is shown; the next refresh retries (FEX-18).
    }
  }

  // The root prop identifies the worktree that the tree shows. `expanded`
  // and the rendered `nodes` are keyed to that root. When `root` changes,
  // the component must clear both before it rebuilds. Otherwise a new
  // session would inherit the previous session's open folders, paths
  // that do not exist under the new root.
  //
  // Today App wraps FileTree in {#key active.id}. This remounts FileTree
  // and hides the problem. Resetting here makes the component correct on
  // its own. Removing that key can then never leak expansion state
  // across sessions.
  let prevRoot: string | undefined;
  $effect(() => {
    root;      // track: a new session's worktree resets the tree
    refresh;   // track: a file write re-lists in place and keeps folders open
    visible;   // track: visible becomes true and re-lists a deferred refresh
    untrack(() => {
      // Off-screen: do not list on a hide, and do not list on a
      // background write while hidden. The effect re-runs when `visible`
      // becomes true again, and it rebuilds then. A refresh that arrives
      // while hidden is picked up when the tree becomes visible again.
      if (!visible) return;
      if (root !== prevRoot) {
        prevRoot = root;
        expanded.clear();
        nodes = [];
      }
      rebuild();
    });
  });

  async function toggle(node: TreeNode) {
    if (!node.isDir) return;
    const path = node.path;
    if (expanded.has(path) || node.expanded) {
      // Collapse the folder AND forget its expanded descendants, so they do
      // not pop open by themselves on the next refresh (FEX-18).
      for (const p of [...expanded]) if (p === path || p.startsWith(path + "/")) expanded.delete(p);
      const cur = findNode(nodes, path);
      if (cur) { cur.expanded = false; cur.children = undefined; }
      nodes = [...nodes];
      return;
    }
    let children: TreeNode[];
    try {
      children = await buildLevel(path, rebuildToken);
    } catch {
      return; // the folder vanished or cannot be read; nothing to expand
    }
    expanded.add(path);
    const cur = findNode(nodes, path);
    if (cur) {
      cur.children = children;
      cur.expanded = true;
      nodes = [...nodes];
    }
  }

  const MENU_APPROX_W = 180;
  const MENU_APPROX_H = 140;
  function openMenu(e: MouseEvent, node: TreeNode) {
    e.preventDefault();
    const x = Math.min(e.clientX, window.innerWidth  - MENU_APPROX_W);
    const y = Math.min(e.clientY, window.innerHeight - MENU_APPROX_H);
    menu = { node, x, y };
  }
  function closeMenu() { menu = null; }
  // "Open" on a folder expands or collapses it; only a file goes to the
  // editor, which cannot read a directory (FEX-3).
  function menuOpen()   {
    if (!menu) return;
    const node = menu.node;
    closeMenu();
    if (node.isDir) void toggle(node);
    else onOpen(node.path);
  }
  function menuReveal() { if (!menu) return; revealInFiles(menu.node.path); closeMenu(); }
  function menuCopy()   {
    if (!menu) return;
    // copyPath is a Wails IPC call. It already writes to the system
    // clipboard. Do not chain navigator.clipboard.writeText after it. The
    // Promise would be coerced to the string "[object Promise]" and
    // would corrupt the clipboard contents.
    void copyPath(menu.node.path);
    closeMenu();
  }
  function menuSend()   { if (!menu) return; onOpen(`@mention:${menu.node.path}`); closeMenu(); }

  function handleContextMenuKey(e: KeyboardEvent, action: () => void) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      action();
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const menuEl = (e.currentTarget as HTMLElement).closest('[role="menu"]') as HTMLElement;
      if (!menuEl) return;
      const items = Array.from(menuEl.querySelectorAll<HTMLElement>('[role="menuitem"]'));
      const idx = items.findIndex((el) => el === e.currentTarget);
      if (e.key === "ArrowDown") {
        const next = items[idx + 1];
        if (next) next.focus();
      } else {
        const prev = items[idx - 1];
        if (prev) prev.focus();
      }
    } else if (e.key === "Escape") {
      closeMenu();
    }
  }
</script>

<svelte:window onclick={closeMenu} />

<nav aria-label="file tree" class="file-tree scrollable">
  {#snippet nodeList(items: TreeNode[], depth: number)}
    <ul class="tree-list">
      {#each items as node (node.path)}
        <li class="tree-item">
          <button
            class="tree-node {node.isDir ? 'is-dir' : 'is-file'} {node.modified ? 'is-modified' : ''} {node.untracked ? 'is-untracked' : ''}"
            class:is-selected={!node.isDir && node.path === selectedPath}
            aria-current={!node.isDir && node.path === selectedPath ? "true" : undefined}
            style="padding-left: calc(var(--perch-sp-2) + {depth} * var(--perch-sp-2))"
            aria-expanded={node.isDir ? node.expanded ?? false : undefined}
            draggable="true"
            ondragstart={(e) => handleDragStart(e, node)}
            onclick={() => node.isDir ? toggle(node) : onOpen(node.path)}
            oncontextmenu={(e) => openMenu(e, node)}
          >
            {#if node.isDir}
              <span class="node-chevron" aria-hidden="true">{node.expanded ? "▾" : "▸"}</span>
              <span class="node-icon" aria-hidden="true">📁</span>
            {:else}
              <span class="node-spacer" aria-hidden="true"></span>
              <span class="node-icon" aria-hidden="true">·</span>
            {/if}
            <span class="node-name" title={node.name}>{node.name}</span>
          </button>
          {#if node.expanded && node.children}{@render nodeList(node.children, depth + 1)}{/if}
        </li>
      {/each}
    </ul>
  {/snippet}
  {@render nodeList(nodes, 0)}
</nav>

{#if menu}
  <ul role="menu" class="context-menu" style="position:fixed;left:{menu.x}px;top:{menu.y}px">
    <li role="menuitem" tabindex="0"
      onclick={(e) => { e.stopPropagation(); menuOpen(); }}
      onkeydown={(e) => handleContextMenuKey(e, menuOpen)}>Open</li>
    <li role="menuitem" tabindex="0"
      onclick={(e) => { e.stopPropagation(); menuReveal(); }}
      onkeydown={(e) => handleContextMenuKey(e, menuReveal)}>Reveal in Files</li>
    <li role="menuitem" tabindex="0"
      onclick={(e) => { e.stopPropagation(); menuCopy(); }}
      onkeydown={(e) => handleContextMenuKey(e, menuCopy)}>Copy path</li>
    <li role="menuitem" tabindex="0"
      onclick={(e) => { e.stopPropagation(); menuSend(); }}
      onkeydown={(e) => handleContextMenuKey(e, menuSend)}>Send to agent</li>
  </ul>
{/if}

<style>
  /* ---------- Container ---------- */
  .file-tree {
    display: flex;
    flex-direction: column;
    flex: none;
    width: 200px;
    min-height: 0;
    overflow-y: auto;
    border-right: 1px solid var(--perch-border);
    background: var(--perch-bg-elev);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }

  /* ---------- Scrollable ---------- */
  .scrollable {
    overflow-y: auto;
    scrollbar-width: thin;
    scrollbar-color: var(--perch-border) transparent;
  }
  .scrollable::-webkit-scrollbar { width: var(--perch-scrollbar-w); }
  .scrollable::-webkit-scrollbar-track { background: transparent; }
  .scrollable::-webkit-scrollbar-thumb { background: var(--perch-border); border-radius: var(--perch-scrollbar-radius); }
  .scrollable::-webkit-scrollbar-thumb:hover { background: var(--perch-text-dim); }

  /* ---------- Tree structure ---------- */
  .tree-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .tree-item {
    display: block;
  }

  /* ---------- Tree node button ---------- */
  .tree-node {
    display: flex;
    align-items: center;
    gap: 4px;
    width: 100%;
    padding-top:    calc(var(--perch-sp-1) * var(--perch-density-scale));
    padding-bottom: calc(var(--perch-sp-1) * var(--perch-density-scale));
    padding-right:  calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5);
    /* padding-left is set inline per depth */
    background: transparent;
    border: none;
    color: var(--perch-text);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    text-align: left;
    cursor: pointer;
    transition: background var(--perch-dur) var(--perch-ease);
    user-select: none;
    box-sizing: border-box;
  }
  .tree-node:hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
  }
  .tree-node[draggable="true"] {
    cursor: grab;
    transition: background var(--perch-dur) var(--perch-ease),
                opacity   var(--perch-dur) var(--perch-ease);
  }
  .tree-node[draggable="true"]:active { opacity: 0.7; }
  .tree-node:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: -2px;
  }

  /* Selected file: the open file keeps a highlight. The highlight stays
     visible across in-place refreshes. */
  .tree-node.is-selected {
    background: color-mix(in srgb, var(--perch-accent) 16%, transparent);
  }

  /* Status coloring */
  .tree-node.is-modified .node-name { color: var(--perch-warn); }
  .tree-node.is-untracked .node-name { color: var(--perch-ok); }

  /* Chevron/icon */
  .node-chevron {
    font-size: 10px;
    color: var(--perch-text-dim);
    width: 12px;
    text-align: center;
    flex-shrink: 0;
  }

  .node-spacer {
    display: inline-block;
    width: 12px;
    flex-shrink: 0;
  }

  .node-icon {
    width: 16px;
    text-align: center;
    flex-shrink: 0;
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
  }

  .node-name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* Directories: the folder icon uses a dimmed accent color */
  .is-dir .node-icon { color: var(--perch-accent); opacity: 0.7; }

  /* ---------- Context menu (floating card) ---------- */
  .context-menu {
    list-style: none;
    margin: 0;
    padding: var(--perch-sp-1) 0;
    min-width: 160px;
    /* Solid, not glass. This menu can overlap the agent terminal, where
       WebKitGTK paints backdrop-filter surfaces as transparent over the
       composited terminal subtree. This mirrors the ApprovalCard fix. */
    background: var(--perch-glass-bg-solid);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-md);
    box-shadow: var(--perch-shadow-float);
    z-index: var(--perch-z-context-menu);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }

  .context-menu [role="menuitem"] {
    display: flex;
    align-items: center;
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale)) var(--perch-sp-2);
    color: var(--perch-text);
    cursor: pointer;
    transition: background var(--perch-dur) var(--perch-ease);
    user-select: none;
  }
  .context-menu [role="menuitem"]:hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
  }
  .context-menu [role="menuitem"]:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: -2px;
  }
</style>
