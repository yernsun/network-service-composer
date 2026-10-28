import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import type { components } from '../../src/shared/api/schema'
type Entity = { id: string; revision: number; [key: string]: unknown }
type Fixture = { pool: Entity; runtime: Entity; image: string }
const fixturePath = process.env.NSC_WORKSPACE_FIXTURE
if (!fixturePath) throw new Error('Use scripts/e2e-workspace.py with an isolated Controller/runtime')
const fixture = JSON.parse(readFileSync(fixturePath, 'utf8')) as Fixture
const admin = process.env.E2E_ADMIN_PASSWORD!
async function api<T = Entity>(request: APIRequestContext, method: string, path: string, data?: unknown, extra: Record<string, string> = {}): Promise<T> {
  const options = { method, headers: { Authorization: `Bearer ${admin}`, 'Content-Type': 'application/json', ...extra }, ...(data === undefined ? {} : { data }) }
  let response = await request.fetch(`/api/v1${path}`, options)
  // Fixture seeding may exceed the normal Controller request budget. A 429
  // explicitly rejects the request; retain the same body/key and respect its delay.
  for (let retry = 0; response.status() === 429 && retry < 3; retry++) {
    await new Promise(resolve => setTimeout(resolve, Math.min(60, Math.max(1, Number(response.headers()['retry-after']) || 1)) * 1000))
    response = await request.fetch(`/api/v1${path}`, options)
  }
  expect(response.ok(), `${method} ${path}: ${await response.text()}`).toBe(true)
  return (await response.json()).data as T
}
async function login(page: Page, username = 'admin', password = admin, timeout = 15000) {
  await page.goto('/login')
  await page.locator('#username').fill(username)
  await page.locator('#password').fill(password)
  await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects$/, { timeout })
  await expect(page.locator('#main h1')).toBeVisible({ timeout })
}
async function scope(request: APIRequestContext, prefix: string) {
  const slug = `${prefix}-${Date.now()}`
  const project = await api(request, 'POST', '/projects', { name: slug, slug })
  const environment = await api(request, 'POST', `/projects/${project.id}/environments`, { name: 'qa', runtime_id: fixture.runtime.id })
  return { project, environment, base: `/projects/${project.id}/environments/${environment.id}` }
}
async function addVariable(page: Page, key: string, value: string) {
  await page.locator('[data-action=y-var-add]').click()
  await expect(page.locator('#y-key')).toBeFocused()
  await page.locator('#y-key').fill(key)
  await page.locator('#y-value').fill(value)
  await page.locator('[data-action=y-var-apply]').click()
  await expect(page.locator('#modal-title')).not.toBeVisible()
}
const errors = new Map<Page, string[]>()
test.beforeEach(async ({ page }) => {
  errors.set(page, [])
  page.on('pageerror', error => errors.get(page)!.push(error.message))
})
test.afterEach(async ({ page }) => { expect(errors.get(page)).toEqual([]) })

test('certificates: all paginated assets appear and automatic issuance updates a filtered page', async ({ page }) => {
  let signed = false
  const reads: string[] = []
  // Only certificate metadata is substituted. Authentication and the workspace
  // directory use the isolated Controller. This is UI acceptance, not a CA trial.
  await page.route(/\/api\/v1\/certificates(?:\?.*)?$/, async route => {
    const cursor = new URL(route.request().url()).searchParams.get('cursor')
    reads.push(route.request().method())
    const metadata = { created_at: '2026-10-08T00:00:00Z', updated_at: '2026-10-08T00:00:00Z' }
    const certificate: components['schemas']['Certificate'] = cursor
      ? { ...metadata, id: 'cert-imported-fixture', pool_id: String(fixture.pool.id), revision: 1, provider: 'custom', status: 'ACTIVE', primary_domain: '*.legacy.example.test', domains: ['*.legacy.example.test', 'legacy.example.test'] }
      : { ...metadata, id: 'cert-acme-fixture', pool_id: String(fixture.pool.id), revision: signed ? 2 : 1, provider: 'acme', acme_challenge: 'dns-01', status: signed ? 'ACTIVE' : 'PENDING', primary_domain: 'managed.example.test', domains: signed ? ['managed.example.test', 'signed.example.test'] : ['managed.example.test'], ...(signed ? { not_after: '2099-05-01T00:00:00Z', fingerprint: 'signed-fixture' } : {}) }
    await route.fulfill({ status: 200, json: { data: [certificate], pagination: { limit: 200, next_cursor: cursor ? '' : 'certificate-page-2' } } })
  })
  await login(page)
  const path = '/projects/all/environments/all/certificates?perspective=ops'
  await page.goto(path)
  const automatic = page.locator('[data-certificate-id=cert-acme-fixture]')
  await expect(page.locator('[data-certificate-id]')).toHaveCount(2)
  await expect(automatic.locator('.badge')).toHaveText('PENDING')
  await expect(page.locator('[data-certificate-id=cert-imported-fixture]')).toContainText('*.legacy.example.test')
  const filter = page.locator('#certificate-domain-filter')
  await filter.fill(' EXAMPLE.TEST ')
  signed = true
  await expect(automatic.locator('.badge')).toHaveText('ACTIVE', { timeout: 15000 })
  await expect(automatic).toContainText('signed.example.test')
  await expect(automatic).toContainText('2099')
  await expect(filter).toHaveValue(' EXAMPLE.TEST ')
  await expect(filter).toBeFocused()
  await expect(page).toHaveURL(new RegExp(`${path.replace('?', '\\?')}$`))
  await filter.fill('one.legacy.example.test')
  await expect(page.locator('[data-certificate-id]')).toHaveCount(1)
  await expect(page.locator('[data-certificate-id=cert-imported-fixture]')).toBeVisible()
  await filter.fill('two.one.legacy.example.test')
  await expect(page.locator('[data-certificate-id]')).toHaveCount(0)
  await filter.clear(); await expect(page.locator('[data-certificate-id]')).toHaveCount(2)
  expect(reads.length).toBeGreaterThanOrEqual(6)
  expect(reads.every(method => method === 'GET')).toBe(true)
})

