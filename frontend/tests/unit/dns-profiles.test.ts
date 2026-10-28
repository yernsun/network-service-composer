import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import DNSProfilesPanel from '../../src/components/DNSProfilesPanel.vue'
import { session } from '../../src/shared/api/client'
import { setLocale } from '../../src/shared/i18n'

const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, session: ref({ role: 'administrator' }), errorText: (e: Error) => e.message }))
let wrapper: VueWrapper
const stored = { reference: 'production-dns', provider: 'cloudflare', source: 'database', revision: 7 }
beforeEach(() => {
  setLocale('en'); session.value.role = 'administrator'
  send.mockReset().mockResolvedValue({ data: [stored, { ...stored, reference: 'file-dns', source: 'config' }] })
})
afterEach(() => wrapper?.unmount())
async function screen() { wrapper = mount(DNSProfilesPanel); await flushPromises(); return wrapper }

it.each([
  { provider: 'cloudflare', fields: ['api-token', 'zone-token'], credentials: { api_token: 'api-token', zone_api_token: 'zone-token' } },
  { provider: 'hurricane', fields: [' ExAmPlE.COM ', 'ddns-key'], credentials: { tokens: { 'example.com': 'ddns-key' } } },
  { provider: 'alidns', fields: ['access-key', 'secret-key'], credentials: { access_key: 'access-key', secret_key: 'secret-key' } },
  { provider: 'tencentcloud', fields: ['secret-id', 'secret-key'], credentials: { secret_id: 'secret-id', secret_key: 'secret-key' } },
  { provider: 'ovh', fields: ['app-key', 'app-secret', 'consumer-key'], credentials: { application_key: 'app-key', application_secret: 'app-secret', consumer_key: 'consumer-key' } },
])('submits $provider credentials with reauthentication and clears the closed form', async ({ provider, fields, credentials }) => {
  const w = await screen()
  expect(w.findAll('.profile-row')[1]!.findAll('button')).toHaveLength(0)
  await w.get('header button').trigger('click')
  await w.get('form input').setValue(' new-profile ')
  await w.get('form select').setValue(provider)
  if (provider === 'ovh') await w.findAll('form select')[1]!.setValue('ovh-ca')
  const inputs = w.findAll('form input')
  for (let i = 0; i < fields.length; i++) await inputs[i + 1]!.setValue(fields[i]!)
  await inputs.at(-1)!.setValue('reauth-password')
  await w.get('form').trigger('submit'); await flushPromises()
  expect(send).toHaveBeenCalledWith('post', '/dns-provider-profiles', {
    body: { reference: 'new-profile', provider, credentials, ...(provider === 'ovh' ? { region: 'ovh-ca' } : {}) },
    headers: { 'X-NSC-Reauthenticate': 'reauth-password' },
  })
  expect(w.emitted('changed')).toEqual([['new-profile']]); expect(w.find('form').exists()).toBe(false)
  await w.get('header button').trigger('click')
  expect(w.findAll('form input').every(input => (input.element as HTMLInputElement).value === '')).toBe(true)
  await w.get('header button').trigger('click'); expect(w.find('form').exists()).toBe(false)
})

it('rotates a stored profile using its revision without exposing existing credentials', async () => {
  const w = await screen(); await w.get('.profile-row button').trigger('click')
  expect(w.get('form input').attributes('disabled')).toBeDefined()
  expect(w.get('form select').attributes('disabled')).toBeDefined()
  const passwords = w.findAll('input[type=password]')
  expect(passwords.every(input => (input.element as HTMLInputElement).value === '')).toBe(true)
  await passwords[0]!.setValue('rotated-token'); await passwords.at(-1)!.setValue('password')
  await w.get('form').trigger('submit'); await flushPromises()
  expect(send).toHaveBeenCalledWith('put', '/dns-provider-profiles/{reference}', expect.objectContaining({
    params: { path: { reference: stored.reference } }, headers: { 'If-Match': '"rev-7"', 'X-NSC-Reauthenticate': 'password' },
    body: { reference: stored.reference, provider: 'cloudflare', credentials: { api_token: 'rotated-token', zone_api_token: '' } },
  }))
})

it('retains an unsaved rotation on failure and clears reauthentication before retry', async () => {
  const w = await screen(); await w.get('.profile-row button').trigger('click')
  const passwords = w.findAll('input[type=password]')
  await passwords[0]!.setValue('new-token'); await passwords.at(-1)!.setValue('password')
  send.mockRejectedValueOnce(new Error('revision conflict'))
  await w.get('form').trigger('submit'); await flushPromises()
  expect(w.get('[role=alert]').text()).toBe('revision conflict')
  expect((w.get('input[type=password]').element as HTMLInputElement).value).toBe('new-token')
  expect((w.findAll('input[type=password]').at(-1)!.element as HTMLInputElement).value).toBe('')
  expect(w.emitted('changed')).toBeUndefined()
})

it('requires delete confirmation and reauthentication and keeps a failed profile visible', async () => {
  const w = await screen(), remove = w.findAll('.profile-row button')[1]!
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
  const prompt = vi.spyOn(window, 'prompt').mockReturnValue(null)
  await remove.trigger('click'); expect(prompt).not.toHaveBeenCalled()
  confirm.mockReturnValue(true); await remove.trigger('click'); expect(send.mock.calls.every(([method]) => method === 'get')).toBe(true)
  prompt.mockReturnValue('password'); send.mockRejectedValueOnce(new Error('profile in use'))
  await remove.trigger('click'); await flushPromises()
  expect(w.get('[role=alert]').text()).toBe('profile in use'); expect(w.text()).toContain(stored.reference)
  await remove.trigger('click'); await flushPromises()
  expect(send).toHaveBeenCalledWith('delete', '/dns-provider-profiles/{reference}', { params: { path: { reference: stored.reference } }, headers: { 'If-Match': '"rev-7"', 'X-NSC-Reauthenticate': 'password' } })
  expect(w.emitted('changed')).toEqual([['']])
})

it('shows load failures and hides mutation controls for viewers', async () => {
  session.value.role = 'viewer'; send.mockRejectedValueOnce(new Error('inventory unavailable'))
  const w = await screen(); expect(w.get('[role=alert]').text()).toBe('inventory unavailable')
  expect(w.find('button').exists()).toBe(false); expect(send).toHaveBeenCalledOnce()
})
