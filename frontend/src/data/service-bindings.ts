import { request } from '../shared/api/client'
import { idempotencyKey } from '../shared/browser/random'
import type { components, paths } from '../shared/api/schema'

type Schema = components['schemas']
export type Binding = Schema['DomainBinding']
export type Certificate = Schema['Certificate']
export type BindingForm = {
  domainID: string; host: string; path: string; port: number; protocol: 'http' | 'https'; serverName: string
  http3: boolean; tls: 'dns-01' | 'http-01' | 'custom'; provider: string; certificateID: string; redirect: boolean
}
export function domainInput(form: BindingForm): Schema['BindingDomainCreate'] {
  return {
    host: form.host.trim(), entrypoints: form.redirect ? ['http', 'https'] : ['https'],
    redirect_http_to_https: form.redirect, http3_enabled: form.http3,
    tls: form.tls === 'custom' ? { mode: 'custom', certificate_id: form.certificateID }
      : { mode: 'managed', acme_challenge: form.tls, ...(form.tls === 'dns-01' ? { dns_provider_ref: form.provider } : {}) },
  }
}
export function bindingInput(form: BindingForm): Schema['DomainBindingCreate'] {
  return {
    ...(form.domainID ? { domain_id: form.domainID } : { domain: domainInput(form) }),
    route: { path: form.path, backend: { port: form.port, protocol: form.protocol, server_name: form.serverName } },
  }
}
export function certificateCovers(certificate: Certificate, host: string, now = Date.now()): boolean {
  const name = host.trim().toLowerCase()
  return (certificate.provider === 'custom' || certificate.provider === 'self-signed') && certificate.status === 'ACTIVE'
    && !!certificate.not_before && Date.parse(certificate.not_before) <= now
    && !!certificate.not_after && Date.parse(certificate.not_after) > now
    && certificate.domains.some(san => {
      const value = san.toLowerCase()
      return value === name || value.startsWith('*.') && name.split('.').length === value.split('.').length && name.endsWith(value.slice(1))
    })
}

// These DTO boundaries are backed by generated OpenAPI schemas.
async function list<T>(path: keyof paths, signal: AbortSignal, query: Record<string, unknown> = {}): Promise<T[]> {
  const rows: T[] = [], seen = new Set<string>()
  let cursor = ''
  do {
    const result = await request('get', path, { signal, params: { query: { ...query, limit: 200, ...(cursor ? { cursor } : {}) } } })
    rows.push(...(result.data || []) as T[])
    cursor = result.pagination?.next_cursor || ''
    if (cursor && seen.has(cursor)) throw new Error('Repeated pagination cursor')
    seen.add(cursor)
  } while (cursor)
  return rows
}
export async function readBindings(id: string, signal: AbortSignal) {
  const service = (await request('get', '/services/{id}', { signal, params: { path: { id } } })).data as Schema['Service']
  const runtime = (await request('get', '/runtimes/{id}', { signal, params: { path: { id: service.runtime_id } } })).data as Schema['Runtime']
  const [bindings, certificates, domains, gateways, settings, operations, allBindings] = await Promise.all([
    request('get', '/services/{id}/domain-bindings', { signal, params: { path: { id } } }),
    list<Certificate>('/certificates', signal), list<Schema['Domain']>('/domains', signal),
    list<Schema['Gateway']>('/gateways', signal), request('get', '/settings', { signal }),
    list<Schema['DeploymentOperation']>('/operations', signal, { application_id: service.application_id }),
    list<Schema['Route']>('/routes', signal),
  ])
  const profiles = settings.data as { acme?: { dns_providers?: Record<string, { configured: boolean }> }; management?: { enabled: boolean } }
  // Only domains already associated with this environment may be reused.
  // An unbound domain can still be selected through an explicit API request.
  const services = await list<Schema['Service']>('/services', signal)
  const allowed = new Set(services.filter(s => s.environment?.id && s.environment.id === service.environment?.id).map(s => s.id))
  return {
    service, runtime, bindings: (bindings.data || []) as Binding[],
    certificates: certificates.filter(c => c.pool_id === runtime.pool_id),
    domains: domains.filter(d => d.pool_id === runtime.pool_id && allBindings.some(r => r.domain_id === d.id && allowed.has(r.backend.service_id))),
    gateways: gateways.filter(g => g.pool_id === runtime.pool_id && g.enabled),
    operations: operations.filter(o => o.service_id === id && ['DOMAIN_ATTACH', 'DOMAIN_DETACH'].includes(o.type) || !!service.application_id && o.application_id === service.application_id && o.type === 'REDEPLOY'),
    providers: Object.keys(profiles.acme?.dns_providers || {}).filter(key => profiles.acme?.dns_providers?.[key]?.configured).sort(),
    management: !!profiles.management?.enabled && !!runtime.management_enabled,
  }
}
export function bindingWriter() {
  const keys = idempotencyKey()
  return async (method: 'post' | 'patch' | 'delete', path: keyof paths, params: Record<string, string>, body: unknown, revision?: number) => {
    const result = await request(method, path, { params: { path: params }, body, headers: {
      'Idempotency-Key': keys.forRequest(method, path, params, body), ...(revision !== undefined ? { 'If-Match': `"rev-${revision}"` } : {}),
    } })
    keys.reset()
    return result
  }
}
