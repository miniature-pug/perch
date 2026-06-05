/** Focus the node immediately on mount (keyboard a11y for dialogs; avoids the autofocus lint warning). */
export function focusOnMount(node: HTMLElement) { node.focus(); }

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

/** Read the --perch-dur-countup CSS custom property from :root, parsed as ms integer. Falls back to 380. */
function readCountUpDuration(): number {
  try {
    const raw = getComputedStyle(document.documentElement).getPropertyValue("--perch-dur-countup").trim();
    const ms = parseInt(raw, 10);
    if (Number.isFinite(ms) && ms > 0) return ms;
  } catch {
    // no-op — jsdom or env without CSS custom properties
  }
  return 380;
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
