import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
const locales = JSON.parse(readFileSync(new URL('../../../internal/i18n/locales.json', import.meta.url), 'utf8')) as { code: string }[]
test('demo: Vue application list keeps scope, detail navigation and install entrypoint', async ({ page }) => {
  const requests: string[] = []
  page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) requests.push(request.url()) })
  await page.goto('/projects/portal/environments/prod/applications')
  const app = page.locator('#main .x-template').filter({ has: page.locator('[data-action=x-app][data-id=app-portal-prod]') })
  await expect(app).toBeVisible()
  await app.getByRole('button').click()
  await expect(page).toHaveURL(/\/applications\/app-portal-prod(?:[/?]|$)/)
  await page.goBack()
  await page.locator('#main [data-action=x-install]').click()
  await expect(page.locator('#modal-title')).toBeVisible()
  expect(requests).toEqual([])
})
test('demo: Vue service list filters, paginates and opens service detail', async ({ page }) => {
  const requests: string[] = []
  page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) requests.push(request.url()) })
  await page.goto('/projects/portal/environments/prod/services')
  await expect(page.locator('#main [data-service-id]').first()).toBeVisible()
  await page.locator('[data-action=x-layout][data-id=flat]').click()
  const id = await page.locator('#main [data-service-id]').first().getAttribute('data-service-id')
  await page.locator('#x-service-search').fill(id!)
  await expect(page.locator('#main [data-service-id]')).toHaveCount(1)
  await page.locator(`#main [data-action=x-service][data-id="${id}"]`).first().click()
  await expect(page).toHaveURL(new RegExp(`/services/${id}(?:[/?]|$)`))
  expect(requests).toEqual([])
})
test('demo: Vue application market filters templates and opens install flow', async ({ page }) => {
  const requests: string[] = []
  page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) requests.push(request.url()) })
  await page.goto('/market')
  await page.locator('#x-market-search').fill('WordPress')
  const template = page.locator('#main .x-template').filter({ hasText: 'WordPress' })
  await expect(template).toHaveCount(1)
  await template.locator('[data-action=x-template-info]').click()
  await expect(page.locator('#modal-title')).toContainText('WordPress')
  await page.keyboard.press('Escape')
  await template.locator('[data-action=x-template]').click()
  await expect(page.locator('#modal-title')).toBeVisible()
  expect(requests).toEqual([])
})
test('demo: service references use literal names across scopes while saved route choices retain distinct IDs', async ({ page }) => {
  const requests: string[] = []
  page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) requests.push(request.url()) })
  await page.goto('/projects')
  await page.locator('[data-action=x-demo]').click()
  await page.locator('[data-action=x-demo-save]').click()
  const fixture = await page.evaluate(() => {
    const key = 'network-service-composer-vue-v1.3'
    const db = JSON.parse(localStorage.getItem(key)!)
    const domain = db.domains.find((d: { id: string }) => d.id === 'd1')
    const route = domain.active.routes.find((r: { kind: string }) => r.kind === 'proxy')
    type Service = { id: string; name: string; project: string; env: string }
    const current = db.services.find((s: Service) => s.id === route.service) as Service
    const other = db.services.find((s: Service) => s.project === current.project && s.env === current.env && s.id !== current.id) as Service
    const cross = db.services.find((s: Service) => s.env !== current.env) as Service
    const name = '删除 {name} <b>Literal & Service</b>'
    current.name = other.name = cross.name = name
    // The archive opens its saved draft first. Seed the missing reference in
    // both configurations so this fixture exercises the visible relationship.
    for (const config of [domain.active, domain.draft.config]) config.routes.push({ ...route, id: 'missing-reference', path: '/missing-reference', service: 'svc-missing-reference' })
    localStorage.setItem(key, JSON.stringify(db))
    return { current: current.id, other: other.id, name, domain: domain.id, route: route.id, base: `/projects/${domain.project}/environments/${domain.env}` }
  })
  for (const path of [`${fixture.base}/routes`, '/projects/portal/environments/all/routes', '/projects/all/environments/all/routes']) {
    await page.goto(path)
    const label = page.locator(`#main [data-action=service-detail][data-id="${fixture.current}"]`).first()
    await expect(label).toContainText(fixture.name)
    await expect(label.locator('b')).toHaveCount(0)
    await label.click()
    await expect(page).toHaveURL(new RegExp(`/services/${fixture.current}(?:\\?|$)`))
    await expect(page.locator('#main h1')).toHaveText(fixture.name)
    await page.goBack()
    await expect(label).toContainText(fixture.name)
  }
  await page.goto(`${fixture.base}/domains/${fixture.domain}?tab=routes`)
  for (const { code } of locales) {
    await page.locator('#workspace-language').selectOption(code)
    await expect(page.locator(`#main [data-action=service-detail][data-id="${fixture.current}"]`)).toContainText(fixture.name)
    const missing = page.locator('#main [data-action=service-detail][data-id="svc-missing-reference"]')
    await expect(missing).toContainText('svc-missing-reference')
    expect((await missing.innerText()).trim()).not.toBe('svc-missing-reference')
  }
  for (const query of [fixture.name, fixture.current]) {
    await page.locator('#route-search').fill(query)
    await expect(page.locator(`#main [data-action=service-detail][data-id="${fixture.current}"]`)).toContainText(fixture.name)
    await expect(page.locator('[data-id="svc-missing-reference"]')).toHaveCount(0)
  }
  await page.locator('#route-search').fill('')
  await page.locator(`[data-action=route-edit][data-id="${fixture.route}"]`).first().click()
  for (const id of [fixture.current, fixture.other]) await expect(page.locator(`#rf-service option[value="${id}"]`)).toContainText(fixture.name)
  await page.locator('#rf-service').selectOption(fixture.other)
  await page.locator('[data-action=route-apply]').click()
  await page.locator('[data-action=save]').first().click()
  await page.reload()
  await page.locator(`[data-action=route-edit][data-id="${fixture.route}"]`).first().click()
  await expect(page.locator('#rf-service')).toHaveValue(fixture.other)
  const saved = await page.evaluate(({ domain, route }) => {
    const db = JSON.parse(localStorage.getItem('network-service-composer-vue-v1.3')!)
    const d = db.domains.find((d: { id: string }) => d.id === domain)
    return { active: d.active.routes.find((r: { id: string }) => r.id === route).service, draft: d.draft.config.routes.find((r: { id: string }) => r.id === route).service }
  }, fixture)
  expect(saved).toEqual({ active: fixture.current, draft: fixture.other })
  expect(requests).toEqual([])
})
test('demo: original create/save flows persist locally with zero business API requests', async ({ page }) => {
  const requests: string[] = []
  page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) requests.push(request.url()) })
  await page.goto('/projects')
  await page.locator('[data-action=p-project-add]').click()
  // Wait for the archive's delayed initial focus before typing into a second
  // field; otherwise that focus can redirect Playwright's keyboard insertion.
  await expect(page.locator('#p-name')).toBeFocused()
  await page.locator('#p-name').fill('Demo literal 删除 {name}')
  await page.locator('#p-key').fill('demo-literal')
  await page.locator('[data-action=p-project-create]').click()
  await expect(page.locator('#main h1')).toHaveText('Demo literal 删除 {name}')
  await page.reload()
  await expect(page.locator('#main h1')).toHaveText('Demo literal 删除 {name}')
  await expect(page.locator('[data-action=x-demo]')).toBeVisible()
  expect(requests).toEqual([])
  expect(await page.evaluate(() => localStorage.getItem('nsc.preferences'))).toBeNull()
})
test('demo: eight languages, literal form data, theme, keyboard and mobile layout', async ({ page }) => {
  await page.goto('/projects')
  for (const { code } of locales) {
    await page.locator('#workspace-language').selectOption(code)
    await expect(page.locator('html')).toHaveAttribute('lang', code)
    await page.reload()
    await expect(page.locator('#workspace-language')).toHaveValue(code)
    await page.locator('[data-action=p-project-add]').click()
    await expect(page.locator('#p-name')).toBeFocused()
    await page.locator('#p-name').fill('删除 {name} Docker $HOME')
    const runtime = await page.locator('#p-runtime option').nth(2).getAttribute('value')
    await page.locator('#p-runtime').selectOption(runtime!)
    const title = await page.locator('#modal-title').textContent()
    await page.locator('#workspace-language').selectOption(code === 'en' ? 'zh-CN' : 'en', { force: true })
    await expect(page.locator('#modal-title')).not.toHaveText(title!)
    await expect(page.locator('#p-name')).toHaveValue('删除 {name} Docker $HOME')
    await expect(page.locator('#p-runtime')).toHaveValue(runtime!)
    await page.keyboard.press('Escape')
    await expect(page.locator('#modal-title')).not.toBeVisible()
  }
  await page.locator('#workspace-theme').selectOption('dark')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.keyboard.press('Control+k')
  await expect(page.locator('#palette-input')).toBeVisible()
  await page.keyboard.press('Escape')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.locator('[data-action=menu]').first().click()
  await expect(page.locator('.sidebar')).toHaveClass(/open/)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.locator('[data-action=menu]').first().click()
  await page.locator('[data-action=p-project-add]').click();await expect(page.locator('#p-name')).toBeFocused()
  await page.locator('#p-description').focus();await page.keyboard.press('Tab');await expect(page.locator('#p-runtime')).toBeFocused()
  await page.keyboard.press('ArrowDown');await expect(page.locator('#p-runtime')).not.toHaveValue('')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: test.info().outputPath('mobile-project-runtime-selector.png'), fullPage: true })
  await page.keyboard.press('Escape')
})

