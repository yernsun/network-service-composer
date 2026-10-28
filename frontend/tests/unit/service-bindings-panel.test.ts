import { flushPromises, mount } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { h, ref } from 'vue'
import ServiceBindings from '../../src/components/ServiceBindings.vue'
import { session } from '../../src/shared/api/client'
const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, asRows: (data: unknown) => Array.isArray(data) ? data : [], ApiRequestError: class extends Error { fields = [] }, session: ref({ role: 'administrator' }), errorText: (e: Error) => e?.message || '' }))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))

beforeEach(() => {
  session.value.role = 'administrator'
  send.mockReset().mockImplementation(async (method, path) => {
    const response = new Response('{}', { status: method === 'post' ? 202 : 200 })
    if (method !== 'get') return { data: { id: 'cert-imported' }, response }
    const values: Record<string, unknown> = {
      '/services/{id}': { id: 'svc', runtime_id: 'rt', managed: true, system: false, discovered_port: 8080, environment: { id: 'env' } },
      '/environments/{id}': { id: 'env', runtime_id: 'rt' },
      '/runtimes/{id}': { id: 'rt', pool_id: 'pool', management_enabled: true },
      '/dns-provider-profiles': [{ reference: 'dns-prod', provider: 'cloudflare', configured: true, source: 'database', revision: 1 }],
      '/settings': { management: { enabled: true }, acme: { dns_providers: { 'dns-prod': { configured: true } } } },
      '/services/{id}/domain-bindings': null, '/certificates': null, '/domains': null, '/routes': null, '/services': [{ id: 'svc', name: 'web', environment: { id: 'env' }, discovered_port: 8080 }], '/gateways': [{ id: 'gw', pool_id: 'pool', name: 'edge', address: '192.0.2.20', enabled: true }],
      '/operations': [{ id: 'op', service_id: 'svc', type: 'DOMAIN_ATTACH', status: 'RUNNING' }],
    }
    return { data: values[path], response }
  })
})
async function panel() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: ServiceBindings, props: { serviceId: 'svc' } }] })
  await router.push('/'); await router.isReady()
  const query = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  const wrapper = mount({ render: () => h(RouterView) }, { global: { plugins: [router, [VueQueryPlugin, { queryClient: query }]] } })
  await flushPromises()
  return { wrapper, cleanup: () => { wrapper.unmount(); query.clear() } }
}
describe('service binding panel', () => {
  it('saves a DNS-01 draft from the service entry point and exposes the existing durable task', async () => {
    const { wrapper, cleanup } = await panel()
    try {
      expect(wrapper.text()).toContain('DOMAIN_ATTACH')
      expect(wrapper.get('details .badge.blue').text()).toBe('RUNNING')
      await wrapper.findAll('header button').at(1)!.trigger('click')
      await flushPromises()
      await wrapper.get('input[placeholder="app.example.com"]').setValue('app.example.com')
      await wrapper.get('.domain-draft-fields input[type="checkbox"]').setValue(true)
      const select = wrapper.findAll('select').find(item => item.html().includes('dns-prod'))!
      await select.setValue('dns-prod')
      await wrapper.get('form').trigger('submit')
      await flushPromises()
      const submission = send.mock.calls.find(([method]) => method === 'post')!
      expect(submission[1]).toBe('/environments/{id}/domain-drafts')
      expect(submission[2].body.domain.tls).toEqual({ mode: 'managed', acme_challenge: 'dns-01', dns_provider_ref: 'dns-prod' })
      expect(submission[2].body.service_id).toBe('svc')
      expect(wrapper.text()).toContain('domainDraft.saved')
    } finally { cleanup() }
  })
  it('switches to HTTP challenge without retaining DNS credentials', async () => {
    const { wrapper, cleanup } = await panel()
    try {
      await wrapper.findAll('header button').at(1)!.trigger('click')
      await flushPromises()
      await wrapper.get('input[placeholder="app.example.com"]').setValue('app.example.com')
      expect(wrapper.text()).toContain('domainDraft.httpChallenge')
      await wrapper.get('form').trigger('submit'); await flushPromises()
      expect(send.mock.calls.find(([method]) => method === 'post')![2].body.domain.tls).toEqual({ mode: 'managed', acme_challenge: 'http-01' })
    } finally { cleanup() }
  })
  it('keeps mutation controls disabled for a viewer', async () => {
    session.value.role = 'viewer'
    const { wrapper, cleanup } = await panel()
    try { expect(wrapper.findAll('header button').every(button => button.attributes('disabled') !== undefined)).toBe(true); expect(send.mock.calls.every(([method]) => method === 'get')).toBe(true) } finally { cleanup() }
  })
})

