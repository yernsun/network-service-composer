// Instance lists are observations, not embedded Service properties. Recheck
// before validation and treat reads older than 30 seconds as unknown.
export const ENDPOINT_MAX_AGE_MS = 30_000
export function endpointState(service, now = Date.now()) {
  const observation = service?.endpointObservation
  if (!observation) return 'not-loaded'
  if (observation.state !== 'loaded') return observation.state
  const age = now - observation.fetchedAt
  return !Number.isFinite(age) || age < 0 || age > ENDPOINT_MAX_AGE_MS ? 'stale' : 'loaded'
}
export function existingRouteTarget(route, domain) {
  return [...(domain?.active?.routes || []), ...(domain?.draft?.config?.routes || [])]
    .some(previous => previous.id === route.id && previous.kind === 'proxy' && !previous.externalUrl && previous.service === route.service)
}
export function routeTargetIssue(route, domain, service) {
  if (route.kind !== 'proxy' || route.externalUrl) return ''
  if (!service || service.status === 'REMOVED' || service.removed_at) return 'serviceMissing'
  if (domain.pool_id && domain.pool_id !== service.poolId) return 'servicePool'
  if (service.dns_status && service.dns_status !== 'READY') return 'serviceDNS'
  // Preserve existing Pool-owned routes. A new or rebound target still needs
  // the current environment; reading another environment never grants a claim.
  if (!existingRouteTarget(route, domain) && (service.project !== domain.project || service.env !== domain.env)) return 'serviceScope'
  return ''
}
