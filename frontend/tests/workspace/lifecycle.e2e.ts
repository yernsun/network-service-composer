import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
const fixture = JSON.parse(readFileSync(process.env.NSC_WORKSPACE_FIXTURE!, 'utf8'))
const password = process.env.E2E_ADMIN_PASSWORD!

test('project lifecycle: multiple real runtime choices recover from ambiguity in the original create modal', async ({ page, request }) => {
  const headers = { Authorization: `Bearer ${password}` }, slug = `runtime-choice-${Date.now()}`
  // Both connections belong to this disposable fixture daemon. No live business
  // runtime is contacted or configured by this test.
  const runtimeResponse = await request.post('/api/v1/runtimes', { headers, data: { pool_id: fixture.pool.id, name: 'Second <runtime> & {name}', runtime_type: fixture.runtime.runtime_type, endpoint: fixture.runtime.endpoint, management_enabled: true } })
  expect(runtimeResponse.status()).toBe(201)
  const runtime = (await runtimeResponse.json()).data
  try {
    await page.goto('/login');await page.locator('#username').fill('admin');await page.locator('#password').fill(password);await page.locator('button[type=submit]').click()
    await expect(page).toHaveURL(/\/projects$/);await page.locator('#workspace-language').selectOption('en')
    await page.locator('[data-action=p-project-add]').click();await expect(page.locator('#p-name')).toBeFocused()
    await page.locator('#p-name').fill('Runtime <project>');await page.locator('#p-key').fill(slug);await page.locator('#p-description').fill('Retained after runtime validation')
    await expect(page.locator(`#p-runtime option[value="${runtime.id}"]`)).toHaveText(runtime.name)
    await expect(page.locator(`#p-runtime option[value="${fixture.runtime.id}"]`)).toHaveText(fixture.runtime.name)
    const rejected = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/api/v1/projects'))
    await page.locator('[data-action=p-project-create]').click();const invalid = await rejected
    expect(invalid.status()).toBe(400);expect((await invalid.json()).code).toBe('validation.project_runtime')
    await expect(page.locator('#p-runtime')).toBeFocused();await expect(page.locator('#p-runtime')).toHaveAttribute('aria-invalid','true')
    await expect(page.locator('#p-name')).toHaveValue('Runtime <project>');await expect(page.locator('#p-description')).toHaveValue('Retained after runtime validation')
    const projects = (await (await request.get('/api/v1/projects?limit=200', { headers })).json()).data
    expect(projects.some((p: { slug: string }) => p.slug === slug)).toBe(false)
    await page.locator('#p-runtime').selectOption(runtime.id)
    await page.locator('#workspace-language').selectOption('zh-CN', { force: true });await expect(page.locator('#p-runtime')).toHaveValue(runtime.id)
    await expect(page.locator(`#p-runtime option[value="${runtime.id}"]`)).toHaveText(runtime.name)
    await page.locator('#workspace-language').selectOption('en', { force: true })
    await page.screenshot({ path: test.info().outputPath('runtime-choice-retained-input.png'), fullPage: true })
    const accepted = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/api/v1/projects'))
    await page.locator('[data-action=p-project-create]').click();const response = await accepted;expect(response.status()).toBe(201)
    const project = (await response.json()).data
    expect(response.request().postDataJSON().default_runtime_id).toBe(runtime.id);expect(project.default_runtime_id).toBe(runtime.id)
    const replay = await request.post('/api/v1/projects', { headers: { ...headers, 'Idempotency-Key': response.request().headers()['idempotency-key']! }, data: response.request().postDataJSON() })
    expect(replay.status()).toBe(201);expect((await replay.json()).data.id).toBe(project.id)
    await expect(page.locator('#main h1')).toHaveText('Runtime <project>');await page.reload();await expect(page.locator('#main h1')).toHaveText('Runtime <project>')
    const environments = (await (await request.get(`/api/v1/projects/${project.id}/environments`, { headers })).json()).data
    expect(environments).toHaveLength(1);expect(environments[0].runtime_id).toBe(runtime.id);expect(environments[0].id).toBe(project.initial_environment_id)
    await page.goto('/projects');await page.locator(`[data-action=p-project-settings][data-id="${project.id}"]`).click()
    const dialog = page.getByTestId('project-settings-dialog');await expect(dialog.locator('#project-settings-runtime')).toHaveValue(runtime.id)
    await dialog.getByRole('button', { name: 'Preview deletion', exact: true }).click();await dialog.locator('#project-delete-confirm').fill(slug)
    const removing = page.waitForResponse(r => r.request().method() === 'DELETE' && r.url().endsWith(`/api/v1/projects/${project.id}`))
    await dialog.locator('.modal-foot .danger').click();const removed = await removing;expect(removed.status()).toBe(202)
    const task = (await removed.json()).data
    await page.reload();await expect.poll(async () => (await (await request.get(`/api/v1/project-deletions/${task.id}`, { headers })).json()).data.status).toBe('SUCCEEDED')
    expect((await request.get(`/api/v1/projects/${project.id}`, { headers })).status()).toBe(404)
    await page.goto('/projects');await expect(page.locator(`[data-action=p-project][data-id="${project.id}"]`)).toHaveCount(0)
  } finally {
    // Restore the fixture's single-runtime default for the other browser cases.
    let status = 412
    for (let attempt = 0; status === 412 && attempt < 3; attempt++) {
      const current = await request.get(`/api/v1/runtimes/${runtime.id}`, { headers })
      status = (await request.patch(`/api/v1/runtimes/${runtime.id}`, { headers: { ...headers, 'If-Match': current.headers().etag! }, data: { enabled: false, management_enabled: false } })).status()
    }
    expect(status).toBe(200)
  }
})

