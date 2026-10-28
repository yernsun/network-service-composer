import { flushPromises, mount } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ImageUpdates from '../../src/components/ImageUpdates.vue'
import { ApiRequestError, session } from '../../src/shared/api/client'
import type { components } from '../../src/shared/api/schema'
const send = vi.hoisted(() => vi.fn())
vi.mock('../../src/shared/api/client', () => ({ request: send, ApiRequestError: class extends Error { constructor(readonly status: number, readonly code: string) { super(code) } }, session: ref({ role: 'administrator' }), errorText: (error: Error) => error.message }))
vi.mock('../../src/i18n', () => ({ translate: (key: string) => key }))
vi.mock('../../src/shared/i18n', () => ({ formatDate: (value: string) => value }))
let policy: components['schemas']['ImageUpdatePolicy'], cap: components['schemas']['EnvironmentCapabilities'], rows: components['schemas']['ImageUpdateCandidate'][]
let failedPath = '', failureCode = 503, next = ''
const cleanup: (() => void)[] = []
const ok = (data: unknown, pagination?: { next_cursor: string; limit: number }) => ({ data, response: new Response(null), ...(pagination ? { pagination } : {}) })
beforeEach(() => {
  send.mockReset(); session.value.role = 'administrator'; failedPath = ''; failureCode = 503; next = ''
  policy = { id: 'env-one:image-policy', revision: 1, enabled: true, poll: true, webhook: false, automatic: false, interval_seconds: 300, services: ['web'] }
  cap = { certificates: { state: 'UNKNOWN' }, acme: { state: 'UNKNOWN' }, notifications: { state: 'UNKNOWN' }, ha: { state: 'UNKNOWN' }, environment_id: 'env-one', runtime_id: 'rt-one', management: { state: 'CONFIGURED' }, image_updates: { state: 'BASELINE_READY' }, base_revision_id: 'revision-one', services: [{ name: 'web', reference: 'registry.example.test/web:stable', image: 'registry.example.test/web@sha256:old' }] }
  rows = [{ id: 'candidate-one', revision: 2, base_revision_id: 'revision-one', status: 'AVAILABLE', images: { web: 'registry.example.test/web@sha256:new' }, changed: ['web'] }]
  send.mockImplementation(async (method, path, options) => {
    if (method === 'get') {
      if (path === failedPath) throw new ApiRequestError(failureCode, 'unavailable')
      if (path.endsWith('/update-policy')) return ok({ ...policy, services: [...policy.services] })
      if (path.endsWith('/capabilities')) return ok(structuredClone(cap))
      if (path.endsWith('/image-checks')) return ok({ requested: false, last_checked: '2026-10-18T00:00:00Z' })
      if (path.endsWith('/webhook')) return ok({ revision: 3, hook_id: 'fixture-hook' })
      return ok(structuredClone(rows), { next_cursor: next, limit: 20 })
    }
    if (method === 'put') { policy = { ...policy, ...options.body, revision: policy.revision + 1 }; return ok({ ...policy }) }
    if (path.endsWith('/webhook')) return ok({ hook: { revision: 4, hook_id: 'fixture-hook' }, secret: 'one-time-private-signing-key' })
    return ok({ status: 'QUEUED' })
  })
})
afterEach(() => { for (const dispose of cleanup.splice(0)) dispose() })
function panel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  const wrapper = mount(ImageUpdates, { props: { environmentId: 'env-one' }, global: { plugins: [[VueQueryPlugin, { queryClient: client }]], stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } } })
  cleanup.push(() => { wrapper.unmount(); client.clear() })
  return wrapper
}
async function settle() { await flushPromises(); await flushPromises() }
function button(wrapper: ReturnType<typeof panel>, text: string) { return wrapper.findAll('button').find(item => item.text() === text)! }

