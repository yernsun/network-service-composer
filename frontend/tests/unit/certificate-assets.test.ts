import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { h, ref } from 'vue'
import CertificatesListPage from '../../src/views/CertificatesListPage.vue'
import { queryClient, invalidateWorkloadInventory } from '../../src/data/inventory-cache'
import type { components } from '../../src/shared/api/schema'

type Certificate = components['schemas']['Certificate']
const send = vi.hoisted(() => vi.fn())
const snapshot = vi.hoisted(() => ({
  certificates: [] as Certificate[],
  domains: [] as { id: string; name: string; project: string; env: string; environmentIds?: string[]; tls: { certificate_id: string } }[],
  environments: [{ id: 'e', name: 'Production' }], project: 'p', env: 'e',
}))
vi.mock('../../src/stores/workspace', () => ({ useWorkspaceStore: () => ({ state: () => snapshot }) }))
vi.mock('../../src/shared/api/client', () => ({ session: ref({ role: 'viewer' }), errorText: (e: Error) => e?.message || '', request: send, asRows: (data: unknown) => Array.isArray(data) ? data : [] }))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))

let wrapper: VueWrapper | undefined
let server: Certificate[]
const certificate = (id: string, extra: Partial<Certificate> = {}): Certificate => ({
  created_at: '2026-10-08T00:00:00Z', updated_at: '2026-10-08T00:00:00Z', revision: 1,
  id, pool_id: 'pool', provider: 'acme', status: 'ACTIVE', primary_domain: id + '.example.test', domains: [id + '.example.test'], ...extra,
})
function result(data: Certificate[], cursor = '') {
  return { data, pagination: { next_cursor: cursor }, response: new Response(null) }
}
async function page(project = 'all', environment = 'all') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/projects/:project/environments/:environment/certificates', component: CertificatesListPage }] })
  await router.push(`/projects/${project}/environments/${environment}/certificates`); await router.isReady()
  wrapper = mount({ render: () => h(RouterView) }, { attachTo: document.body, global: { plugins: [router, [VueQueryPlugin, { queryClient }]] } })
  await flushPromises()
  return { view: wrapper, router }
}
function rows() { return wrapper!.findAll('[data-certificate-id]').map(row => row.attributes('data-certificate-id')) }
beforeEach(() => {
  snapshot.certificates = []; snapshot.domains = []
  server = [certificate('automatic'), certificate('imported', { provider: 'custom' })]
  send.mockReset().mockImplementation(async () => result(server))
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; queryClient.clear(); document.body.innerHTML = ''; vi.useRealTimers() })