test('refresh: navigation waits for fresh data, current links reread and failed reads retain the page', async ({ page, request }) => {
  const target = await scope(request, 'refresh-navigation')
  await login(page)
  const projectPath = `/projects/${target.project.id}`
  const directory = /\/api\/v1\/projects(?:\?.*)?$/
  const rename = async (name: string) => {
    const current = await api(request, 'GET', projectPath)
    await api(request, 'PATCH', projectPath, { name }, { 'If-Match': `"rev-${current.revision}"` })
  }
  await rename('Fresh project name')
  let release!: () => void, started!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  const reading = new Promise<void>(resolve => { started = resolve })
  await page.route(directory, async route => { started(); await gate; await route.continue() })
  try {
    await page.locator(`article[data-action=p-project][data-id="${target.project.id}"]`).click()
    await reading
    await expect(page.locator('.workspace-loading')).toBeVisible()
    await expect(page).toHaveURL(/\/projects$/)
    await expect(page.locator(`article[data-id="${target.project.id}"] h2`)).toHaveText(String(target.project.name))
  } finally { release() }
  await expect(page).toHaveURL(new RegExp(`${projectPath}$`))
  await expect(page.locator('#main h1')).toHaveText('Fresh project name')
  await page.unroute(directory)
  await rename('Refreshed current project')
  const reread = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/projects' && r.request().method() === 'GET')
  await page.locator('.sidebar [data-page=project]').click()
  expect((await reread).ok()).toBe(true)
  await expect(page.locator('#main h1')).toHaveText('Refreshed current project')
  await page.route(directory, route => route.fulfill({ status: 503, json: { error: { code: 'unavailable' } } }))
  await page.locator('.sidebar [data-page=environments]').click()
  await expect(page.locator('.toast').last()).toBeVisible()
  await expect(page.locator('.workspace-loading')).not.toBeVisible()
  await expect(page).toHaveURL(new RegExp(`${projectPath}$`))
  await expect(page.locator('#main h1')).toHaveText('Refreshed current project')
  await page.unroute(directory)
  await page.locator('.sidebar [data-page=environments]').click()
  await expect(page).toHaveURL(new RegExp(`${projectPath}/environments$`))
})

test('refresh: saved variables trigger API reads and a failed refresh retries without another write', async ({ page, request }) => {
  const target = await scope(request, 'refresh-save')
  const path = `/projects/${target.project.id}/variables`
  await login(page)
  await page.goto(path)
  await page.evaluate(() => { document.documentElement.dataset.refreshSession = 'keep this document' })
  const session = await page.locator('html').getAttribute('data-refresh-session')
  await addVariable(page, 'SAVED', 'kept after refresh failure')
  await expect(page).toHaveURL(/\?layer=project$/)
  await expect(page.locator('.workspace-loading')).not.toBeVisible()
  const writes: string[] = [], reads: string[] = []
  page.on('request', r => {
    if (new URL(r.url()).pathname !== '/api/v1' + path) return
    if (r.method() === 'PUT') writes.push(r.url())
    if (r.method() === 'GET') reads.push(r.url())
  })
  await page.route('**/api/v1' + path, route => route.request().method() === 'GET'
    ? route.fulfill({ status: 503, json: { error: { code: 'unavailable' } } }) : route.continue())
  await page.locator('[data-action=y-vars-save]').click()
  await expect(page.locator('.workspace-refresh-notice')).toBeVisible()
  await expect(page.locator('#main')).toContainText('kept after refresh failure')
  await expect(page.locator('[data-action=y-vars-save]')).toBeDisabled()
  expect(writes).toHaveLength(1)
  expect(reads).toHaveLength(1)
  expect((await api<components['schemas']['LayerVariables']>(request, 'GET', path)).values[0]?.value).toBe('kept after refresh failure')
  await page.unroute('**/api/v1' + path)
  const retry = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1' + path && r.request().method() === 'GET')
  await page.locator('.workspace-refresh-notice button').click()
  expect((await retry).ok()).toBe(true)
  await expect(page.locator('.workspace-refresh-notice')).not.toBeVisible()
  await expect(page.locator('[data-action=y-vars-save]')).toBeDisabled()
  expect(writes).toHaveLength(1)
  expect(reads).toHaveLength(2)
  await expect(page.locator('html')).toHaveAttribute('data-refresh-session', session!)
})

test('refresh: rereading an edited variable page preserves its draft and conflict revision', async ({ page, request }) => {
  const target = await scope(request, 'refresh-draft')
  const path = `/environments/${target.environment.id}/variables`
  await login(page)
  await page.goto(target.base + '/variables?layer=environment')
  await addVariable(page, 'UNSAVED', 'preserved draft')
  const baseline = await api<components['schemas']['LayerVariables']>(request, 'GET', path)
  await api(request, 'PUT', path, { values: [{ key: 'REMOTE', value: 'concurrent edit' }] }, { 'If-Match': `"rev-${baseline.revision}"` })
  const refresh = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1' + path && r.request().method() === 'GET')
  await page.locator('.sidebar [data-page=variables]').click()
  expect((await refresh).ok()).toBe(true)
  await expect(page.locator('.workspace-loading')).not.toBeVisible()
  await expect(page.locator('#main')).toContainText('preserved draft')
  await expect(page.locator('[data-action=y-vars-save]')).toBeEnabled()
  const save = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1' + path && r.request().method() === 'PUT')
  await page.locator('[data-action=y-vars-save]').click()
  const response = await save
  expect(response.request().headers()['if-match']).toBe(`"rev-${baseline.revision}"`)
  expect(response.status()).toBe(412)
  await expect(page.locator('#main')).toContainText('preserved draft')
  expect((await api<components['schemas']['LayerVariables']>(request, 'GET', path)).values).toEqual([expect.objectContaining({ key: 'REMOTE', value: 'concurrent edit' })])
})

