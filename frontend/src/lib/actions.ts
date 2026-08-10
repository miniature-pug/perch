import { COUNTUP_FALLBACK_MS } from "./constants";

/** Focus the node immediately after mount. This gives keyboard accessibility for dialogs and avoids the autofocus lint warning. */
export function focusOnMount(node: HTMLElement) { node.focus(); }

/** This selector matches the keyboard-focusable descendants of a dialog container. */
const FOCUSABLE_SELECTOR =
  'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

/**
 * Svelte action: traps keyboard focus inside a modal container. It restores
 * focus to the triggering element when the dialog is destroyed.
 *
 * - On mount: the action records document.activeElement as the return
 *   target. It then moves focus into the node. Focus goes to the element
 *   that matches `initialSelector` if given, or to the first focusable
 *   descendant, or to the node itself if neither exists.
 * - On Tab or Shift+Tab: the action keeps focus inside the node's focusable
 *   elements. It wraps from first to last and last to first, and calls
 *   preventDefault so focus never escapes.
 * - On destroy: the action restores focus to the recorded target, if that
 *   target is still in the document.
 *
 * Nesting: every trap node carries the `data-focus-trap` attribute. When
 * traps are nested (for example, a dialog inside another dialog's scrim),
 * the innermost trap that owns the focused element handles the Tab key. Any
 * ancestor trap detects that focus lives in a more-nested
 * `[data-focus-trap]` element, and stops. Focus never escapes into the
 * outer trap, regardless of DOM order.
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

    // Nesting guard: if the focused element belongs to a more-nested trap
    // that is a descendant of this node, let that inner trap own the event.
    // This guard fires for an outer trap whose scrim contains an inner
    // dialog. It stops the outer trap from wrapping focus over its whole
    // focusable set, which would leak Tab out of the inner dialog when a
    // focusable element follows it in DOM order.
    const active = document.activeElement;
    if (active instanceof Element) {
      const owningTrap = active.closest("[data-focus-trap]");
      if (owningTrap && owningTrap !== node && node.contains(owningTrap)) return;
    }

    const items = focusables();
    if (items.length === 0) {
      // Nothing is focusable inside. Keep focus on the container itself.
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

/** easeOutCubic gives a decelerating curve for the count-up animation. */
function easeOutCubic(t: number): number {
  return 1 - Math.pow(1 - t, 3);
}

/** True when the user prefers reduced motion. Guards the matchMedia call so
    the function is safe in jsdom and other non-browser environments that do
    not implement it. Returns false in that case, so the caller animates. */
function prefersReducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

/** Read the --perch-dur-countup CSS custom property from :root, and parse it
    as an integer in milliseconds. Falls back to COUNTUP_FALLBACK_MS when the
    value is missing or invalid. */
function readCountUpDuration(): number {
  try {
    const raw = getComputedStyle(document.documentElement).getPropertyValue("--perch-dur-countup").trim();
    const ms = parseInt(raw, 10);
    if (Number.isFinite(ms) && ms > 0) return ms;
  } catch {
    // No-op. This is jsdom, or an environment without CSS custom properties.
  }
  return COUNTUP_FALLBACK_MS;
}

/**
 * Svelte action: animates a number element's textContent. The action counts
 * from the previous value to the new target value, using
 * requestAnimationFrame.
 *
 * - First paint: the action shows the real number immediately, with no
 *   count from zero.
 * - Later updates: the action animates from the prior value to the new one.
 * - The action honors prefers-reduced-motion. It sets the final value
 *   immediately in that case.
 * - Duration comes from the CSS custom property --perch-dur-countup
 *   (default 380ms).
 * - The action sets the value immediately when requestAnimationFrame is not
 *   available, for example in jsdom.
 */
export function countUp(node: HTMLElement, value: number): { update(value: number): void; destroy(): void } {
  let current = value;
  let rafId: number | undefined;
  let destroyed = false;

  // First paint: show the real number immediately. Do not animate.
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

    // Guard: requestAnimationFrame is not available in some jsdom setups.
    // Set the value immediately.
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
      // Guard against a stale callback after destroy(), or after a newer
      // update() call.
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
