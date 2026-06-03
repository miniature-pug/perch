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
    <button onclick={onToggleDnd}  aria-pressed={dnd}>Do not disturb</button>
    <button onclick={onClearRead}>Clear read</button>
  </div>
  <ul class="notif-list">
    {#each visible as n (n.id)}
      <li class="notif-item tier-{n.tier}" class:read={n.read}>
        <span class="notif-title">{n.title}</span>
        <span class="notif-body dim">{n.body}</span>
        <button onclick={() => onDismiss(n.id)} aria-label="dismiss notification">✕</button>
      </li>
    {/each}
    {#if visible.length === 0}<li class="notif-empty dim">No notifications</li>{/if}
  </ul>
</section>