test('refresh: a real deployment outcome updates the service list and keeps an open form intact', async ({ page, request }) => {
  const target = await scope(request, 'refresh-deploy')
  await login(page)
  await page.goto(target.base + '/applications')
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  // Delay browser observation, not execution, until another page has an editor.
  await page.route(/\/api\/v1\/operations\/[^/]+$/, async route => { await gate; await route.continue() })
  try {
    await page.locator('[data-action=x-install]').first().click()
    await page.locator('[data-action=x-wsource][data-id=file]').click()
    await page.locator('[data-wf=name]').fill(String(target.environment.name))
    await page.locator('[data-wf=key]').fill(String(target.environment.project_name))
    await page.locator('[data-wf=raw]').fill(`services:\n  web:\n    image: ${fixture.image}\n    expose:\n      - "80"\n`)
    await page.locator('[data-action=x-wnext]').click()
    await page.locator('[data-action=x-wnext]').click()
    const preflight = page.waitForResponse(r => r.url().endsWith(`/environments/${target.environment.id}/preflight`))
    await page.locator('[data-action=x-wnext]').click()
    expect((await preflight).ok()).toBe(true)
    await page.locator('[data-wf=reviewAck]').check()
    await page.locator('[data-wf=warningAck]').check()
    const submitted = page.waitForResponse(r => r.url().endsWith(`/environments/${target.environment.id}/deployments`) && r.request().method() === 'POST')
    await page.locator('[data-action=x-wdeploy]').click()
    const response = await submitted
    expect(response.ok(), await response.text()).toBe(true)
    const operation = (await response.json()).data as Entity
    await expect(page).toHaveURL(new RegExp(`/operations/${operation.id}$`))
    await page.locator('.sidebar [data-page=services]').click()
    await expect(page).toHaveURL(new RegExp(`${target.base}/services(?:\\?|$)`))
    await expect(page.locator('.workspace-loading')).not.toBeVisible()
    const directories: string[] = []
    page.on('request', r => { if (new URL(r.url()).pathname === '/api/v1/projects') directories.push(r.url()) })
    await page.locator('#x-service-search').fill('web')
    await page.locator('[data-action=p-env-settings]').first().click()
    await page.locator('#p-env-name').fill('Unsaved environment title')
    await expect(page).toHaveURL(/[?&]q=web(?:&|$)/)
    expect(directories).toEqual([])
    const baseline = await api(request, 'GET', `/environments/${target.environment.id}`)
    await api(request, 'PATCH', `/environments/${target.environment.id}`, { display_name: 'Concurrent environment title', kind: baseline.kind || 'custom', gateway_ids: baseline.gateway_ids || [] }, { 'If-Match': `"rev-${baseline.revision}"` })
    await expect.poll(async () => (await api(request, 'GET', `/operations/${operation.id}`)).status, { timeout: 90000 }).toBe('SUCCEEDED')
    const refreshed = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/services' && r.request().method() === 'GET')
    release()
    const freshServices = (await (await refreshed).json()).data as Entity[]
    const service = freshServices.find(s => s.environment && (s.environment as { id: string }).id === target.environment.id)
    expect(service).toBeTruthy()
    await expect(page.locator(`#main tr[data-service-id="${service!.id}"]`)).toBeVisible()
    await expect(page.locator(`#scope-env option[value="${target.environment.id}"]`)).toHaveText('Concurrent environment title')
    await expect(page.locator('#p-env-name')).toHaveValue('Unsaved environment title')
    await expect(page.locator('#p-env-name')).toBeFocused()
    await expect(page.locator('#x-service-search')).toHaveValue('web')
    await expect(page.locator('#modal-title')).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`${target.base}/services(?:\\?|$)`))
    const save = page.waitForResponse(r => r.url().endsWith(`/environments/${target.environment.id}`) && r.request().method() === 'PATCH')
    await page.locator('[data-action=p-env-save]').click()
    const conflict = await save
    expect(conflict.status()).toBe(412)
    expect(conflict.request().headers()['if-match']).toBe(`"rev-${baseline.revision}"`)
    await expect(page.locator('#p-env-name')).toHaveValue('Unsaved environment title')
  } finally { release() }
})

test('real Controller: project and environment creation persist, original section switching and deep links', async ({ page, request }) => {
  await login(page)
  await page.locator('[data-action=p-project-add]').click()
  await expect(page.locator('#p-name')).toBeFocused()
  const slug = `browser-${Date.now()}`, name = '删除 {name} literal'
  await page.locator('#p-name').fill(name)
  await page.locator('#p-key').fill(slug)
  await page.locator('[data-action=p-project-create]').click()
  await expect(page.locator('#modal-title')).not.toBeVisible()
  await expect(page.locator('#main h1')).toHaveText(name)
  const id = new URL(page.url()).pathname.split('/')[2]!
  await page.goto(`/projects/${id}/environments`)
  await page.locator('[data-action=p-env-add]').click()
  await expect(page.locator('#p-env-name')).toBeFocused()
  await page.locator('#p-env-name').fill('qa')
  await page.locator('#p-env-slug').fill('qa')
  await page.locator('[data-action=p-env-save]').click()
  await expect(page.locator('#modal-title')).not.toBeVisible()
  await page.reload()
  await expect(page.locator('#main h1')).toHaveText('qa')
  const environments = await api<Entity[]>(request, 'GET', `/projects/${id}/environments`)
  expect(environments.map(e => e.name).sort()).toEqual(['production', 'qa'])
  const qa = environments.find(e => e.name === 'qa')!, prod = environments.find(e => e.name === 'production')!
  for (const section of ['overview', 'applications', 'services', 'domains', 'routes', 'configs', 'certificates', 'workloadHistory']) {
    await page.goto(`/projects/${id}/environments/${qa.id}/${section}`)
    await page.locator(`.env-tabs [data-action=p-env][data-id="${prod.id}"]`).click()
    await expect(page).toHaveURL(new RegExp(`/${prod.id}/${section}(?:\\?|$)`))
    await expect(page.locator('#scope-env')).toHaveValue(prod.id)
    await page.locator('#scope-env').selectOption(qa.id)
    await expect(page).toHaveURL(new RegExp(`/${qa.id}/${section}(?:\\?|$)`))
  }
  await page.reload()
  await expect(page.locator('#scope-env')).toHaveValue(qa.id)
  const other = await scope(request, 'foreign')
  await page.goto(`/projects/${id}/environments/${other.environment.id}/applications`)
  await expect(page.locator('[data-action=x-install]')).toHaveCount(0)
  await expect(page.locator('#main')).toContainText(/不属于|not belong|不存在/)
})