describe('image update console', () => {
  it('loads digest evidence and preserves unsaved edits through refresh', async () => {
    const wrapper = panel(); await settle()
    expect(wrapper.text()).toContain('registry.example.test/web@sha256:old')
    expect(wrapper.text()).toContain('registry.example.test/web@sha256:new')
    await wrapper.get('input[type=number]').setValue(600)
    await wrapper.get('[data-testid=image-refresh]').trigger('click'); await settle()
    expect((wrapper.get('input[type=number]').element as HTMLInputElement).value).toBe('600')
    for (const [method, , options] of send.mock.calls) if (method === 'get') { expect(options.cache).toBe('no-store'); expect(options.signal).toBeInstanceOf(AbortSignal) }
  })
  it('starts new policy forms with automatic deployment off despite the legacy dev default', async () => {
    policy = { ...policy, revision: 0, enabled: false, automatic: true }
    const wrapper = panel(); await settle()
    expect((wrapper.get('[data-testid=image-automatic]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.get('form').trigger('submit'); await settle()
    const call = send.mock.calls.find(([method]) => method === 'put')!
    expect(call[2].headers['If-Match']).toBe('"rev-0"'); expect(call[2].body.automatic).toBe(false)
    expect(wrapper.text()).toContain('imageUpdates.saved')
  })
  it('blocks an external revision change until the user explicitly reloads', async () => {
    const wrapper = panel(); await settle()
    await wrapper.get('input[type=number]').setValue(600)
    policy = { ...policy, revision: 2, interval_seconds: 900 }
    await wrapper.get('[data-testid=image-refresh]').trigger('click'); await settle()
    expect(wrapper.text()).toContain('notifications.changed')
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeDefined()
    expect((wrapper.get('input[type=number]').element as HTMLInputElement).value).toBe('600')
    await button(wrapper, 'notifications.reload').trigger('click'); await settle()
    expect((wrapper.get('input[type=number]').element as HTMLInputElement).value).toBe('900')
  })
  it('blocks detection without a baseline, and validates the polling interval', async () => {
    cap.image_updates = { state: 'BLOCKED', reason: 'baseline_required' }
    const wrapper = panel(); await settle()
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid=image-check]').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit'); await settle()
    expect(send.mock.calls.every(([method]) => method === 'get')).toBe(true)
    cap.image_updates = { state: 'BASELINE_READY' }
    await wrapper.get('[data-testid=image-refresh]').trigger('click'); await settle()
    await wrapper.get('input[type=number]').setValue(59)
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeDefined()
  })
  it('queues checks separately from deployments and refreshes the result', async () => {
    const wrapper = panel(); await settle()
    await wrapper.get('[data-testid=image-check]').trigger('click'); await settle()
    expect(send.mock.calls.find(([method, path]) => method === 'post' && path.endsWith('/image-checks'))![2].params.path.id).toBe('env-one')
    expect(send.mock.calls.some(([method, path]) => method === 'post' && path.includes('updateID'))).toBe(false)
    expect(wrapper.text()).toContain('imageUpdates.checkQueued')
  })
  it.each(['deploy', 'ignore'] as const)('requires confirmation and candidate ETag for %s', async kind => {
    const wrapper = panel(); await settle()
    await button(wrapper, `imageUpdates.${kind}`).trigger('click'); await settle()
    expect(send.mock.calls.every(([method]) => method === 'get')).toBe(true)
    await button(wrapper, 'imageUpdates.confirmAction').trigger('click'); await settle()
    const call = send.mock.calls.find(([method]) => method === 'post')!
    expect(call[2]).toMatchObject({ headers: { 'If-Match': '"rev-2"' }, params: { path: { id: 'env-one', updateID: 'candidate-one' } }, body: { action: kind } })
    expect(wrapper.find('[data-testid=image-confirm]').exists()).toBe(false)
  })
  it('disables a pending confirmation if the latest receipt changed', async () => {
    const wrapper = panel(); await settle()
    await button(wrapper, 'imageUpdates.deploy').trigger('click')
    rows[0] = { ...rows[0]!, status: 'QUEUED', operation_id: 'operation-one' }
    await wrapper.get('[data-testid=image-refresh]').trigger('click'); await settle()
    expect(button(wrapper, 'imageUpdates.confirmAction').attributes('disabled')).toBeDefined()
    await button(wrapper, 'imageUpdates.confirmAction').trigger('click'); await settle()
    expect(send.mock.calls.every(([method]) => method === 'get')).toBe(true)
    expect(wrapper.get('a').attributes('href')).toBe('/operations/operation-one')
  })
  it('allows retry only for fresh failed candidates with the current baseline', async () => {
    rows[0] = { ...rows[0]!, status: 'FAILED', operation_id: 'operation-failed' }
    const wrapper = panel(); await settle()
    expect(button(wrapper, 'imageUpdates.retry').attributes('disabled')).toBeUndefined()
    cap.base_revision_id = 'revision-new'
    await wrapper.get('[data-testid=image-refresh]').trigger('click'); await settle()
    expect(button(wrapper, 'imageUpdates.retry').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('imageUpdates.superseded')
  })
  it('labels historical candidates and blocks actions after a failed candidate refresh', async () => {
    const wrapper = panel(); await settle(); failedPath = '/environments/{id}/image-updates'
    await wrapper.get('[data-testid=image-refresh]').trigger('click'); await settle()
    expect(wrapper.text()).toContain('imageUpdates.historical')
    expect(wrapper.text()).toContain('imageUpdates.status.UNKNOWN')
    expect(button(wrapper, 'imageUpdates.deploy').attributes('disabled')).toBeDefined()
  })
  it.each([401, 403])('hides cached settings and candidates after authorization loss (%s)', async status => {
    const wrapper = panel(); await settle(); failedPath = '/environments/{id}/update-policy'; failureCode = status
    await wrapper.get('[data-testid=image-refresh]').trigger('click'); await settle()
    expect(wrapper.find('form').exists()).toBe(false); expect(wrapper.find('[data-candidate]').exists()).toBe(false)
  })
  it('keeps viewers read-only', async () => {
    session.value.role = 'viewer'
    const wrapper = panel(); await settle()
    expect(wrapper.find('[data-testid=image-check]').exists()).toBe(false)
    expect(wrapper.find('[data-testid=image-hook]').exists()).toBe(false)
    expect(wrapper.get('form > fieldset').attributes('disabled')).toBeDefined()
    expect(send.mock.calls.every(([method]) => method === 'get')).toBe(true)
  })
  it('shows a rotated signing key once and clears it on demand', async () => {
    const wrapper = panel(); await settle()
    await wrapper.get('[data-testid=image-hook]').trigger('click'); await settle()
    const call = send.mock.calls.find(([method, path]) => method === 'post' && path.endsWith('/webhook'))!
    expect(call[2].headers['If-Match']).toBe('"rev-3"')
    expect((wrapper.get('input[type=password]').element as HTMLInputElement).value).toBe('one-time-private-signing-key')
    await button(wrapper, 'imageUpdates.clearSecret').trigger('click'); await settle()
    expect(wrapper.find('input[type=password]').exists()).toBe(false)
  })
  it('paginates candidates using the returned opaque cursor', async () => {
    next = 'next-page'
    const wrapper = panel(); await settle()
    await button(wrapper, 'notifications.next').trigger('click'); await settle()
    expect(send.mock.calls.some(([method, path, options]) => method === 'get' && path.endsWith('/image-updates') && options.params.query.cursor === 'next-page')).toBe(true)
  })
  it('preserves an edited form after a rejected save and requires review of the fresh revision', async () => {
    const wrapper = panel(); await settle()
    await wrapper.get('input[type=number]').setValue(600)
    policy = { ...policy, revision: 2, interval_seconds: 900 }
    const previous = send.getMockImplementation()!
    send.mockImplementation((method, path, options) => method === 'put' ? Promise.reject(new ApiRequestError(412, 'stale')) : previous(method, path, options))
    await wrapper.get('form').trigger('submit'); await settle()
    expect(wrapper.text()).toContain('stale')
    expect(wrapper.text()).toContain('notifications.changed')
    expect((wrapper.get('input[type=number]').element as HTMLInputElement).value).toBe('600')
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeDefined()
  })
  it('clears the one-time signing key when administrator permission is lost', async () => {
    const wrapper = panel(); await settle()
    await wrapper.get('[data-testid=image-hook]').trigger('click'); await settle()
    expect(wrapper.find('input[type=password]').exists()).toBe(true)
    session.value.role = 'viewer'; await settle()
    expect(wrapper.find('input[type=password]').exists()).toBe(false)
  })
  it('discards a late policy response from the previous environment', async () => {
    let finish!: (value: unknown) => void
    const previous = send.getMockImplementation()!
    send.mockImplementation((method, path, options) => {
      const id = options.params.path.id
      if (path.endsWith('/update-policy')) return id === 'env-one' ? new Promise(resolve => { finish = resolve }) : Promise.resolve(ok({ ...policy, revision: 9, interval_seconds: 900 }))
      if (path.endsWith('/capabilities')) return Promise.resolve(ok({ ...cap, environment_id: id }))
      return previous(method, path, options)
    })
    const wrapper = panel(); await settle()
    await wrapper.setProps({ environmentId: 'env-two' }); await settle()
    finish(ok({ ...policy, interval_seconds: 300 })); await settle()
    expect((wrapper.get('input[type=number]').element as HTMLInputElement).value).toBe('900')
    expect(wrapper.text()).not.toContain('notifications.changed')
  })
})
