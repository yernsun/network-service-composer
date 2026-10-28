import { test, expect, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'

const locales = JSON.parse(readFileSync(new URL('../../../internal/i18n/locales.json', import.meta.url), 'utf8')) as { code: string }[]
const source = 'services:\n  wordpress:\n    image: wordpress:6\n    volumes: [web-data:/var/www/html]\n  database:\n    image: mysql:8\n    volumes: [database-data:/var/lib/mysql]\nvolumes:\n  web-data: {}\n  database-data: {}\n'
const longDescription = 'Choose a fixed application version and read its parameters before entering credentials. Preserve the application and database volumes when redeploying. '

async function fixture(page: Page, role: 'manager' | 'worker' = 'manager') {
  const calls: string[] = []
  page.on('request', request => { if (request.method() === 'GET') calls.push(new URL(request.url()).pathname) })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    const send = (data: unknown, collection = false) => route.fulfill({ status: 200, json: { data, ...(collection ? { pagination: { next_cursor: '' } } : {}) } })
    if (path === '/api/v1/runtimes') return send([{ id: 'rt_demo', pool_id: 'pool_demo', name: 'demo-swarm', runtime_type: 'swarm', enabled: true, management_enabled: true, status: 'ONLINE' }], true)
    if (path === '/api/v1/catalog/apps') return send([{ id: 'cat_wizard_wordpress', app_key: 'wordpress', name: 'WordPress', version: '1.0.0', services: ['wordpress', 'database'], compose_compatible: true, swarm_compatible: true, compose_yaml: source, swarm_yaml: source,
      market: { variables: [
        { key: 'MYSQL_ROOT_PASSWORD', secret: true, required: true, min_length: 16, description: { en: longDescription.repeat(2) } },
        { key: 'MYSQL_PASSWORD', secret: true, required: true, min_length: 16, description: { en: longDescription } },
        { key: 'MYSQL_DATABASE', default: 'app', description: { en: longDescription } },
        { key: 'MYSQL_USER', default: 'app', description: { en: longDescription } },
      ] },
    }], true)
    if (path === '/api/v1/runtimes/rt_demo/cluster') return send({ id: 'demo-cluster', stale: false, manager_available: role === 'manager', error: role === 'worker' ? 'manager_required' : '', inventory_complete: true })
    if (path === '/api/v1/runtimes/rt_demo/cluster/nodes') return send([{ id: 'node_demo', name: 'demo-node', role, state: 'ready', availability: 'active', leader: role === 'manager', reachability: role === 'manager' ? 'reachable' : '', connected: true, stale: false, inventory_complete: true }], true)
    if (path === '/api/v1/runtimes/rt_demo/cluster/nodes/node_demo/instances') return send([], true)
    if (path.endsWith('/compose-validation')) return send({ syntax_valid: true, schema_valid: true, deployable: true, diagnostics: [], services: [{ name: 'wordpress', mounts: [], ports: [], constraints: [], matched_nodes: [] }, { name: 'database', mounts: [], ports: [], constraints: [], matched_nodes: [] }] })
    return route.continue()
  })
  await page.goto('/login')
  await page.locator('#username').fill('admin'); await page.locator('#password').fill('demo')
  await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects/)
  const session = (await (await page.request.get('/api/v1/auth/session')).json()).data
  await page.request.post('/api/v1/__faker/reset', { headers: { 'X-CSRF-Token': session.csrf_token } })
  await page.goto('/login'); await page.locator('#password').fill('demo'); await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects/)
  calls.length = 0
  return calls
}

async function open(page: Page, path: string) {
  await page.goto(path)
  if (path === '/market') await page.locator('[data-action=x-template][data-id=cat_wizard_wordpress]').click()
  else await page.locator('#main [data-action=x-install]').first().click()
  await expect(page.getByTestId('market-parameters')).toBeVisible()
  await expect(page.getByTestId('market-data-node').locator('select')).toHaveValue('node_demo')
  await expect(page.locator('#x-w-primary')).toBeEnabled()
}

