import { expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import * as api from '../../src/shared/api/client'
import DomainDraftForm from '../../src/components/DomainDraftForm.vue'
import DNSProfilesPanel from '../../src/components/DNSProfilesPanel.vue'

it('saves a server draft with HTTP-01 when DNS is not managed and DNS-01 only with a profile', async () => {
  const submitted: Record<string, unknown>[] = []
  const response = (data: unknown) => ({ data, response: { headers: { get: () => null } } as unknown as Response })
  const stub = vi.spyOn(api, 'request').mockImplementation(async (method, path, options) => {
    if (method === 'post') {
      submitted.push(options?.body as Record<string, unknown>)
      return response({ id: `draft-${submitted.length}` })
    }
    const data: Record<string, unknown> = {
      '/environments/{id}': { id: 'env', runtime_id: 'runtime' },
      '/runtimes/{id}': { id: 'runtime', pool_id: 'pool' },
      '/services': [{ id: 'service', name: 'web', discovered_port: 8080, environment: { id: 'env' } }],
      '/gateways': [{ id: 'gateway', name: 'edge', pool_id: 'pool', address: '192.0.2.20', enabled: true }],
      '/certificates': [],
      '/dns-provider-profiles': [{ reference: 'edge-dns', provider: 'cloudflare', source: 'database', configured: true, revision: 1 }],
    }
    return response(data[path] || [])
  })
  const wrapper = mount(DomainDraftForm, { props: { environmentId: 'env', serviceId: 'service' } })
  try {
    await flushPromises()
    const port = wrapper.get('input[type="number"]')
    expect((port.element as HTMLInputElement).value).toBe('8080')
    await wrapper.find('input[placeholder="app.example.com"]').setValue('web.example.test')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(submitted).toHaveLength(1)
    expect((submitted[0]!.domain as { tls: unknown }).tls).toEqual({ mode: 'managed', acme_challenge: 'http-01' })
    await wrapper.find('input[type="checkbox"]').setValue(true)
    const provider = wrapper.findAll('select').find(select => select.html().includes('edge-dns'))!
    await provider.setValue('edge-dns')
    await port.setValue('9090')
    await wrapper.get('.domain-draft-fields button[type="button"]').trigger('click')
    wrapper.getComponent(DNSProfilesPanel).vm.$emit('changed', 'edge-dns')
    await flushPromises()
    expect((port.element as HTMLInputElement).value).toBe('9090')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect((submitted[1]!.domain as { tls: unknown }).tls).toEqual({ mode: 'managed', acme_challenge: 'dns-01', dns_provider_ref: 'edge-dns' })
  } finally {
    wrapper.unmount(); stub.mockRestore()
  }
})
