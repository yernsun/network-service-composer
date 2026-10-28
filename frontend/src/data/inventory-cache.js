import { QueryClient } from '@tanstack/vue-query'

export const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 } } })
export const inventoryKey = (path, params = {}, query = {}) => ['workspace-read', path, params.id || '', query]

function invalidateKey(queryKey) {
  // A read started before an accepted write must not repopulate this cache.
  void queryClient.cancelQueries({ queryKey }, { revert: false })
  void queryClient.invalidateQueries({ queryKey, refetchType: 'none' })
}
const invalidate = (path, id) => invalidateKey(['workspace-read', path, ...(id === undefined ? [] : [id])])

export function invalidateComponentQueries(families) {
  const predicate = query => query.queryKey[0] !== 'workspace-read' && (!families || families.includes(query.queryKey[0]))
  void queryClient.cancelQueries({ predicate }, { revert: false })
  void queryClient.invalidateQueries({ predicate, refetchType: 'active' })
}

export function refreshComponentQueries() {
  return queryClient.refetchQueries({ type: 'active', predicate: query => query.queryKey[0] !== 'workspace-read' }, { throwOnError: true })
}

export function invalidateWorkloadInventory() {
  for (const path of [
    '/applications', '/services', '/operations', '/projects/{id}/environments',
    '/applications/{id}/revisions', '/applications/{id}/source', '/applications/{id}/draft',
    '/services/{id}/instances', '/runtimes/{id}/cluster',
    '/domains', '/routes', '/certificates', '/gateway-pools', '/gateways', '/gateway-pools/{id}/nginx',
    '/environments/{id}/domain-drafts', '/environments/{id}/deployment-draft',
  ]) invalidate(path)
  invalidateComponentQueries(['service-bindings', 'application-network', 'restore-service', 'restore-revisions',
    'domain-history-published', 'domain-history-drafts', 'domain-history-current', 'operation-entrypoint', 'operation-readiness', 'runtime-timeline', 'volumes', 'certificate-assets', 'environment-capabilities', 'system-ha', 'activity-hub'])
}

// Workspace reads are scheduled by the refresh coordinator. Component queries
// refresh their active observers; inactive projects retain their cache entries.
export function invalidateInventoryWrite(method, path, params = {}) {
  if (/\/(?:preflight|service-preflight|compose-merge|compose-preview|compose-validation|restore-preview|preview|previews|deletion-previews|restore-reviews|validations|diagnostics)$/.test(path)) return false
  if (/^\/(?:auth|locale|me|users|openapi-keys)(?:\/|$)/.test(path)) return false
  if (path.endsWith('/variables')) {
    invalidate(path, params.id)
    if (path.startsWith('/projects/')) invalidate('/environments/{id}/variables')
    return true
  }
  if (path.endsWith('/deployment-draft')) { invalidate(path, params.id); return true }
  if (path === '/applications/{id}/draft') { invalidate(path, params.id); return true }
  if (path === '/services/{id}/draft') { invalidate('/applications/{id}/draft'); return true }
  if (path === '/projects/{id}' && method === 'patch') { invalidate('/projects'); return true }
  if (path === '/environments/{id}' && method === 'patch') { invalidate('/projects/{id}/environments'); invalidateComponentQueries(['environment-capabilities', 'system-ha', 'activity-hub']); return true }
  if (path.includes('domain-drafts') && !path.endsWith('/publish')) {
    invalidate('/environments/{id}/domain-drafts')
    invalidateComponentQueries(['domain-history-drafts', 'domain-history-current'])
    return true
  }
  if (/^\/(?:applications|services|environments|gateway-pools|domain-drafts)(?:\/|$)/.test(path)) {
    invalidateWorkloadInventory()
    return true
  }
  // Structural and unknown mutations conservatively invalidate the directory.
  // This also covers asynchronous project deletion and runtime registration.
  invalidateKey(['workspace-read'])
  invalidateComponentQueries()
  return true
}
