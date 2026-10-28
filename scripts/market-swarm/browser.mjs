import { readFileSync } from 'node:fs'
import { chromium } from '../../frontend/node_modules/playwright/index.mjs'

const browserBase = process.env.NSC_MARKET_BROWSER_BASE
const fixture = JSON.parse(readFileSync(process.argv[2], 'utf8'))
async function api(method, path, body) {
  const response = await fetch(`${fixture.base}/api/v1${path}`, {
    method, headers: { Authorization: `Bearer ${fixture.secret}`, 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  })
  if (!response.ok) throw new Error(`Fixture API ${path} failed: ${response.status}`)
  return (await response.json()).data
}
const runtime = (await api('GET', '/runtimes?limit=100')).find(r => r.name === fixture.prefix)
const catalog = (await api('GET', '/catalog/apps?limit=100')).find(v => v.app_key === 'redis' && v.swarm_yaml)
const slug = `market-browser-${Date.now()}`
const project = await api('POST', '/projects', { name: slug, slug })
const environment = await api('POST', `/projects/${project.id}/environments`, { name: 'market', runtime_id: runtime.id })
const browser = await chromium.launch({ executablePath: process.env.NSC_BROWSER_BINARY || '/usr/bin/google-chrome', args: ['--no-sandbox'] })
try {
  const page = await browser.newPage()
  const errors = [], reviews = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('response', async response => {
    if (response.url().endsWith(`/environments/${environment.id}/preflight`) && response.ok()) reviews.push((await response.json()).data)
  })
  await page.goto(`${browserBase || fixture.base}/login`)
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill(fixture.secret)
  await page.locator('button[type=submit]').click()
  await page.waitForURL(/\/projects$/)
  await page.goto(`${browserBase || fixture.base}/projects/${project.id}/environments/${environment.id}/applications`)
  await page.locator('[data-action=x-install]').first().click()
  await page.locator('#x-w-template').selectOption(catalog.id)
  await page.locator('#x-w-name').fill('Market preview')
  await page.locator('#x-w-key').fill(environment.project_name)
  const password = page.locator('#market-parameter-REDIS_PASSWORD')
  if (await password.getAttribute('type') !== 'password') throw new Error('Marketplace password is unmasked')
  await password.fill(fixture.secret)
  await page.locator('#x-w-primary').click()
  await page.locator('#x-w-runtime').selectOption(runtime.id)
  await page.locator('#market-data-node').selectOption(fixture.bindings[1].node_id)
  await page.locator('#x-w-primary').click()
  await page.locator('[data-wf=storageAck]').check()
  await page.locator('#x-w-primary').click()
  await page.waitForFunction(() => document.querySelector('#market-data-node')?.disabled === true)
  for (let attempt = 0; attempt < 100 && !reviews.length; attempt++) await page.waitForTimeout(100)
  const before = reviews.at(-1)
  if (!before?.resolved_yaml?.includes(`node.id == ${fixture.bindings[1].node_id}`)) throw new Error('Reviewed Swarm template has no selected data node')
  await page.locator('[data-action=x-wback]').click()
  await password.fill(`${fixture.secret}-changed`)
  await page.locator('#x-w-primary').click()
  for (let attempt = 0; attempt < 100 && reviews.at(-1)?.preflight_id === before.preflight_id; attempt++) await page.waitForTimeout(100)
  if (reviews.at(-1)?.preflight_id === before.preflight_id) throw new Error('Changed marketplace parameter reused the old review')
  const leaked = await page.evaluate(value => Object.values(localStorage).some(item => item.includes(value)), fixture.secret)
  if (leaked || errors.length) throw new Error('Marketplace UI persisted a secret or raised a browser error')
  console.log(JSON.stringify({ marketplace_browser_parameters_node_and_review: true }))
} finally { await browser.close() }
