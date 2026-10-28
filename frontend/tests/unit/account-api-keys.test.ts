import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import ApiKeysPanel from '../../src/components/ApiKeysPanel.vue'
import AccountPage from '../../src/views/AccountPage.vue'
import UsersPage from '../../src/views/UsersPage.vue'
import { session } from '../../src/shared/api/client'

const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({
  request: send,
  session: ref({ user_id: 'admin', username: 'admin', role: 'administrator', csrf_token: 'old-csrf' }),
  asRows: (value: unknown) => Array.isArray(value) ? value : [],
  asRow: (value: unknown) => value && typeof value === 'object' ? value : {},
  errorText: (error: Error) => error.message,
}))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))

beforeEach(() => {
  send.mockReset().mockImplementation(async (method, path) => {
    if (method === 'get' && path === '/projects') return { data: [{ id: 'proj', name: 'Project' }] }
    if (method === 'get' && path === '/users/{userID}/api-keys') return { data: [] }
    if (method === 'get' && path === '/users') return { data: [{ id: 'viewer', username: 'reader', role: 'viewer', enabled: true, revision: 2 }] }
    if (method === 'post' && path === '/users/{userID}/api-keys') return { data: { token: 'one-time-token' } }
    if (method === 'post' && path === '/me/password') return { data: { csrf_token: 'new-csrf' } }
    return { data: {} }
  })
  session.value = { user_id: 'admin', username: 'admin', role: 'administrator', csrf_token: 'old-csrf' } as typeof session.value
})

it('shows a signed key only once and sends the selected project scopes with password reauthentication', async () => {
  const wrapper = mount(ApiKeysPanel, { props: { userId: 'viewer' } })
  try {
    await flushPromises()
    await wrapper.get('input[autocomplete="off"]').setValue('release')
    await wrapper.get('input[value="services.deploy"]').setValue(true)
    await wrapper.get('input[autocomplete="current-password"]').setValue('admin-password')
    await wrapper.findAll('button').find(button => button.text() === 'keys.issue')!.trigger('click')
    await flushPromises()
    expect(send).toHaveBeenCalledWith('post', '/users/{userID}/api-keys', expect.objectContaining({
      params: { path: { userID: 'viewer' } },
      headers: { 'X-NSC-Reauthenticate': 'admin-password' },
      body: expect.objectContaining({ project_id: 'proj', scopes: expect.arrayContaining(['services.read', 'services.deploy']) }),
    }))
    expect(wrapper.get('.key-secret').text()).toBe('one-time-token')
    await wrapper.findAll('button').find(button => button.text() === 'keys.dismiss')!.trigger('click')
    expect(wrapper.text()).not.toContain('one-time-token')
  } finally { wrapper.unmount() }
})

it('prevents a viewer from selecting write scopes when issuing their own key', async () => {
  session.value = { user_id: 'viewer', username: 'reader', role: 'viewer', csrf_token: 'csrf' } as typeof session.value
  const wrapper = mount(ApiKeysPanel, { props: { userId: 'viewer' } })
  try {
    await flushPromises()
    expect(wrapper.get('input[value="services.create"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('input[value="domains.publish"]').attributes('disabled')).toBeDefined()
    await wrapper.get('input[autocomplete="off"]').setValue('self-read')
    await wrapper.get('input[autocomplete="current-password"]').setValue('viewer-password')
    await wrapper.findAll('button').find(button => button.text() === 'keys.issue')!.trigger('click')
    await flushPromises()
    const body = send.mock.calls.find(([method, path]) => method === 'post' && path === '/users/{userID}/api-keys')![2].body
    expect(body.scopes.every((scope: string) => scope.endsWith('.read'))).toBe(true)
  } finally { wrapper.unmount() }
})

it('refreshes the browser CSRF token after a self password change', async () => {
  const wrapper = mount(AccountPage, { global: { stubs: { ApiKeysPanel: true, NotificationSettings: true, NotificationRouting: true } } })
  try {
    await wrapper.get('input[autocomplete="current-password"]').setValue('old-password')
    const replacement = wrapper.findAll('input[autocomplete="new-password"]')
    await replacement[0]!.setValue('replacement-password-123456')
    await replacement[1]!.setValue('replacement-password-123456')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(send).toHaveBeenCalledWith('post', '/me/password', { body: { current_password: 'old-password', new_password: 'replacement-password-123456' } })
    expect(session.value.csrf_token).toBe('new-csrf')
  } finally { wrapper.unmount() }
})

it('resets a selected user password with their ETag and operator reauthentication', async () => {
  const wrapper = mount(UsersPage, { global: { stubs: { ApiKeysPanel: true } } })
  try {
    await flushPromises()
    await wrapper.get('.user-choice').trigger('click')
    await wrapper.get('input[autocomplete="new-password"]').setValue('reset-password-123456')
    await wrapper.get('input[autocomplete="current-password"]').setValue('admin-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(send).toHaveBeenCalledWith('put', '/users/{userID}', expect.objectContaining({
      params: { path: { userID: 'viewer' } },
      headers: { 'If-Match': '"rev-2"', 'X-NSC-Reauthenticate': 'admin-password' },
      body: expect.objectContaining({ password: 'reset-password-123456', role: 'viewer' }),
    }))
  } finally { wrapper.unmount() }
})
