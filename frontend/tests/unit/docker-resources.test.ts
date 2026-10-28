import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import DockerResources from '../../src/components/DockerResources.vue'
import { request } from '../../src/shared/api/client'

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('../../src/shared/api/client', () => ({
  ApiRequestError: class extends Error {}, asRows: (data: unknown) => Array.isArray(data) ? data : [], request: vi.fn(), errorText: (error: Error) => error.message,
  terminal: (value: string) => value === 'SUCCEEDED' || value === 'FAILED',
  session: ref({ role: 'administrator' }),
}))

let wrapper: ReturnType<typeof mount> | undefined
const resource: { id: string; kind: string; name: string; owned: boolean; created_at: string; used_by: string[] } = { id: 'docker-id', kind: 'config', name: 'app.conf', owned: true, created_at: '2026-09-26T00:00:00Z', used_by: [] }

beforeEach(() => {
  vi.mocked(request).mockReset()
  vi.mocked(request).mockImplementation(async (method, path) => {
    if (method === 'get' && path === '/runtimes/{id}') return { data: { name: 'isolated', runtime_type: 'swarm' } } as never
    if (method === 'get' && path === '/runtimes/{id}/docker-resources') return { data: [resource], pagination: { next_cursor: '' } } as never
    if (method === 'get' && path === '/operations/{id}') return { data: { status: 'SUCCEEDED' } } as never
    return { data: { id: 'op-resource' } } as never
  })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined })

it('creates an immutable resource through the async operation without exposing its content in the list', async () => {
  wrapper = mount(DockerResources, { props: { runtimeId: 'runtime-1' } })
  await flushPromises()
  expect(wrapper.text()).toContain('app.conf')
  await wrapper.find('input[maxlength="255"]').setValue('new.conf')
  await wrapper.find('textarea').setValue('private-data')
  await wrapper.find('.resource-create button.primary').trigger('click')
  await flushPromises()
  const call = vi.mocked(request).mock.calls.find(([method]) => method === 'post')
  expect(call?.[2]?.headers?.['Idempotency-Key']).toBeTruthy()
  expect(call?.[2]?.body).toEqual({ kind: 'config', name: 'new.conf', content_base64: btoa('private-data') })
  expect(wrapper.text()).not.toContain('private-data')
  expect(wrapper.text()).toContain('op-resource')
})

it('requires explicit restore-risk acknowledgement and blocks deletion of a resource used by a service', async () => {
  resource.used_by = ['live-service']
  wrapper = mount(DockerResources, { props: { runtimeId: 'runtime-1' } })
  await flushPromises()
  expect(wrapper.find('.resource-list button.danger').attributes('disabled')).toBeDefined()
  await wrapper.find('.restore-warning input').setValue(true)
  expect(wrapper.find('.resource-list button.danger').attributes('disabled')).toBeDefined()
  expect(vi.mocked(request).mock.calls.filter(([method]) => method === 'delete')).toHaveLength(0)
  resource.used_by = []
})

it('opens Docker volumes directly and does not describe external volumes as immutable read-only configs', async () => {
  vi.mocked(request).mockImplementation(async (_method, path) => path === '/runtimes/{id}' ? { data: { name: 'standalone', runtime_type: 'docker' } } as never : { data: [], observation: { complete: true, observed_at: '' } } as never)
  wrapper = mount(DockerResources, { props: { runtimeId: 'docker' }, global: { plugins: [[VueQueryPlugin, { queryClient: new QueryClient({ defaultOptions: { queries: { gcTime: 0, retry: false } } }) }]] } })
  await flushPromises()
  expect(wrapper.find('h1').text()).toBe('Volumes · standalone')
  expect(wrapper.find('.volumes').exists()).toBe(true)
  expect(wrapper.text()).not.toContain('External resources are read only')
  expect(wrapper.find('nav button[aria-pressed=true]').text()).toBe('Volumes')
})