test('project lifecycle: original settings, empty environment deletion, confirmed project deletion and durable result', async ({ page, request }) => {
  const errors: string[] = [];page.on('pageerror', e => errors.push(e.message))
  await page.goto('/login');await page.locator('#username').fill('admin');await page.locator('#password').fill(password);await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects$/);await page.locator('#workspace-language').selectOption('en')
  await page.locator('[data-action=p-project-add]').click()
  const slug = `lifecycle-${Date.now()}`
  await page.locator('#p-name').fill('Lifecycle <literal>');await page.locator('#p-key').fill(slug);await page.locator('#p-description').fill('Atomic description')
  await page.locator('[data-action=p-project-create]').click();await expect(page.locator('#main h1')).toHaveText('Lifecycle <literal>')
  const id = new URL(page.url()).pathname.split('/')[2]!
  await page.goto('/projects');await page.locator(`[data-action=p-project-settings][data-id="${id}"]`).click()
  const dialog = page.getByTestId('project-settings-dialog')
  await expect(dialog.locator('#project-settings-description')).toHaveValue('Atomic description')
  await expect(dialog.locator('#project-settings-runtime')).toHaveValue(fixture.runtime.id)
  await dialog.locator('#project-settings-name').fill('Saved <project>');await dialog.locator('#project-settings-description').fill('Persistent description')
  await dialog.locator('button[type=submit]').click();await expect(dialog.locator('button[type=submit]')).toBeDisabled()
  await page.keyboard.press('Escape');await page.reload()
  await page.locator(`[data-action=p-project-settings][data-id="${id}"]`).click();await expect(dialog.locator('#project-settings-name')).toHaveValue('Saved <project>')
  await page.keyboard.press('Escape')
  await page.goto(`/projects/${id}/environments`);await page.locator('[data-action=p-env-add]').click()
  await page.locator('#p-env-name').fill('Temporary');await page.locator('#p-env-slug').fill('temporary');await page.locator('[data-action=p-env-save]').click()
  await expect(page.locator('#main h1')).toHaveText('Temporary');await page.locator('[data-action=p-env-settings]').first().click()
  await page.locator('[data-action=p-env-delete]').click();await page.locator('[data-action=p-env-delete-confirm]').click();await expect(page.locator('#modal-title')).not.toBeVisible()
  const headers = { Authorization: `Bearer ${password}` }
  const environments = (await (await request.get(`/api/v1/projects/${id}/environments`, { headers })).json()).data
  expect(environments).toHaveLength(1);expect(environments[0].name).toBe('production')
  await page.goto('/projects');await page.locator(`[data-action=p-project-settings][data-id="${id}"]`).click()
  await expect(dialog.locator('input[type=checkbox]')).not.toBeChecked();await dialog.getByRole('button', { name: 'Preview deletion', exact: true }).click()
  await expect(dialog.locator('#project-delete-confirm')).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('deletion-preview.png'), fullPage: true })
  await dialog.locator('#project-delete-confirm').fill(slug)
  const accepted = page.waitForResponse(r => r.request().method() === 'DELETE' && r.url().endsWith(`/api/v1/projects/${id}`))
  await dialog.locator('.modal-foot .danger').click();const response = await accepted;expect(response.status()).toBe(202)
  const task = (await response.json()).data
  await page.reload()
  await expect.poll(async () => (await (await request.get(`/api/v1/project-deletions/${task.id}`, { headers })).json()).data.status).toBe('SUCCEEDED')
  await page.goto('/projects');await expect(page.locator(`[data-action=p-project-settings][data-id="${id}"]`)).toHaveCount(0)
  expect((await request.get(`/api/v1/projects/${id}`, { headers })).status()).toBe(404)
  await page.screenshot({ path: test.info().outputPath('after-project-deletion.png'), fullPage: true })
  expect(errors).toEqual([])
})

