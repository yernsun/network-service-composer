import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { ref } from 'vue'
import HAOverview from '../../src/views/HAOverview.vue'
import { request, session } from '../../src/shared/api/client'
import { i18n, setLocale } from '../../src/shared/i18n'

vi.mock('../../src/shared/api/client', () => ({ request: vi.fn(), session: ref({ authenticated: true, role: 'administrator', user_id: 'admin' }), errorText: () => 'Read unavailable' }))
let wrapper: VueWrapper | undefined
let client: QueryClient
const evidence = () => ({ generated_at: '2026-10-21T00:00:00Z', end_to_end_verified: false, layers: [
  { name: 'controller', state: 'READY', evidence: [{ id: 'controller', state: 'READY', reason: 'leader_scheduling', source: 'live_term_and_readiness', observed_at: '2026-10-21T00:00:00Z' }] },
  { name: 'execution', state: 'UNKNOWN', evidence: [] },
  { name: 'swarm_quorum', state: 'UNKNOWN', evidence: [] },
  { name: 'gateway', state: 'READY', evidence: [] },
  { name: 'ingress', state: 'UNKNOWN', evidence: [{ id: 'external', state: 'UNKNOWN', reason: 'external_ingress_not_observed', source: 'external_monitor_required' }] },
  { name: 'storage', state: 'LIMITED', evidence: [{ id: 'postgres', state: 'LIMITED', reason: 'storage_failover_unverified', source: 'database_ping' }] },
] })
beforeEach(() => { setLocale('en'); vi.mocked(request).mockReset(); session.value = { authenticated: true, role: 'administrator', user_id: 'admin', locale: 'en', supported_languages: [] }; client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } }); vi.mocked(request).mockResolvedValue({ data: evidence(), response: new Response() }) })
afterEach(() => { wrapper?.unmount(); wrapper = undefined; client.clear() })
function show() { wrapper = mount(HAOverview, { global: { plugins: [i18n, [VueQueryPlugin, { queryClient: client }]] } }); return wrapper }

it('keeps configuration evidence separate from external ingress and storage acceptance', async () => {
  show(); await flushPromises()
  expect(wrapper!.findAll('[data-layer]')).toHaveLength(6)
  expect(wrapper!.get('[data-layer=controller]').attributes('data-state')).toBe('READY')
  expect(wrapper!.get('[data-layer=ingress]').attributes('data-state')).toBe('UNKNOWN')
  expect(wrapper!.get('[data-layer=storage]').attributes('data-state')).toBe('LIMITED')
  expect(wrapper!.text()).toContain('not end-to-end HA acceptance')
  expect(vi.mocked(request).mock.calls[0]).toEqual(['get', '/system/ha', { signal: expect.any(AbortSignal), cache: 'no-store' }])
})
it('hides cached health when refresh fails and permits an explicit retry', async () => {
  show(); await flushPromises(); vi.mocked(request).mockRejectedValueOnce(new Error('unavailable'))
  await wrapper!.get('button').trigger('click'); await flushPromises()
  expect(wrapper!.find('[data-layer]').exists()).toBe(false)
  expect(wrapper!.get('[role=alert]').text()).toBe('Read unavailable')
  await wrapper!.get('button').trigger('click'); await flushPromises()
  expect(wrapper!.findAll('[data-layer]')).toHaveLength(6)
})
it('removes cached infrastructure data as soon as authority is lost', async () => {
  show(); await flushPromises(); session.value = { ...session.value, role: 'viewer' }; await flushPromises()
  expect(wrapper!.find('[data-layer]').exists()).toBe(false)
  expect(wrapper!.get('button').attributes('disabled')).toBeDefined()
  session.value = { ...session.value, role: 'administrator', authenticated: false }; await flushPromises()
  expect(wrapper!.find('[data-layer]').exists()).toBe(false)
})
it('does not read HA information for an unauthorized session', async () => {
  session.value = { ...session.value, authenticated: false }; show(); await flushPromises()
  expect(request).not.toHaveBeenCalled()
})
it.each(['en', 'ja', 'de', 'zh-CN', 'zh-TW', 'fr', 'ru', 'es'])('renders reviewed HA labels in %s', async locale => {
  setLocale(locale); show(); await flushPromises()
  expect(wrapper!.text()).not.toMatch(/ha\.(title|layer|source|reason|state)/)
  expect(wrapper!.get('h1').text().length).toBeGreaterThan(3)
})
