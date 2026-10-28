import { domainAccessControls, emptyIPAccess } from './ip-access'
import { ApiRequestError, request } from '../shared/api/client'
import { allPages } from './pagination'
import { NGINX_MANAGED_DEFAULTS } from './nginx-defaults'

export function routeEditorRow(route, snippet = '') {
  return { ipAccess: emptyIPAccess(), id: route.id, name: route.name || route.path, path: route.path, match: route.match || 'prefer', kind: route.kind || 'proxy',
    service: route.service_id || route.backend?.service_id || '', externalUrl: route.external_url || '',
    timeout: route.read_timeout_seconds || NGINX_MANAGED_DEFAULTS.readTimeoutSeconds, connect: route.connect_timeout_seconds || NGINX_MANAGED_DEFAULTS.connectTimeoutSeconds,
    strip: !!route.strip_path, websocket: !!route.websocket_enabled, enabled: !!route.enabled,
    snippet, code: route.response_code || 200, value: route.response_value || '', revision: route.revision || 0 }
}

export function routeWrite(row, domainID, previous) {
  const external = row.kind === 'proxy' && !!row.externalUrl
  return { expected_revision: previous?.revision || 0, route: {
    id: row.id, domain_id: domainID, backend: {
      service_id: row.kind === 'proxy' && !external ? row.service : '',
      port: previous?.backend?.port || 0, protocol: previous?.backend?.protocol || 'http',
      server_name: previous?.backend?.server_name || '',
    },
    kind: row.kind, match: row.match, external_url: external ? row.externalUrl : '',
    response_code: row.kind === 'proxy' ? 0 : Number(row.code), response_value: row.kind === 'proxy' ? '' : row.value,
    path: row.path, priority: previous?.priority || 0, strip_path: row.kind === 'proxy' && !!row.strip,
    websocket_enabled: row.kind === 'proxy' && !!row.websocket,
    read_timeout_seconds: row.kind === 'proxy' ? Number(row.timeout) : 60,
    connect_timeout_seconds: row.kind === 'proxy' ? Number(row.connect) : 5,
    enabled: !!row.enabled,
  } }
}

export function domainRouteChanges(domainID, activeRows, work, published) {
  const publishedByID = new Map(published.map(route => [route.id, route]))
  const activeByID = new Map(activeRows.map(route => [route.id, route]))
  const changes = []
  for (const row of work.routes) {
    const previous = publishedByID.get(row.id)
    const original = activeByID.get(row.id)
    if (original && JSON.stringify({ ...original, revision: 0, snippet: '', ipAccess: null }) === JSON.stringify({ ...row, revision: 0, snippet: '', ipAccess: null })) continue
    changes.push(routeWrite(row, domainID, previous))
  }
  for (const original of activeRows) {
    if (work.routes.some(route => route.id === original.id)) continue
    const previous = publishedByID.get(original.id)
    if (previous) changes.push({ ...routeWrite(original, domainID, previous), delete: true })
  }
  return changes
}

export async function saveDomainDraft(domain, activeRows, work) {
  const poolID = domain.pool_id
  const [config, listed] = await Promise.all([
    request('get', '/gateway-pools/{id}/nginx', { params: { path: { id: poolID } } }),
    allPages('/routes'),
  ])
  const current = config.data.draft || {}
  const baseSnippets = current.revision ? current.snippets : config.data.active?.snippets
  const baseAccess = current.revision ? current.access_controls : config.data.active?.access_controls
  const baseline = domain.draft?.config || domain.active
  if (baseline) {
    const ids = new Set([...baseline.routes, ...work.routes, ...(current.routes || []).filter(change => change.route.domain_id === domain.id).map(change => change.route)].map(route => route.id))
    const normalize = controls => JSON.stringify(controls.map(entry => ({ scope: entry.scope, resource_id: entry.resource_id, access: entry.access || [], forbidden: entry.forbidden || [], ...(entry.client_ip ? { client_ip: { header: entry.client_ip.header, trusted_proxies: entry.client_ip.trusted_proxies } } : {}) })).filter(entry => entry.access.length || entry.forbidden.length || entry.client_ip).sort((a, b) => `${a.scope}:${a.resource_id}`.localeCompare(`${b.scope}:${b.resource_id}`)))
    const remote = (baseAccess || []).filter(entry => entry.scope === 'server' && entry.resource_id === domain.id || entry.scope === 'location' && ids.has(entry.resource_id))
    if (normalize(remote) !== normalize(domainAccessControls([], domain.id, [], baseline))) throw new ApiRequestError(412, 'conflict')
  }
  const published = listed.filter(route => route.domain_id === domain.id)
  const changes = [...(current.routes || []).filter(change => change.route.domain_id !== domain.id), ...domainRouteChanges(domain.id, activeRows, work, published)]
  const ids = new Set([...activeRows, ...work.routes].map(route => route.id))
  const snippets = (baseSnippets || []).filter(snippet => !(snippet.scope === 'server' && snippet.resource_id === domain.id) && !(snippet.scope === 'location' && ids.has(snippet.resource_id)))
  if (work.serverSnippet?.trim()) snippets.push({ scope: 'server', resource_id: domain.id, content: work.serverSnippet })
  for (const route of work.routes) if (route.snippet?.trim()) snippets.push({ scope: 'location', resource_id: route.id, content: route.snippet })
  const saved = await request('put', '/gateway-pools/{id}/nginx/draft', {
    params: { path: { id: poolID } }, headers: { 'If-Match': config.response.headers.get('ETag') || '"rev-0"' },
    body: { snippets, access_controls: domainAccessControls(baseAccess, domain.id, [...activeRows, ...(current.routes || []).filter(change => change.route.domain_id === domain.id).map(change => change.route)], work), routes: changes, description: current.description || '' },
  })
  return saved.data
}

export async function validatePoolDraft(poolID, revision) {
  const result = await request('post', '/gateway-pools/{id}/nginx/validations', {
    params: { path: { id: poolID } }, headers: { 'If-Match': `"rev-${revision}"` },
  })
  return result.data
}

export async function readValidation(poolID, validationID) {
  return (await request('get', '/gateway-pools/{id}/nginx/validations/{validationID}', { params: { path: { id: poolID, validationID } } })).data
}

export async function publishPoolDraft(poolID, validation) {
  return (await request('post', '/gateway-pools/{id}/nginx/releases', {
    params: { path: { id: poolID } }, headers: { 'If-Match': `"rev-${validation.draft_revision}"`, 'Idempotency-Key': crypto.randomUUID() },
    body: { validation_id: validation.id },
  })).data
}

export async function readRelease(poolID, releaseID) {
  return (await request('get', '/gateway-pools/{id}/nginx/releases/{releaseID}', { params: { path: { id: poolID, releaseID } } })).data
}
