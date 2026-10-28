import { defineConfig } from '@playwright/test'
import demo from './playwright.demo.config'

// Same browser cases and API contract as Faker, against an isolated Controller.
export default defineConfig({
  ...demo,
  testMatch: 'shared.e2e.ts',
  webServer: undefined,
  use: { ...demo.use, baseURL: process.env.E2E_BASE_URL },
})
