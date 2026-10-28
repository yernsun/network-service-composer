import { defineConfig, devices } from '@playwright/test'
export default defineConfig({
  testDir: './tests/workspace', testMatch: 'self-signed.e2e.ts', workers: 1, retries: 0,
  timeout: 120000, expect: { timeout: 10000 }, forbidOnly: Boolean(process.env.CI),
  outputDir: '../coverage/self-signed/browser/artifacts', reporter: [['list']],
  use: { baseURL: process.env.E2E_BASE_URL, trace: 'on', screenshot: 'on',
    launchOptions: { executablePath: process.env.NSC_BROWSER_BINARY, args: ['--no-sandbox'] } },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