describe('managed certificate inventory', () => {
  it('shows every paginated asset globally, including unbound automatic and imported certificates', async () => {
    const first = certificate('pending', { status: 'PENDING', acme_challenge: 'dns-01', dns_provider_ref: 'production-dns' })
    send.mockImplementation(async (_method, _path, options) => options.params.query.cursor
      ? result([certificate('unbound', { provider: 'custom', domains: ['*.example.test', 'example.test'] })]) : result([first], 'next'))
    const { view } = await page()
    expect(rows()).toEqual(['pending', 'unbound'])
    expect(view.get('[data-certificate-id=pending]').text()).toContain('certAssets.automatic')
    expect(view.get('[data-certificate-id=pending]').text()).toContain('PENDING')
    expect(view.get('[data-certificate-id=pending]').text()).toContain('dns-01')
    expect(view.get('[data-certificate-id=unbound]').text()).toContain('*.example.test')
    expect(view.get('[data-certificate-id=unbound]').text()).toContain('certAssets.unbound')
    expect(send.mock.calls.map(call => call[0])).toEqual(['get', 'get'])
  })
  it('shows and filters IP-only assets, including legacy null DNS metadata', async () => {
    const legacy: unknown = { ...certificate('console'), provider: 'self-signed', primary_domain: '192.0.2.17', domains: null, ip_addresses: ['192.0.2.17', '2001:db8::17'] }
    send.mockResolvedValue({ data: [legacy], pagination: { next_cursor: '' }, response: new Response(null) })
    const { view } = await page()
    expect(rows()).toEqual(['console'])
    expect(view.text()).toContain('selfSigned.label')
    await view.get('#certificate-domain-filter').setValue('2001:db8::17')
    expect(rows()).toEqual(['console'])
    await view.get('#certificate-domain-filter').setValue('192.0.2.99')
    expect(rows()).toEqual([])
  })
  it('keeps narrower scopes tied to explicit domain links, including a shared multi-environment domain', async () => {
    server = [certificate('shared'), certificate('other'), certificate('unbound')]
    snapshot.domains = [
      { id: 'one', name: 'one.example.test', project: 'p', env: 'e', tls: { certificate_id: 'shared' } },
      { id: 'two', name: 'two.example.test', project: 'p', env: 'preview', environmentIds: ['preview', 'e'], tls: { certificate_id: 'shared' } },
      { id: 'foreign', name: 'shared.example.test', project: 'other', env: 'other', tls: { certificate_id: 'other' } },
    ]
    const { view, router } = await page('p', 'e')
    expect(rows()).toEqual(['shared'])
    expect(view.text()).toContain('one.example.test'); expect(view.text()).toContain('two.example.test')
    await router.push('/projects/p/environments/all/certificates'); await flushPromises()
    expect(rows()).toEqual(['shared'])
    await router.push('/projects/other/environments/other/certificates'); await flushPromises()
    expect(rows()).toEqual(['other'])
    await router.push('/projects/p/environments/missing/certificates'); await flushPromises()
    expect(rows()).toEqual([]); expect(view.text()).toContain('certAssets.empty')
  })
  it('filters locally by primary name, SAN, linked hostname and single-label wildcard coverage', async () => {
    server = [certificate('wildcard', { primary_domain: '*.tenant.test', domains: ['*.tenant.test', 'alias.example.test'] }), certificate('linked'), certificate('primary', { primary_domain: 'primary.only.test', domains: [] })]
    snapshot.domains = [{ id: 'domain', name: 'linked-host.test', project: 'p', env: 'e', tls: { certificate_id: 'linked' } }]
    const { view } = await page()
    const input = view.get('#certificate-domain-filter')
    for (const [value, expected] of [[' ALIAS.EXAMPLE ', 'wildcard'], ['one.tenant.test', 'wildcard'], ['linked-host.test', 'linked'], ['primary.only', 'primary']]) {
      await input.setValue(value); expect(rows()).toEqual([expected])
    }
    await input.setValue('two.one.tenant.test')
    expect(rows()).toEqual([]); expect(view.text()).toContain('certAssets.emptyFiltered')
    await input.setValue(''); expect(rows()).toHaveLength(3)
    expect(send).toHaveBeenCalledOnce()
  })
  it('updates signed metadata and discovers new background assets without navigation or losing the filter', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] })
    server = [certificate('automatic', { status: 'PENDING' })]
    const { view } = await page()
    const input = view.get('#certificate-domain-filter')
    await input.setValue('example.test'); (input.element as HTMLInputElement).focus()
    expect(view.get('.badge').text()).toBe('PENDING')
    server = [certificate('automatic', { status: 'ISSUED', not_after: '2099-05-01T00:00:00Z', domains: ['automatic.example.test', 'signed.example.test'] })]
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(view.get('.badge').text()).toBe('ISSUED')
    expect(view.text()).toContain('signed.example.test'); expect(view.text()).toContain('2099')
    server = [certificate('automatic', { status: 'ACTIVE' })]
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(view.get('.badge').text()).toBe('ACTIVE')
    server = [...server, certificate('new-background', { status: 'PENDING' })]
    await vi.advanceTimersByTimeAsync(30000); await flushPromises()
    expect(rows()).toEqual(['automatic', 'new-background'])
    expect((input.element as HTMLInputElement).value).toBe('example.test'); expect(document.activeElement).toBe(input.element)
    expect(send.mock.calls.every(call => call[0] === 'get')).toBe(true)
  })
  it('retains the last certificate list after a failed refresh and retries only the read', async () => {
    const { view } = await page()
    await view.get('#certificate-domain-filter').setValue('automatic')
    send.mockRejectedValueOnce(new Error('read unavailable'))
    await view.get('[data-testid=certificate-refresh]').trigger('click'); await flushPromises()
    expect(view.get('[role=alert]').text()).toBe('refresh.failed'); expect(rows()).toEqual(['automatic'])
    server = [certificate('automatic', { status: 'RENEWING', last_error: 'Renewal awaiting DNS' })]
    await view.get('[data-testid=certificate-refresh]').trigger('click'); await flushPromises()
    expect(view.find('[role=alert]').exists()).toBe(false)
    expect(view.get('.badge').text()).toBe('RENEWING'); expect(view.text()).toContain('Renewal awaiting DNS')
    expect((view.get('#certificate-domain-filter').element as HTMLInputElement).value).toBe('automatic')
    expect(send.mock.calls.every(call => call[0] === 'get')).toBe(true)
  })
  it('refreshes the visible certificate query immediately when workload state changes', async () => {
    const { view } = await page()
    server = [certificate('automatic', { status: 'ACTIVE' }), certificate('new-issued', { status: 'ISSUED' })]
    invalidateWorkloadInventory(); await flushPromises()
    expect(rows()).toEqual(['automatic', 'new-issued']); expect(view.text()).toContain('ISSUED')
    expect(send).toHaveBeenCalledTimes(2)
  })
  it('aborts outstanding reads and stops polling when the page is closed', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    let finish!: (value: ReturnType<typeof result>) => void
    send.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    await page()
    const signal = send.mock.calls[0]![2].signal as AbortSignal
    wrapper!.unmount(); wrapper = undefined
    expect(signal.aborted).toBe(true)
    finish(result([certificate('late')]))
    await vi.advanceTimersByTimeAsync(60000); await flushPromises()
    expect(send).toHaveBeenCalledOnce()
    expect(document.querySelector('[data-certificate-id]')).toBeNull()
  })
})
