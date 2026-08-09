<!-- frontend/src/lib/FileTree.svelte -->
<script lang="ts">
  import { listDir, revealInFiles, copyPath, type FsNode } from "./wails";
  import { MIME_TEXT } from "./constants";
  import { SvelteSet } from "svelte/reactivity";
  import { untrack } from "svelte";

  function handleDragStart(e: DragEvent, node: TreeNode) {
    if (!e.dataTransfer) return;
    e.dataTransfer.setData(MIME_TEXT, `@${node.path} `);
    e.dataTransfer.effectAllowed = "copy";
  }

  let {
    root,
    onOpen,
    // A bumped signal (fs version) that asks the tree to re-list its directories
    // in place. Changing it re-fetches the currently-visible dirs while KEEPING
    // every open folder open — a file write must not collapse the tree. It never
    // remounts the component, so scroll and expansion state are preserved.
    refresh = 0,
    // The currently-open file path, so the matching row can render a selected cue
    // that persists across refreshes.
    selectedPath = null,
    // False when the tree is mounted but off-screen (on the agent/diff views).
    // Guards the rebuild so no directory listing fires on a hide or on a background
    // file write while hidden; the effect re-lists when the tree becomes visible.
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

  // Absolute paths of every currently-expanded directory. This set is the durable
  // source of truth for expansion — it survives an in-place refresh (the node
  // objects are rebuilt on each re-list, but this set is not), so open folders
  // stay open when files change. SvelteSet so membership reads are reactive.
  const expanded = new SvelteSet<string>();

  // Re-list `dir` and rebuild its child nodes, recursively re-expanding any child
  // dir still in the expanded set. Dirs that no longer exist drop out naturally
  // (they are absent from the fresh listing) and are pruned from the set.
  //
  // `token` is the rebuild generation that owns this call. The prune below mutates
  // the SHARED `expanded` set, so a stale in-flight rebuild (a slower, losing
  // overlapping rebuild whose token no longer matches rebuildToken) must NOT prune
  // — it could drop an entry the winning rebuild still needs. Only the current
  // rebuild is allowed to mutate `expanded`.
  async function buildLevel(dir: string, token: number): Promise<TreeNode[]> {
    const listing = await listDir(dir);
    const present = new Set(listing.map((n) => n.path));
    // Drop expanded paths under this dir that vanished from the listing — but only
    // when this call still owns the latest rebuild.
    if (token === rebuildToken) {
      for (const p of expanded) {
        if (isChildOf(dir, p) && !present.has(p)) expanded.delete(p);
      }
    }
    const out: TreeNode[] = [];
    for (const n of listing) {
      const node: TreeNode = { ...n };
      if (n.isDir && expanded.has(n.path)) {
        node.expanded = true;
        node.children = await buildLevel(n.path, token);
      } else if (n.isDir) {
        node.expanded = false;
      }
      out.push(node);
    }
    return out;
  }

  // True when `p` is a direct child path of `dir` (one segment deeper).
  function isChildOf(dir: string, p: string): boolean {
    const prefix = dir.endsWith("/") ? dir : dir + "/";
    if (!p.startsWith(prefix)) return false;
    return !p.slice(prefix.length).includes("/");
  }

  // Rebuild the whole visible tree from the root, honoring the expanded set. Runs
  // on mount, on a root change, and on every refresh bump. Guarded so a stale
  // async rebuild (root/refresh changed mid-flight) cannot clobber newer content.
  let rebuildToken = 0;
  async function rebuild() {
    const mine = ++rebuildToken;
    const next = await buildLevel(root, mine);
    if (mine === rebuildToken) nodes = next;
  }

  // The root prop identifies the worktree the tree is showing. `expanded` (and the
  // rendered `nodes`) are keyed to that root, so when `root` changes we must clear
  // them before rebuilding — otherwise a new session inherits the previous
  // session's open folders (stale paths that don't exist under the new root).
  // Today App wraps FileTree in {#key active.id}, which remounts and hides this,
  // but resetting here makes the component correct on its own so removing that key
  // can never leak expansion across sessions.
  let prevRoot: string | undefined;
  $effect(() => {
    root;      // track: a new session's worktree resets the tree
    refresh;   // track: a file write re-lists in place, keeping folders open
    visible;   // track: becoming visible again re-lists any deferred refresh
    untrack(() => {
      // Off-screen: don't list on a hide or on a background write while hidden. The
      // effect re-runs when `visible` flips back to true and rebuilds then, so a
      // refresh that arrived while hidden is picked up on show.
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
    if (node.expanded) {
      node.expanded = false;
      node.children = undefined;
      expanded.delete(node.path);
    } else {
      node.children = (await listDir(node.path)).map((c) => ({ ...c }));
      node.expanded = true;
      expanded.add(node.path);
    }
    nodes = [...nodes];
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
  function menuOpen()   { if (!menu) return; onOpen(menu.node.path); closeMenu(); }
  function menuReveal() { if (!menu) return; revealInFiles(menu.node.path); closeMenu(); }
  function menuCopy()   {
    if (!menu) return;
    // copyPath is a Wails IPC call that already writes to the system clipboard.
    // Do NOT chain navigator.clipboard.writeText — the Promise would be coerced
    // to the string "[object Promise]" and corrupt the clipboard contents.
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

  /* Selected file: the currently-open file keeps a persistent highlight so it
     stays visible across in-place refreshes. */
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

  /* Dirs: folder icon uses accent dim */
  .is-dir .node-icon { color: var(--perch-accent); opacity: 0.7; }

  /* ---------- Context menu (floating card) ---------- */
  .context-menu {
    list-style: none;
    margin: 0;
    padding: var(--perch-sp-1) 0;
    min-width: 160px;
    /* Solid, never glass: this menu can overlap the agent terminal, where
       WebKitGTK paints backdrop-filter surfaces transparent over the composited
       terminal subtree (mirrors the ApprovalCard fix). */
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
