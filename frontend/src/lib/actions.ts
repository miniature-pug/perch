import { COUNTUP_FALLBACK_MS } from "./constants";

/** Focus the node immediately on mount (keyboard a11y for dialogs; avoids the autofocus lint warning). */
export function focusOnMount(node: HTMLElement) { node.focus(); }

/** Selector matching keyboard-focusable descendants of a dialog container. */
const FOCUSABLE_SELECTOR =
  'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

/**
 * Svelte action: traps keyboard focus inside a modal container and restores it
 * to the triggering element when the dialog is destroyed.
 *
 * - On mount: records document.activeElement as the return target, then moves
 *   focus into the node — to the element matching `initialSelector` if given,
 *   otherwise the first focusable descendant (falling back to the node itself).
 * - On Tab / Shift+Tab: keeps focus cycling within the node's focusable
 *   elements, wrapping first<->last (preventDefault so it never escapes).
 * - On destroy: restores focus to the recorded target if still in the document.
 *
 * Nesting: every trap node is tagged with `data-focus-trap`. When traps are
 * nested (e.g. a dialog rendered inside another dialog's scrim), the innermost
 * trap owning the focused element handles the Tab; any ancestor trap sees that
 * focus lives in a more-nested `[data-focus-trap]` and bails, so focus never
 * escapes into the outer trap regardless of DOM order.
 */
export function trapFocus(node: HTMLElement, initialSelector?: string): { destroy(): void } {
  const returnTo = document.activeElement as HTMLElement | null;
  node.setAttribute("data-focus-trap", "");

  function focusables(): HTMLElement[] {
    return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
  }

  function focusInitial(): void {
    const target = initialSelector
      ? node.querySelector<HTMLElement>(initialSelector)
      : focusables()[0];
    (target ?? node).focus();
  }

  function handleKey(e: KeyboardEvent): void {
    if (e.key !== "Tab") return;

    // Nesting guard: if the focused element belongs to a more-nested trap that
    // is a descendant of this node, let that inner trap own the event. This
    // fires for outer traps whose scrim contains an inner dialog, so the outer
    // trap never wraps focus over its whole focusable set (which would leak Tab
    // out of the inner dialog when a focusable follows it in DOM order).
    const active = document.activeElement;
    if (active instanceof Element) {
      const owningTrap = active.closest("[data-focus-trap]");
      if (owningTrap && owningTrap !== node && node.contains(owningTrap)) return;
    }

    const items = focusables();
    if (items.length === 0) {
      // Nothing focusable inside — keep focus on the container itself.
      e.preventDefault();
      node.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];

    if (e.shiftKey) {
      if (active === first || !node.contains(active)) {
        e.preventDefault();
        last.focus();
      }
    } else {
      if (active === last || !node.contains(active)) {
        e.preventDefault();
        first.focus();
      }
    }
  }

  node.addEventListener("keydown", handleKey);
  focusInitial();

  return {
    destroy(): void {
      node.removeEventListener("keydown", handleKey);
      node.removeAttribute("data-focus-trap");
      if (returnTo && document.contains(returnTo)) returnTo.focus();
    },
  };
}

/** easeOutCubic: decelerating curve for the count-up animation. */
function easeOutCubic(t: number): number {
  return 1 - Math.pow(1 - t, 3);
}

/** Whether the user prefers reduced motion. Guards matchMedia so it is safe in
    jsdom / non-browser envs that don't implement it (returns false → animate). */
function prefersReducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

/** Read the --perch-dur-countup CSS custom property from :root, parsed as ms integer. Falls back to COUNTUP_FALLBACK_MS. */
function readCountUpDuration(): number {
  try {
    const raw = getComputedStyle(document.documentElement).getPropertyValue("--perch-dur-countup").trim();
    const ms = parseInt(raw, 10);
    if (Number.isFinite(ms) && ms > 0) return ms;
  } catch {
    // no-op — jsdom or env without CSS custom properties
  }
  return COUNTUP_FALLBACK_MS;
}

/**
 * Svelte action: animates a number element's textContent counting from the
 * previous value to the new target value using requestAnimationFrame.
 *
 * - First paint shows the real number immediately (no animate-from-zero).
 * - Subsequent updates animate from the prior value to the new one.
 * - Respects prefers-reduced-motion (sets final value immediately).
 * - Duration is read from CSS custom property --perch-dur-countup (default 380ms).
 * - Falls back to immediate set when rAF is unavailable (e.g. jsdom).
 */
export function countUp(node: HTMLElement, value: number): { update(value: number): void; destroy(): void } {
  let current = value;
  let rafId: number | undefined;
  let destroyed = false;

  // First paint: show the real number immediately, no animation.
  node.textContent = String(value);

  function cancelInFlight(): void {
    if (rafId !== undefined) {
      if (typeof cancelAnimationFrame === "function") {
        cancelAnimationFrame(rafId);
      }
      rafId = undefined;
    }
  }

  function update(next: number): void {
    cancelInFlight();

    const from = current;
    current = next;

    // Guard: no rAF available (some jsdom configs) — set immediately.
    if (typeof requestAnimationFrame !== "function") {
      node.textContent = String(next);
      return;
    }

    // Reduced motion: set immediately.
    if (prefersReducedMotion()) {
      node.textContent = String(next);
      return;
    }

    const duration = readCountUpDuration();
    let startTime: number | undefined;

    function frame(timestamp: number): void {
      // Guard against stale callbacks after destroy() or a superseded update().
      if (destroyed) return;

      if (startTime === undefined) startTime = timestamp;
      const elapsed = timestamp - startTime;
      const progress = Math.min(elapsed / duration, 1);
      const eased = easeOutCubic(progress);
      const displayed = Math.round(from + (next - from) * eased);
      node.textContent = String(displayed);

      if (progress < 1) {
        rafId = requestAnimationFrame(frame);
      } else {
        // Guarantee exact final value.
        node.textContent = String(next);
        rafId = undefined;
      }
    }

    rafId = requestAnimationFrame(frame);
  }

  function destroy(): void {
    destroyed = true;
    cancelInFlight();
  }

  return { update, destroy };
}
