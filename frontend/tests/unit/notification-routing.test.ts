import { flushPromises, mount } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import NotificationRouting from '../../src/components/NotificationRouting.vue'
import { ApiRequestError, session } from '../../src/shared/api/client'
const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, ApiRequestError: class extends Error { constructor(readonly status: number, readonly code: string) { super(code) } }, session: ref({ authenticated: true, role: 'administrator', user_id: 'admin' }), errorText: (error: Error) => error.message }))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))
vi.mock('../../src/shared/i18n', () => ({ formatDate: (value: string) => value }))
const cleanup: (() => void)[] = []
let receivers: Record<string, unknown>[], routes: Record<string, unknown>[], silences: Record<string, unknown>[]
const ok = (data: unknown) => ({ data, response: new Response(null) })
beforeEach(() => {
  send.mockReset(); session.value.role = 'administrator'
  receivers = [{ id: 'default', revision: 1, name: 'Default', enabled: true, endpoint_host: 'receiver.example.test' }, { id: 'receiver_team', revision: 2, name: 'Team', enabled: true, endpoint_host: 'team.example.test' }]
  routes = [{ id: 'global-route', revision: 0, scope_type: 'global', scope_id: '', base_settings_revision: 1, enabled: true, receiver_ids: ['default'], events: ['runtimes'], severities: [] }]
  silences = []
  send.mockImplementation(async (method: string, path: string, options?: { body?: Record<string, unknown>; params?: { path?: { id?: string } } }) => {
    if (method === 'get') {
      const rows: Record<string, unknown> = { '/notification-receivers': receivers.map(v => ({ ...v })), '/notification-routes': routes.map(v => ({ ...v })), '/notification-silences': silences, '/notification-health': { initialized: true, sources: [{ group: 'runtimes', failed: false, attempted_at: '2026-10-21T00:00:00Z' }], queue: [{ kind: 'notification-delivery', status: 'FAILED', count: 1 }] }, '/issues': [{ id: 'issue-1', active: true, code: 'runtime_unavailable', resource_id: 'runtime-1' }], '/projects': [{ id: 'project-1', name: 'Project' }], '/projects/{id}/environments': [{ id: 'env-1', name: 'Production', project_id: 'project-1' }] }
      return ok(rows[path] || [])
    }
    if (path === '/notification-receivers/{id}') { const value = { ...options!.body, id: options!.params!.path!.id, revision: 3 }; receivers = [...receivers.filter(v => v.id !== value.id), value]; return ok(value) }
    if (path === '/notification-routes') { const value = { ...options!.body, id: 'route-1', revision: 1 }; routes = [value]; return ok(value) }
    if (path === '/notification-routing/preview') return ok({ matches: [{ receiver_id: 'receiver_team', scopes: [{ environment_id: 'env-1' }] }], silenced_scopes: [], globally_silenced: false })
    return ok({ status: 'QUEUED' })
  })
})
afterEach(() => { for (const stop of cleanup.splice(0)) stop() })
function panel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  const wrapper = mount(NotificationRouting, { global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } })
  cleanup.push(() => { wrapper.unmount(); client.clear() })
  return { wrapper, client }
}
async function settle() { await flushPromises(); await flushPromises() }
describe('notification routing', () => {
  it('shows collection failures and queue health without receiver secrets', async () => {
    const { wrapper } = panel(); await settle()
    expect(wrapper.get('[data-testid=routing-health]').text()).toContain('routing.state.FAILED')
    expect(wrapper.get('[data-testid=routing-health]').text()).toContain('2026-10-21')
    expect(wrapper.text()).toContain('routing.defaultReceiver')
    for (const field of wrapper.findAll('input[type=password]')) expect((field.element as HTMLInputElement).value).toBe('')
    for (const [method, , options] of send.mock.calls) if (method === 'get') { expect(options.signal).toBeInstanceOf(AbortSignal); expect(options.cache).toBe('no-store') }
  })
  it('preserves editable receiver and route inputs through background refresh', async () => {
    const { wrapper } = panel(); await settle()
    await wrapper.get('[data-testid=receiver-form] input[maxlength="100"]').setValue('Unsubmitted receiver')
    await wrapper.get('[data-testid=route-form] input[value=gateways]').setValue(true)
    await wrapper.get('[data-testid=routing-refresh]').trigger('click'); await settle()
    expect((wrapper.get('[data-testid=receiver-form] input[maxlength="100"]').element as HTMLInputElement).value).toBe('Unsubmitted receiver')
    expect((wrapper.get('[data-testid=route-form] input[value=gateways]').element as HTMLInputElement).checked).toBe(true)
  })
  it('edits a receiver with its saved revision and clears one-request secrets', async () => {
    const { wrapper } = panel(); await settle()
    await wrapper.findAll('.routing-list')[0]!.findAll('button').find(b => b.text() === 'routing.edit')!.trigger('click')
    const inputs = wrapper.get('[data-testid=receiver-form]').findAll('input[type=password]')
    await inputs[0]!.setValue('https://new.example.test/hook'); await inputs[1]!.setValue('private-signing-secret')
    await wrapper.get('.routing-password input').setValue('one-request-password')
    await wrapper.get('[data-testid=receiver-form]').trigger('submit'); await settle()
    const call = send.mock.calls.find(([, path]) => path === '/notification-receivers/{id}')!
    expect(call[2].params.path.id).toBe('receiver_team'); expect(call[2].headers).toEqual({ 'If-Match': '"rev-2"', 'X-NSC-Reauthenticate': 'one-request-password' })
    expect(inputs.every(input => !(input.element as HTMLInputElement).value)).toBe(true)
    expect((wrapper.get('.routing-password input').element as HTMLInputElement).value).toBe('')
  })
  it('blocks externally changed route revisions and reloads only on explicit selection', async () => {
    const { wrapper } = panel(); await settle()
    routes = [{ ...routes[0], revision: 2 }]
    await wrapper.get('[data-testid=routing-refresh]').trigger('click'); await settle()
    expect(wrapper.get('[data-testid=route-form] button').attributes('disabled')).toBeDefined()
    await wrapper.findAll('.routing-list')[1]!.get('button').trigger('click'); await settle()
    expect(wrapper.get('[data-testid=route-form] button').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid=route-form]').trigger('submit'); await settle()
    expect(send.mock.calls.find(([method, path]) => method === 'put' && path === '/notification-routes')![2].headers['If-Match']).toBe('"rev-2"')
  })
  it('creates a bounded silence with a reason and current administrator proof', async () => {
    const { wrapper } = panel(); await settle()
    const form = wrapper.get('[data-testid=silence-form]')
    await form.findAll('select')[1]!.setValue('env-1'); await form.get('input[maxlength="512"]').setValue('Planned maintenance')
    await form.trigger('submit'); await settle()
    const call = send.mock.calls.find(([, path]) => path === '/notification-silences/{id}')!
    expect(call[2].body).toMatchObject({ scope_type: 'environment', scope_id: 'env-1', reason: 'Planned maintenance' })
    expect(call[2].params.path.id).toMatch(/^silence_[a-z0-9]+$/)
    expect(new Date(call[2].body.expires_at).getTime() - Date.now()).toBeGreaterThan(3590000)
  })
  it('previews a stored issue and discards a late result after selection changes', async () => {
    const { wrapper } = panel(); await settle()
    const form = wrapper.get('[data-testid=routing-preview]'); await form.get('select').setValue('issue-1')
    let finish: (value: unknown) => void = () => {}
    send.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    await form.trigger('submit'); await form.get('select').setValue('')
    finish(ok({ matches: [{ receiver_id: 'private-old-result', scopes: [] }], silenced_scopes: [], globally_silenced: false })); await settle()
    expect(wrapper.text()).not.toContain('private-old-result')
  })
  it.each([401, 403])('hides cached routing after permission failure %s', async status => {
    const { wrapper } = panel(); await settle(); expect(wrapper.find('[data-testid=receiver-form]').exists()).toBe(true)
    send.mockRejectedValue(new ApiRequestError(status, 'denied'))
    await wrapper.get('[data-testid=routing-refresh]').trigger('click'); await settle()
    expect(wrapper.find('[data-testid=receiver-form]').exists()).toBe(false); expect(wrapper.text()).not.toContain('team.example.test')
  })
  it('makes no administrator queries for a viewer', async () => {
    session.value.role = 'viewer'; const { wrapper } = panel(); await settle()
    expect(wrapper.find('section').exists()).toBe(false); expect(send).not.toHaveBeenCalled()
  })
  it('switches scope before selecting an environment and preserves the selected route identity', async () => {
    routes.push({ id: 'env-route', revision: 3, scope_type: 'environment', scope_id: 'env-1', base_settings_revision: 0, enabled: true, receiver_ids: ['receiver_team'], events: ['runtimes'], severities: ['critical'] })
    const { wrapper } = panel(); await settle()
    await wrapper.findAll('.routing-list')[1]!.findAll('button')[1]!.trigger('click'); await settle()
    const form = wrapper.get('[data-testid=route-form]')
    expect((form.findAll('select')[1]!.element as HTMLSelectElement).value).toBe('env-1')
    await form.trigger('submit'); await settle()
    const call = send.mock.calls.find(([method, path]) => method === 'put' && path === '/notification-routes')!
    expect(call[2].headers['If-Match']).toBe('"rev-3"'); expect(call[2].body.scope_id).toBe('env-1')
  })
})
