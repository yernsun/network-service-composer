import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { ref } from 'vue'
import DomainHistory from '../../src/components/DomainHistory.vue'
import { i18n, setLocale } from '../../src/shared/i18n'
import { allPages } from '../../src/data/pagination'
import { request } from '../../src/shared/api/client'

vi.mock('../../src/data/pagination', () => ({ allPages: vi.fn() }))
vi.mock('../../src/shared/api/client', () => ({ request: vi.fn(), errorText: error => error.message, session: ref({ locale: 'zh-CN' }) }))

let wrapper
beforeEach(() => {
  setLocale('zh-CN')
  vi.mocked(allPages).mockReset()
  vi.mocked(request).mockReset()
  vi.mocked(request).mockResolvedValue({ data: { active: { version_id: 'ngver_4' }, draft: { revision: 9, updated_at: '2026-09-25T16:20:00Z' } } })
})
afterEach(() => { wrapper?.unmount() })

function show() {
  wrapper = mount(DomainHistory, { props: { poolId: 'pool-a', domainId: 'domain-a', routeIds: ['route-a'] },
    global: { plugins: [i18n, [VueQueryPlugin, { queryClient: new QueryClient({ defaultOptions: { queries: { retry: false } } }) }]] } })
}

it('shows persisted Pool publication records and current draft on the real history page', async () => {
  vi.mocked(allPages).mockImplementation(async path => path.endsWith('/draft-versions') ? [] : [1, 2, 3, 4].map(n => ({
    id: `ngver_${n}`, parent_version: n > 1 ? `ngver_${n - 1}` : '', created_at: `2026-09-25T1${n}:00:00Z`, actor: 'operator', revision: 1,
    routes: [{ route: { id: 'route-a', domain_id: 'domain-a', path: n === 4 ? '/hello' : '/' } }], snippets: [],
  })))
  show()
  await flushPromises()
  expect(wrapper.findAll('.domain-history-item')).toHaveLength(4)
  expect(wrapper.text()).toContain('当前 Pool 草稿')
  expect(wrapper.text()).toContain('/hello')
  expect(wrapper.text()).toContain('Controller 当前版本')
  expect(wrapper.text()).toContain('发布版本')
  expect(wrapper.text()).not.toContain('尚无已保存的历史记录')
})

it('keeps published evidence visible when draft history cannot be read', async () => {
  vi.mocked(allPages).mockImplementation(async path => {
    if (path.endsWith('/draft-versions')) throw new Error('draft history unavailable')
    return [{ id: 'ngver_4', created_at: '2026-09-25T14:00:00Z', routes: [{ route: { id: 'route-a', domain_id: 'domain-a', path: '/' } }], snippets: [] }]
  })
  show()
  await flushPromises()
  expect(wrapper.findAll('.domain-history-item')).toHaveLength(1)
  expect(wrapper.find('[role="alert"]').text()).toContain('draft history unavailable')
})

