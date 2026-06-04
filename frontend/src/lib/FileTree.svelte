<!-- frontend/src/lib/FileTree.svelte -->
<script lang="ts">
  import { listDir, revealInFiles, copyPath, type FsNode } from "./wails";

  const MIME_TEXT = "application/x-perch-text";

  function handleDragStart(e: DragEvent, node: TreeNode) {
    if (!e.dataTransfer) return;
    e.dataTransfer.setData(MIME_TEXT, `@${node.path} `);
    e.dataTransfer.effectAllowed = "copy";
  }

  let { root, onOpen }: { root: string; onOpen: (path: string) => void } = $props();

  type TreeNode = FsNode & { children?: TreeNode[]; expanded?: boolean };

  let nodes = $state<TreeNode[]>([]);
  let menu  = $state<{ node: TreeNode; x: number; y: number } | null>(null);

  $effect(() => { listDir(root).then((ns) => { nodes = ns.map((n) => ({ ...n })); }); });

  async function toggle(node: TreeNode) {
    if (!node.isDir) return;
    if (node.expanded) { node.expanded = false; node.children = undefined; }
    else { node.children = (await listDir(node.path)).map((c) => ({ ...c })); node.expanded = true; }
    nodes = [...nodes];
  }

  function openMenu(e: MouseEvent, node: TreeNode) { e.preventDefault(); menu = { node, x: e.clientX, y: e.clientY }; }
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
            <span class="node-name">{node.name}</span>
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
  .scrollable::-webkit-scrollbar { width: 6px; }
  .scrollable::-webkit-scrollbar-track { background: transparent; }
  .scrollable::-webkit-scrollbar-thumb { background: var(--perch-border); border-radius: 3px; }
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
                opacity   100ms var(--perch-ease);
  }
  .tree-node[draggable="true"]:active { opacity: 0.7; }
  .tree-node:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: -2px;
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
    font-size: 12px;
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
    background: var(--perch-bg);
    border: 1px solid var(--perch-border);
    border-radius: 6px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.45);
    z-index: 250;
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
    outline: 2px solid var(--perch-accent);
    outline-offset: -2px;
  }
</style>
