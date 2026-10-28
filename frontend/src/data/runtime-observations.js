import { request } from '../shared/api/client'
import { allPages } from './pagination'
import { latestInstances, instanceViews } from './service-instances'
import { nodeMetrics } from './node-health'

const titleCase = value => value ? value[0].toUpperCase() + value.slice(1).toLowerCase() : 'Unknown'
export const nodeViews = nodes => nodes.filter(n => !n.virtual).map(n => ({ ...n, status: titleCase(n.state), availability: titleCase(n.availability), manager: n.leader ? 'Leader' : titleCase(n.reachability), agent: n.connected && !n.stale && n.inventory_complete, sample: n.observed_at, ...nodeMetrics(n) }))

export async function readRuntimeInventory(id, signal) {
  const [overview, nodes] = await Promise.all([
    request('get', '/runtimes/{id}/cluster', { params: { path: { id } }, signal }),
    allPages('/runtimes/{id}/cluster/nodes', { id }, signal),
  ])
  const observations = await Promise.allSettled(nodes.map(n => allPages('/runtimes/{id}/cluster/nodes/{nodeId}/instances', { id, nodeId: n.id }, signal, { history: true })))
  const failed = observations.find(result => result.status === 'rejected')
  return { runtime: id, nodes, overview: overview.data,
    instances: latestInstances(observations.flatMap(result => result.status === 'fulfilled' ? result.value : [])),
    observationError: failed ? failed.reason?.code || 'unavailable' : '', error: '' }
}

export function runtimeObservation(c) {
  return { nodes: nodeViews(c.nodes), engineId: c.overview?.id || '', observed: c.overview?.observed_at || '',
    inventoryComplete: !c.error && !c.observationError && c.overview?.inventory_complete === true && !c.overview?.stale && c.nodes.filter(n => !n.virtual).every(n => n.connected && !n.stale && n.inventory_complete),
    inventoryObservedAt: c.overview?.observed_at || '',
    managerRead: !c.error && c.overview?.stale === false && !c.overview.error && c.overview.manager_available === true }
}

// Refresh observation fields only. Never replace the workspace, navigate, or
// repaint a modal/editor while a background response is arriving.
function observationTargets(state, context) {
  const page = context.ui.page, x = context.workload.x
  let ids
  if (page === 'runtimes') return state.runtimes
  if (page === 'runtime') ids = [x.runtime]
  else if (page === 'service') ids = [state.services.find(s => s.id === x.service)?.runtimeId]
  else if (page === 'application') ids = [state.apps.find(a => a.id === x.app)?.runtime]
  else {
    const allProjects = context.ui.allProjects || page === 'services' && x.svcScope === 'all'
    const allEnvironments = context.ui.allEnvironments || page === 'services' && x.svcScope === 'project'
    const belongs = row => allProjects || row.project === state.project && (allEnvironments || row.env === state.env)
    ids = [...(state.services || []).filter(belongs).map(s => s.runtimeId), ...(page === 'applications' ? (state.apps || []).filter(belongs).map(a => a.runtime) : [])]
  }
  const selected = new Set(ids.filter(Boolean))
  return state.runtimes.filter(r => selected.has(r.id))
}
export function watchRuntimeObservations({ state, context, signal, interval = 10000 }) {
  let stopped = false, timer
  const visible = () => !stopped && !signal.aborted && ['runtime', 'runtimes', 'service', 'services', 'application', 'applications'].includes(context.ui.page) && !context.modal && !context.workspace.varsDirty() && !context.workspace.docsDirty?.()
  const scopeKey = targets => JSON.stringify([context.ui.page, state.project, state.env, context.workload.x.runtime, context.workload.x.service, context.workload.x.app, targets.map(r => r.id)])
  async function tick() {
    try {
      if (!visible()) return
      const targets = observationTargets(state, context)
      if (!targets.length) return
      const scope = scopeKey(targets)
      const results = await Promise.allSettled(targets.map(r => readRuntimeInventory(r.id, signal)))
      const currentTargets = observationTargets(state, context)
      if (!visible() || scopeKey(currentTargets) !== scope) return
      const services = new Map((state.services || []).map(s => [s.id, s]))
      results.forEach((result, i) => {
        const target = currentTargets[i]
        if (result.status === 'fulfilled') {
          Object.assign(target, runtimeObservation(result.value))
          if (state.instances) state.instances = [...state.instances.filter(instance => instance.runtime !== target.id), ...instanceViews(result.value.instances, target.id, services)]
        } else {
          target.inventoryComplete = false
          target.managerRead = false
          target.nodes = target.nodes.map(n => ({ ...n, agent: false, stale: true, host: n.host && { ...n.host, stale: true }, controller: n.controller && { ...n.controller, stale: true } }))
        }
      })
      context.render()
    } finally { if (!stopped && !signal.aborted) timer = setTimeout(tick, interval) }
  }
  timer = setTimeout(tick, interval)
  return () => { stopped = true; clearTimeout(timer) }
}
