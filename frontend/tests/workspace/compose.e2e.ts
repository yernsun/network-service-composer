import { test, expect, type APIRequestContext } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
const enabled = process.env.NSC_COMPOSE_ACCEPTANCE === 'true'
const fixture = enabled ? JSON.parse(readFileSync(process.env.NSC_WORKSPACE_FIXTURE!, 'utf8')) : null
const admin = process.env.E2E_ADMIN_PASSWORD!
async function api(request: APIRequestContext, method: string, path: string, data?: unknown) {
  const response = await request.fetch(`/api/v1${path}`, { method, headers: { Authorization: `Bearer ${admin}` }, data })
  expect(response.ok(), await response.text()).toBe(true)
  return (await response.json()).data
}
test('compose placement trial: validation, service workspace, review and actual node', async ({ page, request }) => {
  test.skip(!enabled, 'Requires disposable Compose acceptance fixture')
  test.setTimeout(240000)
  const f = fixture, errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  const docker = (...args: string[]) => execFileSync('docker', ['--host', f.private_docker, ...args], { encoding: 'utf8' }).trim()
  expect(docker('info', '--format', '{{.ID}}')).toBe(f.private_daemon_id)
  const slug = `compose-${Date.now()}`
  const project = await api(request, 'POST', '/projects', { name: slug, slug })
  const env = await api(request, 'POST', `/projects/${project.id}/environments`, { name: 'trial', runtime_id: f.runtime.id })
  const source = `# preserve user comment\nservices:\n  web:\n    image: ${f.image}\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n`
  const initial = await api(request, 'POST', `/environments/${env.id}/compose-validation`, { compose_yaml: source })
  expect(initial.syntax_valid).toBe(true)
  expect(initial.deployable).toBe(!f.swarm)
  if (f.swarm) {
    expect(initial.diagnostics.map((d: { code: string }) => d.code)).toContain('SWARM_LOCAL_VOLUME_WITHOUT_PLACEMENT')
    const rejected = await request.post(`/api/v1/environments/${env.id}/preflight`, { headers: { Authorization: `Bearer ${admin}` }, data: { compose_yaml: source } })
    expect(rejected.status()).toBe(400)
    expect((await rejected.json()).code).toBe('validation.application_preflight_failed')
    const unknownSource = source.replace('data: {}', 'data: {external: true}')
    const unknown = await api(request, 'POST', `/environments/${env.id}/compose-validation`, { compose_yaml: unknownSource })
    const mount = unknown.services[0].mounts[0]
    const acknowledged = await api(request, 'POST', `/environments/${env.id}/compose-validation`, { compose_yaml: unknownSource, deployment_plan: { acknowledged: unknown.diagnostics.map((d: { id: string }) => d.id) } })
    expect(acknowledged.deployable).toBe(false)
    const declaration = { resource: mount.key, fingerprint: mount.fingerprint, capability: 'shared', confirmed: true }
    const shared = await api(request, 'POST', `/environments/${env.id}/compose-validation`, { compose_yaml: unknownSource, deployment_plan: { storage: [declaration] } })
    expect(shared.deployable).toBe(true)
    const mixed = await api(request, 'POST', `/environments/${env.id}/compose-validation`, { compose_yaml: unknownSource.replace('- data:/data', '- data:/data\n      - /srv/backup:/backup'), deployment_plan: { storage: [declaration] } })
    expect(mixed.diagnostics.some((d: { code: string }) => d.code === 'SWARM_BIND_MOUNT_WITHOUT_PLACEMENT')).toBe(true)

  }
  await page.goto('/login'); await page.locator('#username').fill('admin'); await page.locator('#password').fill(admin)
  await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
  await page.goto(`/projects/${project.id}/environments/${env.id}/applications`)
  await page.locator('[data-action=x-install]').first().click()
  await page.locator('[data-action=x-wsource][data-id=file]').click()
  await page.locator('#x-w-name').fill('trial'); await page.locator('#x-w-key').fill(env.project_name)
  await page.locator('#x-w-raw').fill('services: [')
  await expect(page.locator('#x-w-primary')).toBeDisabled()
  await page.locator('#x-w-raw').fill(source)
  await expect(page.locator('#x-w-primary')).toBeEnabled()
  await expect(page.locator('#x-w-raw')).toBeFocused()
  await page.locator('#x-w-primary').click()
  await page.locator('#x-w-runtime').selectOption(f.runtime.id)
  await page.locator('#x-w-primary').click()
  await page.locator('#x-w-primary').click()
  const analysis = page.getByTestId('compose-analysis')
  if (f.swarm) {
    await expect(page.locator('#x-w-primary')).toBeDisabled()
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(analysis.locator('.between').first()).toBeVisible()
    await analysis.locator('.compose-service button').first().click()
    const workspace = page.getByTestId('compose-workspace')
    await workspace.locator('input[type=radio][value=node]').check()
    await workspace.locator('select').last().selectOption(f.worker_node)
    await workspace.locator('header button').click()
    await expect(workspace.locator('.callout.amber').last()).toBeVisible()
    await workspace.locator('.callout.amber').last().locator('button').last().click()
    await page.setViewportSize({ width: 1440, height: 1000 })
    await workspace.locator('.compose-actions button').nth(1).click()
    await expect(workspace).toHaveCount(0)
    await expect(analysis).toContainText(docker('node', 'inspect', f.worker_node, '--format', '{{.Description.Hostname}}'))
  }
  await page.locator('[data-wf=reviewAck]').check()
  const warningAck = page.locator('[data-wf=warningAck]')
  if (await warningAck.count()) await warningAck.check()
  await expect(page.locator('#x-w-primary')).toBeEnabled({ timeout: 45000 })
  const operationResponse = page.waitForResponse(r => r.url().endsWith(`/environments/${env.id}/${f.swarm ? 'service-deployments' : 'deployments'}`) && r.request().method() === 'POST')
  await page.locator('#x-w-primary').click()
  const response = await operationResponse
  expect(response.ok(), await response.text()).toBe(true)
  const operation = (await response.json()).data
  await expect.poll(async () => (await api(request, 'GET', `/operations/${operation.id}`)).status, { timeout: 120000 }).toBe('SUCCEEDED')
  if (f.swarm) {
    const task = docker('service', 'ps', `${env.project_name}_web`, '--filter', 'desired-state=running', '-q').split('\n')[0]!
    expect(docker('inspect', task, '--format', '{{.NodeID}}')).toBe(f.worker_node)
    const hostname = docker('node', 'inspect', f.worker_node, '--format', '{{.Description.Hostname}}')
    const restricted = source.replace('    volumes:', `    environment:\n      TRIAL: refreshed\n    deploy:\n      placement:\n        constraints: ['node.hostname == ${hostname}']\n    volumes:`)
    const current = await api(request, 'GET', `/environments/${env.id}`)
    const review = await api(request, 'POST', `/environments/${env.id}/service-preflight`, { service_mode: 'update', compose_yaml: restricted })
    docker('node', 'update', '--availability', 'drain', f.worker_node)
    try {
      const blocked = await request.post(`/api/v1/environments/${env.id}/deployments`, { headers: { Authorization: `Bearer ${admin}`, 'If-Match': `"rev-${current.revision}"`, 'Idempotency-Key': `drained-${slug}` }, data: { preflight_id: review.preflight_id } })
      expect(blocked.status(), await blocked.text()).toBe(400)
      expect((await blocked.json()).code).toBe('validation.application_preflight_failed')
      const stop = await api(request, 'POST', `/applications/${current.application_id}/operations`, { action: 'STOP' })
      await expect.poll(async () => (await api(request, 'GET', `/operations/${stop.id}`)).status, { timeout: 60000 }).toBe('SUCCEEDED')
    } finally { docker('node', 'update', '--availability', 'active', f.worker_node) }

  } else expect(docker('ps', '--filter', `label=com.docker.compose.project=${env.project_name}`, '--format', '{{.Names}}')).toContain('web')
  expect(errors).toEqual([])
})
