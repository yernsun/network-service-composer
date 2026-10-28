import { test, expect, type Page } from '@playwright/test'

async function prepare(page: Page) {
  let fail = false
  await page.route('**/api/v1/operations**', async route => {
    const path = new URL(route.request().url()).pathname
    const operation = { id: 'op-readiness', application_id: '', runtime_id: 'rt_demo', type: 'INSTALL', status: 'SUCCEEDED', revision: 3, attempt: 1, created_at: '2026-10-13T00:00:00Z' }
    if (path.endsWith('/readiness')) {
      if (fail) return route.fulfill({ status: 503, json: { code: 'internal.operation_failed' } })
      const data = { operation_id: operation.id, revision: 3, observed_at: '2026-10-13T00:00:00Z', baseline_digest: 'a'.repeat(64), stages: ['accepted', 'workload', 'certificate', 'gateway', 'client_access'].map(name => ({ name, state: name === 'client_access' ? 'UNKNOWN' : 'CONFIRMED', ...(name === 'client_access' ? { error_code: 'client_not_verified' } : {}) })), gateway_targets: [{ gateway_id: 'gateway-confirmed', pool_id: 'pool_demo', state: 'CONFIRMED', desired_revision: 5, applied_revision: 5 }, { gateway_id: 'gateway-unavailable', pool_id: 'pool_demo', state: 'UNKNOWN', desired_revision: 5, applied_revision: 4 }], access_targets: [] }
      data.stages.find(stage => stage.name === 'gateway')!.state = 'UNKNOWN'
      return route.fulfill({ status: 200, json: { data } })
    }
    if (path.endsWith('/entrypoint')) return route.fulfill({ status: 404, json: { code: 'resource.not_found' } })
    return route.fulfill({ status: 200, json: { data: path === '/api/v1/operations' ? [operation] : operation, pagination: { next_cursor: '' } } })
  })
  await page.goto('/login')
  await page.locator('#username').fill('admin'); await page.locator('#password').fill('demo')
  await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects/)
  await page.goto('/operations/op-readiness')
  await expect(page.getByTestId('operation-readiness')).toBeVisible()
  return () => { fail = true }
}

test('readiness distinguishes partial loading and client verification across responsive locales', async ({ page }) => {
  await prepare(page)
  const panel = page.getByTestId('operation-readiness')
  await expect(panel.locator('[data-stage=workload]')).toHaveAttribute('data-state', 'CONFIRMED')
  await expect(panel.locator('[data-stage=client_access]')).toHaveAttribute('data-state', 'UNKNOWN')
  await expect(panel.locator('[data-gateway=gateway-confirmed]')).toHaveAttribute('data-state', 'CONFIRMED')
  await expect(panel.locator('[data-gateway=gateway-unavailable]')).toHaveAttribute('data-state', 'UNKNOWN')
  for (const locale of ['en', 'ja', 'de', 'zh-CN', 'zh-TW', 'fr', 'ru', 'es']) {
    await page.locator('#workspace-language').selectOption(locale, { force: true })
    await page.setViewportSize({ width: 390, height: 850 })
    expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await expect(panel.locator('[data-stage=client_access]')).toHaveAttribute('data-state', 'UNKNOWN')
  }
  await page.reload()
  await expect(panel.locator('[data-stage=workload]')).toHaveAttribute('data-state', 'CONFIRMED')
})

test('a failed refresh invalidates displayed confirmations and retains operation result', async ({ page }) => {
  const fail = await prepare(page)
  const panel = page.getByTestId('operation-readiness')
  await expect(panel.locator('[data-stage=workload]')).toHaveAttribute('data-state', 'CONFIRMED')
  fail()
  await panel.locator('button').click()
  await expect(panel.getByTestId('readiness-observation-error')).toBeVisible()
  for (const stage of ['accepted', 'workload', 'certificate', 'gateway', 'client_access']) await expect(panel.locator(`[data-stage=${stage}]`)).toHaveAttribute('data-state', 'UNKNOWN')
  await expect(panel.locator('[data-gateway=gateway-confirmed]')).toHaveAttribute('data-state', 'UNKNOWN')
  // The operation HTTP contract is still the successful recorded execution.
  expect(await page.evaluate(async () => (await (await fetch('/api/v1/operations/op-readiness')).json()).data.status)).toBe('SUCCEEDED')
})
