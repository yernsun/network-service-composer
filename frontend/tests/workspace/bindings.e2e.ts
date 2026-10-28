import { test, expect, type APIRequestContext } from '@playwright/test'
import { readFileSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFileSync } from 'node:child_process'

const enabled = process.env.NSC_BINDING_ACCEPTANCE === 'true'
const fixture = enabled ? JSON.parse(readFileSync(process.env.NSC_WORKSPACE_FIXTURE!, 'utf8')) as { runtime: { id: string }; pool: { id: string }; image: string; private_docker: string; private_daemon_id: string; worker_node: string; https_address: string; https_port: number } : undefined
const admin = process.env.E2E_ADMIN_PASSWORD!
type Entity = { id: string; revision: number; [key: string]: unknown }
async function api<T = Entity>(request: APIRequestContext, method: string, path: string, data?: unknown, extra: Record<string, string> = {}): Promise<T> {
  const response = await request.fetch(`/api/v1${path}`, { method, headers: { Authorization: `Bearer ${admin}`, ...extra }, ...(data === undefined ? {} : { data }) })
  expect(response.ok(), `${method} ${path}: ${await response.text()}`).toBe(true)
  return (await response.json()).data as T
}

test('service binding trial: independent overlay, custom certificate and cross-node HTTPS', async ({ page, request }) => {
  test.skip(!enabled, 'Requires the isolated two-node binding fixture')
  test.setTimeout(240000)
  const f = fixture!
  const docker = (...args: string[]) => execFileSync('docker', ['--host', f.private_docker, ...args], { encoding: 'utf8' }).trim()
  expect(docker('info', '--format', '{{.ID}}')).toBe(f.private_daemon_id)
  expect(f.worker_node).toBeTruthy()
  const suffix = Date.now(), host = `bindings-${suffix}.example.test`
  const project = await api(request, 'POST', '/projects', { name: `bindings-${suffix}`, slug: `bindings-${suffix}` })
  const environment = await api(request, 'POST', `/projects/${project.id}/environments`, { name: 'trial', runtime_id: f.runtime.id })
  const stack = String(environment.project_name)
  const definition = `services:\n  web:\n    image: ${f.image}\n    expose: ["80"]\n    deploy:\n      placement:\n        constraints: ["node.role == worker"]\n  database:\n    image: ${f.image}\n    expose: ["80"]\n`
  const preview = await api(request, 'POST', `/environments/${environment.id}/preflight`, { compose_yaml: definition })
  expect(preview.network_name).toBe(stack)
  const operation = await api(request, 'POST', `/environments/${environment.id}/deployments`, { preflight_id: preview.preflight_id }, { 'If-Match': `"rev-${environment.revision}"`, 'Idempotency-Key': `install-${suffix}` })
  await expect.poll(async () => (await api(request, 'GET', `/operations/${operation.id}`)).status, { timeout: 120000 }).toBe('SUCCEEDED')
  const service = (await api<Entity[]>(request, 'GET', `/services?runtime_id=${f.runtime.id}&limit=200`)).find(s => s.service_key === `${stack}_web`)!
  expect(service).toBeTruthy()
  const networkID = docker('network', 'inspect', stack, '--format', '{{.Id}}')
  const networks = (name: string) => JSON.parse(docker('service', 'inspect', `${stack}_${name}`, '--format', '{{json .Spec.TaskTemplate.Networks}}')) as { Target: string }[]
  expect(networks('web').some(n => n.Target === networkID)).toBe(true)
  expect(networks('database').some(n => n.Target === networkID)).toBe(true)
  const defaultID = docker('network', 'inspect', `${stack}_default`, '--format', '{{.Id}}')
  expect(networks('web').some(n => n.Target === defaultID)).toBe(true)
  const taskID = docker('service', 'ps', `${stack}_web`, '--filter', 'desired-state=running', '-q').split('\n')[0]!
  expect(docker('inspect', taskID, '--format', '{{.NodeID}}')).toBe(f.worker_node)
  const work = mkdtempSync(join(tmpdir(), 'nsc-binding-cert-'))
  try {
    const cert = join(work, 'certificate.pem'), key = join(work, 'key.pem')
    execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', `/CN=${host}`, '-addext', `subjectAltName=DNS:${host}`, '-addext', 'basicConstraints=critical,CA:FALSE', '-addext', 'extendedKeyUsage=serverAuth', '-keyout', key, '-out', cert], { stdio: 'ignore' })
    await page.goto('/login')
    await page.locator('#username').fill('admin'); await page.locator('#password').fill(admin)
    await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
    await page.goto(`/services/${service.id}`)
    const panel = page.getByTestId('service-bindings')
    await expect(panel.locator('header button')).toBeEnabled()
    await panel.locator('header button').click()
    await panel.locator('input[name=host]').fill(host)
    await panel.locator('input[name=port]').fill('80')
    await panel.locator('select[name=tls]').selectOption('custom')
    await panel.locator('form button[type=button]').first().click()
    await panel.locator('.binding-import textarea').nth(0).fill(readFileSync(cert, 'utf8'))
    await panel.locator('.binding-import textarea').nth(1).fill(readFileSync(key, 'utf8'))
    await panel.locator('.binding-import button').click()
    await expect(panel.locator('.binding-import')).toHaveCount(0)
    await panel.locator('form button[type=submit]').click()
    await expect(panel.locator('form')).toHaveCount(0)
    await expect.poll(async () => (await api<{ status: string }[]>(request, 'GET', `/services/${service.id}/domain-bindings`))[0]?.status, { timeout: 120000 }).toBe('ACTIVE')
    await expect.poll(() => {
      try { return execFileSync('curl', ['--noproxy', '*', '--silent', '--show-error', '--fail', '--max-time', '5', '--cacert', cert, '--resolve', `${host}:${f.https_port}:${f.https_address}`, `https://${host}:${f.https_port}/`], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }) } catch { return '' }
    }, { timeout: 60000 }).toContain('Welcome to nginx!')
    const ingress = docker('network', 'inspect', 'nsc-workspace-ingress', '--format', '{{.Id}}')
    expect(networks('web').map(n => n.Target)).toEqual(expect.arrayContaining([ingress, networkID, defaultID]))
    expect(networks('database').some(n => n.Target === ingress)).toBe(false)
    await page.reload(); await expect(panel.locator('.binding-row')).toContainText(host)
    const first = (await api<{ domain: Entity }[]>(request, 'GET', `/services/${service.id}/domain-bindings`))[0]!
    await api(request, 'POST', `/services/${service.id}/domain-bindings`, { domain_id: first.domain.id, route: { path: '/second', backend: { port: 80, protocol: 'http' } } }, { 'Idempotency-Key': `second-${suffix}` })
    await expect.poll(async () => (await api<{ status: string }[]>(request, 'GET', `/services/${service.id}/domain-bindings`)).filter(b => b.status === 'ACTIVE').length, { timeout: 120000 }).toBe(2)
    await page.reload(); await expect(panel.locator('.binding-row')).toHaveCount(2)
    page.once('dialog', dialog => dialog.accept())
    await panel.locator('.binding-row').first().locator('button').last().click()
    await expect.poll(async () => (await api<{ status: string }[]>(request, 'GET', `/services/${service.id}/domain-bindings`)).filter(b => b.status === 'ACTIVE').length).toBe(1)
    expect(networks('web').some(n => n.Target === ingress)).toBe(true)
    await page.reload(); await expect(panel.locator('.binding-row').filter({ hasNotText: 'DISABLED' })).toHaveCount(1)
    page.once('dialog', dialog => dialog.accept())
    await panel.locator('.binding-row').filter({ hasNotText: 'DISABLED' }).locator('button').last().click()
    await expect.poll(() => networks('web').some(n => n.Target === ingress), { timeout: 120000 }).toBe(false)
    expect(networks('web').map(n => n.Target)).toEqual(expect.arrayContaining([networkID, defaultID]))
  } finally { rmSync(work, { recursive: true, force: true }) }
})
