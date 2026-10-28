import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
const enabled = process.env.NSC_PORT_ACCEPTANCE === 'true'
const fixture = enabled ? JSON.parse(readFileSync(process.env.NSC_WORKSPACE_FIXTURE!, 'utf8')) : null

test('port publication trial: browser review and actual TCP listener', async ({ page, request }, testInfo) => {
 test.skip(!enabled, 'Requires a disposable Docker daemon')
 test.setTimeout(240000)
 const f = fixture, admin = process.env.E2E_ADMIN_PASSWORD!, errors: string[] = []
 page.on('pageerror', error => errors.push(error.message))
 const docker = (...args: string[]) => execFileSync('docker', ['--host', f.private_docker, ...args], { encoding: 'utf8' }).trim()
 expect(docker('info', '--format', '{{.ID}}')).toBe(f.private_daemon_id)
 const api = async (method: string, path: string, data?: unknown) => {
  const response = await request.fetch(`/api/v1${path}`, { method, headers: { Authorization: `Bearer ${admin}` }, data })
  expect(response.ok(), await response.text()).toBe(true)
  return (await response.json()).data
 }
 const slug = `ports-${Date.now()}`
 const project = await api('POST', '/projects', { name: slug, slug })
 const env = await api('POST', `/projects/${project.id}/environments`, { name: 'trial', runtime_id: f.runtime.id })
 await page.goto('/login'); await page.locator('#username').fill('admin'); await page.locator('#password').fill(admin)
 await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
 await page.goto(`/projects/${project.id}/environments/${env.id}/applications`)
 await page.locator('[data-action=x-install]').first().click()
 await page.locator('[data-action=x-wsource][data-id=image]').click()
 await page.locator('#x-w-name').fill('tcp-demo'); await page.locator('#x-w-key').fill('tcp-demo')
 await page.locator('#x-w-image').fill(f.image); await page.locator('#x-w-port').fill('0')
 await page.locator('#x-w-primary').click(); await page.locator('#x-w-primary').click()
 if (f.swarm) await page.locator('#x-w-replicas').fill('2')
 const ports = page.getByTestId('deployment-ports')
 await expect(ports).toBeVisible()
 await ports.locator('.ports-heading button').click()
 await ports.locator('[data-port=published]').fill('70000')
 await ports.locator('[data-port=target]').fill('80')
 await ports.locator('.ports-form .primary').click()
 await expect(ports.locator('[role=alert]')).toBeVisible()
 await ports.locator('[data-port=published]').fill('13306')
 await ports.locator('.ports-form .primary').click()
 await expect(ports.locator('.port-row')).toContainText('13306 → 80 / TCP')
 await page.setViewportSize({ width: 390, height: 844 })
 await expect(ports.locator('.port-row')).toBeVisible()
 expect(await ports.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
 await page.setViewportSize({ width: 1280, height: 960 })
 await page.locator('#x-w-primary').click()
 await expect(ports.locator('.port-row')).toContainText('13306 → 80 / TCP')
 await page.locator('[data-wf=reviewAck]').check()
 if (await page.locator('[data-wf=warningAck]').isVisible()) await page.locator('[data-wf=warningAck]').check()
 await expect(page.locator('#x-w-primary')).toBeEnabled({ timeout: 45000 })
 const deploymentPath = f.swarm ? 'service-deployments' : 'deployments'
 const responsePromise = page.waitForResponse(r => r.url().endsWith(`/environments/${env.id}/${deploymentPath}`) && r.request().method() === 'POST')
 await page.locator('#x-w-primary').click()
 const response = await responsePromise
 expect(response.ok(), await response.text()).toBe(true)
 const operation = (await response.json()).data
 let finished = operation
 await expect.poll(async () => {
  finished = await api('GET', `/operations/${operation.id}`)
  return ['SUCCEEDED', 'FAILED', 'ROLLED_BACK', 'ROLLBACK_FAILED'].includes(finished.status)
 }, { timeout: 120000 }).toBe(true)
 await testInfo.attach('deployment-result', { body: JSON.stringify(finished, null, 2), contentType: 'application/json' })
 expect(finished.status, finished.log || finished.error_message).toBe('SUCCEEDED')
 if (f.swarm) {
  const service = JSON.parse(docker('service', 'inspect', `${env.project_name}_tcp-demo`))[0]
  expect(service.Spec.EndpointSpec.Ports).toContainEqual({ Protocol: 'tcp', TargetPort: 80, PublishedPort: 13306, PublishMode: 'ingress' })
  const nodes = JSON.parse(docker('node', 'inspect', ...docker('node', 'ls', '-q').split('\n')))
  expect(nodes.length).toBe(2)
  for (const node of nodes) {
   await expect.poll(async () => {
    try { return (await request.get(`http://${node.Status.Addr}:13306`, { timeout: 5000 })).text() } catch { return '' }
   }, { timeout: 30000 }).toContain('Welcome to nginx!')
  }
 } else {
  const id = docker('ps', '-q', '--filter', `label=com.docker.compose.project=${env.project_name}`, '--filter', 'label=com.docker.compose.service=tcp-demo')
  const container = JSON.parse(docker('inspect', id))[0]
  expect(container.HostConfig.PortBindings['80/tcp'][0].HostPort).toBe('13306')
 }
 await expect.poll(async () => {
  try { return (await request.get(`http://${f.https_address}:13306`, { timeout: 5000 })).text() } catch { return '' }
 }, { timeout: 30000 }).toContain('Welcome to nginx!')
 // Bypassing the form cannot admit an invalid published port.
 const invalid = await api('POST', `/environments/${env.id}/compose-validation`, { compose_yaml: `services: {invalid: {image: '${f.image}', ports: [{target: 3306, published: '65536'}]}}` })
 expect(invalid.deployable).toBe(false)
 expect(invalid.diagnostics.some((d: { code: string }) => d.code === 'COMPOSE_PORT_INVALID')).toBe(true)
 expect(errors).toEqual([])
})
