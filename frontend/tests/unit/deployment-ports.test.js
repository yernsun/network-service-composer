import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { markRaw } from 'vue'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import DeploymentPorts from '../../src/components/DeploymentPorts.vue'
import { useWorkspaceStore } from '../../src/stores/workspace'
import { composeSource, composeSession, composeSignature, deploymentReady, reviewCompose, checkCompose } from '../../src/compose/session'
import { editServicePort, parseCompose } from '../../src/compose/validator'
import { portFields, portValue } from '../../src/compose/ports'
import { request } from '../../src/shared/api/client'
vi.mock('../../src/shared/api/client', () => ({ request: vi.fn() }))
let wrapper, wizard, store
beforeEach(() => {
 vi.resetAllMocks(); setActivePinia(createPinia()); store = useWorkspaceStore()
 wizard = { source: 'image', image: 'mysql:8.4', key: 'mysql', port: 0, replicas: 1, memory: 512, step: 3, runtime: 'r', env: 'e', access: 'internal', reviewAck: true }
 store.service = markRaw({ state: { runtimes: [{ id: 'r', type: 'swarm' }] }, workload: { wizard } })
 wrapper = mount(DeploymentPorts)
})
afterEach(() => wrapper.unmount())
it('keeps MySQL internal by default and applies independent TCP publication without a Gateway', async () => {
 expect(parseCompose(composeSource(wizard)).tree.services.mysql.ports).toBeUndefined()
 await wrapper.find('.ports-heading button').trigger('click')
 expect(wizard.reviewAck).toBe(false)
 await wrapper.find('[data-port=published]').setValue('13306')
 await wrapper.find('[data-port=target]').setValue('3306')
 await wrapper.find('.ports-form .primary').trigger('click')
 expect(parseCompose(composeSource(wizard)).tree.services.mysql).toMatchObject({ ports: [{ target: 3306, published: '13306', protocol: 'tcp', mode: 'ingress' }] })
 expect(wizard.access).toBe('internal')
 expect(wrapper.find('.port-row').text()).toContain('13306 → 3306 / TCP')
 expect(wizard.portDraft).toBeNull()
 await wrapper.findAll('.port-actions button').at(1).trigger('click')
 expect(parseCompose(composeSource(wizard)).tree.services.mysql.ports).toBeUndefined()
})
it('keeps invalid pending edits visible across step changes and blocks final submission', async () => {
 await wrapper.find('.ports-heading button').trigger('click')
 await wrapper.find('[data-port=target]').setValue('3306')
 await wrapper.find('[data-port=published]').setValue('70000')
 await wrapper.find('.ports-form .primary').trigger('click')
 expect(wrapper.find('[role=alert]').text()).toContain('65535')
 expect(parseCompose(composeSource(wizard)).tree.services.mysql.ports).toBeUndefined()
 wizard.step = 4; store.revision++; await flushPromises()
 expect(wrapper.find('[data-port=published]').element.value).toBe('70000')
 const s = composeSession(wizard); s.pending = false; s.signature = composeSignature(wizard); s.result = { deployable: true }
 expect(deploymentReady(wizard)).toBe(false)
 await reviewCompose(wizard); expect(request).not.toHaveBeenCalled()
 await wrapper.find('.ports-form button').trigger('click')
 expect(wizard.portDraft).toBeNull()
})
it('binds review to published ports and shows the server-resolved mappings', async () => {
 wizard.publishedPorts = [{ target: 3306, published: '13306' }]; wizard.step = 4; store.revision++
 request.mockResolvedValueOnce({ data: { deployable: true, diagnostics: [] } })
 await checkCompose(wizard, true)
 request.mockResolvedValueOnce({ data: {}, response: new Response() })
 request.mockResolvedValueOnce({ data: { preflight_id: 'review', resolved_yaml: composeSource(wizard) } })
 await reviewCompose(wizard); await flushPromises()
 expect(request.mock.calls[2][2].body.compose_yaml).toContain('13306')
 expect(request.mock.calls[2][2].body.entrypoint).toBeUndefined()
 expect(wrapper.find('.port-row').text()).toContain('13306 → 3306')
 wizard.publishedPorts[0].published = '23306'; store.revision++; await flushPromises()
 expect(deploymentReady(wizard)).toBe(false)
 expect(wrapper.find('.port-row').exists()).toBe(false)
})
it('preserves unrelated services, aliases and comments on imported YAML edits', async () => {
 wizard.source = 'file'; wizard.raw = '# compose\nservices:\n  mysql: &base\n    image: mysql:8.4\n    ports: &ports\n      - "13306:3306" # database port\n    deploy: {replicas: 1}\n  copy: *base\n  third:\n    image: mysql:8.4\n    ports: *ports\n'; store.revision++; await flushPromises()
 const before = parseCompose(wizard.raw).tree
 await wrapper.find('.port-row button').trigger('click')
 await wrapper.find('[data-port=published]').setValue('23306')
 await wrapper.find('.ports-form .primary').trigger('click')
 const after = parseCompose(wizard.raw)
 expect(after.diagnostics).toEqual([])
 expect(after.tree.services.copy).toEqual(before.services.copy)
 expect(after.tree.services.third).toEqual(before.services.third)
 expect(after.tree.services.mysql.deploy).toEqual(before.services.mysql.deploy)
 expect(after.tree.services.mysql.ports[0].published).toBe('23306')
 expect(wizard.raw).toContain('# compose'); expect(wizard.raw).toContain('# database port')
})
it('supports standalone bind addresses and never carries a Swarm mode into new mappings', async () => {
 store.service.state.runtimes[0].type = 'docker'; store.revision++; await flushPromises()
 await wrapper.find('.ports-heading button').trigger('click')
 await wrapper.find('[data-port=published]').setValue('13306')
 await wrapper.find('[data-port=target]').setValue('3306')
 await wrapper.find('input[placeholder="0.0.0.0"]').setValue('127.0.0.1')
 await wrapper.find('.ports-form .primary').trigger('click')
 expect(wizard.publishedPorts).toEqual([{ target: 3306, published: '13306', protocol: 'tcp', host_ip: '127.0.0.1' }])
})
it('preserves imported metadata and represents ranges and IPv6 without flattening them', () => {
 expect(portFields('[::1]:13306:3306/tcp')).toMatchObject({ host_ip: '::1', target: '3306', published: '13306' })
 expect(portFields('8000-8005:80')).toMatchObject({ published: '8000-8005' })
 expect(portValue({ published: '8000-8005', target: '80', protocol: 'tcp' }, true)).toBeNull()
 const source = 'services:\n  db:\n    image: mysql:8.4\n    ports: ["13306:3306"]\n'
 const removed = editServicePort(source, 'db', 0, null)
 expect(parseCompose(removed.source).tree.services.db.ports).toEqual([])
 expect(editServicePort(source, 'absent', 0, null).error).toBeTruthy()
 expect(editServicePort(source, 'db', 2, null).source).toBe(source)
})
it('refuses to apply a port draft to a source changed elsewhere', async () => {
 await wrapper.find('.ports-heading button').trigger('click')
 await wrapper.find('[data-port=published]').setValue('13306')
 await wrapper.find('[data-port=target]').setValue('3306')
 wizard.image = 'mysql:9'; store.revision++; await flushPromises()
 await wrapper.find('.ports-form .primary').trigger('click')
 expect(wrapper.find('[role=alert]').text()).toContain('changed')
 expect(wizard.publishedPorts).toBeUndefined()
 expect(wizard.portDraft.published).toBe('13306')
})
it('preserves port field comments and isolates anchors defined in the port list', () => {
 const raw = 'services:\n  db:\n    image: mysql:8.4\n    ports:\n      - &p\n        target: 3306 # internal\n        published: "13306" # external\n        name: mysql # metadata\n        x-notes:\n          owner: team # nested metadata\n  other:\n    image: mysql:8.4\n    ports: [*p]\n'
 const changed = editServicePort(raw, 'db', 0, { target: 3306, published: '23306', name: 'mysql', protocol: 'tcp', 'x-notes': { owner: 'team' } })
 const parsed = parseCompose(changed.source)
 expect(parsed.diagnostics).toEqual([])
 expect(parsed.tree.services.other.ports[0].published).toBe('13306')
 expect(parsed.tree.services.db.ports[0].published).toBe('23306')
 expect(changed.source).toContain('# internal')
 expect(changed.source).toContain('# metadata')
 expect(changed.source.match(/# nested metadata/g)).toHaveLength(2)
})
it('opens the exact mapping selected by a server diagnostic', async () => {
 wizard.publishedPorts = [{ target: 3306, published: '13306' }, { target: 3307, published: '13306' }]
 store.revision++; await flushPromises()
 window.dispatchEvent(new CustomEvent('nsc:edit-port', { detail: { service: 'mysql', index: '1' } }))
 await flushPromises()
 expect(wizard.portDraft.index).toBe(1)
 expect(wrapper.find('[data-port=target]').element.value).toBe('3307')
})
