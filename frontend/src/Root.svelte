<!-- frontend/src/Root.svelte -->
<script lang="ts">
  import App from "./App.svelte";
  import CrashScreen from "./lib/CrashScreen.svelte";
</script>

<svelte:boundary onerror={(error) => { console.error("perch: uncaught UI error", error); }}>
  <App />

  {#snippet failed(error, reset)}
    <CrashScreen {error} onRetry={reset} onReload={() => location.reload()} />
  {/snippet}
</svelte:boundary>
