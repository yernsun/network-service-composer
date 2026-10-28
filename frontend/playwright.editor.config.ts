import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/browser',
  testMatch: ['compose-editor.e2e.ts', 'domain-history.e2e.ts'],
  reporter: 'list',
  use: { baseURL: 'http://127.0.0.1:5175', launchOptions: { executablePath: process.env.NSC_BROWSER_BINARY || '/usr/bin/google-chrome', args: ['--no-sandbox'] } },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: { command: 'npm run dev -- --port 5175 --strictPort', url: 'http://127.0.0.1:5175/tests/browser/compose-editor.html', reuseExistingServer: false, timeout: 30000 },
})
