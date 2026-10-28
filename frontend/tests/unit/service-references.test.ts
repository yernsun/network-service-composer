import { afterEach, describe, expect, it } from 'vitest'
import { serviceName, serviceSearch } from '../../src/data/service-references'
import { i18n, languages, setLocale } from '../../src/shared/i18n'

afterEach(() => setLocale('en'))
describe('service reference presentation', () => {
  it('resolves identical names by identity across scopes without changing the records', () => {
    const services = [{ id: 'svc_a', name: 'nginx' }, { id: 'svc_b', name: 'nginx' }]
    expect(serviceName(services, 'svc_a')).toBe('nginx')
    expect(serviceName(services, 'svc_b')).toBe('nginx')
    services[1]!.name = 'renamed nginx'
    expect(serviceName(services, 'svc_b')).toBe('renamed nginx')
    expect(services.map(s => s.id)).toEqual(['svc_a', 'svc_b'])
    expect(serviceSearch(services, 'svc_b')).toBe('renamed nginx svc_b')
  })
  it('keeps names literal in every locale and localizes missing references with the exact ID', () => {
    const literal = '删除 {id} <b>&lt;name&gt;</b> "Service"'
    for (const { code } of languages) {
      setLocale(code)
      expect(serviceName([{ id: 'svc_literal', name: literal }], 'svc_literal')).toBe(literal)
      for (const services of [[], [{ id: 'svc_missing', name: ' ' }], [{ id: 'svc_missing', name: null }]])
        expect(serviceName(services, 'svc_missing')).toBe(i18n.global.t('references.service_unavailable', { id: 'svc_missing' }))
    }
    expect(serviceName([], '')).toBe('')
    expect(serviceName([], undefined)).toBe('')
  })
})
