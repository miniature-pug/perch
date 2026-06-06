import { defineConfig, devices } from "@playwright/test";
import { PREVIEW_PORT } from "./preview-port.mjs";

const baseURL = `http://localhost:${PREVIEW_PORT}`;

export default defineConfig({
  testDir: "./e2e",
  use: {
    baseURL,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: {
    command: "npm run preview",
    url: baseURL,
    reuseExistingServer: false,
    timeout: 30000,
  },
});