test('real Controller: variables import, inheritance, saved state, conflicts and original leave confirmation', async ({ page, request }) => {
  const target = await scope(request, 'variables')
  await login(page)
  await page.goto(`/projects/${target.project.id}/variables`)
  await addVariable(page, 'LABEL', '删除 {name}')
  await page.locator('[data-action=y-vars-save]').click()
  await expect.poll(async () => (await api<components['schemas']['LayerVariables']>(request, 'GET', `/projects/${target.project.id}/variables`)).values[0]?.value).toBe('删除 {name}')
  await page.reload()
  await expect(page.locator('#main')).toContainText('删除 {name}')
  await page.goto(target.base + '/variables?layer=environment')
  await page.locator('[data-action=y-env-import]').click()
  await page.locator('#y-import').fill('LABEL=environment\nSECOND=kept')
  await page.locator('[data-action=y-import-analyze]').click()
  await page.locator('[data-action=y-import-apply]').click()
  await page.locator('[data-action=y-vars-save]').click()
  await expect.poll(async () => (await api<components['schemas']['LayerVariables']>(request, 'GET', `/environments/${target.environment.id}/variables`)).values.length).toBe(2)
  await page.reload()
  await expect(page.locator('#main')).toContainText('environment')
  await addVariable(page, 'UNSAVED', 'keep me')
  await page.locator('.sidebar [data-page=applications]').click()
  await expect(page.locator('[data-action=y-guard-stay]')).toBeVisible()
  await page.locator('[data-action=y-guard-stay]').click()
  await expect(page.locator('#main')).toContainText('keep me')
  const old = await api<components['schemas']['LayerVariables']>(request, 'GET', `/environments/${target.environment.id}/variables`)
  await api(request, 'PUT', `/environments/${target.environment.id}/variables`, { values: [{ key: 'REMOTE', value: 'concurrent' }] }, { 'If-Match': `"rev-${old.revision}"` })
  await page.locator('[data-action=y-vars-save]').click()
  await expect(page.locator('.toast').last()).toContainText(/冲突|变化|changed|conflict/i)
  await expect(page.locator('#main')).toContainText('keep me')
  expect((await api<components['schemas']['LayerVariables']>(request, 'GET', `/environments/${target.environment.id}/variables`)).values).toEqual([expect.objectContaining({ key: 'REMOTE', value: 'concurrent' })])
})

