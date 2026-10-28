import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import { ref } from 'vue'
import Volumes from '../../src/components/Volumes.vue'
import { request } from '../../src/shared/api/client'
vi.mock('../../src/shared/api/client', () => ({ ApiRequestError: class extends Error {}, request: vi.fn(), asRows: (d: unknown) => Array.isArray(d) ? d : [], session: ref({ role: 'administrator' }), errorText: (e: Error) => e.message, terminal: (s: string) => ['SUCCEEDED', 'FAILED'].includes(s) }))
let wrapper: ReturnType<typeof mount> | undefined
const volume = { name: 'external-volume', node_id: 'node', engine_id: 'engine', kind: 'unknown', driver: 'local', scope: 'local', created_at: '2026-10-01T00:00:00Z', version: 'version', owned: false, used_by: [], blocked_reasons: [] }
beforeEach(() => {
 vi.mocked(request).mockReset()
 vi.mocked(request).mockImplementation(async (method, path) => {
  if (path === '/runtimes/{id}/cluster/nodes') return { data: [{ id: 'node', name: 'worker', state: 'ready', availability: 'active' }] } as never
  if (path === '/operations/{id}') return { data: { status: 'SUCCEEDED' } } as never
  if (method !== 'get') return { data: { id: 'operation' } } as never
  return { data: path.endsWith('{name}') ? volume : [volume], observation: { complete: true, observed_at: '2026-10-06T00:00:00Z' }, response: new Response(null, { headers: { ETag: '"volume-version"' } }) } as never
 })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined })
async function open(type = 'docker') {
 wrapper = mount(Volumes, { props: { runtimeId: 'rt', runtimeType: type }, global: { plugins: [[VueQueryPlugin, { queryClient: new QueryClient({ defaultOptions: { queries: { gcTime: 0, retry: false } } }) }]] } })
 await flushPromises(); return wrapper
}
it('retries anonymous creation with the same idempotency key after an unknown response', async () => {
 const w = await open(), base = vi.mocked(request).getMockImplementation()!
 let first = true
 vi.mocked(request).mockImplementation(async (method, path, options) => { if (method === 'post' && first) { first = false; throw new Error('lost acknowledgement') } return base(method, path, options) })
 await w.find('form').trigger('submit'); await flushPromises()
 await w.findAll('button').find(b => b.text() === 'Retry the same request')!.trigger('click'); await flushPromises()
 const calls = vi.mocked(request).mock.calls.filter(([m]) => m === 'post')
 expect(calls).toHaveLength(2); expect(calls[0]?.[2]?.body).toEqual({ name: '' })
 expect(calls[0]?.[2]?.headers?.['Idempotency-Key']).toBe(calls[1]?.[2]?.headers?.['Idempotency-Key'])
 expect(w.text()).toContain('operation')
})
it('allows unused external deletion only after fresh detail and acknowledgement', async () => {
 const w = await open('swarm')
 expect(w.find('select').element.value).toBe('node')
 await w.find('li button.danger').trigger('click'); await flushPromises()
 expect(w.find('.detail button.danger').attributes('disabled')).toBeDefined()
 await w.find('.detail input[type=checkbox]').setValue(true); await w.find('.detail button.danger').trigger('click'); await flushPromises()
 const call = vi.mocked(request).mock.calls.find(([m]) => m === 'delete')
 expect(call?.[2]?.params?.query).toEqual({ node_id: 'node' }); expect(call?.[2]?.headers?.['If-Match']).toBe('"volume-version"')
 expect(call?.[2]?.body).toEqual({ acknowledge_data_loss: true })
})
it('keeps incomplete inventories and stopped container references read-only', async () => {
 const base = vi.mocked(request).getMockImplementation()!
 vi.mocked(request).mockImplementation(async (method, path, options) => path === '/runtimes/{id}/volumes' ? { data: [{ ...volume, used_by: [{ kind: 'container', id: 'c', name: 'stopped', state: 'exited' }], blocked_reasons: ['referenced', 'inventory_unknown'] }], observation: { complete: false, observed_at: '' } } as never : base(method, path, options))
 const w = await open(); expect(w.text()).toContain('stopped'); expect(w.find('li button.danger').attributes('disabled')).toBeDefined(); expect(w.find('form button').attributes('disabled')).toBeDefined()
})
it('shows exact ID and data-node constraint for reuse', async () => {
 const w = await open('swarm'); await w.find('li button').trigger('click'); await flushPromises()
 expect(w.find('textarea').element.value).toContain('name: external-volume'); expect(w.find('textarea').element.value).toContain('node.id == node'); expect(w.find('textarea').element.value).toContain('external: true')
})
it('discards a late detail from a previous node', async () => {
 const base = vi.mocked(request).getMockImplementation()!; let finish!: (value: never) => void
 vi.mocked(request).mockImplementation(async (method, path, options) => path === '/runtimes/{id}/volumes/{name}' ? new Promise(resolve => { finish = resolve }) : base(method, path, options))
 const w = await open('swarm'); await w.find('li button').trigger('click'); await flushPromises(); await w.find('select').setValue('')
 finish({ data: volume, response: new Response(), observation: { complete: true } } as never); await flushPromises(); expect(w.find('.detail').exists()).toBe(false)
})
