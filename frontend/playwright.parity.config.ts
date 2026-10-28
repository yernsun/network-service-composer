import { defineConfig } from '@playwright/test'
import { resolve } from 'node:path'
const report = resolve(process.env.NSC_RESTORATION_REPORT || 'test-results/restoration')
export default defineConfig({
  testDir: './tests/restoration', testMatch: 'parity.e2e.ts', workers: 1, retries: 0,
  timeout: 180000, expect: { timeout: 10000 },
  outputDir: resolve(report, 'artifacts'),
  snapshotPathTemplate: resolve(report, 'original-baselines/{arg}{ext}'),
  reporter: [['list'], ['html', { outputFolder: resolve(report, 'html'), open: 'never' }], ['json', { outputFile: resolve(report, 'results.json') }]],
  use: { actionTimeout: 7000, baseURL: 'http://127.0.0.1:5174', trace: 'on', screenshot: 'on', locale: 'zh-CN',
    launchOptions: { executablePath: process.env.NSC_BROWSER_BINARY || '/opt/google/chrome/chrome', args: ['--no-sandbox'] } },
})
