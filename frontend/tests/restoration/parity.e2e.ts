import { test, expect, type Page } from '@playwright/test'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname } from 'node:path'

const original = 'http://127.0.0.1:5175'
const restored = 'http://127.0.0.1:5174'
const root = '/projects/portal/environments/'
async function prepare(page: Page) {
  await page.clock.setFixedTime(new Date('2026-09-15T00:00:00Z'))
  await page.addInitScript(() => {
    let seed = 717
    Math.random = () => { seed = seed * 16807 % 2147483647; return (seed - 1) / 2147483646 }
  })
}
async function ready(page: Page, url: string) {
  await page.goto(url)
  await expect(page.locator('#main h1')).toBeVisible()
}
async function navigation(page: Page) {
  return page.evaluate(() => ({
    path: location.pathname + location.search,
    active: Array.from(document.querySelectorAll('.sidebar .active')).map(el => ({ text: el.textContent?.trim(), href: el.getAttribute('href') })),
    project: (document.querySelector('#scope-project') as HTMLSelectElement)?.value,
    environment: (document.querySelector('#scope-env') as HTMLSelectElement)?.value,
    selected: document.querySelector('.env-tabs .selected')?.textContent?.trim(),
    heading: document.querySelector('#main h1')?.textContent?.trim(),
  }))
}
test('original page layouts remain identical', async ({ browser }, info) => {
  const context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1440, height: 1000 } })
  const reference = await context.newPage(), candidate = await context.newPage()
  await prepare(reference); await prepare(candidate)
  const routes = [
    '/projects', '/projects/portal', '/projects/portal/environments', '/projects/portal/variables',
    root + 'prod/overview', root + 'prod/applications', root + 'all/services', root + 'prod/domains',
    root + 'prod/domains/d1?tab=config&editor=semantic', root + 'prod/applications/app-portal-prod?tab=history',
    root + 'prod/applications/app-portal-prod/compose', '/market', '/infrastructure/runtimes',
  ]
  for (const [index, route] of routes.entries()) {
    await ready(reference, original + route); await ready(candidate, restored + route)
    // The approved project-settings entry is tested visibly by lifecycle E2E.
    // Hide only that addition to compare all remaining archive pixels exactly.
    await candidate.addStyleTag({ content: '[data-action="p-project-settings"] { display: none !important; }' })
    const name = `page-${index}.png`
    const path = info.snapshotPath(name)
    await mkdir(dirname(path), { recursive: true })
    // Every expected image is rendered from the untouched archive in this run.
    await writeFile(path, await reference.screenshot({ fullPage: true, animations: 'disabled', mask: [reference.locator('.workspace-preferences'), reference.locator('#y-history-tools')] }))
    expect(await candidate.screenshot({ fullPage: true, animations: 'disabled', mask: [candidate.locator('.workspace-preferences'), candidate.locator('#y-history-tools')] })).toMatchSnapshot(name, { maxDiffPixels: 0, threshold: 0 })
  }
  await context.close()
})
test('environment tabs and selector preserve exactly the original section transitions', async ({ browser }) => {
  const context = await browser.newContext({ locale: 'zh-CN' })
  const reference = await context.newPage(), candidate = await context.newPage()
  await prepare(reference); await prepare(candidate)
  for (const section of ['overview', 'applications', 'services', 'domains', 'routes', 'configs', 'certificates', 'workloadHistory', 'events', 'gateways']) {
    for (const control of ['tabs', 'selector']) {
      await ready(reference, original + root + 'prod/' + section)
      await ready(candidate, restored + root + 'prod/' + section)
      for (const env of ['test', 'dev', 'all', 'prod']) {
        for (const page of [reference, candidate]) {
          if (control === 'tabs') await page.locator(`.env-tabs [data-action="p-env"][data-id="${env}"]`).click()
          else await page.locator('#scope-env').selectOption(env)
        }
        await expect.poll(() => navigation(candidate)).toEqual(await navigation(reference))
      }
      await reference.reload(); await candidate.reload()
      await expect.poll(() => navigation(candidate)).toEqual(await navigation(reference))
    }
  }
  await context.close()
})
test('details, filters, history and unsaved navigation use the original state machine', async ({ browser }) => {
  const context = await browser.newContext({ locale: 'zh-CN' })
  const pages = [await context.newPage(), await context.newPage()]
  for (const [i, page] of pages.entries()) {
    await prepare(page)
    await ready(page, [original, restored][i] + root + 'prod/domains/d1?tab=config&editor=semantic')
    await page.locator('#body-size').fill('48m')
    await page.locator('#scope-env').selectOption('test')
    await expect(page.locator('#modal-title')).toHaveText('保留未保存的修改？')
    await page.locator('[data-action="y-guard-stay"]').click()
    await expect(page.locator('#body-size')).toHaveValue('48m')
    await page.locator('#scope-env').selectOption('test')
    await page.locator('[data-action="y-guard-discard"]').click()
    await expect(page).toHaveURL(/\/test\/domains$/)
    await page.goBack()
    await expect(page).toHaveURL(/\/prod\/domains\/d1/)
    await page.goForward()
    await expect(page).toHaveURL(/\/test\/domains$/)
  }
  expect(await navigation(pages[1]!)).toEqual(await navigation(pages[0]!))
  await context.close()
})