function existingBindings() {
  const domain = { id: 'domain', host: 'web.example.test', pool_id: 'pool', revision: 3, tls: { mode: 'managed', acme_challenge: 'dns-01', dns_provider_ref: 'dns-prod' }, last_error: 'DNS propagation pending' }
  const certificate = { id: 'cert', pool_id: 'pool', provider: 'custom', status: 'ACTIVE', domains: ['web.example.test'], not_before: '2020-01-01T00:00:00Z', not_after: '2100-01-01T00:00:00Z', fingerprint: 'certificate-fingerprint', last_error: 'certificate refresh failed' }
  send.mockImplementation(async (method, path) => {
    if (method !== 'get') return { data: { id: path === '/certificates' ? 'cert-imported' : 'operation' }, response: new Response(null, { status: 202 }) }
    const values: Record<string, unknown> = {
      '/services/{id}': { id: 'svc', runtime_id: 'rt', application_id: 'app', managed: true, system: false, discovered_port: 8080, environment: { id: 'env' } },
      '/runtimes/{id}': { id: 'rt', pool_id: 'pool', management_enabled: true },
      '/settings': { management: { enabled: true }, acme: { dns_providers: { 'dns-prod': { configured: true } } } },
      '/services/{id}/domain-bindings': [{ id: 'binding', editable: true, status: 'ERROR', domain, route: { revision: 4, path: '/', backend: { port: 8080, protocol: 'http' }, last_error: 'route not loaded' }, dns_targets: [{ gateway_id: 'gw', gateway_name: 'edge', record_type: 'A', value: '192.0.2.20' }] }],
      '/domains': [domain, { ...domain, id: 'foreign-domain', pool_id: 'other-pool', host: 'foreign.example.test' }],
      '/routes': [{ domain_id: domain.id, backend: { service_id: 'svc' } }],
      '/services': [{ id: 'svc', environment: { id: 'env' } }],
      '/certificates': [certificate, { ...certificate, id: 'foreign-cert', pool_id: 'other-pool' }],
      '/gateways': [{ id: 'gw', pool_id: 'pool', name: 'edge', enabled: true, status: 'DEGRADED', evidence: { configuration_state: 'UNKNOWN', observed_at: '2026-10-06T00:00:00Z' } }],
      '/operations': [], '/dns-provider-profiles': [],
    }
    return { data: values[path], response: new Response(null) }
  })
}

