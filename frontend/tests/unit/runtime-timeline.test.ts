import { flushPromises, mount } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { ref } from 'vue'
import { afterEach, expect, it, vi } from 'vitest'
import RuntimeTimeline from '../../src/components/RuntimeTimeline.vue'

const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, session: ref({ locale: 'en' }), errorText: (error: Error) => error.message }))
const state = { projects: [{ id: 'project', name: 'demoapp' }], environments: [{ id: 'env', name: 'prod', runtime_id: 'runtime' }],
  runtimes: [{ id: 'runtime', name: 'Docker' }], apps: [{ id: 'app', runtime: 'runtime' }],
  services: [{ id: 'service', runtimeId: 'runtime', name: 'api', project: 'project', env: 'env', app: 'app' }] }
const operation = { id: 'op-scale', operation_id: 'op-scale', source: 'operation', runtime_id: 'runtime', service_id: 'service',
  application_id: 'app', project_id: 'project', environment_id: 'env', actor_type: 'operation', action: 'SCALE', category: 'planned',
  status: 'SUCCEEDED', oom_killed: false, occurred_at: '2026-09-25T01:00:00Z', observed_at: '2026-09-25T01:00:00Z' }
const queries: QueryClient[] = []
afterEach(() => { queries.forEach(query => query.clear()); queries.length = 0; send.mockReset() })
function screen(scope = {}) {
  const query = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  queries.push(query)
  return mount(RuntimeTimeline, { props: { state, projectId: 'project', environmentId: 'env', ...scope }, global: { plugins: [[VueQueryPlugin, { queryClient: query }]] } })
}

