import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ref } from 'vue'
import SystemInstallation from '../../src/views/SystemInstallation.vue'
import { request, session } from '../../src/shared/api/client'
import { i18n, setLocale } from '../../src/shared/i18n'
vi.mock('../../src/shared/api/client', () => ({ request: vi.fn(), session: ref({ authenticated: true, role: 'administrator', user_id: 'admin' }), errorText: () => 'Evidence unavailable' }))
const plan = () => ({ installation_id: 'installation-fixture', revision: 2, action: 'repair', hash: 'approved-plan-digest', expires_at: new Date(Date.now() + 600000).toISOString(), scope: { mode: 'single', targets: [{ name: 'node-original', address: '192.0.2.1', node_id: 'node-original-id' }] }, impact: [], steps: [{ key: 'update:controller', state: 'PENDING' }] })
const installed = () => ({ id: 'installation-fixture', revision: 2, mode: 'single', health: 'VERIFIED', state: 'SUCCEEDED', components: { controller: { id: 'controller-original', image: 'registry.example.test/controller@sha256:fixture' } }, topology: { managers: 1, majority: 1 }, console_urls: ['https://192.0.2.1:8443'], database: { node_id: 'original-data-node', volume: 'original-data-volume' }, certificate_id: 'certificate-fixture', console_certificate_expires_at: '2046-11-01T00:00:00Z', drift: [], allowed_operations: ['verify', 'repair', 'upgrade', 'rollback'] })
const job = (state = 'RUNNING') => ({ id: 'maintenance-fixture', state, plan: plan(), updated_at: '2026-11-01T00:00:00Z', operation: { plan: { ...plan(), steps: [{ key: 'update:controller', state }] } } })
let wrapper: VueWrapper | undefined, client: QueryClient
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:all(.*)*', component: { template: '<div />' } }] })
beforeEach(async () => {
  setLocale('en'); session.value = { authenticated: true, role: 'administrator', user_id: 'admin', locale: 'en', supported_languages: [] }
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } }); vi.mocked(request).mockReset()
  vi.mocked(request).mockImplementation(async (method, path) => ({ data: path === '/system/installation' ? installed() : path === '/system/installation/plans' ? plan() : method === 'post' ? job() : job('UNKNOWN'), response: new Response() }))
  await router.replace('/infrastructure/installation')
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; client.clear() })
async function show(path?: string) {
  if (path) await router.replace(path)
  wrapper = mount(SystemInstallation, { global: { plugins: [router, i18n, [VueQueryPlugin, { queryClient: client }]] } }); await flushPromises(); return wrapper
}
it('requires exact approval and submits a stable idempotency key', async () => {
  const view = await show(); expect(view.text()).toContain('PostgreSQL remains a single point of failure')
  await view.get('select').setValue('repair'); await view.get('form').trigger('submit'); await flushPromises()
  const section = view.get('[data-plan]'); expect(section.text()).toContain('approved-plan-digest'); expect(section.text()).toContain('node-original'); expect(section.text()).toContain('interrupts console'); expect(view.text()).toContain('original-data-volume'); expect(section.get('button').attributes('disabled')).toBeDefined()
  await section.get('input').setValue('incorrect'); expect(section.get('button').attributes('disabled')).toBeDefined()
  await section.get('input').setValue('approved-plan-digest'); await section.get('button').trigger('click'); await flushPromises()
  const call = vi.mocked(request).mock.calls.find(([method, path]) => method === 'post' && path === '/system/installation/operations')
  expect(call?.[2]?.body).toEqual({ plan_hash: 'approved-plan-digest', revision: 2 }); expect(call?.[2]?.headers?.['Idempotency-Key']).toBeTruthy()
  expect(router.currentRoute.value.query.job).toBe('maintenance-fixture'); expect(view.find('[data-plan]').exists()).toBe(false)
})
it('reconnects to durable UNKNOWN progress and resumes the recorded operation', async () => {
  const view = await show('/infrastructure/installation?job=maintenance-fixture')
  expect(view.get('[data-operation]').attributes('data-state')).toBe('UNKNOWN'); expect(view.text()).toContain('Outcome unconfirmed')
  await view.get('[data-operation] button').trigger('click'); await flushPromises()
  expect(vi.mocked(request).mock.calls.find(([method, path]) => method === 'post' && path === '/system/installation/operations')?.[2]?.body).toEqual({ operation_id: 'maintenance-fixture', plan_hash: 'approved-plan-digest', revision: 2 })
})
it('preserves release input during refresh and rejects malformed manifests', async () => {
  const view = await show(); await view.get('select').setValue('upgrade'); await view.get('textarea').setValue('{invalid')
  await view.get('form').trigger('submit'); await flushPromises(); expect(view.text()).toContain('Enter a valid release manifest')
  expect(vi.mocked(request).mock.calls.filter(([, path]) => path === '/system/installation/plans')).toHaveLength(0)
  await view.get('button').trigger('click'); await flushPromises(); expect((view.get('textarea').element as HTMLTextAreaElement).value).toBe('{invalid')
  await view.get('textarea').setValue('[]'); await view.get('form').trigger('submit'); await flushPromises(); expect(view.find('[data-plan]').exists()).toBe(false)
  await view.get('textarea').setValue('{"version":"v0.1.0"}'); await view.get('form').trigger('submit'); await flushPromises(); expect(view.find('[data-plan]').exists()).toBe(true)
})
it('hides cached installation and success after read failure or lost authority', async () => {
  const view = await show('/infrastructure/installation?job=maintenance-fixture'); vi.mocked(request).mockRejectedValue(new Error('unavailable'))
  await view.get('button').trigger('click'); await flushPromises()
  expect(view.find('[data-operation]').exists()).toBe(false); expect(view.text()).not.toContain('controller-original'); expect(view.text()).toContain('Evidence unavailable')
  session.value = { ...session.value, role: 'viewer' }; await flushPromises(); expect(view.find('form').exists()).toBe(false); expect(view.get('button').attributes('disabled')).toBeDefined()
})
it('shows awaiting adoption and does not read maintenance for a viewer', async () => {
  vi.mocked(request).mockResolvedValueOnce({ data: { ...installed(), id: '', allowed_operations: [], drift: ['awaiting_adoption'] }, response: new Response() })
  const view = await show(); expect(view.text()).toContain('Awaiting verified adoption'); expect(view.find('form').exists()).toBe(false)
  wrapper?.unmount(); wrapper = undefined; vi.mocked(request).mockClear(); session.value = { ...session.value, role: 'viewer' }; await show(); expect(request).not.toHaveBeenCalled()
})
it.each(['en', 'ja', 'de', 'zh-CN', 'zh-TW', 'fr', 'ru', 'es'])('renders reviewed maintenance labels in %s', async locale => {
  setLocale(locale); const view = await show(); expect(view.text()).not.toMatch(/installation\.(title|action|state|limit)/); expect(view.get('h1').text().length).toBeGreaterThan(3)
})
it('keeps recovery of an independent CLI operation in the CLI', async () => {
  vi.mocked(request).mockImplementation(async (_method, path) => ({ data: path === '/system/installation' ? installed() : { ...job('UNKNOWN'), id: 'cli-fixture', execution_mode: 'cli' }, response: new Response() }))
  const view = await show('/infrastructure/installation?job=cli-fixture')
  expect(view.get('[data-operation]').text()).toContain('CLI')
  expect(view.find('[data-operation] button').exists()).toBe(false)
})
