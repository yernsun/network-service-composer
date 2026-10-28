import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import ServiceRestore from '../../src/components/ServiceRestore.vue'
import { session } from '../../src/shared/api/client'
import { i18n, setLocale } from '../../src/shared/i18n'

const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, session: ref({ role: 'administrator' }), errorText: (e: Error) => e.message }))
let wrapper: VueWrapper, query: QueryClient
let service: Record<string, unknown>
beforeEach(() => {
  setLocale('en'); session.value.role = 'administrator'
  service = { id: 'svc', managed: true, system: false, application_id: 'app', status: 'RUNNING', revision: 8 }
  send.mockReset().mockImplementation(async (_method, path) => ({ data:
    path === '/services/{id}' ? service : path === '/applications/{id}/revisions' ? [{ id: 'rev-1', sequence: 1 }, { id: 'rev-2', sequence: 2 }] :
      path.endsWith('restore-preview') ? { id: 'review', base_revision: 8, preview: { resolved_yaml: 'services: frozen-content' } } : { id: 'operation' },
  }))
})
afterEach(() => { wrapper?.unmount(); query?.clear() })
async function screen() {
  query = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  wrapper = mount(ServiceRestore, { props: { serviceId: 'svc' }, global: { plugins: [i18n, [VueQueryPlugin, { queryClient: query }]] } })
  await flushPromises(); return wrapper
}

it('requires a reviewed revision and data acknowledgement, then retains the same retry key', async () => {
  const w = await screen()
  expect(w.findAll('option').map(option => option.text())).toEqual([i18n.global.t('serviceRestore.choose'), 'r2', 'r1'])
  await w.get('select').setValue('rev-1'); await flushPromises()
  expect(w.get('pre').text()).toBe('services: frozen-content')
  expect(w.get('button.primary').attributes('disabled')).toBeDefined()
  await w.get('input[type=checkbox]').setValue(true)
  send.mockRejectedValueOnce(new Error('acknowledgement lost'))
  await w.get('button.primary').trigger('click'); await flushPromises()
  expect(w.get('[role=alert]').text()).toBe('acknowledgement lost')
  await w.get('button.primary').trigger('click'); await flushPromises()
  const calls = send.mock.calls.filter(([, path]) => path === '/services/{id}/restore')
  expect(calls).toHaveLength(2)
  expect(calls[0]![2]).toMatchObject({ headers: { 'If-Match': '"rev-8"' }, body: { review_id: 'review', acknowledge_data_boundary: true } })
  expect(calls[0]![2].headers['Idempotency-Key']).toBeTruthy()
  expect(calls[1]![2].headers['Idempotency-Key']).toBe(calls[0]![2].headers['Idempotency-Key'])
  expect(w.get('[role=status]').text()).toContain('operation')
  expect(w.get('button.primary').attributes('disabled')).toBeDefined()
  await w.get('select').setValue('rev-2'); await flushPromises()
  expect(w.find('[role=status]').exists()).toBe(false); expect((w.get('input[type=checkbox]').element as HTMLInputElement).checked).toBe(false)
  await w.setProps({ serviceId: 'svc-other' }); await flushPromises()
  expect(w.find('pre').exists()).toBe(false); expect((w.get('select').element as HTMLSelectElement).value).toBe('')
})

it('clears an unsuccessful preview and does not publish an empty revision', async () => {
  const w = await screen(); send.mockRejectedValueOnce(new Error('revision unavailable'))
  await w.get('select').setValue('rev-1'); await flushPromises()
  expect(w.get('[role=alert]').text()).toBe('revision unavailable'); expect(w.find('button.primary').exists()).toBe(false)
  const count = send.mock.calls.length; await w.get('select').setValue(''); await flushPromises()
  expect(send.mock.calls).toHaveLength(count); expect(w.find('[role=alert]').exists()).toBe(false)
})

it('requires deletion confirmation and reuses its key after an unknown result', async () => {
  const w = await screen(), confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
  await w.get('button.danger').trigger('click'); expect(send.mock.calls.every(([method]) => method === 'get')).toBe(true)
  confirm.mockReturnValue(true); send.mockRejectedValueOnce(new Error('connection lost'))
  await w.get('button.danger').trigger('click'); await flushPromises(); expect(w.get('[role=alert]').text()).toBe('connection lost')
  await w.get('button.danger').trigger('click'); await flushPromises()
  const calls = send.mock.calls.filter(([, path]) => path === '/services/{id}/delete')
  expect(calls).toHaveLength(2); expect(calls[0]![2]).toMatchObject({ headers: { 'If-Match': '"rev-8"' }, body: { acknowledge_data_boundary: true } })
  expect(calls[1]![2].headers['Idempotency-Key']).toBe(calls[0]![2].headers['Idempotency-Key'])
  expect(w.get('[role=status]').text()).toContain('operation'); expect(w.get('button.danger').attributes('disabled')).toBeDefined()
})

it.each(['viewer', 'external', 'system', 'no-application', 'removed'])('enforces the %s service boundary', async boundary => {
  if (boundary === 'viewer') session.value.role = 'viewer'
  if (boundary === 'external') service.managed = false
  if (boundary === 'system') service.system = true
  if (boundary === 'no-application') service.application_id = ''
  if (boundary === 'removed') { service.status = 'REMOVED'; service.removed_at = '2026-10-06T00:00:00Z' }
  const w = await screen()
  if (boundary === 'removed') expect(w.find('button.danger').exists()).toBe(false)
  else expect(w.find('.service-restore').exists()).toBe(false)
  expect(send.mock.calls.every(([method]) => method === 'get')).toBe(true)
})
