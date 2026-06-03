<!-- frontend/src/lib/FileTree.svelte -->
<script lang="ts">
  import { listDir, revealInFiles, copyPath, type FsNode } from "./wails";

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
  function menuCopy()   { if (!menu) return; const p = copyPath(menu.node.path); navigator.clipboard?.writeText(p).catch(() => {}); closeMenu(); }
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

<nav aria-label="file tree" class="file-tree">
  {#snippet nodeList(items: TreeNode[])}
    <ul>
      {#each items as node (node.path)}
        <li>
          <button
            class="tree-node {node.isDir ? 'is-dir' : 'is-file'}"
            aria-expanded={node.isDir ? node.expanded ?? false : undefined}
            onclick={() => node.isDir ? toggle(node) : onOpen(node.path)}
            oncontextmenu={(e) => openMenu(e, node)}
          >
            <span aria-hidden="true">{node.isDir ? (node.expanded ? "▾" : "▸") : "·"}</span>
            <span class="node-name">{node.name}</span>
          </button>
          {#if node.expanded && node.children}{@render nodeList(node.children)}{/if}
        </li>
      {/each}
    </ul>
  {/snippet}
  {@render nodeList(nodes)}
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