it('edits an existing binding with separate domain and route revisions and clears imported secrets', async () => {
  existingBindings()
  const { wrapper, cleanup } = await panel()
  try {
    expect(wrapper.text()).toContain('DNS propagation pending'); expect(wrapper.text()).toContain('route not loaded')
    expect(wrapper.text()).toContain('192.0.2.20'); expect(wrapper.text()).toContain('UNKNOWN')
    await wrapper.get('.binding-row button').trigger('click')
    expect(wrapper.get('input[name=host]').attributes('readonly')).toBeDefined()
    await wrapper.get('input[name=path]').setValue('/api'); await wrapper.get('input[name=port]').setValue(9443)
    await wrapper.findAll('form select')[0]!.setValue('https')
    const serverName = wrapper.findAll('form input').find(input => input.attributes('autocomplete') === 'off' && !input.attributes('name'))!
    await serverName.setValue('upstream.example.test')
    await wrapper.get('select[name=tls]').setValue('custom')
    expect(wrapper.text()).not.toContain('foreign-cert')
    const importButton = wrapper.findAll('form button').find(button => button.text() === 'bindingUI.import')!
    await importButton.trigger('click')
    await wrapper.findAll('textarea')[0]!.setValue('fixture-chain'); await wrapper.findAll('textarea')[1]!.setValue('fixture-private-key')
    await wrapper.get('.binding-import button').trigger('click'); await flushPromises()
    expect(send).toHaveBeenCalledWith('post', '/certificates', expect.objectContaining({ body: { pool_id: 'pool', certificate_pem: 'fixture-chain', private_key_pem: 'fixture-private-key' } }))
    expect(wrapper.find('textarea').exists()).toBe(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(send).toHaveBeenCalledWith('patch', '/services/{id}/domain-bindings/{bindingID}', expect.objectContaining({
      params: { path: { id: 'svc', bindingID: 'binding' } }, headers: expect.objectContaining({ 'If-Match': '"rev-4"' }),
      body: expect.objectContaining({ domain_revision: 3, domain: expect.objectContaining({ tls: { mode: 'custom', certificate_id: 'cert-imported' } }), route: { path: '/api', backend: { port: 9443, protocol: 'https', server_name: 'upstream.example.test' } } }),
    }))
    expect(wrapper.find('form').exists()).toBe(false); expect(wrapper.get('[role=status]').text()).toContain('bindingUI.submitted')
  } finally { cleanup() }
})

it('protects unsaved binding input and attaches only a domain in the service environment', async () => {
  existingBindings()
  const { wrapper, cleanup } = await panel()
  try {
    await wrapper.get('header button').trigger('click')
    expect(wrapper.findAll('form option').some(option => option.text() === 'foreign.example.test')).toBe(false)
    await wrapper.get('input[name=path]').setValue('/health')
    const event = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(event); expect(event.defaultPrevented).toBe(true)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    await wrapper.findAll('form button').at(-1)!.trigger('click'); expect(wrapper.find('form').exists()).toBe(true)
    expect((wrapper.get('input[name=path]').element as HTMLInputElement).value).toBe('/health')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(send).toHaveBeenCalledWith('post', '/services/{id}/domain-bindings', expect.objectContaining({ body: { domain_id: 'domain', route: { path: '/health', backend: { port: 8080, protocol: 'http', server_name: '' } } } }))
    await wrapper.get('header button').trigger('click'); await wrapper.get('input[name=path]').setValue('/changed')
    confirm.mockReturnValue(true); await wrapper.findAll('form button').at(-1)!.trigger('click'); expect(wrapper.find('form').exists()).toBe(false)
  } finally { cleanup() }
})

it('preserves an unsuccessful retry key and confirms removal against the binding revision', async () => {
  existingBindings()
  const { wrapper, cleanup } = await panel()
  try {
    const retry = wrapper.findAll('.binding-row button').find(button => button.text() === 'bindingUI.retry')!
    send.mockRejectedValueOnce(new Error('response lost'))
    await retry.trigger('click'); await flushPromises(); expect(wrapper.get('[role=alert]').text()).toBe('response lost')
    await retry.trigger('click'); await flushPromises()
    const calls = send.mock.calls.filter(([, path]) => path.endsWith('/retry'))
    expect(calls).toHaveLength(2); expect(calls[0]![2].headers['Idempotency-Key']).toBeTruthy()
    expect(calls[1]![2].headers['Idempotency-Key']).toBe(calls[0]![2].headers['Idempotency-Key'])
    const remove = wrapper.findAll('.binding-row button')[1]!, confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    await remove.trigger('click'); expect(send.mock.calls.some(([method]) => method === 'delete')).toBe(false)
    confirm.mockReturnValue(true); await remove.trigger('click'); await flushPromises()
    expect(send).toHaveBeenCalledWith('delete', '/services/{id}/domain-bindings/{bindingID}', expect.objectContaining({ headers: expect.objectContaining({ 'If-Match': '"rev-4"' }), params: { path: { id: 'svc', bindingID: 'binding' } } }))
  } finally { cleanup() }
})
