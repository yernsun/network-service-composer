import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { ref } from 'vue'
import SelfSignedCertificateForm from '../../src/components/SelfSignedCertificateForm.vue'
import { session } from '../../src/shared/api/client'
import { certificateCovers } from '../../src/data/service-bindings'
import type { components } from '../../src/shared/api/schema'

const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, session: ref({ role: 'administrator' }), errorText: (e: Error) => e?.message || '', asRows: (data: unknown) => Array.isArray(data) ? data : [] }))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))
let wrapper: VueWrapper
let query: QueryClient
const asset = { id: 'self', pool_id: 'pool', provider: 'self-signed', status: 'ACTIVE', primary_domain: 'app.example.test', domains: ['app.example.test'], not_before: '2020-01-01T00:00:00Z', not_after: '2046-01-01T00:00:00Z' } as components['schemas']['Certificate']
beforeEach(() => {
  session.value.role = 'administrator'
  send.mockReset().mockImplementation(async (method: string) => ({ data: method === 'get' ? [{ id: 'pool', name: 'Edge' }] : asset, response: new Response('{}') }))
  query = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
})
afterEach(() => { wrapper?.unmount(); query.clear() })
async function open(props = {}) {
  wrapper = mount(SelfSignedCertificateForm, { props, global: { plugins: [[VueQueryPlugin, { queryClient: query }]] } })
  await wrapper.get('button').trigger('click'); await flushPromises()
}
it('defaults to twenty years, creates a selectable asset and refreshes data', async () => {
  await open()
  expect(wrapper.get('[data-testid=self-signed-years]').element).toHaveProperty('value', '20')
  expect(wrapper.text()).toContain('selfSigned.trust')
  expect(wrapper.get('[data-testid=self-signed-submit]').attributes('disabled')).toBeDefined()
  await wrapper.get('select').setValue('pool')
  await wrapper.get('textarea').setValue('app.example.test, other.example.test')
  const refresh = vi.fn(); window.addEventListener('nsc:resources-changed', refresh)
  try {
    await wrapper.get('[data-testid=self-signed-submit]').trigger('click'); await flushPromises()
    expect(send).toHaveBeenCalledWith('post', '/certificates/self-signed', { body: { pool_id: 'pool', domains: ['app.example.test', 'other.example.test'], validity_years: 20 } })
    expect(wrapper.emitted('created')?.[0]).toEqual([asset]); expect(refresh).toHaveBeenCalledOnce()
    expect(certificateCovers(asset, 'app.example.test')).toBe(true)
    expect(certificateCovers({ ...asset, status: 'REVOKED' }, 'app.example.test')).toBe(false)
  } finally { window.removeEventListener('nsc:resources-changed', refresh) }
})
it('keeps input after a failure and allows an explicit validity with a fixed pool', async () => {
  await open({ poolId: 'pool', host: 'app.example.test' })
  expect(send).not.toHaveBeenCalled()
  await wrapper.get('input').setValue(0)
  expect(wrapper.get('[data-testid=self-signed-submit]').attributes('disabled')).toBeDefined()
  await wrapper.get('input').setValue(5)
  send.mockRejectedValueOnce(new Error('unavailable'))
  await wrapper.get('[data-testid=self-signed-submit]').trigger('click'); await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toContain('unavailable')
  expect(wrapper.get('textarea').element).toHaveProperty('value', 'app.example.test')
  await wrapper.get('[data-testid=self-signed-submit]').trigger('click'); await flushPromises()
  expect(send).toHaveBeenLastCalledWith('post', '/certificates/self-signed', { body: { pool_id: 'pool', domains: ['app.example.test'], validity_years: 5 } })
})
it('hides signing from viewers', () => {
  session.value.role = 'viewer'
  wrapper = mount(SelfSignedCertificateForm, { global: { plugins: [[VueQueryPlugin, { queryClient: query }]] } })
  expect(wrapper.find('button').exists()).toBe(false); expect(send).not.toHaveBeenCalled()
})