it('shows historical scale records in the current scope and switches to all authorized resources', async () => {
  send.mockImplementation(async (_method, _path, options) => ({
    data: options.params.query.project_id ? [operation] : [{ ...operation, id: 'op-unknown', operation_id: 'op-unknown', project_id: '', environment_id: '' }],
    pagination: { next_cursor: '', limit: 50 }, coverage: { state: 'collecting', retained_from: '2026-06-27T00:00:00Z', sources: [{ id: 'runtime:runtime', state: 'collecting' }] },
  }))
  const wrapper = screen()
  try {
    await flushPromises()
    expect(send.mock.calls[0]?.[2].params.query).toMatchObject({ project_id: 'project', environment_id: 'env', runtime_id: 'runtime' })
    expect(wrapper.find('a[href="/operations/op-scale"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('2026-09-12')
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await flushPromises()
    expect(send.mock.calls.at(-1)?.[2].params.query.project_id).toBeUndefined()
    expect(wrapper.text()).toContain('Scope unknown')
  } finally { wrapper.unmount() }
})

it('paginates records and clears the cursor when the category changes', async () => {
  send.mockImplementation(async () => ({ data: [operation], pagination: { next_cursor: 'page-2', limit: 50 },
    coverage: { state: 'collecting', retained_from: '2026-06-27T00:00:00Z', sources: [{ id: 'runtime:runtime', state: 'collecting' }] } }))
  const wrapper = screen()
  try {
    await flushPromises()
    await wrapper.get('.timeline-pages button').trigger('click')
    await flushPromises()
    expect(send.mock.calls.at(-1)?.[2].params.query.cursor).toBe('page-2')
    await wrapper.findAll('select')[2]!.setValue('health')
    await flushPromises()
    expect(send.mock.calls.at(-1)?.[2].params.query).toMatchObject({ category: 'health', runtime_id: 'runtime' })
    expect(send.mock.calls.at(-1)?.[2].params.query.cursor).toBeUndefined()
  } finally { wrapper.unmount() }
})

it('uses the same timeline for application and service tabs and keeps gap evidence distinct', async () => {
  const health = { ...operation, id: 'docker-health', source: 'docker', operation_id: '', action: 'health_status: healthy',
    actor_type: 'container', actor_id: 'container-1', status: 'observed', category: 'health' }
  const gap = { ...operation, id: 'gap-1', source: 'gap', operation_id: '', action: 'collection_gap', actor_type: 'gap',
    project_id: '', environment_id: '', category: 'gap', status: 'stream_disconnected' }
  send.mockResolvedValue({ data: [health, gap], pagination: { next_cursor: '', limit: 50 },
    coverage: { state: 'partial', retained_from: '2026-06-27T00:00:00Z', sources: [{ id: 'node-a', node_id: 'node-a', state: 'unavailable', last_error: 'stream_disconnected' }] } })
  for (const scope of [{ applicationId: 'app' }, { serviceId: 'service' }]) {
    const wrapper = screen(scope)
    try {
      await flushPromises()
      expect(send.mock.calls.at(-1)?.[2].params.query).toMatchObject({ runtime_id: 'runtime',
        ...(scope.applicationId ? { application_id: 'app' } : { service_id: 'service' }) })
      expect(wrapper.text()).toContain('Observed')
      expect(wrapper.text()).toContain('Event stream disconnected')
      expect(wrapper.text()).not.toContain('collection_gap')
      expect(wrapper.text()).not.toContain('Cause undetermined')
      expect(wrapper.find('a[href^="/operations/"]').exists()).toBe(false)
    } finally { wrapper.unmount() }
  }
})

it('shows colored result badges for progress, health failures, shutdown and collection gaps', async () => {
  const docker = { ...operation, source: 'docker', operation_id: '', actor_type: 'task', action: 'update' }
  send.mockResolvedValue({
    data: [
      { ...operation, id: 'failed', status: 'FAILED' },
      { ...docker, id: 'starting', status: 'starting' },
      { ...docker, id: 'running', status: 'running' },
      { ...docker, id: 'unhealthy', status: 'unhealthy', action: 'health_status: unhealthy' },
      { ...docker, id: 'shutdown', status: 'shutdown' },
      { ...docker, id: 'observed', status: 'observed' },
      { ...docker, id: 'gap', source: 'gap', status: 'stream_disconnected', action: 'collection_gap' },
    ],
    pagination: { next_cursor: '', limit: 50 },
    coverage: { state: 'partial', retained_from: '2026-06-27T00:00:00Z', sources: [] },
  })
  const wrapper = screen()
  try {
    await flushPromises()
    expect(wrapper.get('.timeline-alert .badge').classes()).toContain('amber')
    const badges = wrapper.findAll('tbody tr .timeline-result .badge')
    expect(badges).toHaveLength(7)
    expect(badges.map(badge => badge.classes())).toEqual([
      expect.arrayContaining(['red']),
      expect.arrayContaining(['blue']),
      expect.arrayContaining(['green']),
      expect.arrayContaining(['red']),
      expect.arrayContaining(['badge']),
      expect.arrayContaining(['blue']),
      expect.arrayContaining(['amber']),
    ])
    expect(badges[4]!.classes()).toEqual(['badge'])
  } finally { wrapper.unmount() }
})

it('distinguishes missing collection from a read error', async () => {
  send.mockResolvedValue({ data: [], pagination: { next_cursor: '', limit: 50 }, coverage: { state: 'unavailable', retained_from: '2026-06-27T00:00:00Z', sources: [{ id: 'node-a', node_id: 'node-a', state: 'unavailable', last_error: 'collector_not_configured' }] } })
  const wrapper = screen()
  try {
    await flushPromises()
    expect(wrapper.text()).toContain('Runtime event collection is unavailable')
    expect(wrapper.text()).toContain('Collector is not configured')
    expect(wrapper.text()).not.toContain('Could not read records')
  } finally { wrapper.unmount() }
  send.mockRejectedValue(new Error('read failed'))
  const failed = screen()
  try {
    await flushPromises()
    expect(failed.text()).toContain('Could not read records')
    expect(failed.text()).not.toContain('No collected events')
  } finally { failed.unmount() }
})
