<script lang="ts">
  let {
    theme,
    density = "dense",
    children,
  }: { theme: string; density?: "dense" | "comfortable" | "ultra"; children?: any } = $props();

  $effect(() => { document.documentElement.setAttribute("data-theme", theme); });
  $effect(() => { document.documentElement.setAttribute("data-density", density); });
</script>

{@render children?.()}
