import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { markRaw } from 'vue'
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import DeploymentAnalysis from '../../src/components/DeploymentAnalysis.vue'
import { useWorkspaceStore } from '../../src/stores/workspace'
import { composeSession } from '../../src/compose/session'
import { request } from '../../src/shared/api/client'
vi.mock('../../src/shared/api/client', () => ({ request: vi.fn(), asRows: data => Array.isArray(data) ? data : [], session: { role: 'administrator' } }))
let wrapper, wizard, render
const required = { id: 'local', code: 'SWARM_LOCAL_VOLUME_WITHOUT_PLACEMENT', severity: 'required', service: 'web', path: ['services', 'web', 'volumes'] }
beforeEach(async () => {
 vi.useFakeTimers(); vi.resetAllMocks()
 setActivePinia(createPinia())
 wizard = { source: 'file', raw: 'services:\n  web:\n    image: nginx:1\n    volumes: [/data]\n', env: 'e', runtime: 'rt', step: 1, reviewAck: true }
 render = vi.fn(() => { useWorkspaceStore().revision++ })
 useWorkspaceStore().service = markRaw({ state: { runtimes: [{ id: 'rt', type: 'swarm' }] }, workload: { wizard }, deploymentWizard: { render, collect: vi.fn(), issues: () => ({ errors: [], warnings: [] }) } })
 document.body.innerHTML = '<div class="x-wizard-grid"><div></div><aside class="x-summary"></aside></div><input id="review" type="checkbox" data-wf="reviewAck"><button id="x-w-primary"></button>'
 request.mockImplementation(async (method) => method === 'get' ? { data: [{ id: 'worker', name: 'worker-01', state: 'ready', availability: 'active', role: 'worker' }] } : { data: { syntax_valid: true, schema_valid: true, deployable: false, diagnostics: [required], services: [{ name: 'web', mounts: [{ key: 'm', fingerprint: 'f', index: 0, type: 'volume', source: '', target: '/data', capability: 'local' }], constraints: [], matched_nodes: [] }], nodes: [{ id: 'worker', name: 'worker-01', state: 'ready', availability: 'active', role: 'worker' }] } })
 wrapper = mount(DeploymentAnalysis, { attachTo: document.body, global: { plugins: [[VueQueryPlugin, { queryClient: new QueryClient({ defaultOptions: { queries: { gcTime: 0, retry: false } } }) }]] } })
 await vi.advanceTimersByTimeAsync(1); await flushPromises()
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers(); document.body.innerHTML = '' })
it('does not invalidate analysis or clear the final acknowledgement when checking confirmation', async () => {
 wizard.reviewAck = true
 const sequence = composeSession(wizard).sequence
 document.querySelector('#review').dispatchEvent(new Event('change', { bubbles: true }))
 await flushPromises()
 expect(wizard.reviewAck).toBe(true)
 expect(composeSession(wizard).sequence).toBe(sequence)
})
it('opens the same workspace without choosing a node and guards unapplied changes', async () => {
 document.querySelector('[data-compose-service=web]').click(); await flushPromises()
 const radios = wrapper.findAll('input[type=radio]')
 await radios.find(r => r.element.value === 'node').setValue(true)
 const selects = wrapper.findAll('select')
 expect(selects.at(-1).element.value).toBe('')
 await selects.at(-1).setValue('worker')
 await wrapper.find('.compose-actions button').trigger('click')
 expect(wrapper.find('.callout.amber').exists()).toBe(true)
 expect(wizard.raw).not.toContain('node.hostname')
 const guard = wrapper.findAll('.callout.amber').at(-1)
 await guard.findAll('button')[1].trigger('click')
 expect(wrapper.find('[data-testid=compose-workspace]').exists()).toBe(false)
 expect(wizard.raw).not.toContain('node.hostname')
})
it('keeps unresolved required issues blocking even when generic warning acknowledgements exist', async () => {
 wizard.deploymentPlan = { acknowledged: ['local'] }
 wizard.step = 4
 document.querySelector('#review').dispatchEvent(new Event('change', { bubbles: true }))
 await flushPromises()
 expect(composeSession(wizard).result.deployable).toBe(false)
 expect(composeSession(wizard).review).toBeNull()
})

it('reenables navigation after pending analysis completes', () => {
 expect(document.querySelector('#x-w-primary').disabled).toBe(false)
})
it('does not redraw a closed wizard when an older validation response arrives', async () => {
 let resolve
 request.mockImplementationOnce(() => new Promise(done => { resolve = done }))
 const editor = document.createElement('textarea'); editor.dataset.wf = 'raw'; document.body.append(editor)
 wizard.raw += '# another edit\n'
 editor.dispatchEvent(new Event('input', { bubbles: true }))
 await vi.advanceTimersByTimeAsync(400)
 expect(resolve).toBeTypeOf('function')
 useWorkspaceStore().service.workload.wizard = null
 useWorkspaceStore().revision++
 await flushPromises(); render.mockClear()
 resolve({ data: { diagnostics: [], services: [], syntax_valid: true, schema_valid: true, deployable: true } })
 await flushPromises()
 expect(render).not.toHaveBeenCalled()
})
it('does not disable the clicked next button when a validated editor blurs', async () => {
 const editor = document.createElement('textarea'); editor.dataset.wf = 'raw'; document.body.append(editor)
 const sequence = composeSession(wizard).sequence
 editor.dispatchEvent(new Event('change', { bubbles: true })); await flushPromises()
 expect(composeSession(wizard).sequence).toBe(sequence)
 expect(document.querySelector('#x-w-primary').disabled).toBe(false)
})

