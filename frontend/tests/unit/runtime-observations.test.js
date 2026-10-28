import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { readRuntimeInventory, runtimeObservation, watchRuntimeObservations } from '../../src/data/runtime-observations'
import { request } from '../../src/shared/api/client'
import { allPages } from '../../src/data/pagination'
vi.mock('../../src/shared/api/client', () => ({ request: vi.fn() }))
vi.mock('../../src/data/pagination', () => ({ allPages: vi.fn() }))
let state, context, stop, signal
beforeEach(() => {
  vi.useFakeTimers()
  state = { project: 'literal-project', env: 'literal-env', runtimes: [{ id: 'rt', nodes: [] }] }
  context = { ui: { page: 'runtime' }, modal: null, workspace: { varsDirty: () => false, docsDirty: () => false }, workload: { x: { runtime: 'rt', runtimeTab: 'nodes' } }, render: vi.fn() }
  signal = new AbortController().signal
  request.mockResolvedValue({ data: { id: 'actual-cluster', observed_at: new Date().toISOString(), stale: false, manager_available: true, error: '', inventory_complete: true } })
  const nodes = [{ id: 'node', role: 'manager', connected: true, stale: false, inventory_complete: true, host: { at: new Date().toISOString(), cpu_percent: 0, memory_used: 0, memory_total: 1024, disks: [] } }, { id: 'unassigned', virtual: true }]
  allPages.mockImplementation(async path => path.endsWith('/instances') ? [] : nodes)
})
afterEach(() => { stop?.(); vi.useRealTimers() })
it('reads current physical-node observations and actual cluster identity', async () => {
  const result = runtimeObservation(await readRuntimeInventory('rt', signal))
  expect(result).toMatchObject({ engineId: 'actual-cluster', managerRead: true, nodes: [{ id: 'node', cpu: 0, agent: true }] })
  expect(allPages).toHaveBeenCalledWith('/runtimes/{id}/cluster/nodes', { id: 'rt' }, signal)
})
it.each([
  { nodes: [{ role: 'worker', connected: true, stale: false }], overview: {} },
  { nodes: [{ role: 'manager', connected: false, stale: false }], overview: {} },
  { nodes: [{ role: 'manager', connected: true, stale: true }], overview: {} },
  { nodes: [{ role: 'manager', connected: true, stale: false }], overview: { stale: true } },
  { nodes: [{ role: 'manager', connected: true, stale: false }], overview: { error: 'manager_required' } },
  { nodes: [{ role: 'manager', connected: true, stale: false }], overview: {}, error: 'unavailable' },
])('does not grant manager capability for worker, disconnected, stale or failed evidence %#', observation => {
  expect(runtimeObservation(observation).managerRead).toBe(false)
})
it('updates only runtime evidence and stops polling on disposal', async () => {
  stop = watchRuntimeObservations({ state, context, signal })
  await vi.advanceTimersByTimeAsync(10000)
  expect(state.runtimes[0].nodes).toHaveLength(1)
  expect(state).toMatchObject({ project: 'literal-project', env: 'literal-env' })
  expect(context.workload.x).toEqual({ runtime: 'rt', runtimeTab: 'nodes' })
  expect(context.render).toHaveBeenCalledTimes(1)
  stop(); await vi.advanceTimersByTimeAsync(20000)
  expect(request).toHaveBeenCalledTimes(1)
})
it('does not overwrite an editor opened while a request was in flight', async () => {
  let finish
  request.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  stop = watchRuntimeObservations({ state, context, signal })
  await vi.advanceTimersByTimeAsync(10000)
  context.modal = { title: 'unchanged editor', value: 'unsaved input' }
  finish({ data: { id: 'actual-cluster' } })
  await vi.advanceTimersByTimeAsync(1)
  expect(state.runtimes[0].nodes).toEqual([])
  expect(context.render).not.toHaveBeenCalled()
  await vi.advanceTimersByTimeAsync(20000)
  expect(request).toHaveBeenCalledTimes(1)
  expect(context.modal.value).toBe('unsaved input')
})
it('marks failed reads unavailable rather than retaining a healthy badge', async () => {
  state.runtimes[0].nodes = [{ id: 'node', agent: true, host: { stale: false }, controller: { stale: false } }]
  request.mockRejectedValue(new Error('offline'))
  stop = watchRuntimeObservations({ state, context, signal })
  await vi.advanceTimersByTimeAsync(10000)
  expect(state.runtimes[0].nodes[0]).toMatchObject({ id: 'node', agent: false, stale: true, host: { stale: true }, controller: { stale: true } })
})

