import { test, expect } from '@playwright/test'

const password = process.env.E2E_ADMIN_PASSWORD || 'demo'

test('shared frontend uses the HTTP session and project API', async ({ page }) => {
  const calls: string[] = []
  page.on('request', request => {
    if (new URL(request.url()).pathname.startsWith('/api/v1/')) calls.push(request.url())
  })
  await page.goto('/projects')
  await expect(page).toHaveURL(/\/login/)
  await expect(page.locator('h1')).toHaveText('Network Service Composer')
  await expect(page).toHaveTitle('Network Service Composer')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill(password)
  await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects/)
  await expect(page.locator('#main')).toBeVisible()
  await expect(page.locator('.brand-name')).toHaveText('Network Service Composer')
  await expect(page).toHaveTitle(/ · Network Service Composer$/)
  await page.locator('.topbar .avatar').click()
  await expect(page.locator('#modal-title')).toHaveText('Network Service Composer for Nginx')
  await page.locator('.modal-head [data-action="close"]').click()
  expect(calls.some(url => url.includes('/auth/session'))).toBe(true)
  expect(calls.some(url => url.includes('/projects'))).toBe(true)
  for (const resource of ['projects', 'applications', 'services', 'catalog/apps']) {
    const response = await page.request.get(`/api/v1/${resource}`)
    expect(response.status(), resource).toBe(200)
    expect(Array.isArray((await response.json()).data), resource).toBe(true)
  }
  expect(await page.evaluate(() => localStorage.getItem('network-service-composer-vue-v1.3'))).toBeNull()
})
