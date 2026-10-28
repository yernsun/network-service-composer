import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { ref } from 'vue'
import OperationReadiness from '../../src/components/OperationReadiness.vue'
import { i18n, setLocale } from '../../src/shared/i18n'
import { request, session } from '../../src/shared/api/client'

vi.mock('../../src/shared/api/client', () => ({ session: ref({ authenticated: true, user_id: 'admin', role: 'administrator' }), request: vi.fn() }))
let wrapper: VueWrapper | undefined
const client = () => new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
const evidence = (operation = 'op-one') => ({ operation_id: operation, revision: 3, baseline_digest: 'a'.repeat(64), observed_at: '2026-10-13T00:00:00Z', stages: ['accepted', 'workload', 'certificate', 'gateway', 'client_access'].map(name => ({ name, state: name === 'client_access' ? 'UNKNOWN' : 'CONFIRMED', ...(name === 'client_access' ? { error_code: 'client_not_verified' } : {}) })), gateway_targets: [{ gateway_id: 'gateway-one', pool_id: 'pool-one', state: 'CONFIRMED', desired_revision: 7, applied_revision: 7 }], access_targets: [] })
beforeEach(() => { setLocale('en'); vi.mocked(request).mockReset(); session.value.authenticated = true })
afterEach(() => { wrapper?.unmount(); wrapper = undefined })
function show(queryClient = client()) {
  wrapper = mount(OperationReadiness, { props: { operationId: 'op-one' }, global: { plugins: [i18n, [VueQueryPlugin, { queryClient }]] } })
  return wrapper
}

it('keeps loading confirmation and actual client access separate', async () => {
  vi.mocked(request).mockResolvedValue({ data: evidence(), response: new Response() })
  show(); await flushPromises()
  expect(wrapper!.get('[data-stage=gateway]').attributes('data-state')).toBe('CONFIRMED')
  expect(wrapper!.get('[data-stage=client_access]').attributes('data-state')).toBe('UNKNOWN')
  expect(wrapper!.text()).toContain('Actual client access has not been verified')
  expect(vi.mocked(request).mock.calls[0]?.[0]).toBe('get')
})

it('shows retained recovery holds and requires new observations after release', async () => {
  const held = evidence()
  held.stages = held.stages.map(stage => stage.name === 'accepted' ? stage : { ...stage, state: 'UNKNOWN', error_code: 'recovery_held' })
  held.gateway_targets[0]!.state = 'UNKNOWN'
  const released = evidence()
  released.stages = released.stages.map(stage => ['workload', 'gateway'].includes(stage.name) ? { ...stage, state: 'UNKNOWN', error_code: 'recovery_observation_pending' } : stage)
  released.gateway_targets[0]!.state = 'UNKNOWN'
  vi.mocked(request).mockResolvedValueOnce({ data: held, response: new Response() })
    .mockResolvedValueOnce({ data: released, response: new Response() })
  show(); await flushPromises()
  expect(wrapper!.get('[data-stage=accepted]').attributes('data-state')).toBe('CONFIRMED')
  for (const stage of ['workload', 'certificate', 'gateway', 'client_access']) expect(wrapper!.get(`[data-stage=${stage}]`).attributes('data-state')).toBe('UNKNOWN')
  expect(wrapper!.text()).toContain('Recovery mode permits inspection only')
  expect(wrapper!.get('[data-gateway]').attributes('data-state')).toBe('UNKNOWN')
  await wrapper!.get('button').trigger('click'); await flushPromises()
  expect(wrapper!.get('[data-stage=certificate]').attributes('data-state')).toBe('CONFIRMED')
  for (const stage of ['workload', 'gateway', 'client_access']) expect(wrapper!.get(`[data-stage=${stage}]`).attributes('data-state')).toBe('UNKNOWN')
})

it('replaces previous confirmations with unknown when refreshing evidence fails', async () => {
  vi.mocked(request).mockResolvedValueOnce({ data: evidence(), response: new Response() }).mockRejectedValueOnce(new Error('unavailable'))
  show(); await flushPromises()
  expect(wrapper!.get('[data-stage=workload]').attributes('data-state')).toBe('CONFIRMED')
  await wrapper!.get('button').trigger('click'); await flushPromises()
  expect(wrapper!.get('[data-testid=readiness-observation-error]').text()).toContain('recorded operation result is unchanged')
  expect(wrapper!.findAll('[data-stage]').every(row => row.attributes('data-state') === 'UNKNOWN')).toBe(true)
  expect(wrapper!.get('[data-gateway]').attributes('data-state')).toBe('UNKNOWN')
})

