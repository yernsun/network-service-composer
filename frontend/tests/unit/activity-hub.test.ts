import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import ActivityHub from '../../src/views/ActivityHub.vue'
import { request, session } from '../../src/shared/api/client'
import { i18n, setLocale } from '../../src/shared/i18n'

vi.mock('../../src/shared/api/client', () => ({ request: vi.fn(), session: ref({ authenticated: true, role: 'administrator', user_id: 'admin' }), errorText: () => 'Observation unavailable' }))
let wrapper: VueWrapper | undefined, client: QueryClient, router: Router
const issue = () => ({ id: 'issue-one', revision: 7, code: 'runtime_unavailable', resource_type: 'runtime', resource_id: 'runtime-one', severity: 'critical', active: true, acknowledged: false, first_seen_at: '2026-10-20T00:00:00Z', last_seen_at: '2026-10-20T00:00:00Z', occurrences: 2, link: 'https://untrusted.example.test', scopes: [{ project_id: 'project-one', environment_id: 'env-one' }], observation_state: 'UNKNOWN', operation_id: 'op-one' })
let current = issue()
const response = (data: unknown, next = '') => ({ data, response: new Response(), pagination: { next_cursor: next, limit: 50 } })
beforeEach(() => {
  setLocale('en'); current = issue(); vi.mocked(request).mockReset()
  session.value = { authenticated: true, role: 'administrator', user_id: 'admin', locale: 'en', supported_languages: [] }
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/operations/activity', component: ActivityHub }, { path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
  vi.mocked(request).mockImplementation(async (method, path) => {
    if (method !== 'get') { current = { ...current, acknowledged: true }; return response(current) }
    if (path === '/issues') return response([{ ...current }])
    return response([{ id: 'delivery-one', status: 'DELIVERED', receiver_id: 'receiver-team', event: { id: 'event-one', type: 'runtime.problem', occurred_at: '2026-10-20T00:00:00Z', issue_id: current.id, occurrence: 2, operation_id: 'op-one' } }])
  })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; client.clear() })
async function show(path = '/operations/activity?resource_type=runtime&resource_id=runtime-one') {
  await router.push(path); await router.isReady()
  wrapper = mount(ActivityHub, { global: { plugins: [router, i18n, [VueQueryPlugin, { queryClient: client }]] } }); await flushPromises()
  return wrapper
}

it('uses stored issue ownership and local operation links while separating acknowledgement from recovery', async () => {
  await show()
  expect(wrapper!.get('[data-issue]').text()).toContain('Active')
  expect(wrapper!.get('[data-delivery]').text()).toContain('event-one')
  expect(wrapper!.get('[data-issue]').findAll('a').map(a => a.attributes('href'))).toEqual(['/projects/project-one/environments/env-one/overview', '/operations/op-one'])
  const read = vi.mocked(request).mock.calls.find(([, path]) => path === '/issues')!
  expect(read[2]).toMatchObject({ cache: 'no-store', signal: expect.any(AbortSignal), params: { query: { resource_id: 'runtime-one', resource_type: 'runtime' } } })
  await wrapper!.get('[data-issue]').findAll('button')[0]!.trigger('click'); await flushPromises()
  expect(request).toHaveBeenCalledWith('post', '/issues/{issueID}/acknowledge', expect.objectContaining({ params: { path: { issueID: 'issue-one' } }, headers: { 'If-Match': '"rev-7"' } }))
  expect(wrapper!.get('[data-issue]').text()).toContain('Active')
  expect(wrapper!.get('[data-issue]').text()).toContain('Not confirmed')
  expect(wrapper!.get('[data-issue]').findAll('button')[0]!.attributes('disabled')).toBeDefined()
})
it('retains an unsaved filter on background refresh and applies it through navigation', async () => {
  await show(); await wrapper!.get('select').setValue('resolved')
  await wrapper!.get('button').trigger('click'); await flushPromises()
  expect(wrapper!.get('select').element.value).toBe('resolved')
  expect(router.currentRoute.value.query.state).toBeUndefined()
  await wrapper!.get('form').trigger('submit'); await flushPromises()
  expect(router.currentRoute.value.query.state).toBe('resolved')
})
it('filters associated deliveries by issue and uses the generated ETag on assignment', async () => {
  await show(); await wrapper!.get('[data-issue]').findAll('button')[1]!.trigger('click'); await flushPromises()
  expect(request).toHaveBeenCalledWith('put', '/issues/{issueID}/responsibility', expect.objectContaining({ body: { user_id: 'admin' }, headers: { 'If-Match': '"rev-7"' } }))
  await wrapper!.get('[data-issue]').findAll('button')[2]!.trigger('click'); await flushPromises()
  expect(vi.mocked(request).mock.calls.filter(([, path]) => path === '/notifications').at(-1)?.[2]).toMatchObject({ params: { query: { issue_id: 'issue-one' } } })
})
it('hides old confirmations after a failed refresh and all cached records after session loss', async () => {
  await show(); vi.mocked(request).mockRejectedValue(new Error('offline'))
  await wrapper!.get('button').trigger('click'); await flushPromises()
  expect(wrapper!.find('[data-issue]').exists()).toBe(false)
  expect(wrapper!.find('[data-delivery]').exists()).toBe(false)
  session.value = { ...session.value, authenticated: false }; await flushPromises()
  expect(wrapper!.text()).not.toContain('runtime-one')
})
it('removes delivery metadata and mutations immediately when administrator permission is lost', async () => {
  await show(); session.value = { ...session.value, role: 'viewer' }; await flushPromises()
  expect(wrapper!.find('[data-delivery]').exists()).toBe(false)
  expect(wrapper!.get('[data-issue]').findAll('button')).toHaveLength(0)
})
it.each(['en', 'ja', 'de', 'zh-CN', 'zh-TW', 'fr', 'ru', 'es'])('renders reviewed activity labels in %s', async locale => {
  setLocale(locale); await show()
  expect(wrapper!.text()).not.toMatch(/activityHub\.|routing\.state\./)
  expect(wrapper!.get('h1').text().length).toBeGreaterThan(3)
})