it('expands a saved definition diff, starts with the domain and can show the whole Pool safely', async () => {
  vi.mocked(allPages).mockImplementation(async path => path.endsWith('/draft-versions') ? [] : [
    { id: 'ngver_1', created_at: '2026-09-25T10:00:00Z', snippets: [
      { scope: 'http', content: 'gzip on;' }, { scope: 'server', resource_id: 'domain-a', content: 'add_header X-Mode old;' },
    ], routes: [] },
    { id: 'ngver_2', parent_version: 'ngver_1', created_at: '2026-09-25T11:00:00Z', actor: 'operator', snippets: [
      { scope: 'http', content: 'gzip off;' }, { scope: 'server', resource_id: 'domain-a', content: 'add_header X-Mode new;' },
      { scope: 'server', resource_id: 'domain-b', content: '<img src=x onerror=alert(1)>' },
      { scope: 'location', resource_id: 'orphan', content: 'add_header X-Unknown yes;' },
    ], routes: [] },
  ])
  show()
  await flushPromises()
  await wrapper.find('.domain-history-open').trigger('click')
  expect(wrapper.find('.domain-history-detail').text()).toContain('ngver_1')
  expect(wrapper.find('.domain-history-detail').text()).toContain('gzip off;')
  expect(wrapper.find('.domain-history-detail').text()).toContain('add_header X-Mode old;')
  expect(wrapper.find('.domain-history-detail').text()).not.toContain('X-Unknown')
  expect(wrapper.findAll('.domain-history-line.add').length).toBeGreaterThan(0)
  expect(wrapper.findAll('.domain-history-line.remove').length).toBeGreaterThan(0)
  await wrapper.findAll('.domain-history-switch button')[1].trigger('click')
  expect(wrapper.find('.domain-history-detail').text()).toContain('X-Unknown')
  expect(wrapper.find('.domain-history-detail').text()).toContain('<img src=x onerror=alert(1)>')
  expect(wrapper.find('.domain-history-detail img').exists()).toBe(false)
  await wrapper.find('.domain-history-open').trigger('click')
  expect(wrapper.find('.domain-history-detail').exists()).toBe(false)
})

it('shows the saved snapshot and missing-baseline reason for an old retained record', async () => {
  vi.mocked(allPages).mockImplementation(async path => path.endsWith('/draft-versions') ? [] : [
    { id: 'ngver_2', parent_version: 'ngver_pruned', created_at: '2026-09-25T11:00:00Z', snippets: [
      { scope: 'server', resource_id: 'domain-a', content: 'add_header X-Known yes;' },
    ], routes: [] },
  ])
  show()
  await flushPromises()
  await wrapper.find('.domain-history-open').trigger('click')
  expect(wrapper.find('.domain-history-detail').text()).toContain('基线不可用')
  expect(wrapper.find('.domain-history-detail').text()).toContain('add_header X-Known yes;')
  expect(wrapper.findAll('.domain-history-line')).toHaveLength(0)
})

it('keeps both saved values readable when a snippet is too long for inline line matching', async () => {
  const before = `${'before\n'.repeat(600)}last-before`
  const after = `${'after\n'.repeat(600)}last-after`
  vi.mocked(allPages).mockImplementation(async path => path.endsWith('/draft-versions') ? [] : [
    { id: 'ngver_1', created_at: '2026-09-25T10:00:00Z', snippets: [{ scope: 'server', resource_id: 'domain-a', content: before }], routes: [] },
    { id: 'ngver_2', parent_version: 'ngver_1', created_at: '2026-09-25T11:00:00Z', snippets: [{ scope: 'server', resource_id: 'domain-a', content: after }], routes: [] },
  ])
  show()
  await flushPromises()
  await wrapper.find('.domain-history-open').trigger('click')
  expect(wrapper.find('.domain-history-detail').text()).toContain('内容过长')
  expect(wrapper.findAll('.domain-history-snapshots pre').map((node, index) => node.text().endsWith(index ? 'last-after' : 'last-before'))).toEqual([true, true])
})

it('labels saved Route actions without claiming to know the previous effective values', async () => {
  vi.mocked(allPages).mockImplementation(async path => path.endsWith('/draft-versions') ? [] : [
    { id: 'ngver_1', created_at: '2026-09-25T10:00:00Z', snippets: [], routes: [] },
    { id: 'ngver_2', parent_version: 'ngver_1', created_at: '2026-09-25T11:00:00Z', snippets: [], routes: [
      { expected_revision: 3, route: { id: 'route-a', domain_id: 'domain-a', path: '/updated', kind: 'proxy' } },
    ] },
  ])
  show()
  await flushPromises()
  await wrapper.find('.domain-history-open').trigger('click')
  expect(wrapper.find('.domain-history-detail').text()).toContain('路由变更前的生效值')
  expect(wrapper.find('.domain-history-detail').text()).toContain('/updated')
  expect(wrapper.findAll('.domain-history-line')).toHaveLength(0)
})
