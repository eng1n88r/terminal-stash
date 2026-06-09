import { defineConfig } from '@playwright/test';

// The suite runs single-worker against one shared server instance: the SSE
// sync tests and the rate-limit lockout (03-ratelimit, which must run last)
// depend on ordering.
export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  timeout: 30_000,
  use: {
    baseURL: 'http://localhost:7832',
    permissions: ['clipboard-read', 'clipboard-write'],
  },
  webServer: {
    command: 'node start-server.js',
    url: 'http://localhost:7832/login',
    reuseExistingServer: false,
    timeout: 120_000,
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