it('refreshes service observations without double-counting and marks partial collection unknown', async () => {
  context.ui.page = 'service'; context.workload.x.service = 'service'
  state.services = [{ id: 'service', runtimeId: 'rt', app: 'app' }]
  state.instances = [{ id: 'preserved', runtime: 'other' }]
  const original = allPages.getMockImplementation()
  allPages.mockImplementation((path, params, ...rest) => path.endsWith('/instances') ? Promise.resolve(params.nodeId === 'unassigned' ? [] : [{ id: 'task', service_id: 'service', state: 'running', kind: 'task', observed_at: new Date().toISOString(), historical: false }]) : original(path, params, ...rest))
  stop = watchRuntimeObservations({ state, context, signal })
  await vi.advanceTimersByTimeAsync(10000)
  expect(state.instances).toHaveLength(2)
  expect(state.instances[1]).toMatchObject({ service: 'service', runtime: 'rt', app: 'app', state: 'RUNNING' })
  expect(state.runtimes[0].inventoryComplete).toBe(true)
  await vi.advanceTimersByTimeAsync(10000)
  expect(state.instances).toHaveLength(2)
  allPages.mockImplementation((path, params, ...rest) => path.endsWith('/instances') ? Promise.reject(new Error('collector disconnected')) : original(path, params, ...rest))
  await vi.advanceTimersByTimeAsync(10000)
  expect(state.runtimes[0].inventoryComplete).toBe(false)
  expect(state.instances).toEqual([{ id: 'preserved', runtime: 'other' }])
})

it('polls only runtimes used by the current list scope and expands for explicit aggregate views', async () => {
  context.ui.page = 'services'; context.workload.x.svcScope = 'environment'
  state.runtimes.push({ id: 'same-project', nodes: [] }, { id: 'foreign', nodes: [] }, { id: 'unowned', nodes: [] })
  state.services = [
    { project: state.project, env: state.env, runtimeId: 'rt' },
    { project: state.project, env: 'another-env', runtimeId: 'same-project' },
    { project: 'foreign', env: 'foreign', runtimeId: 'foreign' },
    { project: '', env: '', runtimeId: 'unowned' },
  ]
  stop = watchRuntimeObservations({ state, context, signal })
  await vi.advanceTimersByTimeAsync(10000)
  expect(request.mock.calls.map(([, , options]) => options.params.path.id)).toEqual(['rt'])
  expect(state.runtimes.find(r => r.id === 'foreign').nodes).toEqual([])
  request.mockClear(); context.workload.x.svcScope = 'project'
  await vi.advanceTimersByTimeAsync(10000)
  expect(request.mock.calls.map(([, , options]) => options.params.path.id)).toEqual(['rt', 'same-project'])
  request.mockClear(); context.workload.x.svcScope = 'all'
  await vi.advanceTimersByTimeAsync(10000)
  expect(request.mock.calls.map(([, , options]) => options.params.path.id)).toEqual(['rt', 'same-project', 'foreign', 'unowned'])
})

it('does not apply a background read after changing environment on the same page', async () => {
  context.ui.page = 'applications'
  state.apps = [{ project: state.project, env: state.env, runtime: 'rt' }]
  let finish
  request.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  stop = watchRuntimeObservations({ state, context, signal })
  await vi.advanceTimersByTimeAsync(10000)
  state.env = 'another-env'
  finish({ data: { id: 'old-scope', inventory_complete: true } })
  await vi.advanceTimersByTimeAsync(1)
  expect(state.runtimes[0].nodes).toEqual([])
  expect(context.render).not.toHaveBeenCalled()
})

it('accepts selected-runtime Manager capability without node collectors', () => {
  expect(runtimeObservation({ overview: { stale: false, manager_available: true }, nodes: [{ role: 'manager', connected: false, stale: false }] }).managerRead).toBe(true)
})
it('does not infer selected-runtime authority from another manager collector', () => {
  expect(runtimeObservation({ overview: { stale: false, manager_available: false }, nodes: [{ role: 'manager', connected: true, stale: false }] }).managerRead).toBe(false)
})
it('withdraws old Manager authority after a failed refresh', async () => {
  state.runtimes[0].managerRead = true
  request.mockRejectedValue(new Error('unavailable'))
  stop = watchRuntimeObservations({ state, context, signal })
  await vi.advanceTimersByTimeAsync(10000)
  expect(state.runtimes[0].managerRead).toBe(false)
})
it.each([undefined, {}, { manager_available: true }, { stale: true, manager_available: true }, { stale: false, manager_available: true, error: 'unavailable' }])('denies missing, stale or failed Manager evidence (%j)', overview => {
  expect(runtimeObservation({ overview, nodes: [] }).managerRead).toBe(false)
})
