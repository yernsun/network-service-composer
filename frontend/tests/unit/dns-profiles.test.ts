import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import DNSProfilesPanel from '../../src/components/DNSProfilesPanel.vue'
import { session } from '../../src/shared/api/client'
import { queryClient } from '../../src/data/inventory-cache'
import { setLocale } from '../../src/shared/i18n'

const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, session: ref({ role: 'administrator' }), errorText: (e: Error) => e.message }))
let wrapper: VueWrapper
const stored = { reference: 'production-dns', provider: 'cloudflare', source: 'database', configured: true, revision: 7 }
let detail: typeof stored & { region?: string; credential_fields: string[]; token_domains: string[] }
beforeEach(() => {
  queryClient.clear(); setLocale('en'); session.value.role = 'administrator'
  detail = { ...stored, credential_fields: ['api_token', 'zone_api_token'], token_domains: [] }
  send.mockReset().mockImplementation(async (method, path) => ({ data: method === 'get' && path.includes('{reference}') ? detail : [{ ...stored, provider: detail.provider }, { ...stored, reference: 'file-dns', source: 'config' }], response: new Response(null, { headers: { ETag: `"rev-${detail.revision}"` } }) }))
})
afterEach(() => { wrapper?.unmount(); queryClient.clear() })
async function screen() { wrapper = mount(DNSProfilesPanel); await flushPromises(); return wrapper }
async function edit() { const w = await screen(); await w.get('.profile-row button').trigger('click'); await flushPromises(); return w }
async function input(name: string, value: string) { await wrapper.get(`[name="${name}"]`).setValue(value) }
async function submit() { await input('password', 'reauth-password'); await wrapper.get('form').trigger('submit'); await flushPromises() }