test('real executor: original install steps preflight, deploy, persist and managed restart', async ({ page, request }) => {
  const target = await scope(request, 'deploy')
  await login(page)
  await page.goto(target.base + '/applications')
  await page.locator('[data-action=x-install]').first().click()
  await page.locator('[data-action=x-wsource][data-id=file]').click()
  await page.locator('[data-wf=name]').fill(String(target.environment.name))
  await page.locator('[data-wf=key]').fill(String(target.environment.project_name))
  await expect(page.locator('[data-wf=name]')).toHaveValue(String(target.environment.name))
  await expect(page.locator('[data-wf=key]')).toHaveValue(String(target.environment.project_name))
  await page.locator('[data-wf=raw]').fill(`services:\n  web:\n    image: ${fixture.image}\n    expose:\n      - "80"\n`)
  await page.locator('[data-action=x-wnext]').click()
  await page.locator('[data-action=x-wnext]').click()
  const preflight = page.waitForResponse(r => r.url().endsWith(`/environments/${target.environment.id}/preflight`))
  await page.locator('[data-action=x-wnext]').click()
  const reviewed = await preflight
  expect(reviewed.ok(), await reviewed.text()).toBe(true)
  await page.locator('[data-wf=reviewAck]').check()
  await page.locator('[data-wf=warningAck]').check()
  const submission = page.waitForResponse(r => r.url().endsWith(`/environments/${target.environment.id}/deployments`) && r.request().method() === 'POST')
  await page.locator('[data-action=x-wdeploy]').click()
  const response = await submission
  expect(response.ok(), await response.text()).toBe(true)
  const operation = (await response.json()).data as Entity
  await expect.poll(async () => (await api(request, 'GET', `/operations/${operation.id}`)).status, { timeout: 90000 }).toBe('SUCCEEDED')
  await page.reload()
  await expect(page.locator('#main')).toContainText(operation.id)
  const environment = await api(request, 'GET', `/environments/${target.environment.id}`)
  expect(environment.application_id).toBeTruthy()
  const services = (await api<Entity[]>(request, 'GET', '/services')).filter(s => s.application_id === environment.application_id)
  expect(services).toHaveLength(1)
  await page.goto(target.base + `/services/${services[0]!.id}`)
  await page.locator('[data-action=x-op-form][data-kind=restart]').click()
  await page.locator('#x-op-value').fill('isolated browser acceptance')
  await page.locator('#x-op-ack').check()
  const restart = page.waitForResponse(r => r.url().endsWith(`/services/${services[0]!.id}/restart`) && r.request().method() === 'POST')
  await page.locator('[data-action=x-op-confirm]').click()
  const restarted = await restart
  expect(restarted.ok(), await restarted.text()).toBe(true)
  const next = (await restarted.json()).data as Entity
  await expect.poll(async () => (await api(request, 'GET', `/operations/${next.id}`)).status, { timeout: 90000 }).toBe('SUCCEEDED')
  await page.reload()
  await expect(page.locator('#main')).toContainText(next.id)

  const servicePath = target.base + `/services/${services[0]!.id}`
  const submitService = async (kind: string, endpoint: string, prepare: () => Promise<void>) => {
    await page.goto(servicePath)
    await page.locator(`[data-action=x-op-form][data-kind=${kind}]`).click()
    await prepare()
    await page.locator('#x-op-ack').check()
    const pending = page.waitForResponse(r => r.url().endsWith(`/services/${services[0]!.id}/${endpoint}`) && r.request().method() === 'POST')
    await page.locator('[data-action=x-op-confirm]').click()
    return pending
  }
  const temporaryResponse = await submitService('scale', 'scale', async () => {
    await expect(page.locator('input[name=x-op-persist][value=runtime]')).toBeChecked()
    await page.locator('#x-op-value').fill('2')
  })
  expect(temporaryResponse.ok(), await temporaryResponse.text()).toBe(true)
  expect(temporaryResponse.request().postDataJSON()).toMatchObject({ replicas: 2, persist: false })
  const temporaryOperation = (await temporaryResponse.json()).data as Entity
  await expect.poll(async () => (await api(request, 'GET', `/operations/${temporaryOperation.id}`)).status, { timeout: 90000 }).toBe('SUCCEEDED')
  await expect.poll(async () => (await api(request, 'GET', `/services/${services[0]!.id}`)).desired_instances, { timeout: 30000 }).toBe(2)

  const persistedResponse = await submitService('stop', 'scale', async () => {
    await page.locator('input[name=x-op-persist][value=source]').check()
  })
  expect(persistedResponse.ok(), await persistedResponse.text()).toBe(true)
  expect(persistedResponse.request().postDataJSON()).toMatchObject({ replicas: 0, persist: true })
  const persistedOperation = (await persistedResponse.json()).data as Entity
  await expect.poll(async () => (await api(request, 'GET', `/operations/${persistedOperation.id}`)).status, { timeout: 90000 }).toBe('SUCCEEDED')
  await expect.poll(async () => (await api(request, 'GET', `/services/${services[0]!.id}`)).desired_instances, { timeout: 30000 }).toBe(0)
  const source = await api<{ compose_yaml: string }>(request, 'GET', `/applications/${String(environment.application_id)}/source`)
  expect(source.compose_yaml).toContain('replicas: 0')

  const removalResponse = await submitService('remove_runtime', 'operations', async () => {})
  expect(removalResponse.ok(), await removalResponse.text()).toBe(true)
  expect(removalResponse.request().postDataJSON()).toMatchObject({ action: 'remove_runtime' })
  const removalOperation = (await removalResponse.json()).data as Entity
  await expect.poll(async () => (await api(request, 'GET', `/operations/${removalOperation.id}`)).status, { timeout: 90000 }).toBe('SUCCEEDED')
  await page.goto(servicePath)
  await expect(page.locator('#main .callout')).toContainText('runtime instance was removed')
  await expect(page.locator(`#main [data-action=x-app][data-id="${String(environment.application_id)}"]`)).toBeVisible()
  await page.locator('[data-action=x-op-form][data-kind=scale]').click()
  await expect(page.locator('input[name=x-op-persist][value=runtime]')).toBeDisabled()
  await expect(page.locator('input[name=x-op-persist][value=source]')).toBeChecked()
})

test('real Controller: environment display metadata persists without changing its technical identity', async ({ page, request }) => {
  const target = await scope(request, 'gap')
  await login(page)
  await page.goto(target.base + '/overview')
  await page.locator('[data-action=p-env-settings]').first().click()
  await page.locator('#p-env-name').fill('Do not lose this input')
  await page.locator('[data-action=p-env-save]').click()
  await expect(page.locator('#modal-title')).not.toBeVisible()
  await page.reload()
  await expect(page.locator('#main h1')).toHaveText('Do not lose this input')
  const saved = await api(request, 'GET', `/environments/${target.environment.id}`)
  expect(saved.name).toBe('qa')
  expect(saved.display_name).toBe('Do not lose this input')
  expect(saved.project_name).toBe(target.environment.project_name)

})

test('real authentication: logout and expired sessions stay outside the original workspace', async ({ page, context }) => {
  await login(page)
  await page.goto('/logout')
  await expect(page).toHaveURL(/\/login/)
  await expect(page.locator('#username')).toBeVisible()
  await login(page)
  await context.clearCookies()
  await page.reload()
  await expect(page).toHaveURL(/\/login/)
  await expect(page.locator('.sidebar')).toHaveCount(0)
})

