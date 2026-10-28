import { flushPromises, mount } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import NotificationSettings from '../../src/components/NotificationSettings.vue'
import { ApiRequestError, session } from '../../src/shared/api/client'
const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, ApiRequestError: class extends Error { constructor(readonly status: number, readonly code: string) { super(code) } }, session: ref({ role: 'administrator' }), errorText: (error: Error) => error.message }))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))
vi.mock('../../src/shared/i18n', () => ({ formatDate: (value: string) => value }))
const cleanups: (() => void)[] = []
let policy: Record<string, unknown>, history: unknown[]
const ok = (data: unknown) => ({ data, response: new Response(null) })
beforeEach(() => {
  send.mockReset(); session.value.role = 'administrator'
  policy = { revision: 1, enabled: true, events: ['images'], endpoint_host: 'receiver.example.test', endpoint_configured: true, secret_configured: true }
  history = []
  send.mockImplementation(async (method: string, path: string, options?: { body?: Record<string, unknown> }) => {
    if (method === 'get') return ok(path === '/notification-settings' ? { ...policy } : history)
    if (method === 'put') { policy = { ...policy, enabled: options!.body!.enabled, events: options!.body!.events, revision: Number(policy.revision) + 1 }; return ok({ ...policy }) }
    return ok({ status: 'QUEUED' })
  })
})
afterEach(() => { for (const cleanup of cleanups.splice(0)) cleanup() })
function panel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  const wrapper = mount(NotificationSettings, { global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } })
  cleanups.push(() => { wrapper.unmount(); client.clear() })
  return { wrapper, client }
}
async function settle() { await flushPromises(); await flushPromises() }
function failedRow() { return { id: 'notice-1', revision: 4, event: { id: 'notice-1', type: 'image.available', resource_id: 'candidate', occurred_at: '2026-10-17T00:00:00Z' }, status: 'FAILED', attempts: 5, error_code: 'transport_error' } }
describe('notification settings', () => {
  it('can explicitly select Runtime availability notifications', async () => {
    const { wrapper } = panel(); await settle()
    await wrapper.get('input[value=runtimes]').setValue(true)
    await wrapper.get('form').trigger('submit'); await settle()
    expect(send.mock.calls.find(([method]) => method === 'put')![2].body.events).toContain('runtimes')
  })
  it('loads saved settings without exposing secrets and refreshes without replacing an edited form', async () => {
    const { wrapper } = panel(); await settle()
    await vi.waitFor(() => expect(wrapper.find('form').exists()).toBe(true))
    const fields = wrapper.findAll('input[type=password]')
    expect(fields.map(input => (input.element as HTMLInputElement).value)).toEqual(['', '', ''])
    expect(fields[0]!.attributes('placeholder')).toBe('receiver.example.test')
    await fields[0]!.setValue('https://new.example.test/hook'); await fields[1]!.setValue('secret-in-memory-only')
    await wrapper.get('[data-testid=notification-refresh]').trigger('click'); await settle()
    expect((fields[0]!.element as HTMLInputElement).value).toBe('https://new.example.test/hook')
    expect((fields[1]!.element as HTMLInputElement).value).toBe('secret-in-memory-only')
    for (const [method, , options] of send.mock.calls) if (method === 'get') { expect(options.cache).toBe('no-store'); expect(options.signal).toBeInstanceOf(AbortSignal) }
  })
  it('saves with reauthentication and ETag, refreshes results, and clears sensitive fields', async () => {
    const { wrapper } = panel(); await settle()
    const fields = wrapper.findAll('input[type=password]')
    await fields[0]!.setValue('https://new.example.test/hook'); await fields[1]!.setValue('private-notification-secret'); await fields[2]!.setValue('current-admin-password')
    await wrapper.get('form').trigger('submit'); await settle()
    const call = send.mock.calls.find(([method]) => method === 'put')!
    expect(call[2].headers).toEqual({ 'If-Match': '"rev-1"', 'X-NSC-Reauthenticate': 'current-admin-password' })
    expect(call[2].body).toMatchObject({ endpoint: 'https://new.example.test/hook', secret: 'private-notification-secret', enabled: true })
    expect(wrapper.text()).toContain('notifications.saved')
    expect(fields.every(field => !(field.element as HTMLInputElement).value)).toBe(true)
  })
  it('retains saved credentials when submitting blank URL and secret fields', async () => {
    const { wrapper } = panel(); await settle(); await wrapper.get('form').trigger('submit'); await settle()
    const body = send.mock.calls.find(([method]) => method === 'put')![2].body
    expect(body.endpoint).toBeUndefined(); expect(body.secret).toBeUndefined()
  })
  it('queues a test of saved configuration and explicitly retries failed receipts', async () => {
    history = [failedRow()]
    const { wrapper } = panel(); await settle()
    await wrapper.get('[data-testid=notification-test]').trigger('click'); await settle()
    expect(send.mock.calls.find(([, path]) => path === '/notifications/test')![2].headers['If-Match']).toBe('"rev-1"')
    expect(wrapper.text()).toContain('notifications.queued')
    await wrapper.get('tbody button').trigger('click'); await settle()
    const retry = send.mock.calls.find(([, path]) => path === '/notifications/{id}/retry')!
    expect(retry[2].params.path.id).toBe('notice-1'); expect(retry[2].headers['If-Match']).toBe('"rev-4"')
  })
  it('blocks stale edits after an external configuration change and allows explicit reload', async () => {
    const { wrapper } = panel(); await settle()
    policy = { ...policy, revision: 2, events: ['gateways'] }
    await wrapper.get('[data-testid=notification-refresh]').trigger('click'); await settle()
    expect(wrapper.text()).toContain('notifications.changed')
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeDefined()
    const reload = wrapper.findAll('button').find(button => button.text() === 'notifications.reload')!
    await reload.trigger('click'); await settle()
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeUndefined()
    expect((wrapper.get('input[value=gateways]').element as HTMLInputElement).checked).toBe(true)
  })
  it.each([401, 403])('hides cached notification data after permission loss (%s)', async status => {
    history = [failedRow()]
    const { wrapper } = panel(); await settle()
    expect(wrapper.find('tbody tr').exists()).toBe(true)
    send.mockRejectedValue(new ApiRequestError(status, 'denied'))
    await wrapper.get('[data-testid=notification-refresh]').trigger('click'); await settle()
    expect(wrapper.find('form').exists()).toBe(false); expect(wrapper.find('tbody').exists()).toBe(false)
  })
  it('disables stale retry controls when history refresh fails', async () => {
    history = [failedRow()]
    const { wrapper } = panel(); await settle()
    send.mockImplementation(async (_method, path) => { if (path === '/notifications') throw new ApiRequestError(503, 'unavailable'); return ok(policy) })
    await wrapper.get('[data-testid=notification-refresh]').trigger('click'); await settle()
    expect(wrapper.get('tbody button').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('notifications.readFailed')
  })
  it('reports a rejected write, clears the password and refreshes the server revision', async () => {
    const { wrapper } = panel(); await settle()
    send.mockImplementation(async (method, path) => { if (method === 'put') throw new ApiRequestError(412, 'stale'); return ok(path === '/notification-settings' ? policy : history) })
    await wrapper.findAll('input[type=password]')[2]!.setValue('one-request-password')
    await wrapper.get('form').trigger('submit'); await settle()
    expect(wrapper.text()).toContain('stale')
    expect((wrapper.findAll('input[type=password]')[2]!.element as HTMLInputElement).value).toBe('')
  })
  it('does not request global notification data for viewers', async () => {
    session.value.role = 'viewer'
    const { wrapper } = panel(); await settle()
    expect(wrapper.find('section').exists()).toBe(false); expect(send).not.toHaveBeenCalled()
  })
  it('retries a custom receiver while the legacy default is disabled and filters history on the server', async () => {
    policy.enabled = false
    history = [{ ...failedRow(), receiver_id: 'receiver_team', event_key: 'shared-event' }]
    const receiver = { id: 'receiver_team', name: 'Operations', revision: 2, enabled: true }
    send.mockImplementation(async (method, path) => method === 'get' ? ok(path === '/notification-settings' ? policy : path === '/notification-receivers' ? [receiver] : history) : ok({ status: 'QUEUED' }))
    const { wrapper } = panel(); await settle()
    expect(wrapper.get('tbody button').attributes('disabled')).toBeUndefined()
    await wrapper.get('tbody button').trigger('click'); await settle()
    expect(send.mock.calls.some(([, path]) => path === '/notifications/{id}/retry')).toBe(true)
    await wrapper.get('[data-testid=notification-receiver-filter]').setValue('receiver_team'); await settle()
    expect(send.mock.calls.filter(([, path]) => path === '/notifications').at(-1)![2].params.query.receiver_id).toBe('receiver_team')
  })
})
