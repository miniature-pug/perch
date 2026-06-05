<!-- frontend/src/lib/NotificationHub.svelte -->
<script lang="ts">
  import type { Notification, Tier } from "./stores/notifications.svelte";

  let {
    items, dnd, onDismiss, onToggleDnd, onClearRead,
  }: {
    items: Notification[];
    dnd: boolean;
    onDismiss: (id: string) => void;
    onToggleDnd: () => void;
    onClearRead: () => void;
  } = $props();

  type Filter = "all" | "approvals" | "errors" | "done";
  let filter = $state<Filter>("all");

  let visible = $derived(
    filter === "all"       ? items :
    filter === "approvals" ? items.filter((n) => n.tier === "blocking") :
    filter === "errors"    ? items.filter((n) => /error|fail/i.test(n.title)) :
                             items.filter((n) => n.tier === "ambient")
  );
</script>

<section aria-label="notification hub" class="notif-hub">
  <div class="hub-toolbar">
    <button onclick={() => (filter = "all")}       aria-pressed={filter === "all"}>All</button>
    <button onclick={() => (filter = "approvals")} aria-pressed={filter === "approvals"}>Approvals</button>
    <button onclick={() => (filter = "errors")}    aria-pressed={filter === "errors"}>Errors</button>
    <button onclick={() => (filter = "done")}      aria-pressed={filter === "done"}>Done</button>
    <button onclick={onToggleDnd}  aria-pressed={dnd} class="dnd-btn">Do not disturb</button>
    <button onclick={onClearRead} class="clear-btn" aria-label="clear read notifications">Clear read</button>
  </div>
  <ul class="notif-list scrollable">
    {#each visible as n (n.id)}
      <li class="notif-item tier-{n.tier}" class:read={n.read}>
        <span class="notif-title">{n.title}</span>
        <span class="notif-body">{n.body}</span>
        <button class="dismiss-btn" onclick={() => onDismiss(n.id)} aria-label="dismiss notification">✕</button>
      </li>
    {/each}
    {#if visible.length === 0}<li class="notif-empty">No notifications</li>{/if}
  </ul>
</section>

<style>
  /* Panel — App positions this; we own bg, border, overflow */
  .notif-hub {
    display: flex;
    flex-direction: column;
    background: var(--perch-glass-bg);
    -webkit-backdrop-filter: var(--perch-glass-filter);
    backdrop-filter: var(--perch-glass-filter);
    border-left: 1px solid var(--perch-glass-border);
    box-shadow: var(--perch-glass-shadow);
    width: 320px;
    max-height: 60vh;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    color: var(--perch-text);
  }

  /* Filter toolbar strip */
  .hub-toolbar {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px var(--perch-sp-1);
    background: var(--perch-surface);
    border-bottom: 1px solid var(--perch-border);
    flex-shrink: 0;
    flex-wrap: wrap;
  }

  /* All toolbar buttons share this base */
  .hub-toolbar button {
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    background: var(--perch-bg);
    color: var(--perch-text-dim);
    border: 1px solid var(--perch-border-strong);
    border-radius: 4px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-caption);
    cursor: pointer;
    transition: border-color var(--perch-dur) var(--perch-ease),
                color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
  }

  .hub-toolbar button:hover {
    border-color: var(--perch-accent);
    color: var(--perch-accent);
  }

  .hub-toolbar button:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: 2px;
  }

  /* Active filter button (aria-pressed=true) */
  .hub-toolbar button[aria-pressed="true"] {
    background: color-mix(in srgb, var(--perch-accent) 18%, var(--perch-bg));
    color: var(--perch-accent);
    border-color: var(--perch-accent);
  }

  /* DND button when active */
  .dnd-btn[aria-pressed="true"] {
    background: color-mix(in srgb, var(--perch-warn) 18%, var(--perch-bg));
    color: var(--perch-warn);
    border-color: var(--perch-warn);
  }

  /* Clear read — subtle, text-dim */
  .clear-btn {
    margin-left: auto;
  }

  /* Scrollable list */
  .notif-list {
    list-style: none;
    margin: 0;
    padding: 0;
    flex: 1;
    overflow-y: auto;
    scrollbar-width: thin;
    scrollbar-color: var(--perch-border) transparent;
  }

  .notif-list::-webkit-scrollbar { width: 6px; }
  .notif-list::-webkit-scrollbar-track { background: transparent; }
  .notif-list::-webkit-scrollbar-thumb { background: var(--perch-border); border-radius: 3px; }
  .notif-list::-webkit-scrollbar-thumb:hover { background: var(--perch-text-dim); }

  /* Base notification row — grid: [icon] [title dismiss] / [icon] [body] */
  .notif-item {
    display: grid;
    grid-template-columns: 16px 1fr auto;
    grid-template-rows: auto auto;
    column-gap: var(--perch-sp-1);
    row-gap: 2px;
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale)) calc(var(--perch-sp-1) * var(--perch-density-scale) * 1.5);
    border-bottom: 1px solid var(--perch-border);
    border-left: 3px solid transparent;
    transition: background var(--perch-dur) var(--perch-ease),
                border-color var(--perch-dur) var(--perch-ease);
  }

  .notif-item:last-child {
    border-bottom: none;
  }

  .notif-item:hover {
    background: color-mix(in srgb, var(--perch-accent) 8%, transparent);
  }

  /* Unread = slightly elevated bg */
  .notif-item:not(.read) {
    background: color-mix(in srgb, var(--perch-text) 4%, transparent);
  }

  /* Tier — left accent border + ::before icon in col 1 row 1
     (satisfies color+icon+label rule: border=color, ::before=icon, .notif-title=label) */
  .tier-blocking { border-left-color: var(--perch-err); }
  .tier-ambient  { border-left-color: var(--perch-info); }
  .tier-routine  { border-left-color: var(--perch-text-dim); }

  /* ::before occupies grid col 1, spans both rows */
  .notif-item::before {
    grid-column: 1;
    grid-row: 1 / 3;
    align-self: center;
    font-size: 12px;
    line-height: 1;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .tier-blocking::before { content: "⚠"; color: var(--perch-err); }
  .tier-ambient::before  { content: "ℹ"; color: var(--perch-info); }
  .tier-routine::before  { content: "·"; color: var(--perch-text-dim); font-size: var(--perch-fs-body); }

  /* Title — col 2 row 1 */
  .notif-title {
    grid-column: 2;
    grid-row: 1;
    font-size: var(--perch-fs-body);
    color: var(--perch-text);
    font-weight: 500;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Body — col 2 row 2 */
  .notif-body {
    grid-column: 2;
    grid-row: 2;
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Dismiss icon button — col 3, spans both rows */
  .dismiss-btn {
    grid-column: 3;
    grid-row: 1 / 3;
    align-self: center;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    padding: 0;
    background: transparent;
    color: var(--perch-text-dim);
    border: 1px solid transparent;
    border-radius: 4px;
    cursor: pointer;
    font-size: var(--perch-fs-caption);
    transition: color var(--perch-dur) var(--perch-ease),
                border-color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
  }

  .dismiss-btn:hover {
    color: var(--perch-text);
    border-color: var(--perch-border);
    background: color-mix(in srgb, var(--perch-text) 8%, transparent);
  }

  .dismiss-btn:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: 2px;
  }

  /* Empty state */
  .notif-empty {
    padding: var(--perch-sp-2);
    color: var(--perch-text-dim);
    font-size: var(--perch-fs-caption);
    font-style: italic;
    text-align: center;
    list-style: none;
  }
</style>