test('market wizard keeps parameter fields and long descriptions inside the responsive main column', async ({ page }, testInfo) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  const calls = await fixture(page)
  await open(page, '/market')
  expect(calls).toContain('/api/v1/runtimes/rt_demo/cluster')
  await page.locator('#market-parameter-MYSQL_ROOT_PASSWORD').fill('x'.repeat(20))
  for (const width of [1440, 1024, 390]) {
    await page.setViewportSize({ width, height: 900 })
    for (const code of width === 390 ? locales.map(locale => locale.code) : ['zh-CN']) {
      await page.locator('#workspace-language').selectOption(code, { force: true })
      await expect(page.locator('#market-parameter-MYSQL_ROOT_PASSWORD')).toHaveValue('x'.repeat(20))
      const bounds = await page.getByTestId('market-parameters').evaluate(section => {
        const rect = section.getBoundingClientRect()
        return {
          inMain: !!section.closest('.x-wizard-grid > div:first-child'),
          overflow: section.scrollWidth > section.clientWidth + 1,
          fields: [...section.querySelectorAll('.market-parameter')].map(field => {
            const label = field.querySelector('label')!.getBoundingClientRect()
            const input = field.querySelector('input')!.getBoundingClientRect()
            const help = field.querySelector('.parameter-help')!.getBoundingClientRect()
            return { separated: label.bottom <= input.top && input.bottom <= help.top, contained: input.left >= rect.left && input.right <= rect.right && help.right <= rect.right }
          }),
        }
      })
      expect(bounds.inMain).toBe(true); expect(bounds.overflow).toBe(false)
      expect(bounds.fields.every(field => field.separated && field.contained)).toBe(true)
      expect(await page.locator('.modal-body').evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
      expect(await page.locator('.modal').evaluate(el => {
        const modal = el.getBoundingClientRect(), heading = el.querySelector('#modal-title')!.getBoundingClientRect(), close = el.querySelector('.modal-head .iconbtn')!.getBoundingClientRect()
        return close.right >= modal.right - 40 && close.top <= heading.bottom
      })).toBe(true)
      if (code === 'zh-CN') {
        await page.getByTestId('market-parameters').scrollIntoViewIfNeeded()
        await page.screenshot({ path: testInfo.outputPath(`wizard-${width}.png`) })
      }
    }
  }
  expect(await page.getByTestId('deployment-network').evaluate(el => !!el.closest('.x-wizard-grid > div:first-child'))).toBe(true)
  expect(errors).toEqual([])
})

for (const path of ['/market', '/projects/prj_demo/environments/env_demo/applications']) {
  test(`fresh manager evidence permits the deployment target step from ${path}`, async ({ page }) => {
    const calls = await fixture(page)
    await open(page, path)
    await page.locator('#x-w-primary').click()
    await expect(page.locator('#x-w-runtime')).toHaveValue('rt_demo')
    await expect(page.locator('#x-w-runtime option:checked')).not.toContainText('当前不可部署')
    const before = calls.filter(path => path === '/api/v1/runtimes/rt_demo/cluster').length
    await page.locator('#x-w-primary').click()
    await expect(page.locator('[data-wf=access]')).toBeVisible()
    expect(calls.filter(path => path === '/api/v1/runtimes/rt_demo/cluster').length).toBeGreaterThan(before)
    await expect(page.locator('.modal')).not.toContainText('需要连接有管理能力的 Manager')
  })
}

test('a worker remains blocked and the manager warning appears once', async ({ page }) => {
  await fixture(page, 'worker')
  await open(page, '/market')
  await page.locator('#x-w-primary').click()
  await expect(page.locator('#x-w-runtime')).toBeVisible()
  await page.locator('#x-w-primary').click()
  await expect(page.locator('.modal .callout.red')).toContainText('需要连接有管理能力的 Manager')
  expect((await page.locator('.modal').innerText()).split('需要连接有管理能力的 Manager')).toHaveLength(2)
  await expect(page.locator('[data-wf=access]')).toHaveCount(0)
})
