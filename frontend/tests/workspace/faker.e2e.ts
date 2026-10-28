import { test, expect } from '@playwright/test'

async function admin(page: import('@playwright/test').Page) {
  await page.goto('/login')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill('demo')
  await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects/)
  const session = await (await page.request.get('/api/v1/auth/session')).json()
  const headers = { 'X-CSRF-Token': session.data.csrf_token }
  await page.request.post('/api/v1/__faker/reset', { headers })
  await page.goto('/login')
  await page.locator('#password').fill('demo')
  await page.locator('button[type=submit]').click()
  const fresh = await (await page.request.get('/api/v1/auth/session')).json()
  return { 'X-CSRF-Token': fresh.data.csrf_token }
}

test('sidebar keeps one active section through detail refresh, browser history and account navigation', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  const headers = await admin(page)
  await page.route('**/api/v1/users*', route => route.fulfill({ status: 200, json: { data: [] } }))
  const reviewResponse = await page.request.post('/api/v1/environments/env_demo/preflight', { headers, data: { catalog_app_version_id: 'cat_demo_pair' } })
  expect(reviewResponse.ok()).toBe(true)
  const review = (await reviewResponse.json()).data
  const deployed = await page.request.post('/api/v1/environments/env_demo/deployments', {
    headers: { ...headers, 'If-Match': '"rev-1"', 'Idempotency-Key': 'sidebar-navigation-fixture' }, data: { preflight_id: review.preflight_id },
  })
  expect(deployed.ok()).toBe(true)
  const app = (await (await page.request.get('/api/v1/applications')).json()).data[0]
  const service = (await (await page.request.get('/api/v1/services')).json()).data[0]
  async function active(section: string) {
    const item = page.locator('.sidebar .nav-item.active')
    await expect(item).toHaveCount(1)
    await expect(item).toHaveAttribute(section.startsWith('/') ? 'href' : 'data-page', section)
    await expect(page.locator('.sidebar .nav-item[aria-current="page"]')).toHaveCount(1)
  }
  async function refresh() {
    const response = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/applications' && r.request().method() === 'GET')
    await page.evaluate(() => window.dispatchEvent(new Event('nsc:resources-changed')))
    expect((await response).ok()).toBe(true)
  }
  await page.goto(`/projects/prj_demo/environments/env_demo/applications/${app.id}`)
  await active('applications')
  await refresh(); await active('applications')
  await page.locator('.sidebar [data-page=routes]').click()
  await expect(page).toHaveURL(/\/routes$/); await active('routes')
  await page.goBack(); await expect(page).toHaveURL(new RegExp(`/applications/${app.id}$`)); await active('applications')
  await page.goForward(); await expect(page).toHaveURL(/\/routes$/); await active('routes')
  await page.goto(`/projects/prj_demo/environments/env_demo/services/${service.id}?tab=events`)
  await active('services'); await refresh(); await active('services')
  await page.locator('.sidebar [data-page=certificates]').click()
  await expect(page).toHaveURL(/\/certificates$/); await active('certificates')
  await page.goto(`/projects/prj_demo/environments/env_demo/applications/${app.id}/compose`)
  await active('applications')
  await page.goto('/infrastructure/runtimes/rt_demo?tab=events')
  await active('runtimes'); await refresh(); await active('runtimes')
  await page.locator('.sidebar [data-page=market]').click()
  await expect(page).toHaveURL(/\/market$/); await active('market')
  await page.locator('.sidebar a[href="/account"]').click()
  await expect(page).toHaveURL(/\/account$/); await active('/account')
  await page.locator('.sidebar a[href="/users"]').click()
  await expect(page).toHaveURL(/\/users$/); await active('/users')
  await page.goBack(); await active('/account')
  await page.locator('.sidebar [data-page=market]').click()
  await expect(page).toHaveURL(/\/market$/); await active('market')
  await page.goto('/projects/missing')
  await expect(page.locator('.sidebar .nav-item.active')).toHaveCount(0)
  expect(errors).toEqual([])
})