test('real authorization: viewer can browse and original submission reports read-only without a write', async ({ page, request }) => {
  const username = `viewer-${Date.now()}`, password = `isolated-viewer-${Date.now()}`
  await api(request, 'POST', '/users', { username, password, role: 'viewer', enabled: true }, { 'X-NSC-Reauthenticate': admin })
  const writes: string[] = []
  await login(page, username, password)
  page.on('request', r => { if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/projects') writes.push(r.url()) })
  await page.locator('[data-action=p-project-add]').click()
  await expect(page.locator('#p-name')).toBeFocused()
  await page.locator('#p-name').fill('Viewer input retained')
  await page.locator('#p-key').fill('viewer-cannot-create')
  await page.locator('[data-action=p-project-create]').click()
  await expect(page.locator('#p-form-error')).toContainText(/read.only|只读/i)
  await expect(page.locator('#p-name')).toHaveValue('Viewer input retained')
  expect(writes).toEqual([])
  expect((await api<Entity[]>(request, 'GET', '/projects')).some(p => p.slug === 'viewer-cannot-create')).toBe(false)
})

test('real Controller: existing domain routes load in the original editor and unsupported drafts retain input at submit', async ({ page, request }) => {
  const services = await api<Entity[]>(request, 'GET', '/services')
  const service = services.find(s => s.managed && s.application_id)
  expect(service, 'The preceding isolated deployment must produce a managed service').toBeTruthy()
  const application = await api(request, 'GET', `/applications/${service!.application_id}`)
  const projects = await api<Entity[]>(request, 'GET', '/projects')
  let environment: Entity | undefined
  for (const project of projects) {
    environment = (await api<Entity[]>(request, 'GET', `/projects/${project.id}/environments`)).find(e => e.application_id === application.id)
    if (environment) break
  }
  expect(environment).toBeTruthy()
  // A managed binding attaches the service through NSC's existing operation.
  // Applications cannot claim the protected ingress network in Compose.
  const binding = await api<{ operation: Entity }>(request, 'POST', `/services/${service!.id}/domain-bindings`, {
    domain: { host: 'original-workspace.example.test', entrypoints: ['http'], tls: { mode: 'disabled' } },
    route: { path: '/', backend: { port: 80, protocol: 'http' } },
  })
  await expect.poll(async () => (await api(request, 'GET', `/operations/${binding.operation.id}`)).status, { timeout: 90000 }).toBe('SUCCEEDED')
  const domain = (await api<Entity[]>(request, 'GET', '/domains')).find(d => d.host === 'original-workspace.example.test')!
  await expect.poll(async () => (await api(request, 'GET', `/services/${service!.id}`)).dns_status, { timeout: 30000 }).toBe('READY')
  const route = (await api<Entity[]>(request, 'GET', '/routes')).find(r => r.domain_id === domain.id)!
  await login(page)
  const base = `/projects/${environment!.project_id}/environments/${environment!.id}`
  const name = String(service!.name)
  expect(name).not.toBe(service!.id)
  for (const path of [`${base}/routes`, `${base}/domains/${domain.id}?tab=overview`, `/projects/${environment!.project_id}/environments/all/routes`, '/projects/all/environments/all/routes']) {
    await page.goto(path)
    const reference = page.locator(`#main [data-action=service-detail][data-id="${service!.id}"]`).filter({ hasText: name }).first()
    await expect(reference).toBeVisible()
    await expect(reference).not.toContainText(service!.id)
    await reference.click()
    await expect(page).toHaveURL(new RegExp(`/services/${service!.id}(?:\\?|$)`))
    await expect(page.locator('#main h1')).toHaveText(name)
    await page.goBack()
    await expect(reference).toBeVisible()
    if (path === `${base}/routes`) {
      await page.locator(`[data-action=route-edit][data-id="${route.id}"]`).first().click()
      await expect(page.locator('#rf-service')).toHaveValue(service!.id)
      await expect(page.locator(`#rf-service option[value="${service!.id}"]`)).toContainText(name)
      await page.keyboard.press('Escape')
      await page.goto(path)
      await page.locator(`[data-action=open-domain][data-id="${domain.id}"]`).first().click()
      await expect(page.locator('#main .flow-node[data-action=service-detail]').filter({ hasText: name })).toBeVisible()
    }
  }
  await page.goto(`${base}/domains/${domain.id}?tab=routes`)
  await page.locator('#route-search').fill(name)
  await expect(page.locator(`#main [data-action=service-detail][data-id="${service!.id}"]`)).toContainText(name)
  await page.locator('#route-search').fill(service!.id)
  await expect(page.locator(`#main [data-action=service-detail][data-id="${service!.id}"]`)).toContainText(name)
  await page.locator('#route-search').fill('')
  await page.locator('[data-action=diagnose]').first().click()
  await page.locator('#diag-source').selectOption('target')
  await page.locator('#diag-url').fill(`http://${domain.host}/`)
  const traced = page.waitForResponse(r => r.url().includes(`/gateway-pools/${fixture.pool.id}/route-trace?`))
  await page.locator('[data-action=run-diag]').click()
  expect((await traced).ok()).toBe(true)
  await expect(page.locator('#overlay')).toContainText(/Route matched/)
  await expect(page.locator('#overlay')).toContainText(/does not verify an upstream response/)
  await expect(page.locator('#overlay')).toContainText(name)
  await page.locator('[data-action=close]').first().click()
  await page.locator(`[data-action=route-edit][data-id="${route.id}"]`).first().click()
  await expect(page.locator('#rf-path')).toBeFocused()
  await expect(page.locator('#rf-service')).toHaveValue(service!.id)
  await expect(page.locator(`#rf-service option[value="${service!.id}"]`)).toContainText(name)
  await page.locator('#rf-path').fill('/retained/')
  await page.locator('#rf-timeout').fill('30')
  await page.locator('#rf-connect').fill('5')
  await page.locator('[data-action=route-apply]').click()
  await expect(page.locator('#modal-title')).not.toBeVisible()
  await page.locator('[data-action=save]').first().click()
  await expect(page.locator('.toast').last()).toContainText(/not support|unavailable|cannot save|暂不支持|无法/i)
  await expect(page.locator('#main')).toContainText('/retained/')
  expect((await api(request, 'GET', `/routes/${route.id}`)).path).toBe('/')
  expect((await api(request, 'GET', `/routes/${route.id}`)).backend).toMatchObject({ service_id: service!.id })
})

test('real locale preference: all eight languages persist while resource names stay literal', async ({ page, request }) => {
  const target = await scope(request, 'locale')
  await login(page)
  for (const locale of ['ja', 'de', 'fr', 'ru', 'es', 'zh-CN', 'zh-TW', 'en']) {
    const saved = page.waitForResponse(r => r.url().endsWith('/api/v1/locale') && r.request().method() === 'POST')
    await page.locator('#workspace-language').selectOption(locale)
    expect((await saved).ok()).toBe(true)
    await page.goto(target.base + '/overview')
    await expect(page.locator('#workspace-language')).toHaveValue(locale)
    await expect(page.locator('#main h1')).toHaveText('qa')
  }
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('nsc.preferences') || '{}').language)).toBeUndefined()
})

