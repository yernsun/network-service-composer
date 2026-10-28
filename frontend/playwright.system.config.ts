import { defineConfig, devices } from '@playwright/test'
import { resolve } from 'node:path'
const report = process.env.NSC_SYSTEM_REPORT || 'test-results/system'
export default defineConfig({
  testDir: './tests/workspace', testMatch: 'system.e2e.ts', workers: 1, retries: 0,
  timeout: 90000, expect: { timeout: 10000 }, forbidOnly: Boolean(process.env.CI),
  outputDir: resolve(report, 'artifacts'),
  reporter: [['list'], ['html', { outputFolder: resolve(report, 'html'), open: 'never' }], ['json', { outputFile: resolve(report, 'results.json') }]],
  use: { baseURL: process.env.E2E_BASE_URL, trace: 'on', screenshot: 'on',
    launchOptions: { executablePath: process.env.NSC_BROWSER_BINARY, args: ['--no-sandbox'] } },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