for (const scenario of ['success', 'partial', 'unknown']) {
  test(`demo: original Compose deployment and ${scenario} outcome persist without business API traffic`, async ({ page }) => {
    const requests: string[] = [], errors: string[] = []
    page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) requests.push(request.url()) })
    page.on('pageerror', error => errors.push(error.message))
    await page.goto('/projects/portal/environments/prod/applications/app-portal-prod/compose')
    if (scenario !== 'success') {
      await page.locator('[data-action=x-demo]').click()
      await page.locator('#x-demo-scenario').selectOption(scenario)
      await page.locator('[data-action=x-demo-save]').click()
    }
    const editor = page.locator('#y-compose-code')
    await editor.fill(await editor.inputValue() + '\n# retained literal 删除 {name}\n')
    await page.locator('[data-action=y-compose-save]').click()
    await page.locator('[data-action=y-compose-review]').click()
    await page.locator('#y-review-ack').check()
    await page.locator('[data-action=y-compose-deploy]').click()
    await expect(page).toHaveURL(/\/operations\//)
    await page.waitForFunction(expected => JSON.parse(localStorage.getItem('network-service-composer-vue-v1.3') || '{}').appDeployments?.[0]?.state === expected, scenario)
    const original = await page.evaluate(() => JSON.parse(localStorage.getItem('network-service-composer-vue-v1.3')!).appDeployments[0])
    if (scenario === 'partial') await page.locator('[data-action=x-retry]').click()
    if (scenario === 'unknown') {
      await page.locator('[data-action=x-verify]').click()
      await page.locator('[data-action=x-verify-result][data-result=success]').click()
    }
    await page.waitForFunction(() => JSON.parse(localStorage.getItem('network-service-composer-vue-v1.3') || '{}').appDeployments?.[0]?.state === 'success')
    await page.reload()
    await expect(page.locator('#main')).toContainText(original.id)
    const saved = await page.evaluate(() => JSON.parse(localStorage.getItem('network-service-composer-vue-v1.3')!).appDeployments[0])
    expect(saved.id).toBe(original.id)
    expect(saved.state).toBe('success')
    expect(saved.attempts).toHaveLength(scenario === 'partial' ? 2 : 1)
    if (scenario === 'partial') expect(saved.attempts[1].targets).toHaveLength(1)
    expect(requests).toEqual([])
    expect(errors).toEqual([])
  })
}