test('real infrastructure: node and instance navigation, historical inventory and unavailable logs', async ({ page, request }) => {
  const nodes = await api<Entity[]>(request, 'GET', `/runtimes/${fixture.runtime.id}/cluster/nodes`)
  expect(nodes.length).toBeGreaterThan(0)
  const node = nodes[0]!
  const instances = await api<Entity[]>(request, 'GET', `/runtimes/${fixture.runtime.id}/cluster/nodes/${node.id}/instances?history=true&limit=200`)
  const instance = instances.find(i => i.container_id)
  expect(instance).toBeTruthy()
  await login(page)
  await page.goto(`/infrastructure/runtimes/${fixture.runtime.id}/nodes/${encodeURIComponent(node.id)}?tab=containers`)
  await expect(page.locator('#main h1')).toHaveText(String(node.name))
  await page.locator('#y-node-history').check()
  await expect(page.locator(`[data-action=y-instance][data-id="${instance!.id}"]`).first()).toBeVisible()
  await page.locator(`[data-action=y-instance][data-id="${instance!.id}"]`).first().click()
  await expect(page).toHaveURL(/\/instances\/.*kind=container/)
  await expect(page.locator('#main')).toContainText(String(instance!.container_id))
  await page.locator('[data-action=y-instance-tab][data-id=logs]').click()
  await expect(page.locator('#main')).toContainText(/unavailable|not collected|not readable|not available|未采集|不可用/i)
  await page.reload()
  await expect(page.locator('[data-action=y-instance-tab][data-id=logs]')).toHaveClass(/active/)
  await page.locator('[data-action=x-node]').first().click()
  await expect(page.locator('#main h1')).toHaveText(String(node.name))
})

test('real recovery: historical review and an environment revision conflict preserve the original recovery draft', async ({ page, request }, testInfo) => {
  let application = (await api<Entity[]>(request, 'GET', '/applications'))[0]!
  expect(application).toBeTruthy()
  let environment: Entity | undefined
  for (const project of await api<Entity[]>(request, 'GET', '/projects')) {
    environment = (await api<Entity[]>(request, 'GET', `/projects/${project.id}/environments`)).find(e => e.application_id === application.id)
    if (environment) break
  }
  expect(environment).toBeTruthy()
  // Prepare a second immutable revision through the real environment API.
  // The original UI offers recovery only for a historical revision.
  const current = await api(request, 'GET', `/environments/${environment!.id}`)
  const candidate = await api(request, 'POST', `/environments/${environment!.id}/preflight`, {
    compose_yaml: `services:\n  web:\n    image: ${fixture.image}\n    expose: ["80"]\n    environment:\n      RESTORATION_REVISION: "2"\n`,
  })
  const prepared = await api(request, 'POST', `/environments/${environment!.id}/deployments`, { preflight_id: candidate.preflight_id }, { 'If-Match': `"rev-${current.revision}"`, 'Idempotency-Key': `restore-fixture-${Date.now()}` })
  await expect.poll(async () => (await api(request, 'GET', `/operations/${prepared.id}`)).status, { timeout: 90000 }).toBe('SUCCEEDED')
  application = await api(request, 'GET', `/applications/${application.id}`)
  const revisions = await api<Entity[]>(request, 'GET', `/applications/${application.id}/revisions`)
  const historical = revisions.find(r => r.id !== application.active_revision_id)!
  expect(historical).toBeTruthy()
  await login(page)
  await page.goto(`/projects/${environment!.project_id}/environments/${environment!.id}/applications/${application.id}?tab=history`)
  const preview = page.waitForResponse(r => r.url().endsWith(`/revisions/${historical.id}/restore-preview`))
  await page.locator(`[data-action=x-restore][data-version="${historical.sequence}"]`).click()
  const reviewed = await preview
  expect(reviewed.ok(), await reviewed.text()).toBe(true)
  await page.locator('[data-action=x-restore-save]').click()
  await expect(page.locator('#modal-title')).not.toBeVisible()
  await page.locator('[data-action=x-restore-review]').click()
  await page.locator('#x-restore-ack').check()
  const submission = page.waitForResponse(r => r.url().endsWith(`/applications/${application.id}/restore`) && r.request().method() === 'POST')
  await page.locator('[data-action=x-restore-deploy]').click()
  const result = await submission
  // This is evidence of the documented backend boundary, not successful recovery.
  expect(result.status()).toBe(412)
  testInfo.annotations.push({ type: 'backend-gap', description: 'Environment-bound historical restore returns 412; successful real recovery remains blocked.' })
  await expect(page.locator('[data-action=x-restore-deploy]')).toBeVisible()
  await expect(page.locator('#x-restore-ack')).toBeChecked()
  await expect(page.locator('.toast').last()).toContainText(/changed|conflict|过期|变化|冲突/i)
  expect((await api(request, 'GET', `/applications/${application.id}`)).active_revision_id).toBe(application.active_revision_id)
})

