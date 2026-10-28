import { translate } from '../i18n'
import { formatDate } from '../shared/i18n'

// Docker reports `none` when a container has no HEALTHCHECK. This is known,
// explicit evidence and must not be presented as a collection failure.
export function dockerHealth(value) {
  const normalized = String(value || '').toLowerCase()
  return normalized === 'none' ? 'not-configured' : normalized || 'unknown'
}

// Observation identity is scoped to its source Runtime, not to a service name.
export function latestInstances(instances) {
  const unique = new Map()
  const timestamp = value => Date.parse(value?.observed_at || value?.updated || '') || 0
  for (const instance of instances) {
    const key = `${instance.runtime || ''}:${instance.id}`
    const previous = unique.get(key)
    if (!previous || timestamp(previous) <= timestamp(instance)) unique.set(key, instance)
  }
  return [...unique.values()]
}

export function serviceInstances(instances, service, runtime, history = false) {
  if (!runtime || runtime.id !== service.runtimeId) return []
  const kind = runtime.type === 'swarm' ? 'task' : 'container'
  return latestInstances(instances.filter(i => i.runtime === service.runtimeId && i.service === service.id && i.kind === kind))
    .filter(i => history || i.current)
}

// API freshness/completeness applies to the instance snapshot, not a later
// node-only refresh. Expire cached live snapshots after one minute as well.
export function observationUnknown(runtime, now = Date.now()) {
  if (!runtime || runtime.connection !== 'online') return true
  if (runtime.inventoryComplete === undefined) return false // demo fixtures
  const observed = Date.parse(runtime.inventoryObservedAt)
  return !runtime.inventoryComplete || !Number.isFinite(observed) || now - observed > 60000 || observed > now + 5000
}

export function serviceInstanceSummary(instances, service, runtime, now = Date.now()) {
  const current = serviceInstances(instances, service, runtime)
  const running = current.filter(i => i.state === 'RUNNING')
  return { current, running, unknown: observationUnknown(runtime, now), unhealthy: running.some(i => i.health === 'unhealthy') }
}

export function instanceViews(instances, runtime, servicesById) {
  return instances.map(i => {
    const s = servicesById.get(i.service_id)
    const owner = s?.runtime_id || s?.runtimeId || ''
    return { ...i, service: s?.id || '', serviceRuntime: owner, app: owner === runtime ? s.application_id || s.app || '' : '', runtime,
      node: i.node_id, containerId: i.container_id, kind: i.kind, state: i.state.toUpperCase(), current: !i.historical,
      health: dockerHealth(i.health),
      logs: '', previous: null, exitCode: i.exit_code ?? null, oomKilled: i.oom_killed ?? null, cpu: i.metric?.cpu_percent ?? null, memory: i.metric?.memory_usage ?? null,
      created: i.created_at || '', updated: i.updated_at || '', stateChanged: i.state_changed_at || '', containerCreated: i.container_created_at || '',
      started: i.started_at || '', finished: i.finished_at || '', observed: i.observed_at || '', restartCount: i.restart_count ?? null,
      desiredState: i.desired_state, version: null, slot: null }
  })
}

const lifecyclePart = (key, value) => value ? `${translate(key)} ${formatDate(value)}` : ''

// Docker has no generic Container UpdatedAt. Keep Swarm Task metadata, Task
// status transitions and Container lifecycle timestamps visibly distinct.
export function instanceTimeRows(instance) {
  const primary = instance.kind === 'task'
    ? [lifecyclePart('cluster.task_created_at', instance.created), lifecyclePart('cluster.task_updated_at', instance.updated)]
    : [lifecyclePart('cluster.container_created_at', instance.containerCreated || instance.created), lifecyclePart('cluster.started_at', instance.started)]
  const secondary = instance.kind === 'task'
    ? [lifecyclePart('cluster.container_created_at', instance.containerCreated), lifecyclePart('cluster.started_at', instance.started), lifecyclePart('cluster.state_changed_at', instance.stateChanged)]
    : [lifecyclePart('cluster.finished_at', instance.finished), lifecyclePart('cluster.observed_at', instance.observed)]
  const fallback = lifecyclePart('cluster.observed_at', instance.observed) || translate('cluster.time_not_collected')
  return { primary: primary.filter(Boolean).join(' · ') || fallback, secondary: secondary.filter(Boolean).join(' · ') }
}
