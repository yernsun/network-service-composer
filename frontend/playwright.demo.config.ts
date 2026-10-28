import { defineConfig, devices } from '@playwright/test'
import { resolve } from 'node:path'
const report = process.env.NSC_DEMO_REPORT || 'test-results/demo'
const port = Number(process.env.NSC_DEMO_PORT || 4174)
if (!Number.isInteger(port) || port < 1024 || port > 65535) throw new Error('Invalid NSC_DEMO_PORT')
export default defineConfig({
  testDir: './tests/workspace', testMatch: ['shared.e2e.ts', 'faker.e2e.ts', 'volumes.e2e.ts', 'wizard.e2e.ts', 'readiness.e2e.ts', 'entrypoint.e2e.ts'], workers: 1, retries: 0,
  timeout: 60000, expect: { timeout: 10000 }, forbidOnly: Boolean(process.env.CI),
  outputDir: resolve(report, 'artifacts'), reporter: [['list'], ['html', { outputFolder: resolve(report, 'html'), open: 'never' }], ['json', { outputFile: resolve(report, 'results.json') }]],
  use: { baseURL: `http://127.0.0.1:${port}`, trace: 'on', screenshot: 'on',
    launchOptions: { executablePath: process.env.NSC_BROWSER_BINARY, args: ['--no-sandbox'] } },
  webServer: { command: `NSC_FAKER_PORT=${port} npm run preview:demo`, url: `http://127.0.0.1:${port}`, reuseExistingServer: !process.env.CI },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
