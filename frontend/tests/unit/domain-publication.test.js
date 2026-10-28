import { beforeEach, expect, it, vi } from 'vitest'
import { routeEditorRow, routeWrite, saveDomainDraft } from '../../src/data/domain-publication'
import { request } from '../../src/shared/api/client'
import { allPages } from '../../src/data/pagination'
vi.mock('../../src/shared/api/client', async importOriginal => ({ ...(await importOriginal()), request: vi.fn() }))
vi.mock('../../src/data/pagination', () => ({ allPages: vi.fn() }))
beforeEach(() => vi.clearAllMocks())
it('serializes fixed responses, redirects and external proxies without an invented Service', () => {
  for (const [kind, value] of [['return', 'hello'], ['redirect', 'https://example.test/']]) {
    const write = routeWrite({ id: kind, kind, match: 'exact', path: '/hello', code: kind === 'return' ? 200 : 308, value, enabled: true }, 'domain')
    expect(write.route.backend.service_id).toBe('')
    expect(write.route.external_url).toBe('')
    expect(write.route.response_value).toBe(value)
  }
  const external = routeWrite({ id: 'url', kind: 'proxy', match: 'prefer', path: '/api/', externalUrl: 'https://internal.test/', timeout: 20, connect: 5, enabled: true }, 'domain')
  expect(external.route.backend.service_id).toBe('')
  expect(external.route.external_url).toBe('https://internal.test/')
  expect(routeEditorRow({ id: 'url', kind: 'proxy', path: '/api/', backend: { service_id: '' }, external_url: external.route.external_url, enabled: true }).externalUrl).toBe(external.route.external_url)
})
it('preserves another domain draft and snippets while saving with the Pool ETag', async () => {
  const other = { route: { id: 'other', domain_id: 'other-domain' }, expected_revision: 2 }
  const shared = [{ scope: 'http', content: 'gzip on;' }, { scope: 'server', resource_id: 'other-domain', content: 'add_header X-Other yes;' }]
  const active = { revision: 4, snippets: shared }
  const draft = { revision: 8, snippets: [...shared, { scope: 'server', resource_id: 'domain', content: 'add_header X-Old yes;' }], routes: [other] }
  request.mockImplementation(async (method, path, options) => {
    if (method === 'get') return { data: { active, draft }, response: new Response(null, { headers: { ETag: '"rev-8"' } }) }
    expect(path).toBe('/gateway-pools/{id}/nginx/draft')
    expect(options.headers['If-Match']).toBe('"rev-8"')
    expect(options.body.routes[0]).toEqual(other)
    expect(options.body.routes[1].route).toMatchObject({ id: 'new', domain_id: 'domain', kind: 'return', backend: { service_id: '' } })
    expect(options.body.snippets).toEqual([...shared, { scope: 'server', resource_id: 'domain', content: 'add_header X-New yes;' }])
    return { data: { revision: 9 } }
  })
  allPages.mockResolvedValue([])
  const work = { routes: [{ id: 'new', kind: 'return', match: 'exact', path: '/hello', code: 200, value: 'hello', enabled: true }], serverSnippet: 'add_header X-New yes;' }
  expect(await saveDomainDraft({ id: 'domain', pool_id: 'pool' }, [], work)).toEqual({ revision: 9 })
})

it('rejects a stale IP editor without overwriting a newer restriction', async () => {
  const active = { routes: [], ipAccess: { access: ['10.0.0.0/8'], forbidden: [] } }
  request.mockResolvedValue({ data: { active: { access_controls: [{ scope: 'server', resource_id: 'domain', access: ['10.0.0.0/8'], forbidden: ['10.0.0.1'] }] }, draft: { revision: 0 } }, response: new Response() })
  allPages.mockResolvedValue([])
  await expect(saveDomainDraft({ id: 'domain', pool_id: 'pool', active }, [], { ...active, serverSnippet: '', ipAccess: { access: [], forbidden: [] } })).rejects.toMatchObject({ status: 412 })
  expect(request).toHaveBeenCalledOnce()
})
it('rejects a stale client IP source without overwriting new trusted proxy settings', async () => {
  const source = { header: 'X-Forwarded-For', trusted_proxies: ['10.0.0.1'] }
  const active = { routes: [], ipAccess: { access: [], forbidden: [], clientIP: source } }
  request.mockResolvedValue({ data: { active: { access_controls: [{ scope: 'server', resource_id: 'domain', access: [], forbidden: [], client_ip: { ...source, trusted_proxies: ['10.0.0.2'] } }] }, draft: { revision: 0 } }, response: new Response() })
  allPages.mockResolvedValue([])
  await expect(saveDomainDraft({ id: 'domain', pool_id: 'pool', active }, [], active)).rejects.toMatchObject({ status: 412 })
  expect(request).toHaveBeenCalledOnce()
})