test('workspace directory defers details and Compose deep links read only the selected owner', async ({ page }) => {
  const headers = await admin(page)
  const projectResponse = await page.request.post('/api/v1/projects', { headers, data: { name: 'Other scope', slug: 'other-scope' } })
  expect(projectResponse.status()).toBe(201)
  const project = (await projectResponse.json()).data
  const environmentResponse = await page.request.post(`/api/v1/projects/${project.id}/environments`, {
    headers: { ...headers, 'If-Match': '"rev-1"' }, data: { name: 'qa', runtime_id: 'rt_demo' },
  })
  expect(environmentResponse.status()).toBe(201)
  const environment = (await environmentResponse.json()).data
  for (const envID of ['env_demo', environment.id]) {
    const reviewResponse = await page.request.post(`/api/v1/environments/${envID}/preflight`, { headers, data: { catalog_app_version_id: 'cat_demo_nginx' } })
    expect(reviewResponse.status()).toBe(200)
    const review = (await reviewResponse.json()).data
    const installed = await page.request.post(`/api/v1/environments/${envID}/deployments`, {
      headers: { ...headers, 'If-Match': '"rev-1"', 'Idempotency-Key': `scoped-reads-${envID}` }, data: { preflight_id: review.preflight_id },
    })
    expect(installed.status()).toBe(202)
  }
  const apps = (await (await page.request.get('/api/v1/applications')).json()).data
  const calls: string[] = []
  page.on('request', request => { if (request.method() === 'GET') calls.push(new URL(request.url()).pathname) })
  await page.goto('/projects')
  await expect(page.locator('#main h1')).toBeVisible()
  expect(calls.filter(path => /\/(?:variables|source|draft|revisions|cluster|deployment-draft|domain-drafts)(?:\/|$)|\/catalog\/apps/.test(path))).toEqual([])
  calls.length = 0
  await page.goto(`/projects/prj_demo/environments/env_demo/applications/${apps[0].id}/compose`)
  await expect(page.locator('#y-compose-code')).toHaveValue(/services:/)
  expect(calls).toContain(`/api/v1/applications/${apps[0].id}/source`)
  expect(calls).toContain('/api/v1/environments/env_demo/variables')
  expect(calls.some(path => path.startsWith(`/api/v1/applications/${apps[1].id}/`))).toBe(false)
  expect(calls.some(path => path === `/api/v1/projects/${project.id}/variables` || path === `/api/v1/environments/${environment.id}/variables`)).toBe(false)
})

test('saving variables refreshes one resource and cancelling sidebar navigation preserves the draft', async ({ page }) => {
  await admin(page)
  let revision = 1
  let values: { key: string; value?: string }[] = []
  await page.route('**/api/v1/environments/env_demo/variables', async route => {
    if (route.request().method() === 'PUT') {
      expect(route.request().headers()['if-match']).toBe(`"rev-${revision}"`)
      values = route.request().postDataJSON().values
      revision++
    }
    await route.fulfill({ status: 200, headers: { ETag: `"rev-${revision}"` }, json: { data: { revision, values } } })
  })
  const calls: string[] = []
  page.on('request', request => { if (request.method() === 'GET' && new URL(request.url()).pathname.startsWith('/api/v1/')) calls.push(new URL(request.url()).pathname) })
  await page.goto('/projects/prj_demo/environments/env_demo/variables?layer=environment')
  await page.locator('[data-action=y-var-add]').click()
  await page.locator('#y-key').fill('SCOPED')
  await page.locator('#y-value').fill('saved value')
  await page.locator('[data-action=y-var-apply]').click()
  calls.length = 0
  await page.locator('[data-action=y-vars-save]').click()
  await expect(page.locator('[data-action=y-vars-save]')).toBeDisabled()
  expect(values).toEqual([expect.objectContaining({ key: 'SCOPED', value: 'saved value' })])
  expect(calls).toEqual(['/api/v1/environments/env_demo/variables'])
  await page.locator('[data-action=y-var-add]').click()
  await page.locator('#y-key').fill('UNSAVED')
  await page.locator('#y-value').fill('keep this draft')
  await page.locator('[data-action=y-var-apply]').click()
  calls.length = 0
  await expect(page.locator('.sidebar .nav-item.active')).toHaveAttribute('data-page', 'variables')
  await page.locator('.sidebar [data-page=applications]').click()
  await expect(page.locator('[data-action=y-guard-stay]')).toBeVisible()
  await expect(page.locator('.sidebar .nav-item.active')).toHaveCount(1)
  await expect(page.locator('.sidebar .nav-item.active')).toHaveAttribute('data-page', 'variables')
  await page.locator('[data-action=y-guard-stay]').click()
  await expect(page).toHaveURL(/\/variables\?layer=environment$/)
  await expect(page.locator('#main')).toContainText('keep this draft')
  await expect(page.locator('.sidebar .nav-item.active')).toHaveCount(1)
  await expect(page.locator('.sidebar .nav-item.active')).toHaveAttribute('data-page', 'variables')
  expect(calls).toEqual([])
})

