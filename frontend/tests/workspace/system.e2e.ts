import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
const locales = JSON.parse(readFileSync(new URL('../../../internal/i18n/locales.json', import.meta.url), 'utf8')) as { code: string }[]
if (!['empty', 'system'].includes(process.env.NSC_SYSTEM_PROFILE || '')) throw new Error('Run through TestSystemProjectBrowser with isolated API fixtures')
test('real HTTP: protected Gateway references and zero-project navigation', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('/login')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill('test-admin-token')
  await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/projects$/)
  await expect(page.locator('#main h1')).toBeVisible()
  if (process.env.NSC_SYSTEM_PROFILE === 'system') {
    await expect(page.locator('#main')).toContainText('Gateway')
    await page.locator('[data-action=p-project-settings]').click()
    await expect(page.locator('#project-settings-name')).toBeDisabled()
    await expect(page.locator('#project-settings-name')).toHaveValue('Gateway')
    await page.keyboard.press('Escape')
    for (const [id, name, env] of [['controller', 'Controller', 'env_system'], ['postgresql', 'PostgreSQL', 'env_system'], ['nginx', 'NGINX', 'env_system'], ['nginx-sandbox', 'NGINX Sandbox', 'env_sandbox']] as const) {
      await page.goto(`/projects/prj_system/environments/${env}/applications`)
      await expect(page.locator('#main')).toContainText(name)
      await page.goto(`/projects/prj_system/environments/${env}/applications/app_${id}`)
      await expect(page.locator('#main h1')).toHaveText(name)
      await expect(page.locator('#main')).not.toContainText('undefined')
      await page.reload()
      await expect(page.locator('#main h1')).toHaveText(name)
      await page.screenshot({ path: `${process.env.NSC_SYSTEM_REPORT}/${id}.png`, fullPage: true })
    }
    const services = '/projects/prj_system/environments/env_system/services'
    await page.goto(services)
    const link = page.locator('#main .row-link[data-action="x-service"][data-id="svc_system"]')
    await expect(link).toContainText('Controller')
    await link.click()
    await expect(page).toHaveURL(/\/services\/svc_system(?:\?|$)/)
    await expect(page.locator('#main h1')).toHaveText('Controller')
    await page.reload()
    await expect(page.locator('#main h1')).toHaveText('Controller')
    await page.goBack()
    await expect(link).toBeVisible()
    await page.goForward()
    await expect(page.locator('#main h1')).toHaveText('Controller')
  }
  for (const { code } of locales) {
    await page.locator('#workspace-language').selectOption(code)
    for (const section of ['routes', 'services', 'applications']) {
      const path = `/projects/all/environments/all/${section}`
      await page.goto(path)
      await expect(page.locator('#main h1')).toBeVisible()
      await expect(page).toHaveURL(path)
      await expect(page.locator('#main')).not.toContainText('undefined')
      await expect(page.locator('a[href*="undefined"]')).toHaveCount(0)
    }
    await page.goto('/projects/missing/environments/missing/routes')
    await expect(page.locator('#main h1')).toBeVisible()
    await expect(page.locator('#main')).not.toContainText('undefined')
    await expect(page).toHaveURL('/projects/missing/environments/missing/routes')
    await page.reload()
    await expect(page.locator('#main')).not.toContainText('undefined')
  }
  await page.screenshot({ path: `${process.env.NSC_SYSTEM_REPORT}/final.png`, fullPage: true })
  expect(errors).toEqual([])
})
