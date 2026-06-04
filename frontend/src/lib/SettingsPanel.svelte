<!-- frontend/src/lib/SettingsPanel.svelte -->
<script lang="ts">
  import { onMount } from "svelte";
  import { getSettings, type AppSettings } from "./wails";
  import { focusOnMount } from "./actions";
  import { settings as settingsStore } from "./stores/settings.svelte";
  import { setDnd } from "./stores/notifications.svelte";
  import { THEMES, DENSITIES, FONTS, DEFAULT_THEME, DEFAULT_DENSITY, DEFAULT_FONT, type Density } from "./constants";

  let {
    open,
    onClose = () => {},
  }: {
    open: boolean;
    onClose?: () => void;
  } = $props();

  let settings = $state<AppSettings>({
    theme: DEFAULT_THEME, density: DEFAULT_DENSITY as Density, font: DEFAULT_FONT, dnd: false, alwaysRules: [],
  });

  onMount(async () => {
    settings = await getSettings();
  });

  function handleKey(e: KeyboardEvent) {
    if (e.key === "Escape") onClose();
  }

  async function onThemeChange(e: Event) {
    const v = (e.currentTarget as HTMLSelectElement).value;
    settings = { ...settings, theme: v };
    await settingsStore.setTheme(v);
  }

  async function onDensityChange(e: Event) {
    const v = (e.currentTarget as HTMLSelectElement).value as "dense" | "comfortable" | "ultra";
    settings = { ...settings, density: v };
    await settingsStore.setDensity(v);
  }

  async function onFontChange(e: Event) {
    const v = (e.currentTarget as HTMLSelectElement).value;
    settings = { ...settings, font: v };
    await settingsStore.setFont(v);
  }

  async function toggleDnd() {
    const next = !settings.dnd;
    settings = { ...settings, dnd: next };
    setDnd(next);
    await settingsStore.setDnd(next);
  }

  async function revokeRule(i: number) {
    const reduced = settings.alwaysRules.filter((_, j) => j !== i);
    settings = { ...settings, alwaysRules: reduced };
    await settingsStore.setAlwaysRules(reduced);
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div role="dialog" aria-label="Settings" class="dialog-overlay"
       tabindex="-1" onkeydown={handleKey} use:focusOnMount>
    <div class="dialog settings-dialog">

      <section class="settings-section">
        <h2>Appearance</h2>

        <label class="setting-row">
          <span class="setting-label">Theme</span>
          <select aria-label="Theme" value={settings.theme} onchange={onThemeChange}>
            {#each THEMES as t}
              <option value={t}>{t}</option>
            {/each}
          </select>
        </label>

        <label class="setting-row">
          <span class="setting-label">Density</span>
          <select aria-label="Density" value={settings.density} onchange={onDensityChange}>
            {#each DENSITIES as d}
              <option value={d}>{d}</option>
            {/each}
          </select>
        </label>

        <label class="setting-row">
          <span class="setting-label">Font</span>
          <select aria-label="Font" value={settings.font} onchange={onFontChange}>
            {#each FONTS as f}
              <option value={f}>{f}</option>
            {/each}
          </select>
        </label>
      </section>

      <section class="settings-section">
        <h2>Notifications</h2>
        <div class="setting-row">
          <span class="setting-label">Do not disturb</span>
          <button
            role="switch"
            aria-label="Do not disturb"
            aria-checked={settings.dnd}
            class="dnd-switch"
            class:on={settings.dnd}
            onclick={toggleDnd}
          >
            {settings.dnd ? "On" : "Off"}
          </button>
        </div>
      </section>

      <section class="settings-section">
        <h2>Always-allow rules</h2>
        <p class="rules-caveat" role="note" aria-label="Security notice">
          <span class="caveat-icon" aria-hidden="true">⚠</span>
          Always-allow rules let an agent run a matching action again without asking. Each rule matches one exact tool input — review and revoke rules you no longer trust.
        </p>
        {#if settings.alwaysRules.length === 0}
          <p class="empty-rules">No always-allow rules.</p>
        {:else}
          <ul class="rules-list">
            {#each settings.alwaysRules as rule, i}
              <li class="rule-row">
                <span class="rule-agent">{rule.agent}</span>
                <span class="rule-tool">{rule.tool}</span>
                <span class="rule-pattern" title={rule.pattern}>{rule.pattern}</span>
                <button
                  aria-label="Revoke"
                  class="revoke-btn"
                  onclick={() => revokeRule(i)}
                >Revoke</button>
              </li>
            {/each}
          </ul>
        {/if}
      </section>

      <button aria-label="close settings" class="close-btn" onclick={onClose}>Close</button>
    </div>
  </div>
{/if}

<style>
  .dialog-overlay {
    position: fixed;
    inset: 0;
    background: var(--perch-scrim);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: var(--perch-z-modal);
  }

  .dialog {
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border);
    border-radius: 6px;
    padding: var(--perch-sp-3);
    min-width: 480px;
    max-width: 600px;
    max-height: 80vh;
    overflow-y: auto;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }

  .settings-section {
    margin-bottom: var(--perch-sp-2);
  }

  .settings-section h2 {
    font-size: var(--perch-fs-body);
    font-weight: 600;
    margin: 0 0 var(--perch-sp-1) 0;
    color: var(--perch-text);
    border-bottom: 1px solid var(--perch-border);
    padding-bottom: 4px;
  }

  .setting-row {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-2);
    margin-bottom: var(--perch-sp-1);
  }

  .setting-label {
    min-width: 80px;
    color: var(--perch-text);
    font-size: var(--perch-fs-body);
  }

  .setting-row select {
    flex: 1;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: 4px;
    padding: 2px 6px;
    font-size: var(--perch-fs-body);
    font-family: var(--perch-font-sans);
  }

  .dnd-switch {
    padding: 2px 10px;
    border: 1px solid var(--perch-border-strong);
    border-radius: 4px;
    background: var(--perch-bg);
    color: var(--perch-text);
    cursor: pointer;
    font-size: var(--perch-fs-body);
  }

  .dnd-switch.on {
    background: var(--perch-accent);
    color: var(--perch-accent-fg);
    border-color: var(--perch-accent);
  }

  .rules-caveat {
    display: flex;
    align-items: flex-start;
    gap: var(--perch-sp-1);
    margin: 0 0 var(--perch-sp-1) 0;
    padding: 6px 8px;
    border: 1px solid var(--perch-warn);
    border-radius: 4px;
    background: color-mix(in srgb, var(--perch-warn) 8%, var(--perch-bg));
    color: var(--perch-text-dim, var(--perch-text));
    font-size: var(--perch-fs-code);
    line-height: 1.4;
  }

  .caveat-icon {
    color: var(--perch-warn);
    flex-shrink: 0;
    font-size: var(--perch-fs-body);
    line-height: 1.4;
  }

  .empty-rules {
    margin: 0;
    color: var(--perch-text);
    font-style: italic;
    opacity: 0.7;
  }

  .rules-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .rule-row {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-1);
    padding: 4px 0;
    border-bottom: 1px solid var(--perch-border);
    font-size: var(--perch-fs-body);
  }

  .rule-row:last-child {
    border-bottom: none;
  }

  .rule-agent {
    min-width: 60px;
    opacity: 0.7;
    font-size: var(--perch-fs-code);
    font-family: var(--perch-font-mono);
  }

  .rule-tool {
    min-width: 80px;
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    color: var(--perch-accent);
  }

  .rule-pattern {
    flex: 1;
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .revoke-btn {
    padding: 2px 8px;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: 4px;
    cursor: pointer;
    font-size: var(--perch-fs-body);
    flex-shrink: 0;
  }

  .revoke-btn:hover {
    border-color: var(--perch-accent);
    color: var(--perch-accent);
  }

  .close-btn {
    margin-top: var(--perch-sp-2);
    padding: 4px 12px;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: 4px;
    font-size: var(--perch-fs-body);
    cursor: pointer;
  }
</style>
