import { expect, it } from 'vitest'
import { diffLines, domainHistoryDetail, domainHistoryEntries } from '../../src/data/domain-history'

it('keeps Pool releases and saved drafts ordered while attributing only evidenced domain content', () => {
  const published = [
    { id: 'ngver_1', created_at: '2026-09-25T10:00:00Z', actor: 'alice', routes: [{ route: { id: 'route-a', domain_id: 'domain-a', path: '/' } }], snippets: [] },
    { id: 'ngver_2', parent_version: 'ngver_1', created_at: '2026-09-25T11:00:00Z', actor: 'bob', routes: [{ route: { id: 'route-b', domain_id: 'domain-b', path: '/elsewhere' } }], snippets: [] },
  ]
  const drafts = [
    { id: 'ngdraftver_1', created_at: '2026-09-25T12:00:00Z', actor: 'alice', routes: [], snippets: [{ scope: 'server', resource_id: 'domain-a', content: 'add_header X-First yes;' }] },
    { id: 'ngdraftver_2', created_at: '2026-09-25T13:00:00Z', actor: 'alice', routes: [], snippets: [] },
  ]
  const entries = domainHistoryEntries(published, drafts, 'domain-a', ['route-a'])
  expect(entries.map(entry => [entry.id, entry.type, entry.scope])).toEqual([
    ['ngdraftver_2', 'draft', 'domain'], ['ngdraftver_1', 'draft', 'domain'], ['ngver_2', 'published', 'pool'], ['ngver_1', 'published', 'domain'],
  ])
  expect(entries.find(entry => entry.id === 'ngver_1').routePaths).toEqual(['/'])
  expect(entries.find(entry => entry.id === 'ngver_2').routePaths).toEqual([])
})

it('diffs only evidenced snippet changes and keeps Pool-wide HTTP changes visible to a domain', () => {
  const published = [
    { id: 'ngver_a', created_at: '2026-09-25T10:00:00Z', snippets: [
      { scope: 'http', content: 'gzip on;' }, { scope: 'server', resource_id: 'domain-a', content: 'add_header X-Mode old;' },
      { scope: 'location', resource_id: 'route-a', content: 'proxy_set_header X-Test yes;' },
    ], routes: [{ route: { id: 'route-a', domain_id: 'domain-a', path: '/' } }] },
    { id: 'ngver_b', parent_version: 'ngver_a', created_at: '2026-09-25T11:00:00Z', snippets: [
      { scope: 'http', content: 'gzip off;' }, { scope: 'server', resource_id: 'domain-a', content: 'add_header X-Mode new;' },
      { scope: 'server', resource_id: 'domain-b', content: 'add_header X-Other yes;' },
      { scope: 'location', resource_id: 'route-lost', content: 'add_header X-Unknown yes;' },
    ], routes: [] },
  ]
  const entry = domainHistoryEntries(published, [], 'domain-a', ['route-a']).find(item => item.id === 'ngver_b')
  expect(entry.scope).toBe('domain')
  expect(entry.snippetChanged).toBe(true)
  const domain = domainHistoryDetail(entry)
  expect(domain.map(change => [change.snippetScope, change.action])).toEqual([
    ['http', 'updated'], ['location', 'removed'], ['server', 'updated'],
  ])
  expect(domain[1].lines.some(line => line.type === 'remove' && line.text === 'proxy_set_header X-Test yes;')).toBe(true)
  const wholePool = domainHistoryDetail(entry, 'pool')
  expect(wholePool).toHaveLength(5)
  expect(wholePool.find(change => change.resourceId === 'route-lost').scope).toBe('unknown')
  expect(wholePool.find(change => change.resourceId === 'domain-b').scope).toBe('pool')
})

it('uses the last publication as the next draft baseline without treating published route writes as pending', () => {
  const published = [
    { id: 'ngver_a', created_at: '2026-09-25T10:00:00Z', snippets: [{ scope: 'server', resource_id: 'domain-a', content: 'old;' }], routes: [] },
    { id: 'ngver_b', parent_version: 'ngver_a', created_at: '2026-09-25T12:00:00Z', snippets: [{ scope: 'server', resource_id: 'domain-a', content: 'middle;' }],
      routes: [{ expected_revision: 2, route: { id: 'route-a', domain_id: 'domain-a', path: '/old' } }] },
  ]
  const drafts = [
    { id: 'draft_a', created_at: '2026-09-25T11:00:00Z', snippets: [{ scope: 'server', resource_id: 'domain-a', content: 'middle;' }],
      routes: [{ expected_revision: 2, route: { id: 'route-a', domain_id: 'domain-a', path: '/old' } }] },
    { id: 'draft_b', created_at: '2026-09-25T13:00:00Z', snippets: [{ scope: 'server', resource_id: 'domain-a', content: 'new;' }], routes: [] },
  ]
  const entry = domainHistoryEntries(published, drafts, 'domain-a').find(item => item.id === 'draft_b')
  expect([entry.baselineId, entry.baselineType]).toEqual(['ngver_b', 'published'])
  expect(domainHistoryDetail(entry).map(change => [change.type, change.action])).toEqual([['snippet', 'updated']])
})

