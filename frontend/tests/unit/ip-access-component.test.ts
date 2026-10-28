import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import IPAccessControl from '../../src/components/IPAccessControl.vue'
import { useWorkspaceStore } from '../../src/stores/workspace'
import { i18n } from '../../src/shared/i18n'
import { ApiRequestError, session } from '../../src/shared/api/client'
import type { ClientIPSource, DomainConfig } from '../../src/types/domain'
const savedSession = session.value
let wrapper: ReturnType<typeof mount> | undefined
afterEach(() => { wrapper?.unmount(); wrapper = undefined; session.value = savedSession })
function fixture() {
  setActivePinia(createPinia())
  session.value = { ...session.value, role: 'administrator' }
  const store = useWorkspaceStore()
  const work: DomainConfig = { mode: 'managed', tls: false, redirect: false, body: '1m', native: '', serverSnippet: '', routes: [], ipAccess: { access: [], forbidden: [] } }
  const update = vi.fn((id: string, list: 'access' | 'forbidden', raw: string) => { work.ipAccess![list] = raw.split('\n').filter(line => line.trim()); store.revision++ })
  const preview = vi.fn(async () => 'CIDR configuration')
  const updateSource = vi.fn((source: ClientIPSource | null) => { if (source) work.ipAccess!.clientIP = source; else delete work.ipAccess!.clientIP; store.revision++ })
  store.configureIPAccess({ ipAccessEditorState: () => ({ domainID: 'domain', domainName: 'example.test', work }), updateIPAccess: update, updateClientIP: updateSource, previewIPAccess: preview })
  wrapper = mount(IPAccessControl, { global: { plugins: [i18n] } })
  return { store, update, preview, updateSource, work, panel: wrapper }
}
it('preserves multiline typing while updating the same draft', async () => {
  const { panel, update } = fixture()
  await panel.get('[name=ip-access-domain]').setValue(' 10.0.0.1-10.0.0.4 \n')
  expect(update).toHaveBeenCalledWith('', 'access', ' 10.0.0.1-10.0.0.4 \n')
  expect((panel.get('textarea').element as HTMLTextAreaElement).value).toBe(' 10.0.0.1-10.0.0.4 \n')
  await panel.get('button').trigger('click'); await flushPromises()
  expect(panel.get('pre').text()).toBe('CIDR configuration')
})
it('discards an older preview after newer edits and displays current preview errors', async () => {
  const { panel, preview } = fixture()
  let resolve!: (content: string) => void
  preview.mockImplementationOnce(() => new Promise<string>(done => { resolve = done }))
  await panel.get('button').trigger('click')
  await panel.get('[name=ip-forbidden-domain]').setValue('10.0.0.1')
  resolve('stale preview'); await flushPromises()
  expect(panel.find('pre').exists()).toBe(false)
  preview.mockRejectedValueOnce(new ApiRequestError(400, 'validation.control_invalid', { reason: 'preview failed' }))
  await panel.get('button').trigger('click'); await flushPromises()
  expect(panel.get('[role=alert]').text()).toContain('preview failed')
})
it('keeps viewer configuration read only', async () => {
  const { panel, update, updateSource } = fixture()
  session.value = { ...session.value, role: 'viewer' }; await nextTick()
  expect(panel.get('textarea').attributes()).toHaveProperty('readonly')
  expect(panel.get('button').attributes()).toHaveProperty('disabled')
  expect(panel.get('select').attributes()).toHaveProperty('disabled')
  await panel.get('textarea').setValue('::1')
  expect(update).not.toHaveBeenCalled()
  await panel.get('select').setValue('X-Forwarded-For')
  expect(updateSource).not.toHaveBeenCalled()
})
it('selects headers and trusted raw ranges, clears previews and returns to connection source', async () => {
  const { panel, updateSource, work } = fixture()
  await panel.get('button').trigger('click'); await flushPromises()
  await panel.get('[name=ip-client-source]').setValue('X-Forwarded-For')
  expect(panel.find('pre').exists()).toBe(false)
  await panel.get('[name=ip-trusted-proxies]').setValue(' 10.0.0.1-10.0.0.4 \n')
  expect(updateSource).toHaveBeenLastCalledWith({ header: 'X-Forwarded-For', trusted_proxies: [' 10.0.0.1-10.0.0.4 '] })
  expect((panel.get('[name=ip-trusted-proxies]').element as HTMLTextAreaElement).value).toBe(' 10.0.0.1-10.0.0.4 \n')
  await panel.get('[name=ip-client-source]').setValue('X-Real-IP')
  expect(work.ipAccess?.clientIP?.header).toBe('X-Real-IP')
  await panel.get('[name=ip-client-source]').setValue('custom')
  await panel.get('[name=ip-client-header]').setValue('CF-Connecting-IP')
  expect(updateSource).toHaveBeenLastCalledWith({ header: 'CF-Connecting-IP', trusted_proxies: [' 10.0.0.1-10.0.0.4 '] })
  await panel.get('[name=ip-client-source]').setValue('connection')
  expect(updateSource).toHaveBeenLastCalledWith(null)
  expect(work.ipAccess?.clientIP).toBeUndefined()
  expect(panel.find('[name=ip-trusted-proxies]').exists()).toBe(false)
})
