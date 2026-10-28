import { flushPromises, mount } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { beforeEach, expect, it, vi } from 'vitest'
import { h, ref } from 'vue'
import ApplicationNetwork from '../../src/components/ApplicationNetwork.vue'
const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, session: ref({ role: 'administrator' }), errorText: (e: Error) => e.message }))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))
beforeEach(() => {
  send.mockReset().mockImplementation(async (method, path, options) => {
    if (path.endsWith('/preflight')) return { data: { network_name: options.body.network_name, services: [{ name: 'web' }, { name: 'database' }] } }
    if (method === 'post') return { data: { operation: { id: 'deployment' } } }
    return { data: path === '/applications/{id}' ? { id: 'app', runtime_id: 'rt', revision: 7, managed: true, project_name: 'demoapp', active_revision_id: 'revision' }
      : path === '/runtimes/{id}' ? { runtime_type: 'swarm', management_enabled: true } : { configuration_source: { network_name: 'demoapp' } } }
  })
})
it('requires a fresh review after editing and submits the reviewed network with its application revision', async () => {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: ApplicationNetwork, props: { applicationId: 'app' } }] })
  await router.push('/'); await router.isReady()
  const query = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  const wrapper = mount({ render: () => h(RouterView) }, { global: { plugins: [router, [VueQueryPlugin, { queryClient: query }]] } })
  try {
    await flushPromises()
    expect(wrapper.text()).toContain('demoapp')
    await wrapper.get('button').trigger('click')
    await wrapper.get('input').setValue('demoapp-dev')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.text()).toContain('database')
    await wrapper.get('input').setValue('demoapp-test')
    expect(wrapper.find('button.primary').exists()).toBe(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    await wrapper.get('button.primary').trigger('click'); await flushPromises()
    const submission = send.mock.calls.find(([method, path]) => method === 'post' && path === '/applications/{id}/revisions')!
    expect(submission[2].body).toEqual({ network_name: 'demoapp-test' })
    expect(submission[2].headers['If-Match']).toBe('"rev-7"')
    expect(submission[2].headers['Idempotency-Key']).toBeTruthy()
    expect(wrapper.get('a').attributes('href')).toBe('/operations/deployment')
  } finally { wrapper.unmount(); query.clear() }
})
