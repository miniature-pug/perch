// frontend/src/lib/portal.ts
//
// Single-mount split terminal (F10a). The split session's agent Terminal
// mounts exactly once, in App's primary keep-alive {#each}, so toggling the
// secondary pane on or off never destroys and recreates its xterm. A
// destroy-and-recreate cycle would blank the frontend scrollback even
// though the backend pty survives, because a fresh xterm has no buffer.
// When a session becomes the secondary (split) pane, the code physically
// relocates its already-mounted `terminal-zone` DOM node into the
// secondary pane, using appendChild. Svelte never owns a second instance,
// so the live DOM node, and its xterm buffer, survive every split toggle
// and re-pick.
//
// Two cooperating actions:
//
//   keepHome  applies to the movable node (the terminal-zone in the
//             primary loop). It drops an invisible comment "home" anchor
//             beside the node, so the code can return the node to its
//             exact each-block position later. On its own teardown it
//             removes only that anchor, and never re-inserts the node:
//             Svelte is disposing of the owning {#each} item, and Svelte
//             alone owns the node's removal.
//
//   adoptInto applies to the secondary pane host. It moves the given
//             source node into the host, swaps occupants when the split
//             target changes, and on teardown (split toggled off, host
//             unmounting) returns the current occupant to its home before
//             Svelte removes the host. Without that step, removing the
//             host would cascade-destroy the adopted terminal node and
//             its xterm buffer, defeating the whole point.

type Home = { anchor: Comment };

// Per-movable-node home anchor. This is a WeakMap, so the garbage collector
// reclaims a genuinely disposed node, and its entry, without manual
// bookkeeping.
const homes = new WeakMap<HTMLElement, Home>();

/** Return `node` to its recorded home position, if that home is still connected. */
function returnHome(node: HTMLElement): void {
  const home = homes.get(node);
  if (home && home.anchor.parentNode) {
    home.anchor.parentNode.insertBefore(node, home.anchor);
  }
}

/**
 * Action for the movable node: records an invisible home anchor as its
 * previous sibling, so the code can relocate the node away and later
 * return it to exactly this spot in its owning {#each} block.
 */
export function keepHome(node: HTMLElement): { destroy(): void } {
  const anchor = document.createComment("perch-split-home");
  node.parentNode?.insertBefore(anchor, node);
  homes.set(node, { anchor });
  return {
    destroy(): void {
      // Svelte is tearing down the owning {#each} item, and Svelte will
      // remove `node`. Clean up only the anchor here. Re-inserting `node`
      // would risk resurrecting a node Svelte has already detached.
      homes.get(node)?.anchor.remove();
      homes.delete(node);
    },
  };
}

/**
 * Action for the secondary pane host: adopts `source`, the split
 * session's terminal-zone, into itself, and returns any prior occupant
 * home first. On teardown it returns the current occupant home, so the
 * split terminal survives the pane being removed.
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
      // The split toggled off, or the host is otherwise unmounting: return
      // the occupant to its home in the primary loop before Svelte removes
      // this host, so the adopted terminal node, and its xterm buffer,
      // survive the toggle.
      if (current) returnHome(current);
      current = null;
    },
  };
}
