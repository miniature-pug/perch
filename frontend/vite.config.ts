import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { svelteTesting } from "@testing-library/svelte/vite";
import { PREVIEW_PORT } from "./preview-port.mjs";

export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  build: { outDir: "dist", emptyOutDir: true },
  preview: { port: PREVIEW_PORT, strictPort: true },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/setupTests.ts"],
    css: true,
    exclude: ["**/node_modules/**", "**/dist/**", "e2e/**"],
  },
});
