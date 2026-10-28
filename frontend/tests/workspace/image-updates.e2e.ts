import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
const locales = JSON.parse(readFileSync(new URL('../../../internal/i18n/locales.json', import.meta.url), 'utf8')) as { code: string }[]
if (process.env.NSC_PRACTICAL_IMAGE_BROWSER !== '1') throw new Error('Run through TestImageUpdatesBrowser with isolated API fixtures')
test('image updates: actual policy save, explicit candidate submission and localized narrow layout', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('/login')
  await page.locator('#username').fill('admin'); await page.locator('#password').fill('test-admin-token')
  await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
  await page.goto(process.env.NSC_PRACTICAL_ENVIRONMENT_PATH!)
  const panel = page.getByTestId('image-updates')
  await expect(panel.locator('form')).toBeVisible()
  await expect(panel.getByTestId('image-automatic')).not.toBeChecked()
  await panel.getByTestId('image-enabled').check()
  await panel.getByTestId('image-poll').check()
  await panel.locator('input[value=web]').check()
  const saved = page.waitForResponse(response => response.request().method() === 'PUT' && response.url().endsWith('/update-policy'))
  await panel.locator('button[type=submit]').click()
  expect((await saved).status()).toBe(200)
  await panel.locator('input[type=number]').fill('600')
  await panel.getByTestId('image-refresh').click()
  await expect(panel.locator('input[type=number]')).toHaveValue('600')
  for (const { code } of locales) {
    await page.locator('#workspace-language').selectOption(code)
    await expect(panel).not.toContainText('imageUpdates.')
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 })
      expect(await panel.evaluate(element => element.getBoundingClientRect().width <= window.innerWidth)).toBe(true)
    }
  }
  await page.setViewportSize({ width: 1440, height: 1000 })
  const candidate = panel.locator('[data-candidate=candidate-browser]')
  const submitted = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/image-updates/candidate-browser'))
  await candidate.locator('.update-actions button').first().click()
  await expect(panel.getByTestId('image-confirm')).toBeVisible()
  await panel.getByTestId('image-confirm').locator('button').first().click()
  expect((await submitted).status()).toBe(202)
  await expect(candidate.locator('a')).toHaveAttribute('href', /\/operations\//)
  await expect(panel.getByTestId('image-confirm')).toHaveCount(0)
  await expect(panel.locator('input[type=number]')).toHaveValue('600')
  await page.route('**/image-updates?*', route => route.fulfill({ status: 503, contentType: 'application/problem+json', body: JSON.stringify({ code: 'internal.operation_failed' }) }))
  await panel.getByTestId('image-refresh').click()
  for (const button of await candidate.locator('.update-actions button').all()) await expect(button).toBeDisabled()
  expect(errors).toEqual([])
})
