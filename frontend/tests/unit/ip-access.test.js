import { expect, it } from 'vitest'
import { domainAccessControls, ipAccessFor } from '../../src/data/ip-access'
import { domainHistoryEntries, domainHistoryDetail } from '../../src/data/domain-history'
it('retains raw ranges, removes cleared and deleted rules, and preserves other resources', () => {
  const foreign = { scope: 'server', resource_id: 'foreign', access: ['::1'], forbidden: [] }
  const base = [foreign, { scope: 'server', resource_id: 'domain', access: ['10.0.0.0/8'], forbidden: [] }, { scope: 'location', resource_id: 'deleted', access: ['127.0.0.1'], forbidden: [] }]
  const raw = ' 192.168.1.10-192.168.1.20 '
  const work = { ipAccess: { access: [raw], forbidden: [] }, routes: [{ id: 'route', ipAccess: { access: [], forbidden: ['2001:db8::/32'] } }] }
  const controls = domainAccessControls(base, 'domain', [{ id: 'deleted' }], work)
  expect(controls).toEqual([foreign, { scope: 'server', resource_id: 'domain', access: [raw], forbidden: [] }, { scope: 'location', resource_id: 'route', access: [], forbidden: ['2001:db8::/32'] }])
  const lists = ipAccessFor(controls, 'server', 'domain'); lists.access.push('::1')
  expect(controls[1].access).toEqual([raw])
  expect(ipAccessFor(controls, 'server', 'missing')).toEqual({ access: [], forbidden: [] })
  expect(domainAccessControls(controls, 'domain', [{ id: 'route' }], { ipAccess: { access: [], forbidden: [] }, routes: [] })).toEqual([foreign])
})
it('shows IP access changes and raw values in domain history with honest baseline evidence', () => {
  const original = { id: 'v1', created_at: '2026-10-01', access_controls: [{ scope: 'server', resource_id: 'domain', access: ['10.0.0.0/8'], forbidden: [] }] }
  const current = { id: 'v2', parent_version: 'v1', created_at: '2026-10-02', access_controls: [{ scope: 'server', resource_id: 'domain', access: ['10.0.0.0/8'], forbidden: ['10.0.0.10-10.0.0.20'] }] }
  const entries = domainHistoryEntries([original, current], [], 'domain')
  expect(entries[0].scope).toBe('domain')
  expect(domainHistoryDetail(entries[0])[0]).toMatchObject({ type: 'access', action: 'updated', resourceId: 'domain' })
  expect(domainHistoryDetail(entries[0])[0].lines).toContainEqual(expect.objectContaining({ type: 'add', text: expect.stringContaining('10.0.0.10-10.0.0.20') }))
  expect(entries[1].baselineAvailable).toBe(false)
  expect(domainHistoryDetail(entries[1])[0].lines).toBeNull()
})
it('retains source-only policies, clones trusted ranges and shows source changes in history', () => {
  const source = { header: 'X-Forwarded-For', trusted_proxies: [' 10.0.0.1-10.0.0.9 '] }
  const controls = domainAccessControls([], 'domain', [], { ipAccess: { access: [], forbidden: [], clientIP: source }, routes: [] })
  expect(controls).toEqual([{ scope: 'server', resource_id: 'domain', access: [], forbidden: [], client_ip: source }])
  const work = ipAccessFor(controls, 'server', 'domain')
  work.clientIP.trusted_proxies.push('::1')
  expect(controls[0].client_ip.trusted_proxies).toEqual([' 10.0.0.1-10.0.0.9 '])
  const history = domainHistoryEntries([{ id: 'v1', created_at: '2026-10-01', access_controls: [] }, { id: 'v2', parent_version: 'v1', created_at: '2026-10-02', access_controls: controls }], [], 'domain')
  expect(domainHistoryDetail(history[0])[0].lines).toContainEqual(expect.objectContaining({ type: 'add', text: expect.stringContaining('X-Forwarded-For') }))
  expect(domainAccessControls(controls, 'domain', [], { ipAccess: { access: [], forbidden: [] }, routes: [] })).toEqual([])
})