test('controlled observation fault: unknown operation remains unknown and verification cannot simulate success', async ({ page, request }, testInfo) => {
  testInfo.annotations.push({ type: 'controlled-fault', description: 'Only operation GET status is replaced; resource identity and all other reads come from the isolated Controller.' })
  const operation = (await api<Entity[]>(request, 'GET', '/operations'))[0]!
  expect(operation).toBeTruthy()
  await page.route('**/api/v1/operations?*', async route => {
    const response = await route.fetch()
    const body = await response.json()
    body.data = body.data.map((row: Entity) => row.id === operation.id ? { ...row, status: 'UNKNOWN' } : row)
    await route.fulfill({ response, json: body })
  })
  await login(page)
  await page.goto(`/operations/${operation.id}`)
  await expect(page.locator('#main')).toContainText('unknown')
  const mutations: string[] = []
  page.on('request', r => { if (r.method() !== 'GET' && new URL(r.url()).pathname.startsWith('/api/v1/operations')) mutations.push(r.url()) })
  await page.locator('[data-action=x-verify]').click()
  await page.locator('[data-action=x-verify-result][data-result=success]').click()
  await expect(page.locator('.toast').last()).toContainText(/not support|unavailable|暂不支持/i)
  await expect(page.locator('[data-action=x-verify]')).toBeVisible()
  expect(mutations).toEqual([])
  expect((await api(request, 'GET', `/operations/${operation.id}`)).status).toBe(operation.status)
})

test('real preflight conflict: a changed variable revision prevents submission and preserves the original review', async ({ page, request }) => {
  const target = await scope(request, 'stale-review')
  await login(page)
  await page.goto(target.base + '/applications')
  await page.locator('[data-action=x-install]').first().click()
  await page.locator('[data-action=x-wsource][data-id=file]').click()
  await expect(page.locator('[data-wf=name]')).toBeFocused()
  await page.locator('[data-wf=name]').fill(String(target.environment.name))
  await page.locator('[data-wf=key]').fill(String(target.environment.project_name))
  await page.locator('[data-wf=raw]').fill(`services:\n  web:\n    image: ${fixture.image}\n    expose: ["80"]\n`)
  await page.locator('[data-action=x-wnext]').click()
  await page.locator('[data-action=x-wnext]').click()
  const reviewed = page.waitForResponse(r => r.url().endsWith(`/environments/${target.environment.id}/preflight`))
  await page.locator('[data-action=x-wnext]').click()
  expect((await reviewed).ok()).toBe(true)
  const variables = await api(request, 'GET', `/environments/${target.environment.id}/variables`)
  await api(request, 'PUT', `/environments/${target.environment.id}/variables`, { values: [{ key: 'CHANGED_AFTER_REVIEW', value: 'server-value' }] }, { 'If-Match': `"rev-${variables.revision}"` })
  await page.locator('[data-wf=reviewAck]').check()
  await page.locator('[data-wf=warningAck]').check()
  const submitted = page.waitForResponse(r => r.url().endsWith(`/environments/${target.environment.id}/deployments`) && r.request().method() === 'POST')
  await page.locator('[data-action=x-wdeploy]').click()
  expect((await submitted).status()).toBe(412)
  await expect(page.locator('#overlay')).toContainText(/changed|conflict|变化|冲突/i)
  await expect(page.locator('[data-action=x-wdeploy]')).toBeVisible()
  await page.locator('[data-action=x-wback]').click()
  await page.locator('[data-action=x-wback]').click()
  await page.locator('[data-action=x-wback]').click()
  await expect(page.locator('[data-wf=raw]')).toHaveValue(new RegExp(fixture.image.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
  expect((await api(request, 'GET', `/environments/${target.environment.id}`)).application_id).toBeFalsy()
})

test('real pagination and search: projects beyond the first API page remain reachable in the original workspace', async ({ page, request }) => {
  // Every reload reads complete paginated projects and their variables. Keep
  // the normal Controller rate limit and allow its documented retry delays.
  test.setTimeout(240000)
  const loading = { timeout: 60000 }
  const created: Entity[] = []
  const prefix = `pages-${Date.now()}`
  for (let n = 0; n < 201; n++) created.push(await api(request, 'POST', '/projects', { name: `${prefix}-${n}`, slug: `${prefix}-${n}` }))
  const firstPage = await api<Entity[]>(request, 'GET', '/projects?limit=200')
  const last = created.find(p => !firstPage.some(row => row.id === p.id))!
  expect(last, 'The target must be outside the first 200 results').toBeTruthy()
  // Original project routes require a concrete environment. Prepare that real
  // association without inventing an environment or changing the route guard.
  await api(request, 'POST', `/projects/${last.id}/environments`, { name: 'qa', runtime_id: fixture.runtime.id })
  const cursors: string[] = []
  page.on('request', r => {
    const url = new URL(r.url())
    if (url.pathname === '/api/v1/projects' && url.searchParams.has('cursor')) cursors.push(url.searchParams.get('cursor')!)
  })
  await login(page, 'admin', admin, loading.timeout)
  expect(cursors.length).toBeGreaterThan(0)
  await page.goto(`/projects/${last.id}`)
  await expect(page.locator('#main h1')).toHaveText(String(last.name), loading)
  await page.reload()
  await expect(page.locator('#main h1')).toHaveText(String(last.name), loading)
  // The original command palette searches resources within the selected scope.
  const service = (await api<Entity[]>(request, 'GET', '/services')).find(s => s.managed)!
  await page.goto('/projects/all/environments/all/services')
  await expect(page.locator('#main h1')).toBeVisible(loading)
  await page.keyboard.press('Control+k')
  await page.locator('#palette-input').fill(String(service.name))
  await page.locator(`#palette-results [data-action=service-detail][data-id="${service.id}"]`).click()
  await expect(page).toHaveURL(new RegExp(`/services/${service.id}(?:\\?|$)`))
  await expect(page.locator('#main')).toContainText(String(service.name))
})
