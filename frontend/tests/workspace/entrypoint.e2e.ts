import { test, expect, type Page } from '@playwright/test'

async function prepare(page: Page) {
  let status = 200
  let result = 'SUCCEEDED'
  await page.route('**/api/v1/operations**', async route => {
    const path = new URL(route.request().url()).pathname
    const operation = { id: 'op-entrypoint', application_id: '', runtime_id: 'rt_demo', type: 'UPGRADE', business_action: 'DELETE_SERVICE', status: 'SUCCEEDED', revision: 3, attempt: 1, created_at: '2026-10-13T00:00:00Z' }
    if (path.endsWith('/entrypoint')) {
      if (status !== 200) return route.fulfill({ status, json: { code: 'internal.operation_failed' } })
      return route.fulfill({ status: 200, json: { data: { status: result, revision: 4, attempts: 1, intent_id: 'intent-entrypoint' } } })
    }
    if (path.endsWith('/readiness')) return route.fulfill({ status: 200, json: { data: { operation_id: operation.id, revision: 3, observed_at: '2026-10-13T00:00:00Z', baseline_digest: 'a'.repeat(64), stages: ['accepted', 'workload', 'certificate', 'gateway', 'client_access'].map(name => ({ name, state: 'UNKNOWN' })), gateway_targets: [], access_targets: [] } } })
    return route.fulfill({ status: 200, json: { data: path === '/api/v1/operations' ? [operation] : operation, pagination: { next_cursor: '' } } })
  })
  await page.goto('/login')
  await page.locator('#username').fill('admin'); await page.locator('#password').fill('demo')
  await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects/)
  await page.goto('/operations/op-entrypoint')
  await expect(page.getByTestId('operation-entrypoint')).toHaveAttribute('data-state', 'SUCCEEDED')
  return { fail: (code: number) => { status = code }, recover: (value = 'SUCCEEDED') => { status = 200; result = value } }
}

test('entrypoint refresh failures show unknown current state and labelled history across locales', async ({ page }) => {
  const control = await prepare(page)
  const panel = page.getByTestId('operation-entrypoint')
  control.fail(503)
  await panel.getByTestId('entrypoint-refresh').click()
  await expect(panel).toHaveAttribute('data-state', 'UNKNOWN')
  await expect(panel.getByTestId('entrypoint-observation-error')).toBeVisible()
  await expect(panel.getByTestId('entrypoint-last-result')).toBeVisible()
  for (const locale of ['en', 'ja', 'de', 'zh-CN', 'zh-TW', 'fr', 'ru', 'es']) {
    await page.locator('#workspace-language').selectOption(locale, { force: true })
    await page.setViewportSize({ width: 390, height: 850 })
    await expect(panel).toHaveAttribute('data-state', 'UNKNOWN')
    expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  }
  control.recover('FAILED')
  await panel.getByTestId('entrypoint-refresh').click()
  await expect(panel).toHaveAttribute('data-state', 'FAILED')
  await expect(panel.getByTestId('entrypoint-observation-error')).toHaveCount(0)
  await expect(panel.getByTestId('entrypoint-retry')).toBeEnabled()
  expect(await page.evaluate(async () => (await (await fetch('/api/v1/operations/op-entrypoint')).json()).data.status)).toBe('SUCCEEDED')
})

test('entrypoint permission loss removes cached result and retry controls', async ({ page }) => {
  const control = await prepare(page)
  const panel = page.getByTestId('operation-entrypoint')
  control.recover('FAILED')
  await panel.getByTestId('entrypoint-refresh').click()
  await expect(panel.getByTestId('entrypoint-retry')).toBeEnabled()
  control.fail(403)
  await panel.getByTestId('entrypoint-refresh').click()
  await expect(panel).toHaveAttribute('data-state', 'UNKNOWN')
  await expect(panel.getByTestId('entrypoint-last-result')).toHaveCount(0)
  await expect(panel.getByTestId('entrypoint-retry')).toHaveCount(0)
  await expect(panel.getByTestId('entrypoint-checked-at')).toHaveCount(0)
})
