import { flushPromises, mount } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import OperationEntrypoint from '../../src/components/OperationEntrypoint.vue'
import { ApiRequestError, session } from '../../src/shared/api/client'

const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, asRow: (value: unknown) => value && typeof value === 'object' ? value : {}, ApiRequestError: class extends Error { constructor(readonly status: number, readonly code: string) { super(code) } }, session: ref({ role: 'administrator' }), errorText: (error: Error) => error?.message || '' }))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))
const cleanups: (() => void)[] = []
beforeEach(() => { send.mockReset(); session.value.role = 'administrator' })
afterEach(() => { for (const cleanup of cleanups.splice(0)) cleanup() })

function panel(id = 'op-first', client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })) {
  const wrapper = mount(OperationEntrypoint, { props: { operationId: id }, global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } })
  cleanups.push(() => { wrapper.unmount(); client.clear() })
  return { wrapper, client }
}
const success = (status = 'SUCCEEDED', revision = 4) => ({ data: { status, revision, attempts: 1, intent_id: 'intent' }, response: new Response(null) })
async function settle() { await flushPromises(); await flushPromises() }

describe('entrypoint observations', () => {
  it('marks a failed refresh unknown, preserves only labelled history, and recovers on manual refresh', async () => {
    send.mockResolvedValueOnce(success()).mockRejectedValueOnce(new ApiRequestError(503, 'unavailable')).mockResolvedValue(success('FAILED', 5))
    const { wrapper } = panel(); await settle()
    await vi.waitFor(() => expect(wrapper.attributes('data-state')).toBe('SUCCEEDED'))
    const options = send.mock.calls[0]![2]
    expect(options.cache).toBe('no-store'); expect(options.signal).toBeInstanceOf(AbortSignal)
    await wrapper.get('[data-testid=entrypoint-refresh]').trigger('click'); await settle()
    await vi.waitFor(() => expect(wrapper.attributes('data-state')).toBe('UNKNOWN'))
    expect(wrapper.get('[data-testid=entrypoint-observation-error]').text()).toContain('entrypointUI.refreshFailed')
    expect(wrapper.get('[data-testid=entrypoint-last-result]').text()).toContain('SUCCEEDED')
    expect(wrapper.find('[data-testid=entrypoint-retry]').exists()).toBe(false)
    await wrapper.get('[data-testid=entrypoint-refresh]').trigger('click'); await settle()
    await vi.waitFor(() => expect(wrapper.attributes('data-state')).toBe('FAILED'))
    expect(wrapper.find('[data-testid=entrypoint-observation-error]').exists()).toBe(false)
    expect(wrapper.get('[data-testid=entrypoint-retry]').attributes('disabled')).toBeUndefined()
  })
  it.each([401, 403])('hides cached receipt and controls when access becomes %s', async status => {
    send.mockResolvedValueOnce(success('FAILED')).mockRejectedValue(new ApiRequestError(status, 'denied'))
    const { wrapper } = panel(); await settle()
    await vi.waitFor(() => expect(wrapper.find('[data-testid=entrypoint-retry]').exists()).toBe(true))
    await wrapper.get('[data-testid=entrypoint-refresh]').trigger('click'); await settle()
    await vi.waitFor(() => expect(wrapper.attributes('data-state')).toBe('UNKNOWN'))
    expect(wrapper.find('[data-testid=entrypoint-retry]').exists()).toBe(false)
    expect(wrapper.find('[data-testid=entrypoint-last-result]').exists()).toBe(false)
    expect(wrapper.find('[data-testid=entrypoint-checked-at]').exists()).toBe(false)
  })
  it('disables a failed receipt retry while its observation is unavailable', async () => {
    send.mockResolvedValueOnce(success('FAILED')).mockRejectedValue(new ApiRequestError(0, 'network'))
    const { wrapper } = panel(); await settle()
    await vi.waitFor(() => expect(wrapper.find('[data-testid=entrypoint-retry]').exists()).toBe(true))
    await wrapper.get('[data-testid=entrypoint-refresh]').trigger('click'); await settle()
    await vi.waitFor(() => expect(wrapper.get('[data-testid=entrypoint-retry]').attributes('disabled')).toBeDefined())
    await wrapper.get('[data-testid=entrypoint-retry]').trigger('click')
    expect(send.mock.calls.every(([method]) => method === 'get')).toBe(true)
  })
  it.each([404, 503])('shows the correct initial outcome for %s', async status => {
    send.mockRejectedValue(new ApiRequestError(status, 'unavailable'))
    const { wrapper } = panel(); await settle()
    if (status === 404) await vi.waitFor(() => expect(wrapper.find('section').exists()).toBe(false))
    else await vi.waitFor(() => expect(wrapper.find('[data-testid=entrypoint-observation-error]').exists()).toBe(true))
  })
  it('rejects malformed success data instead of presenting a fabricated state', async () => {
    send.mockResolvedValue({ data: {}, response: new Response(null) })
    const { wrapper } = panel(); await settle()
    await vi.waitFor(() => expect(wrapper.attributes('data-state')).toBe('UNKNOWN'))
    expect(wrapper.get('[data-testid=entrypoint-observation-error]').text()).toContain('internal.operation_failed')
  })
  it('refreshes after a rejected stale retry without rewriting the accepted operation', async () => {
    send.mockResolvedValueOnce(success('FAILED', 4)).mockRejectedValueOnce(new ApiRequestError(412, 'stale')).mockResolvedValue(success('PENDING', 5))
    const { wrapper } = panel(); await settle()
    await vi.waitFor(() => expect(wrapper.find('[data-testid=entrypoint-retry]').exists()).toBe(true))
    await wrapper.get('[data-testid=entrypoint-retry]').trigger('click'); await settle()
    await vi.waitFor(() => expect(wrapper.attributes('data-state')).toBe('PENDING'))
    const submission = send.mock.calls.find(([method]) => method === 'post')!
    expect(submission[2].headers['If-Match']).toBe('"rev-4"')
    expect(submission[2].headers['Idempotency-Key']).toBeTruthy()
    expect(send.mock.calls.filter(([method]) => method === 'get')).toHaveLength(2)
    expect(wrapper.text()).toContain('stale')
  })
  it('refetches an existing cache on mount and never shows the previous operation after navigation', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: Infinity } } })
    client.setQueryData(['operation-entrypoint', 'op-first'], success('SUCCEEDED').data)
    send.mockResolvedValue(success('FAILED'))
    const { wrapper } = panel('op-first', client); await settle()
    await vi.waitFor(() => expect(wrapper.attributes('data-state')).toBe('FAILED'))
    send.mockResolvedValue(success('QUEUED'))
    await wrapper.setProps({ operationId: 'op-second' }); await settle()
    await vi.waitFor(() => expect(wrapper.attributes('data-state')).toBe('QUEUED'))
    expect(send.mock.calls.at(-1)![2].params.path.id).toBe('op-second')
    expect(wrapper.find('[data-testid=entrypoint-last-result]').exists()).toBe(false)
  })
  it('keeps viewer mutation controls disabled', async () => {
    session.value.role = 'viewer'; send.mockResolvedValue(success('FAILED'))
    const { wrapper } = panel(); await settle()
    await vi.waitFor(() => expect(wrapper.find('[data-testid=entrypoint-retry]').exists()).toBe(true))
    expect(wrapper.get('[data-testid=entrypoint-retry]').attributes('disabled')).toBeDefined()
  })
})
