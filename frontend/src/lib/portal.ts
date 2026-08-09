// frontend/src/lib/portal.ts
//
// Single-mount split terminal (F10a). The split session's agent Terminal is
// mounted exactly ONCE — in App's primary keep-alive {#each} — so toggling the
// secondary pane on/off never destroys and recreates its xterm. (A destroy+
// recreate would blank the frontend scrollback even though the backend pty
// survives, because a fresh xterm has no buffer.) When a session becomes the
// secondary/split pane, its already-mounted `terminal-zone` DOM node is
// physically relocated into the secondary pane via appendChild. Svelte never
// owns a second instance, so the live DOM node (and its xterm buffer) is
// preserved across every split toggle and re-pick.
//
// Two cooperating actions:
//
//   keepHome  — applied to the movable node (the terminal-zone in the primary
//               loop). Drops an invisible comment "home" anchor beside the node
//               so it can be returned to its exact each-block position later. On
//               its OWN teardown it removes only that anchor and never re-inserts
//               the node: the owning {#each} item is being disposed by Svelte,
//               which alone owns the node's removal.
//
//   adoptInto — applied to the secondary pane host. Moves the given source node
//               into the host, swaps occupants when the split target changes,
//               and — on teardown (split toggled off, host unmounting) — returns
//               the current occupant to its home BEFORE Svelte removes the host.
//               Without that, removing the host would cascade-destroy the adopted
//               terminal node and its xterm buffer, defeating the whole point.

type Home = { anchor: Comment };

// Per-movable-node home anchor. WeakMap so a genuinely-disposed node (and its
// entry) is collected without manual bookkeeping.
const homes = new WeakMap<HTMLElement, Home>();

/** Return `node` to its recorded home position, if that home is still connected. */
function returnHome(node: HTMLElement): void {
  const home = homes.get(node);
  if (home && home.anchor.parentNode) {
    home.anchor.parentNode.insertBefore(node, home.anchor);
  }
}

/**
 * Action for the movable node: records an invisible home anchor as its previous
 * sibling so the node can be relocated away and later returned to exactly this
 * spot in its owning {#each} block.
 */
export function keepHome(node: HTMLElement): { destroy(): void } {
  const anchor = document.createComment("perch-split-home");
  node.parentNode?.insertBefore(anchor, node);
  homes.set(node, { anchor });
  return {
    destroy(): void {
      // The owning {#each} item is being torn down; Svelte will remove `node`.
      // Only clean up the anchor here — re-inserting `node` would risk
      // resurrecting a node Svelte has already detached.
      homes.get(node)?.anchor.remove();
      homes.delete(node);
    },
  };
}

/**
 * Action for the secondary pane host: adopts `source` (the split session's
 * terminal-zone) into itself, returning any prior occupant home first. On
 * teardown it returns the current occupant home so the split terminal survives
 * the pane being removed.
 */
export function adoptInto(
  host: HTMLElement,
  source: HTMLElement | null | undefined,
): { update(next: HTMLElement | null | undefined): void; destroy(): void } {
  let current: HTMLElement | null = null;

  function set(next: HTMLElement | null | undefined): void {
    const src = next ?? null;
    if (src === current) return;
    if (current) returnHome(current); // evict the previous occupant (re-pick)
    current = src;
    if (current) host.appendChild(current);
  }

  set(source);

  return {
    update(next: HTMLElement | null | undefined): void {
      set(next);
    },
    destroy(): void {
      // Split toggled off (or host otherwise unmounting): return the occupant to
      // its home in the primary loop BEFORE Svelte removes this host, so the
      // adopted terminal node (and its xterm buffer) survives the toggle.
      if (current) returnHome(current);
      current = null;
    },
  };
}
