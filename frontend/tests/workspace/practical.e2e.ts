import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
const locales = JSON.parse(readFileSync(new URL('../../../internal/i18n/locales.json', import.meta.url), 'utf8')) as { code: string }[]
if (process.env.NSC_PRACTICAL_BROWSER !== '1') throw new Error('Run through TestEnvironmentCapabilitiesBrowser with isolated API fixtures')
test('environment prerequisites: real HTTP, localization, narrow layout and failed refresh', async ({ page }) => {
  await page.goto('/login')
  await page.locator('#username').fill('admin'); await page.locator('#password').fill('test-admin-token')
  await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
  await page.goto(process.env.NSC_PRACTICAL_ENVIRONMENT_PATH!)
  const panel = page.getByTestId('environment-capabilities')
  await expect(panel.locator('dl')).toBeVisible()
  for (const { code } of locales) {
    await page.locator('#workspace-language').selectOption(code)
    await expect(panel).not.toContainText('capabilities.')
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 })
      expect(await panel.evaluate(element => element.getBoundingClientRect().width <= window.innerWidth)).toBe(true)
      for (const button of await page.locator('.global-title > button').all()) expect(await button.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth)).toBe(true)
    }
  }
  await page.route('**/capabilities', route => route.fulfill({ status: 503, contentType: 'application/problem+json', body: JSON.stringify({ code: 'internal.operation_failed' }) }))
  await panel.locator('button').first().click()
  await expect(panel.locator('[role=alert]')).toBeVisible()
  await expect(panel.locator('dl')).toHaveCount(0)
})

test('real HA evidence, eight locales, narrow layout, one active menu and failed refresh', async ({page})=>{
 await page.goto('/login'); await page.locator('#username').fill('admin'); await page.locator('#password').fill('test-admin-token'); await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
 await page.goto('/infrastructure/ha'); const panel=page.locator('.ha-overview');await expect(panel.locator('[data-layer]')).toHaveCount(6)
 await expect(panel.locator('[data-layer=storage]')).toHaveAttribute('data-state','LIMITED');await expect(panel.locator('[data-layer=ingress]')).toHaveAttribute('data-state','UNKNOWN')
 for(const {code} of locales){await page.locator('#workspace-language').selectOption(code);await expect(panel).not.toContainText('ha.'); for(const width of [1440,390]){await page.setViewportSize({width,height:1000});expect(await panel.evaluate(e=>e.getBoundingClientRect().right<=window.innerWidth+1)).toBe(true);await expect(page.locator('.sidebar .nav-item.active')).toHaveCount(1);await expect(page.locator('.sidebar .nav-item[aria-current=page]')).toHaveAttribute('data-page','ha')}}
 await page.goto('/infrastructure/runtimes');await expect(page.locator('.sidebar .nav-item.active')).toHaveCount(1);await page.goto('/infrastructure/ha');await expect(panel.locator('[data-layer]')).toHaveCount(6)
 await page.route('**/api/v1/system/ha',route=>route.fulfill({status:503,contentType:'application/problem+json',body:JSON.stringify({code:'internal.operation_failed'})}));await panel.locator('button').click();await expect(panel.locator('[role=alert]')).toBeVisible();await expect(panel.locator('[data-layer]')).toHaveCount(0)
})