test('Faker keeps encrypted-draft contract semantics and user scope', async ({ page }) => {
  const headers = await admin(page)
  const url = '/api/v1/environments/env_demo/deployment-draft'
  let response = await page.request.get(url)
  expect(response.ok()).toBe(true)
  expect((await response.json()).data.revision).toBe(0)
  response = await page.request.put(url, { headers: { ...headers, 'If-Match': '"rev-0"' }, data: { data: { compose_yaml: 'services: {}' } } })
  expect(response.status()).toBe(200)
  expect((await response.json()).data.revision).toBe(1)
  response = await page.request.put(url, { headers: { ...headers, 'If-Match': '"rev-0"' }, data: { data: {} } })
  expect(response.status()).toBe(412)
  response = await page.request.delete(url, { headers: { ...headers, 'If-Match': '"rev-1"' } })
  expect(response.status()).toBe(204)
})

test('Faker catalog review is version pinned and rejects edited Compose', async ({ page }) => {
  const headers = await admin(page)
  const url = '/api/v1/environments/env_demo/preflight'
  let response = await page.request.post(url, { headers, data: { catalog_app_version_id: 'cat_demo_nginx', compose_yaml: 'services: {}' } })
  expect(response.status()).toBe(422)
  response = await page.request.post(url, { headers, data: { catalog_app_version_id: 'cat_demo_nginx' } })
  expect(response.status()).toBe(200)
  const review = (await response.json()).data
  response = await page.request.post('/api/v1/environments/env_demo/deployments', { headers: { ...headers, 'If-Match': '"rev-1"', 'Idempotency-Key': 'faker-catalog-e2e' }, data: { preflight_id: review.preflight_id } })
  expect(response.status()).toBe(202)
  const apps = (await (await page.request.get('/api/v1/applications')).json()).data
  expect(apps[0].catalog_app_version_id).toBe('cat_demo_nginx')
  const services = (await (await page.request.get('/api/v1/services')).json()).data
  const revisions = (await (await page.request.get(`/api/v1/applications/${apps[0].id}/revisions`)).json()).data
  response = await page.request.post(`/api/v1/services/${services[0].id}/revisions/${revisions[0].id}/restore-preview`, { headers })
  expect(response.status()).toBe(200)
  const restore = (await response.json()).data
  response = await page.request.post(`/api/v1/services/${services[0].id}/restore`, { headers: { ...headers, 'If-Match': `"rev-${restore.base_revision}"`, 'Idempotency-Key': 'faker-restore-e2e' }, data: { review_id: restore.id, acknowledge_data_boundary: true } })
  expect(response.status()).toBe(202)
  response = await page.request.post(`/api/v1/services/${services[0].id}/delete`, {
    headers: { ...headers, 'If-Match': `"rev-${services[0].revision}"`, 'Idempotency-Key': 'faker-delete-last' },
    data: { acknowledge_data_boundary: true },
  })
  expect(response.status()).toBe(422)
  await page.goto('/projects/prj_demo/environments/env_demo/services')
  const entry = page.locator(`#main [data-action=x-service][data-id="${services[0].id}"]`).first()
  await expect(entry).toBeVisible()
  await entry.click()
  await expect(page.locator('.service-restore')).toBeVisible()
  page.once('dialog', dialog => dialog.accept())
  await page.locator('.service-restore button.btn.danger').click()
  await expect(page.locator('.service-restore [role=alert]')).toBeVisible()
})