test('orphan project opens through cards and deep links, preserves scope and remains deletable with viewer restrictions', async ({ page, request }) => {
  const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
  const headers = { Authorization: `Bearer ${password}` }, slug = `empty-${Date.now()}`
  const response = await request.post('/api/v1/projects', { headers, data: { name: 'Empty legacy project', slug } });expect(response.status()).toBe(201)
  const project = (await response.json()).data
  const populatedResponse = await request.post('/api/v1/projects', { headers, data: { name: 'Other project', slug: slug + '-other', initial_environment: { name: 'production' } } })
  expect(populatedResponse.status()).toBe(201)
  const populated = (await populatedResponse.json()).data
  await page.goto('/login');await page.locator('#username').fill('admin');await page.locator('#password').fill(password);await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects$/); await page.locator('#workspace-language').selectOption('en')
  await page.goto(`/projects/${populated.id}/environments/${populated.initial_environment_id}/routes`)
  await page.locator('[data-action=nav][data-page=projects]').first().click()
  await page.locator(`[data-action=p-project][data-id="${project.id}"]`).click()
  await expect(page).toHaveURL(new RegExp(`/projects/${project.id}$`))
  await expect(page.locator('#main h1')).toHaveText('Empty legacy project')
  await expect(page.locator('#scope-project')).toHaveValue(project.id)
  await page.locator('.sidebar [data-action=nav][data-page=environments]').click()
  await page.reload(); await expect(page.locator('.sidebar .active')).toHaveAttribute('data-page', 'environments')
  await expect(page.locator('#scope-project')).toHaveValue(project.id)
  await page.goBack(); await expect(page.locator('#main h1')).toHaveText('Empty legacy project')
  await page.goForward(); await expect(page).toHaveURL(new RegExp(`/projects/${project.id}/environments$`))
  for (const section of ['applications', 'services', 'routes']) {
    await page.locator(`.sidebar [data-action=nav][data-page=${section}]`).click()
    await expect(page).toHaveURL(new RegExp(`/projects/${project.id}/environments/all/${section}(\\?|$)`))
    await expect(page.locator('.sidebar .active')).toHaveAttribute('data-page', section)
    await expect(page.locator('#scope-project')).toHaveValue(project.id)
    await expect(page.locator('#main [data-action=x-service], #main [data-action=service-detail]')).toHaveCount(0)
  }
  await page.screenshot({ path: test.info().outputPath('empty-project-scoped-routes.png'), fullPage: true })
  expect((await (await request.get(`/api/v1/projects/${project.id}/environments`, { headers })).json()).data).toEqual([])
  await page.goto(`/projects/${project.id}`); await expect(page.locator('#main h1')).toHaveText('Empty legacy project')
  await page.screenshot({ path: test.info().outputPath('empty-project-overview.png'), fullPage: true })
  await page.locator('[data-action=nav][data-page=projects]').first().click()
  await page.locator(`[data-action=p-project-settings][data-id="${project.id}"]`).click();await expect(page.locator('#project-settings-name')).toHaveValue('Empty legacy project')
  await page.locator('#project-settings-runtime').selectOption(fixture.runtime.id);await page.locator('[data-testid=project-settings-dialog] button[type=submit]').click()
  await expect.poll(async () => (await (await request.get(`/api/v1/projects/${project.id}`, { headers })).json()).data.default_runtime_id).toBe(fixture.runtime.id)
  const username = `lifecycle-viewer-${Date.now()}`
  const viewer = await request.post('/api/v1/users', { headers: { ...headers, 'X-NSC-Reauthenticate': password }, data: { username, password: 'isolated-lifecycle-viewer', role: 'viewer', enabled: true } });expect(viewer.status()).toBe(201)
  await page.goto('/logout');await expect(page).toHaveURL(/\/login/)
  await page.locator('#username').fill(username);await page.locator('#password').fill('isolated-lifecycle-viewer');await page.locator('button[type=submit]').click()
  await page.locator(`[data-action=p-project-settings][data-id="${project.id}"]`).click()
  await expect(page.locator('#project-settings-name')).toBeDisabled();await expect(page.locator('[data-testid=project-settings-dialog] .btn.danger')).toBeDisabled()
  await page.goto('/logout'); await expect(page).toHaveURL(/\/login/)
  await page.locator('#username').fill('admin'); await page.locator('#password').fill(password); await page.locator('button[type=submit]').click()
  await page.locator(`[data-action=p-project-settings][data-id="${project.id}"]`).click()
  const dialog = page.getByTestId('project-settings-dialog')
  await dialog.getByRole('button', { name: 'Preview deletion', exact: true }).click()
  await expect(dialog.locator('input[type=checkbox]')).not.toBeChecked()
  await dialog.locator('#project-delete-confirm').fill(slug)
  await page.screenshot({ path: test.info().outputPath('empty-project-deletion-preview.png'), fullPage: true })
  const accepted = page.waitForResponse(r => r.request().method() === 'DELETE' && r.url().endsWith(`/api/v1/projects/${project.id}`))
  await dialog.locator('.modal-foot .danger').click()
  const deleted = await accepted; expect(deleted.status()).toBe(202)
  const task = (await deleted.json()).data
  await expect.poll(async () => (await (await request.get(`/api/v1/project-deletions/${task.id}`, { headers })).json()).data.status).toBe('SUCCEEDED')
  await page.reload(); await expect(page.locator(`[data-action=p-project][data-id="${project.id}"]`)).toHaveCount(0)
  expect((await request.get(`/api/v1/projects/${project.id}`, { headers })).status()).toBe(404)
  expect((await request.get(`/api/v1/environments/${populated.initial_environment_id}`, { headers })).status()).toBe(200)
  expect(errors).toEqual([])
})

