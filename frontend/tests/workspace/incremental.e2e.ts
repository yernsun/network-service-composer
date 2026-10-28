import { test, expect, type APIRequestContext } from '@playwright/test'
import { readFileSync, mkdtempSync, rmSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const enabled = process.env.NSC_INCREMENTAL_ACCEPTANCE === 'true'
const fixture = enabled ? JSON.parse(readFileSync(process.env.NSC_WORKSPACE_FIXTURE!, 'utf8')) : null
const admin = process.env.E2E_ADMIN_PASSWORD!
async function api(request: APIRequestContext, method: string, path: string, data?: unknown, headers = {}) {
 const response = await request.fetch(`/api/v1${path}`, { method, headers: { Authorization: `Bearer ${admin}`, ...headers }, data })
 expect(response.ok(), `${method} ${path}: ${await response.text()}`).toBe(true)
 return (await response.json()).data
}
test('incremental compose trial: readable history, diff and retained Docker objects', async ({ page, request }) => {
 test.skip(!enabled, 'Requires disposable two-node Swarm')
 test.setTimeout(240000)
 const f = fixture, errors: string[] = []
 page.on('pageerror', error => errors.push(error.message))
 const docker = (...args: string[]) => execFileSync('docker', ['--host', f.private_docker, ...args], { encoding: 'utf8' }).trim()
 expect(docker('info', '--format', '{{.ID}}')).toBe(f.private_daemon_id)
 const slug = `delta-${Date.now()}`
 const domainMode = process.env.NSC_INCREMENTAL_DOMAIN === 'true'
 const host = `${slug}.example.test`
 let certificateID = '', certificatePEM = ''
 const project = await api(request, 'POST', '/projects', { name: slug, slug })
 let env = await api(request, 'POST', `/projects/${project.id}/environments`, { name: 'trial', runtime_id: f.runtime.id })
 const raw = JSON.stringify({ 'x-note': 'x'.repeat(85000), services: { original: { image: f.image, expose: ['80'], environment: { STRING: '0123', FLAG: 'false' } } } })
 const preview = await api(request, 'POST', `/environments/${env.id}/preflight`, { compose_yaml: raw })
 const initial = await api(request, 'POST', `/environments/${env.id}/deployments`, { preflight_id: preview.preflight_id }, { 'If-Match': `"rev-${env.revision}"`, 'Idempotency-Key': `initial-${slug}` })
 await expect.poll(async () => (await api(request, 'GET', `/operations/${initial.id}`)).status, { timeout: 120000 }).toBe('SUCCEEDED')
 env = await api(request, 'GET', `/environments/${env.id}`)
 const original = await api(request, 'GET', `/applications/${env.application_id}/revisions/${initial.revision_id}/source`)
 const inspect = () => {
  const service = JSON.parse(docker('service', 'inspect', `${env.project_name}_original`))[0]
  return { id: service.ID, version: service.Version, spec: service.Spec, tasks: docker('service', 'ps', `${env.project_name}_original`, '--filter', 'desired-state=running', '-q') }
 }
 if (domainMode) {
  const directory = mkdtempSync(join(tmpdir(), 'nsc-delta-cert-'))
  try {
   const cert = join(directory, 'cert.pem'), key = join(directory, 'key.pem')
   execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=*.example.test', '-addext', 'subjectAltName=DNS:*.example.test', '-addext', 'basicConstraints=critical,CA:FALSE', '-addext', 'extendedKeyUsage=serverAuth', '-keyout', key, '-out', cert], { stdio: 'ignore' })
   certificatePEM = readFileSync(cert, 'utf8')
   const created = await api(request, 'POST', '/certificates', { pool_id: f.pool.id, certificate_pem: certificatePEM, private_key_pem: readFileSync(key, 'utf8') })
   certificateID = created.id
  } finally { rmSync(directory, { recursive: true, force: true }) }
 }
 const before = inspect()
 await page.goto('/login'); await page.locator('#username').fill('admin'); await page.locator('#password').fill(admin); await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
 const sourceURL = `/projects/${project.id}/environments/${env.id}/applications/${env.application_id}/compose?view=source`
 await page.goto(sourceURL)
 const editor = page.locator('#y-compose-code')
 await expect(editor).toBeVisible()
 expect((await editor.inputValue()).split('\n').length).toBeGreaterThan(6)
 expect(await editor.inputValue()).toContain('services:')
 const draft = await api(request, 'GET', `/applications/${env.application_id}/draft`)
 expect(draft.revision).toBe(0)
 await page.goto(`/projects/${project.id}/environments/${env.id}/applications`)
 await page.locator('[data-action=x-install]').first().click()
 await page.locator('[data-action=x-wsource][data-id=image]').click()
 await page.locator('#x-w-name').fill('hello'); await page.locator('#x-w-key').fill('hello')
 await expect(page.locator('#x-w-key')).toHaveValue('hello')
 await page.locator('#x-w-image').fill(f.image)
 await page.locator('#x-w-port').fill('80')
 await expect(page.locator('#x-w-key')).toHaveValue('hello')
 await expect(page.locator('#deployment-network-name')).toHaveValue(slug)
 await page.locator('#x-w-primary').click(); await page.locator('#x-w-primary').click()
 await page.locator('#x-w-replicas').fill('2')
 if (domainMode) {
  await page.locator('#x-w-access').selectOption('domain')
  await page.locator('[data-wf=domain]').fill(host)
  await page.locator('[data-wf=gateway]').selectOption(f.gateways.production)
  await page.locator('#deployment-entry-certificate').selectOption(certificateID)
 }
 await page.locator('#x-w-primary').click()
 await page.locator('[data-wf=reviewAck]').check()
 if (await page.locator('[data-wf=warningAck]').isVisible()) await page.locator('[data-wf=warningAck]').check()
 await expect(page.locator('#x-w-primary')).toBeEnabled({ timeout: 45000 })
 const responsePromise = page.waitForResponse(r => r.url().endsWith(`/environments/${env.id}/service-deployments`) && r.request().method() === 'POST')
 await page.locator('#x-w-primary').click()
 const response = await responsePromise
 expect(response.ok(), await response.text()).toBe(true)
 const operation = (await response.json()).data
 await expect.poll(async () => (await api(request, 'GET', `/operations/${operation.id}`)).status, { timeout: 120000 }).toBe('SUCCEEDED')
 if (domainMode) {
  await expect.poll(async () => (await api(request, 'GET', `/operations/${operation.id}/entrypoint`)).status, { timeout: 120000 }).toBe('SUCCEEDED')
  const receipt = await api(request, 'GET', `/operations/${operation.id}/entrypoint`)
  const bindings = await api(request, 'GET', `/services/${receipt.service_id}/domain-bindings`)
  expect(bindings[0].domain.tls.certificate_id).toBe(certificateID)
  const directory = mkdtempSync(join(tmpdir(), 'nsc-delta-verify-'))
  try {
   const cert = join(directory, 'cert.pem')
   const { writeFileSync } = await import('node:fs'); writeFileSync(cert, certificatePEM)
   await expect.poll(() => {
    try { return execFileSync('curl', ['--noproxy', '*', '--silent', '--show-error', '--fail', '--max-time', '5', '--cacert', cert, '--resolve', `${host}:${f.https_port}:${f.https_address}`, `https://${host}:${f.https_port}/`], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }) } catch { return '' }
   }, { timeout: 60000 }).toContain('Welcome to nginx!')
  } finally { rmSync(directory, { recursive: true, force: true }) }
 }
 expect(inspect()).toEqual(before)
 expect((await api(request, 'GET', `/applications/${env.application_id}/revisions/${initial.revision_id}/source`)).compose_yaml).toBe(original.compose_yaml)
 expect(docker('service', 'inspect', `${env.project_name}_hello`, '--format', '{{.Spec.Mode.Replicated.Replicas}}')).toBe('2')
 const network = docker('network', 'inspect', slug, '--format', '{{.Id}}')
 expect(docker('service', 'inspect', `${env.project_name}_hello`, '--format', '{{json .Spec.TaskTemplate.Networks}}')).toContain(network)
 const duplicate = await request.post(`/api/v1/environments/${env.id}/service-preflight`, { headers: { Authorization: `Bearer ${admin}` }, data: { service_mode: 'create', compose_yaml: `services: {hello: {image: '${f.image}'}}` } })
 expect(duplicate.ok()).toBe(false)
 await page.goto(sourceURL); await expect(editor).toBeVisible()
 const saved = await editor.inputValue()
 await editor.fill(saved.replace('replicas: 2', 'replicas: 3'))
 await page.locator('.compose-workbench nav button').last().click()
 await expect(page.locator('.split-diff .add')).toContainText('replicas: 3')
 await page.setViewportSize({ width: 390, height: 844 })
 await expect(page.locator('.unified-diff')).toBeVisible()
 expect(errors).toEqual([])
})