test('wizard save and resume use the environment draft API', async ({ page }) => {
  await admin(page)
  await page.goto('/market')
  const template = page.locator('[data-action=x-template]').first()
  await expect(template).toBeVisible()
  await template.click()
  await expect(page.locator('[data-action=x-wsave]')).toBeVisible()
  const draftWrite = page.waitForResponse(response => response.url().includes('/environments/env_demo/deployment-draft') && response.request().method() === 'PUT')
  await page.locator('[data-action=x-wsave]').click()
  expect((await draftWrite).status()).toBe(200)
  const saved = (await (await page.request.get('/api/v1/environments/env_demo/deployment-draft')).json()).data
  expect(saved.revision).toBe(1)
  await page.reload()
  await page.goto('/projects/prj_demo/environments/env_demo/applications')
  const resume = page.locator('[data-action=x-resume-wizard]').first()
  await expect(resume).toBeVisible()
  await resume.click()
  await expect(page.locator('[data-action=x-wsave]')).toBeVisible()
})

test('Faker service deletion retains sibling, volume and idempotent operation', async ({ page }) => {
  const headers = await admin(page)
  const reviewResponse = await page.request.post('/api/v1/environments/env_demo/preflight', {
    headers, data: { catalog_app_version_id: 'cat_demo_pair' },
  })
  expect(reviewResponse.status()).toBe(200)
  const review = (await reviewResponse.json()).data
  const install = await page.request.post('/api/v1/environments/env_demo/deployments', {
    headers: { ...headers, 'If-Match': '"rev-1"', 'Idempotency-Key': 'pair-install' },
    data: { preflight_id: review.preflight_id },
  })
  expect(install.status()).toBe(202)
  const app = (await (await page.request.get('/api/v1/applications')).json()).data[0]
  const services = (await (await page.request.get('/api/v1/services')).json()).data
  const web = services.find((item: { name: string }) => item.name === 'web')
  const requestHeaders = { ...headers, 'If-Match': `"rev-${web.revision}"`, 'Idempotency-Key': 'pair-delete-web' }
  const first = await page.request.post(`/api/v1/services/${web.id}/delete`, {
    headers: requestHeaders, data: { acknowledge_data_boundary: true },
  })
  expect(first.status()).toBe(202)
  const operation = (await first.json()).data
  const replay = await page.request.post(`/api/v1/services/${web.id}/delete`, {
    headers: requestHeaders, data: { acknowledge_data_boundary: true },
  })
  expect(replay.status()).toBe(202)
  expect((await replay.json()).data.id).toBe(operation.id)
  const source = (await (await page.request.get(`/api/v1/applications/${app.id}/source`)).json()).data.compose_yaml
  expect(source).toContain('worker:')
  expect(source).toContain('data:')
  expect(source).not.toContain('web:')
  const removed = (await (await page.request.get(`/api/v1/services/${web.id}`)).json()).data
  expect(removed.status).toBe('REMOVED')
  expect((await (await page.request.get('/api/v1/services')).json()).data.find((item: { name: string }) => item.name === 'worker').status).toBe('RUNNING')
})

