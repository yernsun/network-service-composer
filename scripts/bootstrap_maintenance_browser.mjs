import { chromium } from '../frontend/node_modules/playwright/index.mjs'
import assert from 'node:assert/strict'
let raw = ''; for await (const chunk of process.stdin) raw += chunk
const input = JSON.parse(raw); raw = ''
const diagnostics = { errors: [], installation: null, operation: null }
let page
const browser = await chromium.launch({ executablePath: process.env.CHROME_BIN || undefined, args: ['--no-sandbox'], headless: true })
try {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, locale: 'en-US', viewport: { width: 1280, height: 900 } })
  page = await context.newPage()
  page.on('pageerror', error => diagnostics.errors.push(error.message))
  page.on('response', async response => {
    const path = new URL(response.url()).pathname
    if (!path.startsWith('/api/v1/system/installation')) return
    const body = await response.json().catch(() => ({}))
    if (path.includes('/operations/')) { diagnostics.operation = { status: response.status(), state: body.data?.state, reason: body.data?.reason }; return }
    if (path !== '/api/v1/system/installation') return
    diagnostics.installation = { status: response.status(), health: body.data?.health, drift: body.data?.drift, allowed: body.data?.allowed_operations, code: body.code }
  })
  await page.goto(input.url + '/login')
  await page.locator('#username').fill('admin'); await page.locator('#password').fill(input.password)
  await Promise.all([page.waitForURL(url => url.pathname !== '/login', { timeout: 30000 }), page.locator('.auth-card button[type=submit]').click()])
  const cookie = (await context.cookies()).find(value => value.name === 'nsc_session')
  assert(cookie?.secure && cookie.httpOnly)
  await page.goto(input.url + '/infrastructure/installation')
  try {
    await page.locator('.system-installation select').waitFor({ timeout: 30000 })
  } catch (error) {
    process.stderr.write(JSON.stringify({ ...diagnostics, path: new URL(page.url()).pathname, page: await page.locator('.system-installation').innerText().catch(() => 'installation page unavailable') }) + '\n')
    throw error
  }
  assert.equal(await page.locator('.sidebar [aria-current=page]').count(), 1)
  await page.locator('.system-installation select').selectOption('upgrade')
  await page.locator('.system-installation textarea').fill(JSON.stringify(input.release))
  const planning = page.waitForResponse(response => response.url().endsWith('/system/installation/plans') && response.request().method() === 'POST')
  await page.locator('.system-installation form button[type=submit]').click()
  const planned = await planning; assert.equal(planned.status(), 201)
  const plan = (await planned.json()).data
  await page.locator('[data-plan] input').fill(plan.hash)
  const submission = page.waitForResponse(response => response.url().endsWith('/system/installation/operations') && response.request().method() === 'POST')
  await page.locator('[data-plan] button').click()
  const accepted = await submission; assert.equal(accepted.status(), 202)
  const job = (await accepted.json()).data
  await page.waitForURL(url => url.searchParams.get('job') === job.id, { timeout: 30000 })
  // Controller replacement may temporarily remove every API listener. The
  // independent runner remains authoritative and the same session must survive.
  await page.locator('[data-operation][data-state=SUCCEEDED], [data-operation][data-state=BLOCKED], [data-operation][data-state=FAILED]').waitFor({ timeout: 420000 })
  assert.equal(await page.locator('[data-operation]').getAttribute('data-state'), 'SUCCEEDED')
  assert.equal((await context.cookies()).find(value => value.name === 'nsc_session')?.value, cookie.value)
  await page.reload()
  await page.locator('[data-operation][data-state=SUCCEEDED]').waitFor({ timeout: 30000 })
  for (const width of [1280, 600]) {
    await page.setViewportSize({ width, height: 900 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1), false, 'system maintenance layout overflow')
  }
  assert.equal(await page.locator('.sidebar [aria-current=page]').count(), 1)
  await context.close()
  process.stdout.write(JSON.stringify({ status: 'PASS', operation_id: job.id, checks: ['approved_real_upgrade', 'independent_progress_after_console_restart', 'unchanged_secure_session', 'reloaded_durable_progress', 'unique_active_navigation', 'desktop_and_narrow_layout'] }))
} catch (error) {
  process.stderr.write(JSON.stringify({ ...diagnostics, path: page ? new URL(page.url()).pathname : null, page: await page?.locator('.system-installation').innerText().catch(() => 'installation page unavailable') }) + '\n')
  throw error
} finally { await browser.close() }
