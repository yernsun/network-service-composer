import { readableCompose } from '../compose/source'
import { parse as parseYAML } from 'yaml'
import { isCancelledError } from '@tanstack/vue-query'
import { request } from '../shared/api/client'
import { allPages } from './pagination'
import { operationView } from './operation-view'
import { dockerHealth, instanceViews } from './service-instances'
import { NGINX_MANAGED_DEFAULTS } from './nginx-defaults'
import { readRuntimeInventory, runtimeObservation } from './runtime-observations'
import { loadedDomainRevisions } from './gateway-application'
import { applicationServiceVersions } from './application-service-versions'
import { emptyIPAccess, ipAccessFor } from './ip-access'
import { routeEditorRow } from './domain-publication'
import { inventoryKey, queryClient } from './inventory-cache'
import { inventoryScope } from './inventory-scope'

export { queryClient } from './inventory-cache'
function catalogVariables(version) {
  if (Array.isArray(version.market?.variables)) return version.market.variables
  try {
    const variables = parseYAML(version.manifest || '{}')?.variables
    return Array.isArray(variables) ? variables : []
  } catch { return [] }
}
const emptyConfig = () => ({ mode: 'managed', tls: false, redirect: false, body: NGINX_MANAGED_DEFAULTS.bodySize, native: '', serverSnippet: '', ipAccess: emptyIPAccess(), routes: [] })