it('keeps partial target results and refreshes through the shared query family', async () => {
  const data = evidence(); data.gateway_targets.push({ gateway_id: 'gateway-two', pool_id: 'pool-one', state: 'UNKNOWN', desired_revision: 7, applied_revision: 6 })
  vi.mocked(request).mockResolvedValue({ data, response: new Response() })
  const queryClient = client(); show(queryClient); await flushPromises()
  expect(wrapper!.get('[data-gateway=gateway-one]').attributes('data-state')).toBe('CONFIRMED')
  expect(wrapper!.get('[data-gateway=gateway-two]').attributes('data-state')).toBe('UNKNOWN')
  await queryClient.invalidateQueries({ queryKey: ['operation-readiness'] }); await flushPromises()
  expect(vi.mocked(request)).toHaveBeenCalledTimes(2)
})

it('does not reuse another operation’s completed evidence after navigation', async () => {
  vi.mocked(request).mockResolvedValueOnce({ data: evidence(), response: new Response() }).mockRejectedValueOnce(new Error('new target unavailable'))
  show(); await flushPromises()
  await wrapper!.setProps({ operationId: 'op-two' }); await flushPromises()
  expect(wrapper!.get('[data-stage=workload]').attributes('data-state')).toBe('UNKNOWN')
  expect(wrapper!.find('[data-gateway=gateway-one]').exists()).toBe(false)
  expect(vi.mocked(request).mock.calls[1]?.[2]?.params?.path?.id).toBe('op-two')
})

it('ignores a late completion from the previous operation and cancels its observation', async () => {
  let resolveFirst: ((value: Awaited<ReturnType<typeof request>>) => void) | undefined
  vi.mocked(request).mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve }))
    .mockResolvedValueOnce({ data: { ...evidence('op-two'), gateway_targets: [] }, response: new Response() })
  show(); await flushPromises()
  const originalSignal = vi.mocked(request).mock.calls[0]?.[2]?.signal
  await wrapper!.setProps({ operationId: 'op-two' }); await flushPromises()
  resolveFirst?.({ data: evidence('op-one'), response: new Response() }); await flushPromises()
  expect(originalSignal?.aborted).toBe(true)
  expect(wrapper!.get('[data-stage=workload]').attributes('data-state')).toBe('CONFIRMED')
  expect(wrapper!.find('[data-gateway=gateway-one]').exists()).toBe(false)
})

it('distinguishes the configured location from the actual manager and hides it after failed refresh', async () => {
  const data = { ...evidence(), execution: { runtime_id: 'runtime-one', configured_node_id: 'original-manager', actual_instance_id: 'alternate-controller', actual_node_id: 'alternate-manager', attempt: 2, state: 'WAITING', reason: 'verified_executor_unavailable', observed_at: '2026-10-13T00:00:00Z' } }
  vi.mocked(request).mockResolvedValueOnce({ data, response: new Response() }).mockRejectedValueOnce(new Error('permission revoked'))
  show(); await flushPromises()
  const panel = wrapper!.get('[data-testid=operation-execution]')
  expect(panel.text()).toContain('original-manager')
  expect(panel.text()).toContain('alternate-manager')
  expect(panel.text()).toContain('alternate-controller')
  expect(panel.text()).toContain('The task is retained')
  expect(panel.text()).not.toContain('verified_executor_unavailable')
  await wrapper!.get('button').trigger('click'); await flushPromises()
  expect(wrapper!.find('[data-testid=operation-execution]').exists()).toBe(false)
})

it.each([
  ['frozen_target_requires_review', 'Review the frozen target'],
  ['accepted_effects_require_review', 'review the effects before resuming'],
  ['execution_evidence_unavailable', 'Current execution evidence is unavailable'],
  ['execution_result_unconfirmed', 'will not be blindly replayed'],
])('explains %s without exposing raw internal reasons', async (reason, text) => {
  vi.mocked(request).mockResolvedValue({ data: { ...evidence(), execution: { runtime_id: 'runtime-one', attempt: 1, state: 'UNKNOWN', reason } }, response: new Response() })
  show(); await flushPromises()
  expect(wrapper!.get('[data-testid=operation-execution]').text()).toContain(text)
  expect(wrapper!.text()).not.toContain(reason)
})


it('hides cached target identities and confirmations immediately when the session expires', async () => {
  vi.mocked(request).mockResolvedValue({ data: { ...evidence(), execution: { runtime_id: 'runtime-one', actual_node_id: 'private-node', state: 'WAITING', attempt: 1 } }, response: new Response() })
  show(); await flushPromises()
  expect(wrapper!.find('[data-gateway]').exists()).toBe(true)
  session.value.authenticated = false; await flushPromises()
  expect(wrapper!.find('[data-gateway]').exists()).toBe(false)
  expect(wrapper!.find('[data-testid=operation-execution]').exists()).toBe(false)
  expect(wrapper!.findAll('[data-stage]').every(row => row.attributes('data-state') === 'UNKNOWN')).toBe(true)
  expect(wrapper!.get('button').attributes('disabled')).toBeDefined()
})
