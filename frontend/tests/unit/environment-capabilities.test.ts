import { ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { session } from '../../src/shared/api/client'
import EnvironmentCapabilities from '../../src/components/EnvironmentCapabilities.vue'

const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ session: ref({ authenticated: true, user_id: 'admin', role: 'administrator' }), request: send, errorText: () => 'unavailable' }))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))
const cleanup: (() => void)[] = []
beforeEach(() => { send.mockReset(); session.value.authenticated = true })
afterEach(() => { for (const dispose of cleanup.splice(0)) dispose() })
function mountPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  const wrapper = mount(EnvironmentCapabilities, { props: { environmentId: 'env-one' }, global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } })
  cleanup.push(() => { wrapper.unmount(); client.clear() })
  return wrapper
}
const result = { environment_id: 'env-one', runtime_id: 'runtime-one', management: { state: 'CONFIGURED' }, image_updates: { state: 'BASELINE_READY' }, services: [] }

it('shows configuration boundaries and links without claiming live deployment readiness', async () => {
  send.mockResolvedValue({ data: result })
  const wrapper = mountPanel(); await flushPromises()
  await vi.waitFor(() => expect(wrapper.text()).toContain('capabilities.baselineReady'))
  expect(wrapper.text()).toContain('capabilities.boundary')
  expect(send.mock.calls[0]![2]).toMatchObject({ cache: 'no-store', params: { path: { id: 'env-one' } } })
  expect(send.mock.calls[0]![2].signal).toBeInstanceOf(AbortSignal)
  await wrapper.findAll('button')[1]!.trigger('click')
  expect(wrapper.emitted('configure')).toEqual([['runtimes']])
})

it('invalidates on resource mutations and replaces cached success with unknown after a failed refresh', async () => {
  send.mockResolvedValue({ data: result })
  const wrapper = mountPanel(); await flushPromises()
  await vi.waitFor(() => expect(wrapper.text()).toContain('capabilities.baselineReady'))
  send.mockRejectedValue(new Error('failure'))
  window.dispatchEvent(new Event('nsc:resources-changed')); await flushPromises()
  await vi.waitFor(() => expect(wrapper.text()).toContain('capabilities.unknown'))
  expect(wrapper.text()).not.toContain('capabilities.baselineReady')
})

it('does not show old environment results after switching while a request is pending', async () => {
  let finish!: (value: unknown) => void
  send.mockImplementation((_method, _path, options) => options.params.path.id === 'env-one' ? new Promise(resolve => { finish = resolve }) : Promise.resolve({ data: { ...result, image_updates: { state: 'BLOCKED', reason: 'adoption_required' } } }))
  const wrapper = mountPanel(); await flushPromises()
  await wrapper.setProps({ environmentId: 'env-two' }); await flushPromises()
  finish({ data: result }); await flushPromises()
  await vi.waitFor(() => expect(wrapper.text()).toContain('capabilities.reason.adoption_required'))
  expect(wrapper.text()).not.toContain('capabilities.baselineReady')
})

it('explains management restrictions and emits application navigation', async () => {
  send.mockResolvedValue({ data: { ...result, management: { state: 'BLOCKED', reason: 'management.disabled' }, image_updates: { state: 'BLOCKED' } } })
  const wrapper = mountPanel(); await flushPromises()
  await vi.waitFor(() => expect(wrapper.text()).toContain('capabilities.reason.management_disabled'))
  expect(wrapper.text()).toContain('capabilities.reason.baseline_required')
  await wrapper.findAll('button')[2]!.trigger('click')
  expect(wrapper.emitted('configure')).toEqual([['applications']])
})


it('hides cached environment evidence immediately when the session expires', async () => {
  send.mockResolvedValue({ data: result })
  const wrapper = mountPanel(); await flushPromises()
  await vi.waitFor(() => expect(wrapper.find('dl').exists()).toBe(true))
  session.value.authenticated = false; await flushPromises()
  expect(wrapper.find('dl').exists()).toBe(false)
  expect(wrapper.find('button').attributes('disabled')).toBeDefined()
  const calls = send.mock.calls.length
  window.dispatchEvent(new Event('nsc:resources-changed')); await flushPromises()
  expect(send.mock.calls).toHaveLength(calls)
})