test('demo: project settings and forced deletion persist without any business API request', async ({ page }) => {
  const writes: string[] = [];page.on('request', r => { if (new URL(r.url()).pathname.startsWith('/api/')) writes.push(r.url()) })
  await page.goto('/projects');await page.locator('#workspace-language').selectOption('en')
  await page.locator('[data-action=p-project-add]').click();await page.locator('#p-name').fill('Delete demo');await page.locator('#p-key').fill('delete-demo')
  const runtime = await page.locator('#p-runtime option').nth(2).getAttribute('value')
  await page.locator('#p-runtime').selectOption(runtime!);await page.locator('[data-action=p-project-create]').click()
  await expect(page).toHaveURL(/\/projects\/[^/]+$/)
  const id = new URL(page.url()).pathname.split('/')[2]!
  await page.goto('/projects');await page.locator(`[data-action=p-project-settings][data-id="${id}"]`).click()
  const dialog = page.getByTestId('project-settings-dialog')
  await expect(dialog.locator('#project-settings-runtime')).toHaveValue(runtime!)
  await dialog.locator('#project-settings-description').fill('Demo-only metadata');await dialog.locator('button[type=submit]').click();await expect(dialog.locator('button[type=submit]')).toBeDisabled()
  await page.keyboard.press('Escape');await page.reload();await page.locator(`[data-action=p-project-settings][data-id="${id}"]`).click()
  await expect(dialog.locator('#project-settings-description')).toHaveValue('Demo-only metadata');await dialog.locator('input[type=checkbox]').check()
  await expect(dialog.locator('#project-settings-runtime')).toHaveValue(runtime!)
  await dialog.getByRole('button', { name: 'Preview deletion', exact: true }).click();await dialog.locator('#project-delete-confirm').fill('delete-demo');await dialog.locator('.modal-foot .danger').click()
  await expect(dialog).not.toBeVisible();await page.reload();await expect(page.locator(`[data-action=p-project-settings][data-id="${id}"]`)).toHaveCount(0)
  expect(writes).toEqual([])
})

