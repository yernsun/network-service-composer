import { test, expect } from '@playwright/test'
import { readFileSync } from 'node:fs'
const locales = JSON.parse(readFileSync(new URL('../../../internal/i18n/locales.json', import.meta.url), 'utf8')) as { code: string }[]
if (process.env.NSC_SELF_SIGNED_BROWSER !== '1') throw new Error('Run using the isolated TestSelfSignedCertificateBrowser fixture')
test('issues a twenty-year asset, refreshes metadata and displays all locales', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('/login')
  await page.locator('#username').fill('admin'); await page.locator('#password').fill('test-admin-token')
  await page.locator('button[type=submit]').click(); await expect(page).toHaveURL(/\/projects$/)
  await page.goto('/projects/all/environments/all/certificates?perspective=ops')
  const panel = page.getByTestId('self-signed-certificate')
  await panel.getByRole('button').click()
  await panel.getByTestId('self-signed-pool').selectOption({ label: 'Self-signed fixture' })
  await panel.getByTestId('self-signed-domains').fill('app.example.test, other.example.test')
  await expect(panel.getByTestId('self-signed-years')).toHaveValue('20')
  const issued = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/certificates/self-signed'))
  await panel.getByTestId('self-signed-submit').click()
  const response = await issued; expect(response.status()).toBe(201)
  const { data: certificate } = await response.json()
  expect(new Date(certificate.not_after).getUTCFullYear()).toBe(new Date().getUTCFullYear() + 20)
  expect(certificate.provider).toBe('self-signed'); expect(certificate.private_key_pem).toBeUndefined()
  await expect(page.locator(`[data-certificate-id="${certificate.id}"]`)).toContainText('other.example.test')
  await page.reload()
  await expect(page.locator(`[data-certificate-id="${certificate.id}"]`)).toBeVisible()
  for (const { code } of locales) {
    await page.locator('#workspace-language').selectOption(code)
    await expect(panel).not.toContainText('selfSigned.')
    await panel.getByRole('button').click()
    await expect(panel).not.toContainText('undefined')
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 })
      expect(await panel.evaluate(e => e.getBoundingClientRect().width <= window.innerWidth)).toBe(true)
    }
    await panel.getByRole('button').last().click()
  }
  expect(errors).toEqual([])
})