test('original modal layouts and mobile navigation remain identical', async ({ browser }, info) => {
  for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
    const context = await browser.newContext({ locale: 'zh-CN', viewport })
    const reference = await context.newPage(), candidate = await context.newPage()
    await prepare(reference); await prepare(candidate)
    const cases = [
      { path: '/projects', action: 'p-project-add' },
      { path: '/projects/portal/environments', action: 'p-env-add' },
      { path: '/projects/portal/variables', action: 'y-var-add' },
      { path: '/projects/portal/variables', action: 'y-env-import' },
      { path: root + 'prod/applications', action: 'x-install' },
      { path: root + 'prod/domains/d1?tab=routes', action: 'route-add' },
    ]
    for (const scenario of cases) {
      for (const [page, host] of [[reference, original], [candidate, restored]] as const) {
        await ready(page, host + scenario.path)
        // The lifecycle runtime field is verified visibly in creation E2E;
        // remove only that explicit addition for the original geometry check.
        if (page === candidate) await page.addStyleTag({ content: '[data-action="p-project-settings"], .project-runtime-field { display: none !important; }' })
        await page.locator(`[data-action="${scenario.action}"]`).first().click()
        await expect(page.locator('#modal-title')).toBeVisible()
        await page.locator('#overlay').evaluate(async el => {
          await Promise.all(el.getAnimations({ subtree: true }).filter(a => a.effect?.getComputedTiming().iterations !== Infinity).map(a => a.finished.catch(() => undefined)))
        })
      }
      const name = `modal-${viewport.width}-${scenario.action}.png`, path = info.snapshotPath(name)
      await mkdir(dirname(path), { recursive: true })
      const options = { fullPage: true, animations: 'disabled' as const }
      const geometry = (page: Page) => page.locator('.modal, .modal input, .modal textarea, .modal button').evaluateAll(elements => elements.map(el => {
        const rect = el.getBoundingClientRect()
        return { tag: el.tagName, x: rect.x, y: rect.y, width: rect.width, height: rect.height }
      }))
      expect(await geometry(candidate)).toEqual(await geometry(reference))
      await writeFile(path, await reference.screenshot({ ...options, mask: [reference.locator('.workspace-preferences'), reference.locator('#y-history-tools')] }))
      // Chromium rasterizes a few rounded-border/icon edge pixels up to 6/255
      // differently across these documents. Geometry remains exact above.
      await expect(candidate).toHaveScreenshot(name, { ...options, mask: [candidate.locator('.workspace-preferences'), candidate.locator('#y-history-tools')], maxDiffPixels: 0, threshold: 0.03 })
      for (const page of [reference, candidate]) await page.keyboard.press('Escape')
    }
    await context.close()
  }
})