it.each([
  { provider: 'cloudflare', fields: { api_token: 'fixture-token', zone_api_token: 'fixture-zone' }, credentials: { api_token: 'fixture-token', zone_api_token: 'fixture-zone' } },
  { provider: 'alidns', fields: { access_key: 'fixture-id', secret_key: 'fixture-key', security_token: 'fixture-session' }, credentials: { access_key: 'fixture-id', secret_key: 'fixture-key', security_token: 'fixture-session' } },
  { provider: 'tencentcloud', fields: { secret_id: 'fixture-id', secret_key: 'fixture-key', session_token: 'fixture-session' }, credentials: { secret_id: 'fixture-id', secret_key: 'fixture-key', session_token: 'fixture-session' } },
  { provider: 'ovh', fields: { application_key: 'fixture-id', application_secret: 'fixture-key', consumer_key: 'fixture-consumer' }, credentials: { application_key: 'fixture-id', application_secret: 'fixture-key', consumer_key: 'fixture-consumer' } },
])('creates $provider credentials with reauthentication and clears closed inputs', async ({ provider, fields, credentials }) => {
  const w = await screen()
  expect(w.findAll('.profile-row')[1]!.findAll('button')).toHaveLength(0)
  expect(w.findAll('.profile-row')[1]!.text()).toContain('Read only')
  await w.get('header button').trigger('click'); await input('reference', ' new-profile '); await input('provider', provider)
  if (provider === 'ovh') await input('region', 'ovh-ca')
  for (const [key, value] of Object.entries(fields)) await input(key, value)
  await submit()
  expect(send).toHaveBeenCalledWith('post', '/dns-provider-profiles', { body: { reference: 'new-profile', provider, credentials, ...(provider === 'ovh' ? { region: 'ovh-ca' } : {}) }, headers: { 'X-NSC-Reauthenticate': 'reauth-password' } })
  expect(w.emitted('changed')).toEqual([['new-profile']]); expect(w.find('form').exists()).toBe(false)
  await w.get('header button').trigger('click'); expect(w.findAll('form input').every(el => el.attributes('type') === 'checkbox' || (el.element as HTMLInputElement).value === '')).toBe(true)
})
it('edits only supplied fields using a freshly read ETag without exposing stored secrets', async () => {
  detail.revision = 9
  const w = await edit()
  expect(w.get('[name=reference]').attributes('disabled')).toBeDefined()
  expect(w.get('[name=provider]').attributes('disabled')).toBeDefined()
  expect(w.get('[name=api_token]').attributes('placeholder')).toContain('retain')
  expect(w.findAll('input[type=password]').every(el => (el.element as HTMLInputElement).value === '')).toBe(true)
  await input('api_token', 'fixture-new'); await submit()
  expect(send).toHaveBeenCalledWith('patch', '/dns-provider-profiles/{reference}', expect.objectContaining({ params: { path: { reference: stored.reference } }, headers: { 'If-Match': '"rev-9"', 'X-NSC-Reauthenticate': 'reauth-password' }, body: { credentials: { api_token: 'fixture-new' } } }))
  expect(w.find('form').exists()).toBe(false)
})
it('clears a field explicitly and switches Cloudflare authentication without retaining incompatible fields', async () => {
  const w = await edit()
  await input('clear-zone_api_token', 'true'); await submit()
  expect(send).toHaveBeenCalledWith('patch', expect.any(String), expect.objectContaining({ body: { credentials: { zone_api_token: null } } }))
  await w.get('.profile-row button').trigger('click'); await flushPromises(); await input('auth-mode', 'key')
  await input('email', 'admin@example.test'); await input('api_key', 'fixture-key'); await submit()
  expect(send).toHaveBeenLastCalledWith('get', '/dns-provider-profiles', expect.anything())
  expect(send).toHaveBeenCalledWith('patch', expect.any(String), expect.objectContaining({ body: { credentials: { email: 'admin@example.test', api_key: 'fixture-key', api_token: null, zone_api_token: null } } }))
})
it('preserves Aliyun optional fields unless explicitly cleared and supports RAM role authentication', async () => {
  detail.provider = 'alidns'; detail.credential_fields = ['access_key', 'secret_key', 'security_token']
  const w = await edit(); await input('auth-mode', 'role'); await input('ram_role', 'fixture-role'); await submit()
  expect(send).toHaveBeenCalledWith('patch', expect.any(String), expect.objectContaining({ body: { credentials: { ram_role: 'fixture-role', access_key: null, secret_key: null, security_token: null } } }))
  expect(w.find('form').exists()).toBe(false)
})
it('merges Hurricane token edits and removals without sending unchanged secrets', async () => {
  detail.provider = 'hurricane'; detail.credential_fields = ['tokens']; detail.token_domains = ['a.example.test', 'b.example.test']
  // The list and detail must agree on the immutable provider.
  const original = send.getMockImplementation()!
  send.mockImplementation(async (method, path, ...args) => path === '/dns-provider-profiles' ? { data: [{ ...stored, provider: 'hurricane' }] } : original(method, path, ...args))
  const w = await edit(); expect(w.findAll('[name=token-domain]')).toHaveLength(2)
  expect(w.findAll('[name=token-domain]')[0]!.attributes('readonly')).toBeDefined()
  await w.findAll('[name=token-value]')[0]!.setValue('fixture-a2')
  await w.findAll('.token-row')[1]!.get('button').trigger('click')
  await w.get('[data-add-token]').trigger('click'); await w.findAll('[name=token-domain]')[2]!.setValue(' C.Example.Test '); await w.findAll('[name=token-value]')[2]!.setValue('fixture-c')
  await submit()
  expect(send).toHaveBeenCalledWith('patch', expect.any(String), expect.objectContaining({ body: { credentials: { tokens: { 'a.example.test': 'fixture-a2', 'b.example.test': null, 'c.example.test': 'fixture-c' } } } }))
})
it('blocks empty or duplicate Hurricane tokens and supports undo and removing new rows', async () => {
  detail.provider = 'hurricane'; detail.credential_fields = ['tokens']; detail.token_domains = ['a.example.test']
  send.mockImplementation(async (_, path) => ({ data: path.includes('{reference}') ? detail : [{ ...stored, provider: 'hurricane' }], response: new Response(null, { headers: { ETag: '"rev-7"' } }) }))
  const w = await edit(); await w.get('.token-row button').trigger('click'); await submit()
  expect(w.get('[role=alert]').text()).toContain('at least one')
  await w.get('.token-row button').trigger('click'); await w.get('[data-add-token]').trigger('click'); await w.findAll('[name=token-domain]')[1]!.setValue('a.example.test'); await submit()
  expect(w.get('[role=alert]').text()).toContain('unique')
  await w.findAll('.token-row')[1]!.get('button').trigger('click'); await submit()
  expect(w.get('[role=alert]').text()).toContain('Change a field')
})
it('creates several Hurricane domain tokens', async () => {
  const w = await screen(); await w.get('header button').trigger('click'); await input('reference', 'he'); await input('provider', 'hurricane'); await flushPromises()
  await w.get('[name=token-domain]').setValue('a.example.test'); await w.get('[name=token-value]').setValue('fixture-a')
  await w.get('[data-add-token]').trigger('click'); await w.findAll('[name=token-domain]')[1]!.setValue('b.example.test'); await w.findAll('[name=token-value]')[1]!.setValue('fixture-b'); await submit()
  expect(send).toHaveBeenCalledWith('post', expect.any(String), expect.objectContaining({ body: { reference: 'he', provider: 'hurricane', credentials: { tokens: { 'a.example.test': 'fixture-a', 'b.example.test': 'fixture-b' } } } }))
})
it('retains failed input, clears the password and requires an explicit reload after a revision conflict', async () => {
  const w = await edit(); await input('api_token', 'fixture-new')
  send.mockRejectedValueOnce(Object.assign(new Error('revision conflict'), { status: 412 })); await submit()
  expect(w.get('[role=alert]').text()).toBe('revision conflict'); expect((w.get('[name=api_token]').element as HTMLInputElement).value).toBe('fixture-new'); expect((w.get('[name=password]').element as HTMLInputElement).value).toBe('')
  expect(w.get('[type=submit]').attributes('disabled')).toBeDefined(); expect(w.emitted('changed')).toBeUndefined()
  detail.revision = 10
  await w.get('form > button').trigger('click'); await flushPromises()
  expect((w.get('[name=api_token]').element as HTMLInputElement).value).toBe('fixture-new'); expect(w.get('[role=status]').text()).toContain('Review')
  await submit(); expect(send).toHaveBeenCalledWith('patch', expect.any(String), expect.objectContaining({ headers: { 'If-Match': '"rev-10"', 'X-NSC-Reauthenticate': 'reauth-password' } }))
})
it('retains an ordinary failed edit for retry and clears sensitive inputs on cancel', async () => {
  const w = await edit(); await input('api_token', 'fixture-unsaved'); send.mockRejectedValueOnce(new Error('unavailable')); await submit()
  expect(w.get('[role=alert]').text()).toBe('unavailable'); expect((w.get('[name=password]').element as HTMLInputElement).value).toBe('')
  await w.get('header button').trigger('click'); await w.get('.profile-row button').trigger('click'); await flushPromises()
  expect((w.get('[name=api_token]').element as HTMLInputElement).value).toBe('')
})
it('ignores detail responses after cancellation and reports a changed source without enabling save', async () => {
  const w = await screen(); let resolve!: (value: unknown) => void
  send.mockReturnValueOnce(new Promise(value => { resolve = value }))
  await w.get('.profile-row button').trigger('click'); await w.get('header button').trigger('click')
  resolve({ data: detail, response: new Response() }); await flushPromises(); expect(w.find('form').exists()).toBe(false)
  detail.source = 'config'; await w.get('.profile-row button').trigger('click'); await flushPromises()
  expect(w.get('[role=alert]').text()).toContain('different configuration source'); expect(w.get('[type=submit]').attributes('disabled')).toBeDefined()
})
it('requires delete confirmation and reauthentication and retains failures', async () => {
  const w = await screen(), button = w.findAll('.profile-row button')[1]!
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false), prompt = vi.spyOn(window, 'prompt').mockReturnValue(null)
  await button.trigger('click'); expect(prompt).not.toHaveBeenCalled(); confirm.mockReturnValue(true); await button.trigger('click')
  prompt.mockReturnValue('password'); send.mockRejectedValueOnce(new Error('profile in use')); await button.trigger('click'); await flushPromises()
  expect(w.get('[role=alert]').text()).toBe('profile in use'); await button.trigger('click'); await flushPromises()
  expect(send).toHaveBeenCalledWith('delete', expect.any(String), expect.objectContaining({ headers: { 'If-Match': '"rev-7"', 'X-NSC-Reauthenticate': 'password' } })); expect(w.emitted('changed')).toEqual([['']])
})
it('shows load failures and hides mutations for viewers', async () => {
  session.value.role = 'viewer'; send.mockRejectedValueOnce(new Error('inventory unavailable'))
  const w = await screen(); expect(w.get('[role=alert]').text()).toBe('inventory unavailable'); expect(w.find('button').exists()).toBe(false)
})

