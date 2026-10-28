import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
const locales = JSON.parse(readFileSync(new URL('../../../internal/i18n/locales.json', import.meta.url), 'utf8')) as { code: string }[]
if (process.env.NSC_ACTIVITY_BROWSER !== '1') throw new Error('Run through TestActivityWorkflowBrowser with isolated API fixtures')

test('incident response, retained filters, related deliveries and eight-language HA workflow', async ({ page }) => {
  await page.goto('/login')
  await page.locator('#username').fill('admin'); await page.locator('#password').fill('test-admin-token')
  await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
  await page.goto(process.env.NSC_ACTIVITY_ENVIRONMENT_PATH!)
  await expect(page.getByTestId('environment-capabilities').locator('dl > div')).toHaveCount(6)
  await page.goto(process.env.NSC_ACTIVITY_PATH!)
  const panel = page.locator('.activity-hub'), issue = page.locator('[data-issue=issue_browser]')
  await expect(issue).toBeVisible(); await expect(page.locator('[data-delivery=notification_browser]')).toBeVisible()
  await issue.locator('button').nth(0).click(); await expect(issue.locator('button').nth(0)).toBeDisabled()
  await expect(issue).toContainText('Active'); await expect(issue).toContainText('Not confirmed')
  await issue.locator('button').nth(1).click(); await expect(issue).toContainText('user_bootstrap')
  await panel.locator('select').selectOption('resolved'); await panel.locator('button').first().click()
  await expect(panel.locator('select')).toHaveValue('resolved'); await expect(issue).toBeVisible()
  for (const { code } of locales) {
    await page.locator('#workspace-language').selectOption(code)
    await expect(panel).not.toContainText('activityHub.'); await expect(panel).not.toContainText('routing.state.')
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 })
      expect(await panel.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth + 1)).toBe(true)
      await expect(page.locator('.sidebar .nav-item.active')).toHaveCount(1)
      await expect(page.locator('.sidebar .nav-item[aria-current=page]')).toHaveAttribute('data-page', 'activity')
    }
    await page.goto('/infrastructure/ha'); await expect(page.locator('.ha-overview [data-layer]')).toHaveCount(6)
    await page.goto(process.env.NSC_ACTIVITY_PATH!); await expect(issue).toBeVisible()
  }
  await page.route('**/api/v1/issues?**', route => route.fulfill({ status: 503, contentType: 'application/problem+json', body: JSON.stringify({ code: 'internal.operation_failed' }) }))
  await panel.locator('button').first().click(); await expect(panel.locator('[role=alert]')).toBeVisible()
  await expect(panel.locator('[data-issue]')).toHaveCount(0)
})
