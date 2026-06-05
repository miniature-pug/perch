<script lang="ts">
  let {
    theme,
    density = "dense",
    font = "geist",
    glass = true,
    children,
  }: { theme: string; density?: "dense" | "comfortable" | "ultra"; font?: string; glass?: boolean; children?: any } = $props();

  // Map font keys from settings to CSS font-family stacks.
  // Setting --perch-font-sans directly on :root lets every component pick it up
  // without touching tokens.css (which is out of scope for this component).
  const FONT_FAMILIES: Record<string, string> = {
    "geist":       '"Geist", "IBM Plex Sans", "Inter", system-ui, sans-serif',
    "inter":       '"Inter", system-ui, sans-serif',
    "ibm-plex":    '"IBM Plex Sans", system-ui, sans-serif',
  };

  $effect(() => { document.documentElement.setAttribute("data-theme", theme); });
  $effect(() => { document.documentElement.setAttribute("data-density", density); });
  $effect(() => {
    const family = FONT_FAMILIES[font] ?? FONT_FAMILIES["geist"];
    document.documentElement.style.setProperty("--perch-font-sans", family);
  });
  $effect(() => { document.documentElement.setAttribute("data-glass", glass ? "on" : "off"); });
</script>

{@render children?.()}
