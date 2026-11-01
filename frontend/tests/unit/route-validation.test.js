import { expect, it } from 'vitest'
import { endpointState, existingRouteTarget, routeTargetIssue, ENDPOINT_MAX_AGE_MS } from '../../src/data/route-validation'

const route = { id: 'route', kind: 'proxy', service: 'service', path: '/__nsc_validation/' }
const domain = { project: 'gateway', env: 'prod', pool_id: 'pool', active: { routes: [route] } }
const service = { id: 'service', project: 'gateway', env: 'sandbox', poolId: 'pool', dns_status: 'READY', status: 'READY' }
it('preserves a real same-Pool cross-environment target while enforcing new and rebound ownership', () => {
  expect(existingRouteTarget(route, domain)).toBe(true)
  expect(routeTargetIssue(route, domain, service)).toBe('')
  expect(routeTargetIssue({ ...route, id: 'new' }, domain, service)).toBe('serviceScope')
  expect(routeTargetIssue({ ...route, service: 'another' }, domain, { ...service, id: 'another' })).toBe('serviceScope')
  expect(routeTargetIssue(route, { ...domain, active: { routes: [] }, draft: { config: { routes: [route] } } }, service)).toBe('')
  expect(routeTargetIssue({ ...route, kind: 'return' }, domain)).toBe('')
  expect(routeTargetIssue({ ...route, externalUrl: 'https://example.test/' }, domain)).toBe('')
})
it('rejects missing, removed, wrong-Pool and DNS-unavailable Services even for existing routes', () => {
  expect(routeTargetIssue(route, domain)).toBe('serviceMissing')
  expect(routeTargetIssue(route, domain, { ...service, status: 'REMOVED' })).toBe('serviceMissing')
  expect(routeTargetIssue(route, domain, { ...service, removed_at: '2026-11-01' })).toBe('serviceMissing')
  expect(routeTargetIssue(route, domain, { ...service, poolId: 'foreign' })).toBe('servicePool')
  expect(routeTargetIssue(route, domain, { ...service, dns_status: 'PENDING' })).toBe('serviceDNS')
})
it('never interprets absent, failed, expired or future observations as confirmed endpoint data', () => {
  expect(endpointState(service)).toBe('not-loaded')
  for (const state of ['not-loaded', 'error', 'stale']) expect(endpointState({ endpointObservation: { state } })).toBe(state)
  const now = 1_000_000
  expect(endpointState({ endpointObservation: { state: 'loaded', fetchedAt: now } }, now)).toBe('loaded')
  expect(endpointState({ endpointObservation: { state: 'loaded', fetchedAt: now - ENDPOINT_MAX_AGE_MS - 1 } }, now)).toBe('stale')
  expect(endpointState({ endpointObservation: { state: 'loaded', fetchedAt: now + 1 } }, now)).toBe('stale')
  expect(endpointState({ endpointObservation: { state: 'loaded' } }, now)).toBe('stale')
})
