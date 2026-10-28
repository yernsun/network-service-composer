import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/e2e',
  testMatch: '**/*.e2e.ts',
  fullyParallel: false,
  // The repository E2E harness owns one mutable Controller/SQLite instance.
  // Keep the default serial; callers with isolated backends may opt into more workers.
  workers: Number(process.env.NSC_E2E_WORKERS || 1),
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8080',
    launchOptions: {
      ...(process.env.NSC_BROWSER_BINARY ? { executablePath: process.env.NSC_BROWSER_BINARY } : {}),
      args: ['--no-sandbox'],
    },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