it('shows draft route writes as changes to the pending set, including withdrawal', () => {
  const first = { id: 'draft_a', created_at: '2026-09-25T10:00:00Z', snippets: [], routes: [
    { expected_revision: 2, route: { id: 'route-a', domain_id: 'domain-a', path: '/old', kind: 'proxy' } },
    { expected_revision: 1, route: { id: 'route-c', domain_id: 'domain-a', path: '/gone', kind: 'return' } },
  ] }
  const second = { id: 'draft_b', created_at: '2026-09-25T11:00:00Z', snippets: [], routes: [
    { expected_revision: 2, route: { id: 'route-a', domain_id: 'domain-a', path: '/new', kind: 'proxy' } },
    { expected_revision: 0, route: { id: 'route-b', domain_id: 'domain-a', path: '/added', kind: 'return' } },
  ] }
  const entry = domainHistoryEntries([], [first, second], 'domain-a').find(item => item.id === 'draft_b')
  expect(domainHistoryDetail(entry).map(change => [change.key, change.action])).toEqual([
    ['route-a', 'pendingUpdated'], ['route-b', 'pendingAdded'], ['route-c', 'pendingRemoved'],
  ])
  expect(entry.routePaths).toEqual(['/new', '/added', '/gone'])
  expect(domainHistoryDetail(entry)[0].lines.some(line => line.type === 'remove' && line.text.includes('/old'))).toBe(true)
})

it('does not call a carried-over pending Route a new domain change', () => {
  const write = { expected_revision: 2, route: { id: 'route-a', domain_id: 'domain-a', path: '/unchanged', kind: 'proxy' } }
  const drafts = [
    { id: 'draft_a', created_at: '2026-09-25T10:00:00Z', snippets: [], routes: [write] },
    { id: 'draft_b', created_at: '2026-09-25T11:00:00Z', snippets: [], routes: [write] },
  ]
  const entry = domainHistoryEntries([], drafts, 'domain-a').find(item => item.id === 'draft_b')
  expect([entry.scope, entry.routePaths, domainHistoryDetail(entry)]).toEqual(['none', [], []])
})

it('does not invent a diff when publication ancestry or collection evidence is missing', () => {
  const published = [{ id: 'ngver_b', parent_version: 'pruned', created_at: '2026-09-25T10:00:00Z',
    snippets: [{ scope: 'server', resource_id: 'domain-a', content: 'add_header X-New yes;' }], routes: [] }]
  const drafts = [{ id: 'draft_a', created_at: '2026-09-25T11:00:00Z', snippets: [{ scope: 'server', resource_id: 'domain-a', content: 'next;' }], routes: [] }]
  const missing = domainHistoryEntries(published, [], 'domain-a')[0]
  expect([missing.baselineAvailable, missing.scope, domainHistoryDetail(missing)[0].lines]).toEqual([false, 'unknown', null])
  const unavailable = domainHistoryEntries(published, drafts, 'domain-a', [], { publishedComplete: false }).find(item => item.id === 'draft_a')
  expect([unavailable.baselineAvailable, unavailable.scope, domainHistoryDetail(unavailable)[0].lines]).toEqual([false, 'unknown', null])
  const pruned = domainHistoryEntries([{ ...published[0], created_at: '2026-09-25T12:00:00Z' }], [
    { ...drafts[0], id: 'draft_older', created_at: '2026-09-25T09:00:00Z' },
    { ...drafts[0], id: 'draft_newer', created_at: '2026-09-25T09:30:00Z' },
  ], 'domain-a').find(item => item.id === 'draft_newer')
  expect(pruned.baselineAvailable).toBe(false)
  const fullHistory = Array.from({ length: 100 }, (_, index) => ({ id: `draft_${index}`, created_at: `2026-09-26T${String(Math.floor(index / 60)).padStart(2, '0')}:${String(index % 60).padStart(2, '0')}:00Z`, snippets: [], routes: [] }))
  const firstRetained = domainHistoryEntries([{ ...published[0], created_at: '2026-09-25T12:00:00Z' }], fullHistory, 'domain-a').find(item => item.id === 'draft_0')
  expect(firstRetained.baselineAvailable).toBe(false)
})

it('identifies published route actions without reconstructing old effective values', () => {
  const published = [{ id: 'ngver_a', created_at: '2026-09-25T10:00:00Z', snippets: [], routes: [
    { expected_revision: 0, route: { id: 'route-a', domain_id: 'domain-a', path: '/new' } },
    { expected_revision: 3, route: { id: 'route-b', domain_id: 'domain-a', path: '/edited' } },
    { expected_revision: 4, delete: true, route: { id: 'route-c', domain_id: 'domain-a', path: '/removed' } },
  ] }]
  const entry = domainHistoryEntries(published, [], 'domain-a')[0]
  expect(domainHistoryDetail(entry).map(change => [change.action, change.beforeUnavailable])).toEqual([
    ['publishedCreate', false], ['publishedUpdate', true], ['publishedDelete', true],
  ])
})

it('provides bounded line-numbered diffs without dropping large saved values', () => {
  expect(diffLines('one\ntwo', 'one\nthree')).toEqual([
    { type: 'same', text: 'one', beforeLine: 1, afterLine: 1 },
    { type: 'remove', text: 'two', beforeLine: 2, afterLine: null },
    { type: 'add', text: 'three', beforeLine: null, afterLine: 2 },
  ])
  expect(diffLines('x\n'.repeat(601), 'y')).toBeNull()
})