export async function loadInventory(includeRemoved = false, { path = '/projects', signal, reuseCached = false, fresh = false, ...scopeOptions } = {}) {
  const read = (path, params, query, work) => queryClient.fetchQuery({
    queryKey: inventoryKey(path, params, query),
    staleTime: fresh ? 0 : reuseCached ? Infinity : 5000,
    queryFn: ({ signal: cacheSignal }) => work(signal ? AbortSignal.any([signal, cacheSignal]) : cacheSignal),
  }).catch(error => { if (isCancelledError(error)) throw new DOMException('Cancelled', 'AbortError'); throw error })
  const collection = (path, params = {}, query = {}) => read(path, params, query, signal => allPages(path, params, signal, query))
  const resource = (path, id) => read(path, { id }, {}, signal => request('get', path, { params: { path: { id } }, signal }))
  const [projects, pools, runtimes, applications, services, domains, routes, gateways, certificates, operations] = await Promise.all([
    collection('/projects'), collection('/gateway-pools'), collection('/runtimes'), collection('/applications'), collection('/services', {}, { include_removed: includeRemoved }),
    collection('/domains'), collection('/routes'), collection('/gateways'), collection('/certificates'),
    collection('/operations'),
  ])
  const environments = (await Promise.all(projects.map(p => collection('/projects/{id}/environments', { id: p.id })))).flat()
  const envFor = row => row.environment?.id ? environments.find(e => e.id === row.environment.id) : environments.find(e => e.application_id && [row.id, row.application_id].includes(e.application_id))
  const scope = inventoryScope(path, { projects, environments, applications, services, domains, routes, runtimes, operations, environmentFor: envFor }, scopeOptions)
  const catalog = scope.catalog ? await collection('/catalog/apps') : []
  const wizardDrafts = []
  const wizardDraftETags = new Map()
  await Promise.all(scope.draftEnvironments.map(async env => {
    try {
      const result = await resource('/environments/{id}/deployment-draft', env.id)
      wizardDraftETags.set(env.id, result.response.headers.get('ETag'))
      if (result.data.revision && result.data.data && typeof result.data.data === 'object')
        wizardDrafts.push({ ...result.data.data, env: env.id, project: env.project_id, reviewAck: false, warningAck: false })
    } catch (error) { if (![403, 404].includes(error.status)) throw error }
  }))
  const domainDrafts = (await Promise.all(scope.domainEnvironments.map(e => collection('/environments/{id}/domain-drafts', { id: e.id })))).flat()
  const publishedDomainScopes = new Map(domainDrafts.filter(d => d.status === 'published' && d.published_domain_id).map(d => [d.published_domain_id, d.environment_id]))
  const revisions = new Map(await Promise.all(scope.applications.map(async a => [a.id, await collection('/applications/{id}/revisions', { id: a.id })])))
  const poolIDs = [...new Set([...scope.pools, ...domains.filter(d => publishedDomainScopes.has(d.id)).map(d => d.pool_id)])].filter(Boolean)
  const poolConfigs = new Map(await Promise.all(poolIDs.map(async id => {
    const value = await resource('/gateway-pools/{id}/nginx', id)
    return [id, value.data]
  })))
  const endpointServiceIDs = new Set(scope.endpointServices.filter(service => service.healthy_instances > 0 || routes.some(route => route.backend?.service_id === service.id)).map(service => service.id))
  for (const config of poolConfigs.values()) {
    for (const change of config?.draft?.routes || []) {
      if (scope.endpointServices.some(service => service.id === change.route?.backend?.service_id)) endpointServiceIDs.add(change.route.backend.service_id)
    }
  }
  const serviceEndpoints = new Map(await Promise.all([...endpointServiceIDs].map(async id =>
    [id, await collection('/services/{id}/instances', { id })]
  )))
  const parameters13 = { scopes: {}, audit: [], documents: {} }
  const versions = new Map()
  for (const [id, etag] of wizardDraftETags) versions.set(`deployment-draft:${id}`, etag)
  await Promise.all(scope.documents.map(async a => {
    try {
      const [source, draft] = await Promise.all([
        resource('/applications/{id}/source', a.id),
        resource('/applications/{id}/draft', a.id),
      ])
      parameters13.documents[a.id] = { sourcePlan: revisions.get(a.id)?.find(r => r.id === a.active_revision_id)?.configuration_source?.deployment_plan || { storage: [], acknowledged: [] }, rawSource: source.data.compose_yaml, sourceRevision: source.data.baseline_revision_id ?? (source.data.revision_id || a.active_revision_id), source: readableCompose(source.data.compose_yaml), runtime: source.data.runtime_env,
        draft: draft.data.revision ? { source: readableCompose(draft.data.compose_yaml), runtime: draft.data.runtime_env, base: draft.data.application_revision, sourceRevision: draft.data.revision_id || (source.data.baseline_revision_id ?? source.data.revision_id) } : null,
        lastBundle: null, mapping: Object.fromEntries(services.filter(s => s.application_id === a.id).map(s => [s.name, s.id])), unavailable: false }
      versions.set(`application-draft:${a.id}`, draft.response.headers.get('ETag'))
    } catch (error) {
      if (![403, 404].includes(error.status)) throw error
      parameters13.documents[a.id] = { source: '', runtime: '', draft: null, lastBundle: null, mapping: {}, unavailable: true }
    }
  }))
  await Promise.all(scope.variableOwners.map(async ({ layer, id, path }) => {
    const result = await resource(path, id)
    const key = `${layer}:${id}`
    parameters13.scopes[key] = { version: result.data.revision, rows: Object.fromEntries(result.data.values.map(v => [v.key, { value: v.mask ? '' : v.value || '', type: v.secret ? 'credential' : 'string', note: '', mask: v.mask, secret: v.secret }])), history: [] }
    versions.set(key, result.response.headers.get('ETag'))
  }))
  const clusters = await Promise.all(scope.runtimeIDs.map(async id => {
    try {
      return await read('/runtimes/{id}/cluster', { id }, { inventory: true }, signal => readRuntimeInventory(id, signal))
    } catch (e) { if (e.name === 'AbortError') throw e; return { runtime: id, nodes: [], instances: [], error: e.code || 'unavailable' } }
  }))
  const servicesById = new Map(services.map(s => [s.id, s]))
  const poolsById = new Map(pools.map(p => [p.id, p]))
  const runtimesById = new Map(runtimes.map(r => [r.id, r]))
  const clustersByRuntime = new Map(clusters.map(c => [c.runtime, c]))
  const imagesByService = new Map()
  const runningHealthByService = new Map()
  for (const c of clusters) for (const i of c.instances) {
    const service = servicesById.get(i.service_id)
    if (service?.runtime_id === c.runtime && !i.historical && i.state === 'running' && i.image) imagesByService.set(service.id, i.image)
    if (service?.runtime_id === c.runtime && !i.historical && i.state === 'running') {
      const values = runningHealthByService.get(service.id) || []
      values.push(dockerHealth(i.health))
      runningHealthByService.set(service.id, values)
    }
  }
  const initialProject = projects.find(p => environments.some(e => e.project_id === p.id)) || projects[0]
  const workloadDefinitions = new Map(applications.map(app => {
    try { return [app.id, parseYAML(parameters13.documents[app.id]?.rawSource || '')?.services || {}] }
    catch { return [app.id, {}] }
  }))
  const state = {
    certificates, schema: 2, epoch: 0, project: initialProject?.id || '', env: environments.find(e => e.project_id === initialProject?.id)?.id || '', view: 'cloud',
    scenario: 'normal', scenarioGateway: null, serviceLayout: 'grouped', appDrafts: wizardDrafts, deployments: [], events: [], runtimeEvents: [], parameters13,
    projects: projects.map(p => ({ ...p, key: p.slug, description: p.description || '', created: p.created_at })),
    environments: environments.map(e => ({ ...e, project: e.project_id, slug: e.name, name: e.display_name || e.name, kind: e.kind || (/^(prod|production)$/i.test(e.name) ? 'production' : 'custom'), gateways: e.gateway_ids?.length ? e.gateway_ids : gateways.filter(g => g.pool_id === runtimes.find(r => r.id === e.runtime_id)?.pool_id).map(g => g.id) })),
    apps: applications.map(a => { const e = envFor(a); parameters13.documents[a.id] ||= { source: '', runtime: '', draft: null, lastBundle: null, mapping: {}, unavailable: true }; return { ...a, key: a.project_name, project: e?.project_id || '', env: e?.id || '', runtime: a.runtime_id, mode: runtimes.find(r => r.id === a.runtime_id)?.runtime_type === 'swarm' ? 'stack' : 'compose', version: revisions.get(a.id)?.find(r => r.id === a.desired_revision_id)?.sequence || revisions.get(a.id)?.find(r => r.id === a.active_revision_id)?.sequence || 0, draft: null, revisions: (revisions.get(a.id) || []).map(r => ({ v: r.sequence, backendId: r.id, time: r.created_at, note: r.created_by || '', spec: { source_unavailable: true, metadata: r } })), source: a.catalog_app_version_id ? 'template' : 'file', raw: '', stateful: false, created: a.created_at, secretRef: '', access: null } }),
    services: services.map(s => {
      const e = envFor(s)
      const app = applications.find(a => a.id === s.application_id)
      const serviceVersions = applicationServiceVersions(s, app, revisions.get(s.application_id) || [])
      const observedHealth = runningHealthByService.get(s.id) || []
      const health = observedHealth.length > 0 && observedHealth.every(value => value === 'not-configured')
        ? 'not-configured'
        : observedHealth.length > 0 && observedHealth.every(value => value === 'healthy')
          ? 'healthy'
          : s.healthy_instances === s.running_instances && s.running_instances > 0 ? 'healthy' : 'unknown'
      let workloadKind = 'replicated'
      const definition = workloadDefinitions.get(app?.id)?.[s.name]
      const mode = definition?.deploy?.mode
      if (mode === 'global') workloadKind = 'global'
      else if (mode === 'replicated-job' || mode === 'global-job') workloadKind = 'job'
      else if (!definition && s.managed) workloadKind = 'unknown'
      return { ...s, project: e?.project_id || '', env: e?.id || '', app: s.application_id || '', runtimeId: s.runtime_id, runtime: runtimesById.get(s.runtime_id)?.name || s.runtime_id, mode: runtimesById.get(s.runtime_id)?.runtime_type === 'swarm' ? 'stack' : 'compose', role: s.name, image: imagesByService.get(s.id) || '', port: s.target_port_override || s.discovered_port || 0, desired: s.desired_instances, health, specVersion: serviceVersions.desired, observedVersion: serviceVersions.applied, runtimeCreatedAt: s.runtime_created_at || '', runtimeUpdatedAt: s.runtime_updated_at || '', removedAt: s.removed_at || '', recordCreatedAt: s.created_at || '', recordUpdatedAt: s.updated_at || '', endpoints: (serviceEndpoints.get(s.id) || []).filter(ep => ep.runtime_state === 'RUNNING' && ep.ready_state !== 'REMOVED').map(ep => [ep.address, ep.ready_state === 'READY' && ep.health_state !== 'UNHEALTHY']), workloadKind, memory: null, cpu: null }
    }),
    runtimes: runtimes.map(r => { const c = clustersByRuntime.get(r.id) || { nodes: [], instances: [], error: 'unavailable' }; return { ...r, type: r.runtime_type, connection: !r.enabled || r.status === 'OFFLINE' ? 'offline' : r.status === 'ONLINE' ? 'online' : 'unknown', canWrite: r.management_enabled, allowedEnvs: environments.filter(e => e.runtime_id === r.id).map(e => e.id), ...runtimeObservation(c), gaps: c.error || c.observationError ? [{ start: r.last_seen_at, end: null, reason: c.error || c.observationError }] : [], protocol: '', lastSync: r.last_seen_at } }),
    instances: clusters.flatMap(c => instanceViews(c.instances, c.runtime, servicesById)),
    gateways: gateways.map(g => ({ ...g, ip: g.address || '', env: '', runtime: '', available: g.status === 'READY' })),
    domains: domains.map(d => {
      const rr = routes.filter(r => r.domain_id === d.id)
      const owners = [...new Set(rr.map(r => envFor(services.find(s => s.id === r.backend?.service_id) || {})?.id).filter(Boolean))]
      const publishedScope = publishedDomainScopes.get(d.id)
      const e = environments.find(e => e.id === publishedScope) || environments.find(e => e.id === owners[0]) || null
      if (publishedScope && !owners.includes(publishedScope)) owners.push(publishedScope)
      const poolConfig = poolConfigs.get(d.pool_id)
      const activeSnippets = poolConfig?.active?.snippets || []
      const draftSnippets = poolConfig?.draft?.snippets || []
      const activeAccess = poolConfig?.active?.access_controls || []
      const draftAccess = poolConfig?.draft?.access_controls || []
      const editorConfig = (rows, snippets, access) => ({ ...emptyConfig(), tls: !!d.tls?.certificate_id, redirect: !!d.redirect_http_to_https,
        ipAccess: ipAccessFor(access, 'server', d.id),
        serverSnippet: snippets.find(s => s.scope === 'server' && s.resource_id === d.id)?.content || '',
        routes: rows.map(r => ({ ...routeEditorRow(r, snippets.find(s => s.scope === 'location' && s.resource_id === r.id)?.content || ''), ipAccess: ipAccessFor(access, 'location', r.id) })) })
      const config = editorConfig(rr, activeSnippets, activeAccess)
      const staged = new Map(rr.map(r => [r.id, r]))
      for (const change of poolConfig?.draft?.routes || []) if (change.route.domain_id === d.id) {
        if (change.delete) staged.delete(change.route.id)
        else staged.set(change.route.id, { ...change.route, revision: change.expected_revision || 0 })
      }
      const accessChanged = JSON.stringify(draftAccess.filter(s => s.resource_id === d.id || staged.has(s.resource_id))) !== JSON.stringify(activeAccess.filter(s => s.resource_id === d.id || staged.has(s.resource_id)))
      const hasDraft = (poolConfig?.draft?.revision || 0) > 0 && (accessChanged || (poolConfig.draft.routes || []).some(change => change.route.domain_id === d.id) || JSON.stringify(draftSnippets.filter(s => s.resource_id === d.id || staged.has(s.resource_id))) !== JSON.stringify(activeSnippets.filter(s => s.resource_id === d.id || staged.has(s.resource_id))))
      const poolGateways = gateways.filter(g => g.pool_id === d.pool_id)
      const poolRevision = poolsById.get(d.pool_id)?.config_revision
      return { ...d, name: d.host, project: e?.project_id || '', env: e?.id || '', environmentIds: owners, gateways: poolGateways.map(g => g.id), rev: d.revision, active: config, draft: hasDraft ? { config: editorConfig([...staged.values()], draftSnippets, draftAccess), base: d.revision, revision: poolConfig.draft.revision } : null, loaded: loadedDomainRevisions(d, poolGateways, poolRevision), revisions: [], drift: null, fileBackups: [], certExpiry: certificates.find(c => c.id === d.tls?.certificate_id)?.not_after || '', certReadyDemo: false, created: d.created_at }
    }),
    appDeployments: operations.map(o => operationView(o, envFor(o), (revisions.get(o.application_id) || []).find(r => r.id === o.revision_id)?.sequence ?? null)),
  }
  const templates = catalog.filter(a => !a.withdrawn).map(a => ({ ...a, key: a.app_key, glyph: a.name.slice(0, 1), cat: 'Applications', color: 'blue', desc: a.description || '', modes: [a.compose_compatible ? 'compose' : '', a.swarm_compatible ? 'stack' : ''].filter(Boolean), services: a.services.map(name => ({ name, image: '', port: 0, http: false })), stateful: !!a.swarm_yaml, notes: '', raw: a.compose_yaml || '', swarmRaw: a.swarm_yaml || '', catalogVariables: catalogVariables(a) }))
  return { state, templates, versions, raw: { projects, pools, environments, runtimes, applications, services, domains, domainDrafts, routes, gateways, catalog } }
}