test('IP access editor retains raw ranges through preview, draft save and reload', async ({ page }) => {
  await admin(page)
  const domain = { id: 'dom_ip_ui', pool_id: 'pool_demo', host: 'ip.example.test', tls: { mode: 'disabled' }, entrypoints: ['http'], redirect_http_to_https: false, enabled: true, revision: 1 }
  const routeRow = { id: 'route_ip_ui', domain_id: domain.id, kind: 'return', match: 'prefix', path: '/', response_code: 200, response_value: 'hello', enabled: true, revision: 1, backend: { service_id: '', port: 0, protocol: 'http' } }
  const domainDraft = { id: 'draft_ip_ui', project_id: 'prj_demo', environment_id: 'env_demo', status: 'published', published_domain_id: domain.id, published_route_id: routeRow.id }
  const active = { revision: 1, snippets: [], routes: [], access_controls: [] }
  let draft: { revision: number; snippets: unknown[]; routes: unknown[]; access_controls: { scope: string; resource_id: string; access: string[]; forbidden: string[]; client_ip?: { header: string; trusted_proxies: string[] } }[] } = { ...active, revision: 0 }
  let previewInput: unknown
  await page.route('**/api/v1/domains?*', route => route.fulfill({ json: { data: [domain], pagination: {} } }))
  await page.route('**/api/v1/routes?*', route => route.fulfill({ json: { data: [routeRow], pagination: {} } }))
  await page.route('**/api/v1/environments/env_demo/domain-drafts?*', route => route.fulfill({ json: { data: [domainDraft], pagination: {} } }))
  await page.route('**/api/v1/gateway-pools/pool_demo/nginx', route => route.fulfill({ headers: { ETag: `"rev-${draft.revision}"` }, json: { data: { active, draft } } }))
  await page.route('**/api/v1/gateway-pools/pool_demo/nginx/draft', async route => {
    expect(route.request().headers()['if-match']).toBe(`"rev-${draft.revision}"`)
    draft = { ...route.request().postDataJSON(), revision: draft.revision + 1 }
    await route.fulfill({ headers: { ETag: `"rev-${draft.revision}"` }, json: { data: draft } })
  })
  await page.route('**/api/v1/gateway-pools/pool_demo/nginx/preview', async route => {
    previewInput = route.request().postDataJSON()
    await route.fulfill({ json: { data: { candidate: { content: 'geo $remote_addr $nsc_ip_access { default 0; 192.168.1.10/31 1; }' } } } })
  })
  await page.goto(`/projects/prj_demo/environments/env_demo/domains/${domain.id}?tab=config`)
  const panel = page.getByTestId('ip-access-control')
  await expect(panel).toBeVisible()
  const raw = ' 192.168.1.10-192.168.1.20 '
  await panel.locator('[name=ip-access-domain]').fill(raw)
  await panel.locator('[name=ip-forbidden-domain]').fill('192.168.1.15')
  await panel.locator('[name=ip-client-source]').selectOption('X-Forwarded-For')
  const trusted = ' 10.0.0.1-10.0.0.9 '
  await panel.locator('[name=ip-trusted-proxies]').fill(trusted)
  await panel.locator('[name=ip-access-route_ip_ui]').fill('192.168.1.11\n2001:db8::/32')
  await expect(panel.locator('[name=ip-access-domain]')).toHaveValue(raw)
  await panel.locator('button').click()
  await expect(panel.getByTestId('ip-access-preview')).toContainText('192.168.1.10/31')
  expect(previewInput).toMatchObject({ access_controls: [{ scope: 'server', resource_id: domain.id, access: [raw], forbidden: ['192.168.1.15'] }, { scope: 'location', resource_id: routeRow.id, access: ['192.168.1.11', '2001:db8::/32'], forbidden: [] }] })
  expect(previewInput).toMatchObject({ access_controls: [{ client_ip: { header: 'X-Forwarded-For', trusted_proxies: [trusted] } }, {}] })
  await page.locator('[data-action=save]').click()
  await expect.poll(() => draft.revision).toBe(1)
  expect(draft.routes).toEqual([])
  expect(draft.access_controls[0]?.access).toEqual([raw])
  await page.reload()
  await expect(panel.locator('[name=ip-access-domain]')).toHaveValue(raw)
  await expect(panel.locator('[name=ip-access-route_ip_ui]')).toHaveValue('192.168.1.11\n2001:db8::/32')
  await expect(panel.locator('[name=ip-client-source]')).toHaveValue('X-Forwarded-For')
  await expect(panel.locator('[name=ip-trusted-proxies]')).toHaveValue(trusted)
  await panel.locator('[name=ip-client-source]').selectOption('custom')
  await panel.locator('[name=ip-client-header]').fill('CF-Connecting-IP')
  await page.locator('[data-action=save]').click()
  await expect.poll(() => draft.revision).toBe(2)
  await page.reload()
  await expect(panel.locator('[name=ip-client-source]')).toHaveValue('custom')
  await expect(panel.locator('[name=ip-client-header]')).toHaveValue('CF-Connecting-IP')
  await panel.locator('[name=ip-client-source]').selectOption('connection')
  await page.locator('[data-action=save]').click()
  await expect.poll(() => draft.revision).toBe(3)
  expect(draft.access_controls[0]?.client_ip).toBeUndefined()
  expect(draft.access_controls[0]?.access).toEqual([raw])
})
