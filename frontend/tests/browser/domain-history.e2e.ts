import { expect, test } from '@playwright/test'

test('saved Nginx definition diff opens for one domain and switches to the whole Pool', async ({ page }) => {
  const versions = [
    { id: 'ngver_1', created_at: '2026-09-25T10:00:00Z', snippets: [
      { scope: 'http', content: 'gzip on;' }, { scope: 'server', resource_id: 'domain-a', content: 'add_header X-Mode old;' },
    ], routes: [] },
    { id: 'ngver_2', parent_version: 'ngver_1', created_at: '2026-09-25T11:00:00Z', actor: 'operator', snippets: [
      { scope: 'http', content: 'gzip off;' }, { scope: 'server', resource_id: 'domain-a', content: 'add_header X-Mode new;' },
      { scope: 'server', resource_id: 'domain-b', content: 'add_header X-Other yes;' },
    ], routes: [] },
  ]
  await page.route('**/api/v1/gateway-pools/pool-history/nginx**', route => {
    const path = new URL(route.request().url()).pathname
    const data = path.endsWith('/draft-versions') ? [] : path.endsWith('/versions') ? versions : {
      active: { version_id: 'ngver_2' }, draft: { revision: 2, updated_at: '2026-09-25T11:00:00Z' },
    }
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data }) })
  })
  await page.goto('/tests/browser/domain-history.html')
  await expect(page.locator('.domain-history-item')).toHaveCount(2)
  await page.locator('.domain-history-open').first().click()
  const detail = page.locator('.domain-history-detail')
  await expect(detail).toContainText('ngver_1')
  await expect(detail.locator('.domain-history-line.add')).toContainText(['add_header X-Mode new;', 'gzip off;'])
  await expect(detail.locator('.domain-history-line.remove')).toContainText(['add_header X-Mode old;', 'gzip on;'])
  await expect(detail.locator('.domain-history-section-title')).toContainText(['包含此域名配置', '影响此域名的 Pool 级变更'])
  await expect(detail).not.toContainText('X-Other')
  await detail.locator('.domain-history-switch button').nth(1).click()
  await expect(detail.locator('.domain-history-section-title')).toContainText(['包含此域名配置', '影响此域名的 Pool 级变更', '其他 Pool 配置'])
  await expect(detail).toContainText('X-Other')
  await page.screenshot({ path: test.info().outputPath('domain-history-desktop.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(detail).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: test.info().outputPath('domain-history-mobile.png'), fullPage: true })
})
