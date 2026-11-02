import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  workers: 1,
  use: {
    baseURL: "http://127.0.0.1:34115",
    channel: "chrome",
    viewport: { width: 1440, height: 1100 },
  },
  webServer: {
    command: "go run ./cmd/preview",
    url: "http://127.0.0.1:34115",
    reuseExistingServer: !process.env.CI,
  },
});