it('retains a replacement token when its domain was removed concurrently', async () => {
  detail.provider = 'hurricane'; detail.credential_fields = ['tokens']; detail.token_domains = ['a.example.test', 'b.example.test']
  const w = await edit(); await w.findAll('[name=token-value]')[0]!.setValue('fixture-retained')
  send.mockRejectedValueOnce(Object.assign(new Error('conflict'), { status: 412 })); await submit()
  detail.revision = 8; detail.token_domains = ['b.example.test']
  await w.get('form > button').trigger('click'); await flushPromises()
  expect((w.findAll('[name=token-value]')[0]!.element as HTMLInputElement).value).toBe('fixture-retained')
  expect(w.findAll('[name=token-domain]')[0]!.attributes('readonly')).toBeUndefined()
  await submit()
  expect(send).toHaveBeenCalledWith('patch', expect.any(String), expect.objectContaining({ body: { credentials: { tokens: { 'a.example.test': 'fixture-retained' } } } }))
})
it('does not implicitly clear another authentication method after a concurrent change', async () => {
  const w = await edit(); await input('api_token', 'fixture-retained')
  send.mockRejectedValueOnce(Object.assign(new Error('conflict'), { status: 412 })); await submit()
  detail.revision = 8; detail.credential_fields = ['email', 'api_key']
  await w.get('form > button').trigger('click'); await flushPromises(); await submit()
  expect(send).toHaveBeenCalledWith('patch', expect.any(String), expect.objectContaining({ body: { credentials: { api_token: 'fixture-retained' } } }))
})

