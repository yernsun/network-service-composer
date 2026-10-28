import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
const locales = JSON.parse(readFileSync(new URL('../../../internal/i18n/locales.json', import.meta.url), 'utf8')) as { code: string }[]
if (process.env.NSC_NOTIFICATION_BROWSER !== '1') throw new Error('Run through TestNotificationSettingsBrowser using isolated API fixtures')
test('notification settings: localization, secret retention, saved test receipt and narrow layout', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('/login')
  await page.locator('#username').fill('admin'); await page.locator('#password').fill('test-admin-token')
  await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
  await page.goto('/account')
  const panel = page.getByTestId('notification-settings')
  await expect(panel.locator('form')).toBeVisible()
  const passwords = panel.locator('input[type=password]')
  await panel.locator('form input[type=checkbox]').first().check()
  await panel.locator('input[value=images]').check()
  await panel.locator('input[value=certificates]').check()
  await panel.locator('input[value=runtimes]').check()
  await passwords.nth(0).fill('https://receiver.example.test/hook?token=browser-secret')
  await passwords.nth(1).fill('browser-signing-secret')
  await passwords.nth(2).fill('test-admin-token')
  const saved = page.waitForResponse(response => response.request().method() === 'PUT' && response.url().endsWith('/notification-settings'))
  await panel.locator('button[type=submit]').click()
  expect((await saved).status()).toBe(200)
  await expect(passwords.nth(0)).toHaveValue(''); await expect(passwords.nth(1)).toHaveValue(''); await expect(passwords.nth(2)).toHaveValue('')
  // Refresh preserves an unsaved URL rather than replacing the form with server state.
  await passwords.nth(0).fill('https://draft.example.test/webhook')
  await panel.getByTestId('notification-refresh').click()
  await expect(passwords.nth(0)).toHaveValue('https://draft.example.test/webhook')
  await passwords.nth(2).fill('test-admin-token')
  const tested = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/notifications/test'))
  await panel.getByTestId('notification-test').click(); expect((await tested).status()).toBe(202)
  await expect(panel.locator('tbody tr')).toHaveCount(1)
  await expect(panel).not.toContainText('browser-signing-secret')
  for (const { code } of locales) {
    await page.locator('#workspace-language').selectOption(code)
    await expect(panel).not.toContainText('notifications.')
    await expect(panel).not.toContainText('undefined')
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 })
      await expect(panel.locator('form')).toBeVisible()
      const fits = await panel.evaluate(element => element.getBoundingClientRect().width <= window.innerWidth)
      expect(fits).toBe(true)
    }
  }
  await panel.screenshot({ path: `${process.env.NSC_NOTIFICATION_REPORT}/notifications-mobile.png` })
  expect(errors).toEqual([])
})
