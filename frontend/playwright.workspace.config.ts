import { defineConfig, devices } from '@playwright/test'
import { resolve } from 'node:path'
const report = process.env.NSC_WORKSPACE_REPORT || resolve('test-results/workspace')
export default defineConfig({
  testDir: './tests/workspace', testMatch: ['lifecycle.e2e.ts', 'live.e2e.ts', 'bindings.e2e.ts', 'compose.e2e.ts', 'incremental.e2e.ts', 'ports.e2e.ts'], workers: 1, retries: 0,
  timeout: 120000, expect: { timeout: 15000 }, forbidOnly: Boolean(process.env.CI),
  outputDir: resolve(report, 'artifacts'),
  reporter: [['list'], ['html', { outputFolder: resolve(report, 'html'), open: 'never' }], ['json', { outputFile: resolve(report, 'results.json') }]],
  use: { baseURL: process.env.E2E_BASE_URL, actionTimeout: 15000, navigationTimeout: 30000, trace: 'on', screenshot: 'on',
    launchOptions: { executablePath: process.env.NSC_BROWSER_BINARY, args: ['--no-sandbox'] } },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
