import { describe, expect, it } from 'vitest'
import { bindingInput, certificateCovers, domainInput, type BindingForm, type Certificate } from '../../src/data/service-bindings'

const form: BindingForm = { domainID: '', host: 'app.example.com', path: '/', port: 8080, protocol: 'http', serverName: '', http3: false, tls: 'dns-01', provider: 'dns-prod', certificateID: 'old-certificate', redirect: false }
describe('service domain binding contract', () => {
  it('keeps client TLS separate from backend protocol and clears unrelated TLS fields', () => {
    expect(bindingInput(form)).toMatchObject({ domain: { entrypoints: ['https'], tls: { mode: 'managed', acme_challenge: 'dns-01', dns_provider_ref: 'dns-prod' } }, route: { backend: { port: 8080, protocol: 'http' } } })
    expect(domainInput(form).tls).not.toHaveProperty('certificate_id')
    expect(domainInput({ ...form, tls: 'http-01', redirect: true })).toMatchObject({ entrypoints: ['http', 'https'], tls: { mode: 'managed', acme_challenge: 'http-01' } })
    expect(domainInput({ ...form, tls: 'http-01' }).tls).not.toHaveProperty('dns_provider_ref')
    expect(domainInput({ ...form, tls: 'custom' }).tls).toEqual({ mode: 'custom', certificate_id: 'old-certificate' })
  })
  it('reuses an existing domain without silently changing its certificate', () => {
    const payload = bindingInput({ ...form, domainID: 'domain-existing' })
    expect(payload).toHaveProperty('domain_id', 'domain-existing')
    expect(payload).not.toHaveProperty('domain')
  })
  it('offers only current custom certificates matching the hostname', () => {
    const certificate: Certificate = { id: 'cert', pool_id: 'pool', provider: 'custom', status: 'ACTIVE', primary_domain: '*.example.com', domains: ['*.example.com'], not_before: '2026-01-01T00:00:00Z', not_after: '2027-01-01T00:00:00Z', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', revision: 1 }
    const now = Date.parse('2026-09-18T00:00:00Z')
    expect(certificateCovers(certificate, 'APP.example.com', now)).toBe(true)
    expect(certificateCovers(certificate, 'example.com', now)).toBe(false)
    expect(certificateCovers(certificate, 'a.b.example.com', now)).toBe(false)
    expect(certificateCovers(certificate, 'app.example.com.evil.test', now)).toBe(false)
    expect(certificateCovers({ ...certificate, provider: 'acme' }, form.host, now)).toBe(false)
    expect(certificateCovers({ ...certificate, status: 'REVOKED' }, form.host, now)).toBe(false)
    expect(certificateCovers(certificate, form.host, Date.parse('2027-01-01T00:00:00Z'))).toBe(false)
    expect(certificateCovers(certificate, form.host, Date.parse('2025-01-01T00:00:00Z'))).toBe(false)
  })
})
