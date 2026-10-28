import { defineConfig, devices } from '@playwright/test'
import { resolve } from 'node:path'
const report = process.env.NSC_PRACTICAL_REPORT || 'test-results/practical'
export default defineConfig({
  testDir: './tests/workspace', testMatch: process.env.NSC_PRACTICAL_IMAGE_BROWSER === '1' ? 'image-updates.e2e.ts' : 'practical.e2e.ts', workers: 1, retries: 0,
  timeout: 120000, expect: { timeout: 10000 }, forbidOnly: Boolean(process.env.CI),
  outputDir: resolve(report, 'artifacts'),
  reporter: [['list'], ['json', { outputFile: resolve(report, 'results.json') }]],
  use: { baseURL: process.env.E2E_BASE_URL, trace: 'on', screenshot: 'on',
    launchOptions: { executablePath: process.env.NSC_BROWSER_BINARY, args: ['--no-sandbox'] } },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