it('retains an untouched OVH endpoint after a concurrent edit and reload', async () => {
  detail.provider = 'ovh'; detail.region = 'ovh-eu'; detail.credential_fields = ['application_key', 'application_secret', 'consumer_key']
  const w = await edit(); await input('application_key', 'fixture-replacement')
  send.mockRejectedValueOnce(Object.assign(new Error('conflict'), { status: 412 })); await submit()
  detail.revision = 8; detail.region = 'ovh-ca'
  await w.get('form > button').trigger('click'); await flushPromises()
  expect((w.get('[name=region]').element as HTMLSelectElement).value).toBe('ovh-ca')
  await submit()
  expect(send).toHaveBeenCalledWith('patch', expect.any(String), expect.objectContaining({ body: { credentials: { application_key: 'fixture-replacement' } } }))
})
it('updates the OVH endpoint only when explicitly selected', async () => {
  detail.provider = 'ovh'; detail.region = 'ovh-eu'; detail.credential_fields = ['application_key', 'application_secret', 'consumer_key']
  const w = await edit(); await input('region', 'ovh-ca'); await submit()
  expect(send).toHaveBeenCalledWith('patch', expect.any(String), expect.objectContaining({ body: { region: 'ovh-ca' } }))
  expect(w.find('form').exists()).toBe(false)
})
