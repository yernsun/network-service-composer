import { test, expect } from '@playwright/test'
test('mobile volumes pin reuse and require a fresh fingerprint for external deletion', async ({ page }) => {
 await page.setViewportSize({ width: 390, height: 844 })
 const errors: string[] = []; page.on('pageerror', e => errors.push(e.message))
 await page.goto('/login'); await page.locator('#username').fill('admin'); await page.locator('#password').fill('demo'); await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects/)
 const volume = { name: 'anonymous-generated-id', kind: 'anonymous', node_id: 'worker-a', engine_id: 'engine-a', driver: 'local', scope: 'local', created_at: '2026-10-01T00:00:00Z', version: 'fingerprint', owned: false, used_by: [], blocked_reasons: [] }
 await page.route('**/api/v1/runtimes/rt_demo', route => route.fulfill({ json: { data: { id: 'rt_demo', name: 'isolated-swarm', runtime_type: 'swarm', enabled: true, management_enabled: true, revision: 1 } } }))
 await page.route('**/api/v1/runtimes/rt_demo/cluster/nodes*', route => route.fulfill({ json: { data: ['a', 'b'].map(id => ({ id: `worker-${id}`, name: `Worker ${id}`, state: 'ready', availability: 'active', role: 'worker' })), pagination: { next_cursor: '' } } }))
 await page.route('**/api/v1/runtimes/rt_demo/docker-resources*', route => route.fulfill({ json: { data: [], pagination: { next_cursor: '' } } }))
 let deleted = false
 await page.route('**/api/v1/runtimes/rt_demo/volumes**', async route => {
  const req = route.request(), url = new URL(req.url())
  expect(url.searchParams.get('node_id')).toBe('worker-a')
  if (req.method() === 'DELETE') {
   expect(req.headers()['if-match']).toBe('"volume-fingerprint"'); expect(req.headers()['idempotency-key']).toBeTruthy(); expect(req.postDataJSON()).toEqual({ acknowledge_data_loss: true }); deleted = true
   await route.fulfill({ status: 202, json: { data: { id: 'op-volume' } } }); return
  }
  await route.fulfill({ headers: { ETag: '"volume-fingerprint"' }, json: { data: url.pathname.endsWith('/volumes') ? deleted ? [] : [volume] : volume, complete: true, observed_at: '2026-10-06T00:00:00Z', node_name: 'Worker A', pagination: { next_cursor: '' } } })
 })
 await page.route('**/api/v1/operations/op-volume', route => route.fulfill({ json: { data: { id: 'op-volume', status: 'SUCCEEDED' } } }))
 await page.goto('/infrastructure/runtimes/rt_demo?tab=resources')
 await page.locator('.docker-resources nav button').first().click()
 const view = page.locator('.volumes'); await view.locator('select').first().selectOption('worker-a')
 await expect(view).toContainText(volume.name)
 await view.locator('li button').first().click(); await expect(view.locator('textarea')).toHaveValue(/name: anonymous-generated-id/); await expect(view.locator('textarea')).toHaveValue(/node.id == worker-a/)
 await view.locator('.detail button').last().click(); await view.locator('li button.danger').click(); await expect(view.locator('.detail')).toContainText('worker-a')
 await expect(view.locator('.detail button.danger')).toBeDisabled(); await view.locator('.detail input[type=checkbox]').check(); await view.locator('.detail button.danger').click()
 await expect(view).toContainText('op-volume'); expect(deleted).toBe(true); await expect(view.locator('li')).toHaveCount(0); expect(errors).toEqual([])
})