test('demo: node health is distinct from Swarm state and stale host samples stay unavailable', async ({ page }) => {
  const requests: string[] = []
  page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) requests.push(request.url()) })
  await page.clock.setFixedTime(new Date('2026-09-16T10:00:00Z'))
  await page.goto('/projects')
  await page.locator('[data-action=x-demo]').click()
  await page.locator('[data-action=x-demo-save]').click()
  const runtime = await page.evaluate(() => {
    const key = 'network-service-composer-vue-v1.3'
    const db = JSON.parse(localStorage.getItem(key)!)
    const runtime = db.runtimes.find((r: { nodes: unknown[] }) => r.nodes.length >= 2)
    runtime.connection = 'online'
    const current = runtime.nodes[0], old = runtime.nodes[1]
    current.name = 'host <literal> & name'
    current.host = { at: '2026-09-16T10:00:00Z', cpu_percent: 0, memory_used: 0, memory_total: 8589934592, disks: [{ mount: '/data/<b>literal</b>', used: 1073741824, total: 8589934592, read_only: false }], errors: {}, stale: false }
    current.controller = { at: current.host.at, instance_id: 'controller-literal', controller: 'healthy', role: 'follower', collector: 'degraded', gateway: 'ready', stale: false }
    old.host = { ...current.host, at: '2026-09-16T09:58:00Z' }
    old.controller = { ...current.controller, instance_id: 'expired-controller', at: old.host.at }
    localStorage.setItem(key, JSON.stringify(db))
    return { id: runtime.id, current: current.id, old: old.id }
  })
  await page.goto(`/infrastructure/runtimes/${runtime.id}?tab=nodes`)
  const row = page.locator(`[data-action=x-node][data-id="${runtime.current}"]`).locator('xpath=ancestor::tr')
  const old = page.locator(`[data-action=x-node][data-id="${runtime.old}"]`).locator('xpath=ancestor::tr')
  await expect(row.locator('td')).toHaveCount(11)
  await expect(row.locator('td').nth(4)).toHaveText(/^0%/)
  await expect(row.locator('td').nth(5)).toHaveText('0 / 8 GiB')
  await expect(row.locator('td').nth(7)).toContainText('controller-literal')
  await expect(row.locator('td').nth(10)).toContainText('/data/<b>literal</b>')
  await expect(row.locator('td').nth(10).locator('b')).toHaveCount(0)
  await expect(old).not.toContainText('expired-controller')
  await expect(old.locator('td').nth(4)).not.toContainText('%')
  await expect(old.locator('td').nth(5)).not.toContainText('GiB')
  for (const { code } of locales) {
    await page.locator('#workspace-language').selectOption(code)
    await expect(row).toContainText('host <literal> & name')
    await expect(row.locator('td').nth(7)).toContainText('controller-literal')
    expect((await page.locator('#main th').allTextContents()).join(' ')).not.toContain('nodeHealth.')
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.locator('#main .table-scroll')).toBeVisible()
  await row.locator('[data-action=x-node]').click()
  await expect(page).toHaveURL(new RegExp(`/nodes/${runtime.current}`))
  await page.goBack()
  await expect(row.locator('td').nth(4)).toHaveText(/^0%/)
  await page.reload()
  await expect(row.locator('td').nth(7)).toContainText('controller-literal')
  expect(requests).toEqual([])
})