test('project lifecycle: force deletion removes real workloads and refresh restores pending progress', async ({ page, request }) => {
  test.setTimeout(180000)
  const headers = { Authorization: `Bearer ${password}` }, slug = `force-ui-${Date.now()}`
  const created = await request.post('/api/v1/projects', { headers: { ...headers, 'Idempotency-Key': slug }, data: { name: slug, slug, initial_environment: { name: 'production' } } })
  expect(created.status()).toBe(201)
  const project = (await created.json()).data
  const env = (await (await request.get(`/api/v1/environments/${project.initial_environment_id}`, { headers })).json()).data
  // A real workload keeps running through its graceful-stop window so a
  // browser reload can observe an actual pending deletion, without mocked APIs.
  const compose = `services:\n  web:\n    image: ${fixture.image}\n    expose: ["80"]\n    stop_grace_period: 30s\n    command: ["/bin/sh", "-c", "trap '' TERM QUIT; nginx -g 'daemon off;' & wait"]\n`
  const review = await request.post(`/api/v1/environments/${env.id}/preflight`, { headers, data: { compose_yaml: compose } })
  expect(review.status()).toBe(200)
  const deployment = await request.post(`/api/v1/environments/${env.id}/deployments`, { headers: { ...headers, 'Idempotency-Key': slug + '-deploy', 'If-Match': `"rev-${env.revision}"` }, data: { preflight_id: (await review.json()).data.preflight_id } })
  expect(deployment.status()).toBe(202)
  const operation = (await deployment.json()).data
  await expect.poll(async () => (await (await request.get(`/api/v1/operations/${operation.id}`, { headers })).json()).data.status, { timeout: 60000 }).toBe('SUCCEEDED')
  await page.goto('/login');await page.locator('#username').fill('admin');await page.locator('#password').fill(password);await page.locator('button[type=submit]').click()
  await page.locator('#workspace-language').selectOption('en')
  await page.locator(`[data-action=p-project-settings][data-id="${project.id}"]`).click()
  const dialog = page.getByTestId('project-settings-dialog')
  await dialog.getByRole('button', { name: 'Preview deletion', exact: true }).click()
  await expect(dialog.locator('.modal-foot .danger')).toBeDisabled()
  await dialog.locator('input[type=checkbox]').check();await dialog.getByRole('button', { name: 'Preview deletion', exact: true }).click()
  await dialog.locator('#project-delete-confirm').fill(slug)
  const accepted = page.waitForResponse(r => r.request().method() === 'DELETE' && r.url().endsWith(`/api/v1/projects/${project.id}`))
  await dialog.locator('.modal-foot .danger').click()
  const response = await accepted;expect(response.status()).toBe(202)
  const task = (await response.json()).data
  await page.reload();await page.locator(`[data-action=p-project-settings][data-id="${project.id}"]`).click()
  await expect(dialog.locator('[role=status]')).toHaveText(/Queued|In progress/)
  await page.screenshot({ path: test.info().outputPath('restored-running-deletion.png'), fullPage: true })
  await expect.poll(async () => (await (await request.get(`/api/v1/project-deletions/${task.id}`, { headers })).json()).data.status, { timeout: 70000 }).toBe('SUCCEEDED')
  await expect(dialog).not.toBeVisible();await expect(page).toHaveURL(/\/projects$/)
  expect((await request.get(`/api/v1/projects/${project.id}`, { headers })).status()).toBe(404)
  await page.reload();await expect(page.locator(`[data-action=p-project-settings][data-id="${project.id}"]`)).toHaveCount(0)
})