it('offers target selection for system protection and retains the current draft', async () => {
 const raw = wizard.raw
 const s = composeSession(wizard)
 s.error = { code: 'system.project_protected', key: 'compose.systemTargetProtected', params: {} }
 await flushPromises()
 const failure = document.querySelector('[data-testid=compose-analysis] .callout.red')
 expect(failure.textContent).toContain('protected system project')
 expect(failure.textContent).not.toContain('cluster nodes')
 failure.querySelector('button').click()
 await flushPromises()
 expect(wizard.step).toBe(2)
 expect(wizard.raw).toBe(raw)
 expect(render).toHaveBeenCalled()
})
it('sends port diagnostics to port editing instead of the placement workspace', async () => {
 const s = composeSession(wizard)
 s.result.diagnostics = [{ id: 'port', code: 'COMPOSE_PORT_CONFLICT', severity: 'error', service: 'web', path: ['services', 'web', 'ports', '1'] }]
 const edit = vi.fn(); window.addEventListener('nsc:edit-port', edit)
 await flushPromises()
 document.querySelector('[data-compose-service=web]').click(); await flushPromises()
 expect(wizard.step).toBe(3)
 expect(edit.mock.calls[0][0].detail).toEqual({ service: 'web', index: '1' })
 expect(wrapper.find('[data-testid=compose-workspace]').exists()).toBe(false)
 window.removeEventListener('nsc:edit-port', edit)
})
it('loads a data node even when required template parameters prevent interpolation', async () => {
 wizard.source = 'template'; wizard.template = 'wordpress'
 const raw = 'services:\n  wordpress:\n    image: wordpress\n    environment: {PASSWORD: "${PASSWORD}"}\n    volumes: [site:/var/www/html]\nvolumes:\n  site: {}\n'
 useWorkspaceStore().service.marketTemplates = () => [{ id: 'wordpress', raw, swarmRaw: raw, catalogVariables: [{ name: 'PASSWORD', required: true }] }]
 request.mockImplementation(async (method, path) => path.endsWith('/cluster/nodes') ? { data: [{ id: 'worker', name: 'worker-01', state: 'ready', availability: 'active', role: 'worker' }] } : { data: { syntax_valid: true, schema_valid: true, deployable: false, diagnostics: [{ code: 'COMPOSE_ENVIRONMENT_UNRESOLVED', severity: 'required' }], services: [] } })
 window.dispatchEvent(new Event('nsc:compose-change')); await vi.advanceTimersByTimeAsync(400); await flushPromises()
 expect(document.querySelector('#market-data-node option[value=worker]')).not.toBeNull()
 expect(wizard.deploymentPlan.data_node_id).toBe('worker')
 expect(composeSession(wizard).result.deployable).toBe(false)
})
async function reopenStateful(nodeResponse) {
 wrapper.unmount()
 wizard.source = 'template'; wizard.swarmRaw = 'services:\n  db:\n    image: mysql:8\n    volumes: [data:/data]\nvolumes:\n  data: {}\n'; wizard.deploymentPlan = { data_node_id: '' }
 request.mockImplementation(async (method, path, options) => path.endsWith('/cluster/nodes') ? nodeResponse(options) : { data: { syntax_valid: true, schema_valid: true, deployable: false, diagnostics: [{ code: 'COMPOSE_ENVIRONMENT_UNRESOLVED', severity: 'required' }], services: [] } })
 wrapper = mount(DeploymentAnalysis, { attachTo: document.body, global: { plugins: [[VueQueryPlugin, { queryClient: new QueryClient({ defaultOptions: { queries: { gcTime: 0, retry: false } } }) }]] } })
 await vi.advanceTimersByTimeAsync(5); await flushPromises()
}
it('keeps multiple candidates explicit, sorts workers first and refuses a stale selection', async () => {
 await reopenStateful(() => ({ data: [ { id: 'manager', name: 'manager', role: 'manager', state: 'ready', availability: 'active' }, { id: 'worker', name: 'worker', role: 'worker', state: 'ready', availability: 'active' }, { id: 'stale', name: 'stale', role: 'worker', state: 'ready', availability: 'active', stale: true } ] }))
 const select = document.querySelector('#market-data-node')
 expect(select.value).toBe(''); expect(select.options[1].value).toBe('worker'); expect(select.querySelector('option[value=stale]').disabled).toBe(true)
 select.value = 'worker'; select.dispatchEvent(new Event('change', { bubbles: true })); await vi.advanceTimersByTimeAsync(5); await flushPromises()
 expect(wizard.deploymentPlan.data_node_id).toBe('worker')
})
it('shows empty and failed node reads, then retries without requiring passwords', async () => {
 let fail = true
 await reopenStateful(() => { if (fail) throw new Error('offline'); return { data: [] } })
 expect(document.querySelector('#market-data-node').disabled).toBe(true)
 const retry = document.querySelector('[data-testid=market-data-node] button')
 expect(retry).not.toBeNull(); fail = false; retry.click(); await flushPromises()
 expect(document.querySelector('[data-testid=market-data-node]').textContent).toContain('No Ready/Active')
 expect(wizard.deploymentPlan.data_node_id).toBe('')
})
it('reads every node page independently of template analysis', async () => {
 await reopenStateful(options => ({ data: options.params.query.cursor ? [{ id: 'worker', name: 'worker', state: 'ready', availability: 'active', role: 'worker' }] : [{ id: 'down', name: 'down', state: 'down', availability: 'active' }], pagination: { next_cursor: options.params.query.cursor ? '' : 'next' } }))
 expect(document.querySelector('#market-data-node option[value=worker]')).not.toBeNull()
 expect(wizard.deploymentPlan.data_node_id).toBe('worker')
})
