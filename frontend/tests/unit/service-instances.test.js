import { describe, expect, it } from 'vitest'
import { dockerHealth, latestInstances, serviceInstances, serviceInstanceSummary, observationUnknown } from '../../src/data/service-instances'
const now = Date.parse('2026-09-17T12:00:00Z')
const runtime = { id: 'swarm', type: 'swarm', connection: 'online', inventoryComplete: true, inventoryObservedAt: new Date(now).toISOString() }
const service = { id: 'controller', runtimeId: 'swarm', desired: 4 }
const task = (id, extra = {}) => ({ id, service: service.id, runtime: runtime.id, kind: 'task', current: true, state: 'RUNNING', health: 'healthy', updated: new Date(now).toISOString(), ...extra })
describe('service observation ownership', () => {
  it('distinguishes Docker without a HEALTHCHECK from missing health evidence', () => {
    expect(dockerHealth('none')).toBe('not-configured')
    expect(dockerHealth('UNKNOWN')).toBe('unknown')
    expect(dockerHealth('')).toBe('unknown')
  })
  it('counts four tasks once while preserving their four Docker observations and old unhealthy containers', () => {
    const tasks = Array.from({ length: 4 }, (_, n) => task(`task-${n}`, { containerId: `container-${n}` }))
    const rows = [...tasks, ...tasks.map(i => ({ ...i, id: i.containerId, runtime: 'docker-node', kind: 'container' })), task('old', { state: 'EXITED', health: 'unhealthy', kind: 'container', runtime: 'docker-node' })]
    const summary = serviceInstanceSummary(rows, service, runtime, now)
    expect(summary.running).toHaveLength(4); expect(summary.unhealthy).toBe(false); expect(summary.unknown).toBe(false)
    expect(rows).toHaveLength(9)
    expect(serviceInstances(rows, service, runtime, true)).toEqual(tasks)
  })
  it('uses the newest observation before deciding whether an instance is current', () => {
    const rows = [task('same'), task('same', { current: false, state: 'SHUTDOWN', updated: new Date(now + 1).toISOString() })]
    expect(serviceInstances(rows, service, runtime)).toEqual([])
    expect(serviceInstances(rows, service, runtime, true)).toHaveLength(1)
    expect(latestInstances([...rows, task('same', { runtime: 'different' })])).toHaveLength(2)
  })
  it('keeps real rolling-update overlap and pending tasks without capping running counts', () => {
    const rows = Array.from({ length: 5 }, (_, n) => task(String(n)))
    rows.push(task('pending', { state: 'PENDING', health: 'unhealthy' }))
    const summary = serviceInstanceSummary(rows, service, runtime, now)
    expect(summary.current).toHaveLength(6); expect(summary.running).toHaveLength(5); expect(summary.unhealthy).toBe(false)
  })
  it('keeps stopped Docker containers visible but excludes their old health from running health', () => {
    const rt = { ...runtime, type: 'docker' }
    const rows = [task('old', { kind: 'container', state: 'EXITED', health: 'unhealthy' }), task('live', { kind: 'container' }), task('history', { kind: 'container', current: false, health: 'unhealthy' })]
    const summary = serviceInstanceSummary(rows, service, rt, now)
    expect(summary.current).toHaveLength(2); expect(summary.running).toHaveLength(1); expect(summary.unhealthy).toBe(false)
    rows[1].health = 'unhealthy'; expect(serviceInstanceSummary(rows, service, rt, now).unhealthy).toBe(true)
    expect(serviceInstances(rows, service, rt, true)).toHaveLength(3)
  })
  it.each([{ connection: 'offline' }, { inventoryComplete: false }, { inventoryObservedAt: '' }, { inventoryObservedAt: new Date(now - 60001).toISOString() }, { inventoryObservedAt: new Date(now + 6000).toISOString() }])('does not interpret missing or stale evidence as healthy: %j', patch => {
    expect(observationUnknown({ ...runtime, ...patch }, now)).toBe(true)
  })
  it('never attaches foreign or unowned observations and retains zero-instance service evidence', () => {
    expect(serviceInstances([task('foreign', { runtime: 'other' }), task('unowned', { service: '' })], service, runtime)).toEqual([])
    expect(serviceInstances([task('one')], service, { ...runtime, id: 'other' })).toEqual([])
    expect(serviceInstanceSummary([], { ...service, desired: 0 }, runtime, now)).toMatchObject({ running: [], unknown: false, unhealthy: false })
  })
})
