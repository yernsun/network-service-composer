import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ComposeWorkbench from '../../src/components/ComposeWorkbench.vue'
vi.mock('../../src/i18n', () => ({ translate: key => key }))
vi.mock('../../src/shared/api/client', () => ({ errorText: error => error.message }))
const source = 'services:\n  web:\n    image: nginx:1\n'
const wrappers = []
function fixture(overrides = {}) {
 const props = { editor: { composeWork: source, runtimeWork: '', view: 'source' }, application: { managed: true, name: 'demo' }, document: { source, rawSource: source, runtime: '', sourceRevision: 'deployed-1' }, save: vi.fn(), analyze: vi.fn().mockResolvedValue({ validation: { deployable: true, diagnostics: [] }, rendered_yaml: source }), merge: vi.fn(), review: vi.fn().mockResolvedValue({ id: 'review', preview: { resolved_yaml: source, before_resolved_yaml: source, service_changes: [] } }), deploy: vi.fn(), ...overrides }
 const wrapper = mount(ComposeWorkbench, { props }); wrappers.push(wrapper)
 return { wrapper, props }
}
afterEach(() => { wrappers.splice(0).forEach(w => w.unmount()); vi.useRealTimers() })
describe('Compose editor persistence and review boundaries', () => {
 it('opens a rendered source without dirtying or saving and compares the deployed revision', async () => {
  const { wrapper, props } = fixture()
  await flushPromises()
  expect(wrapper.emitted('edit')).toBeUndefined()
  expect(props.save).not.toHaveBeenCalled()
  expect(wrapper.find('textarea').element.value).toBe(source)
  await wrapper.find('header .primary').trigger('click'); await flushPromises()
  expect(props.review).toHaveBeenCalledWith(source, '', 'deployed-1', { storage: [], acknowledged: [] })
 })
 it('retains edits after a save conflict and keeps the original review baseline', async () => {
  const { wrapper, props } = fixture({ save: vi.fn().mockRejectedValue(new Error('conflict')) })
  await flushPromises()
  await wrapper.find('textarea').setValue(source.replace('nginx:1', 'nginx:2'))
  await wrapper.findAll('header button')[1].trigger('click'); await flushPromises()
  expect(wrapper.find('textarea').element.value).toContain('nginx:2')
  expect(wrapper.find('[role=alert]').text()).toBe('conflict')
  await wrapper.setProps({ document: { ...props.document, sourceRevision: 'deployed-2' } })
  expect(wrapper.text()).toContain('deployed-1')
 })
 it('updates a clean editor from fresh source data without submitting a write', async () => {
  const { wrapper, props } = fixture()
  await flushPromises()
  const latest = { ...props.document, source: source.replace('nginx:1', 'nginx:2'), runtime: 'MODE=new', sourceRevision: 'deployed-2' }
  await wrapper.setProps({ document: latest })
  expect(wrapper.find('textarea').element.value).toContain('nginx:2')
  expect(wrapper.text()).toContain('deployed-2')
  expect(props.save).not.toHaveBeenCalled()
  expect(props.deploy).not.toHaveBeenCalled()
 })
 it('invalidates a review immediately after editing and ignores an older asynchronous response', async () => {
  vi.useFakeTimers()
  let complete
  const review = vi.fn(() => new Promise(resolve => { complete = resolve }))
  const { wrapper, props } = fixture({ review })
  await flushPromises()
  await wrapper.find('header .primary').trigger('click')
  await wrapper.find('textarea').setValue(source.replace('nginx:1', 'nginx:2'))
  complete({ id: 'old', preview: { resolved_yaml: source } }); await flushPromises()
  expect(wrapper.find('.callout.blue').exists()).toBe(false)
  expect(wrapper.find('header .primary').element.disabled).toBe(true)
  expect(props.deploy).not.toHaveBeenCalled()
  await vi.advanceTimersByTimeAsync(400); await flushPromises()
  expect(props.analyze).toHaveBeenLastCalledWith(expect.stringContaining('nginx:2'), '', expect.any(Object))
 })
 it('cannot acknowledge away a required or unclassified storage diagnostic', async () => {
  for (const diagnostic of [{ severity: 'required', code: 'SWARM_LOCAL_VOLUME_WITHOUT_PLACEMENT' }, { severity: 'warning', code: 'STORAGE_CAPABILITY_UNKNOWN' }]) {
   const { wrapper } = fixture({ analyze: vi.fn().mockResolvedValue({ validation: { diagnostics: [diagnostic] }, rendered_yaml: source }) })
   await flushPromises()
   expect(wrapper.find('header .primary').element.disabled).toBe(true)
  }
 })
 it('preserves the selected service and input until save or discard succeeds', async () => {
  const save = vi.fn().mockRejectedValueOnce(new Error('conflict')).mockResolvedValue(undefined)
  const selectService = vi.fn()
  const { wrapper } = fixture({ save, selectService, services: [{ id: 'next', name: 'Next' }], editor: { composeWork: source, docsDirty: () => true } })
  await flushPromises()
  await wrapper.find('select').setValue('next')
  expect(selectService).not.toHaveBeenCalled()
  await wrapper.find('[role=alertdialog] .primary').trigger('click'); await flushPromises()
  expect(wrapper.find('[role=alertdialog]').exists()).toBe(true)
  expect(wrapper.find('textarea').element.value).toBe(source)
  expect(selectService).not.toHaveBeenCalled()
  await wrapper.find('[role=alertdialog] .primary').trigger('click'); await flushPromises()
  expect(selectService).toHaveBeenCalledWith('next')
  expect(wrapper.find('[role=alertdialog]').exists()).toBe(false)
 })
 it('shows line numbers, inserts two spaces for Tab, and previews variables before review', async () => {
  const preview = source.replace('nginx:1', 'nginx:2')
  const { wrapper, props } = fixture({ analyze: vi.fn().mockResolvedValue({ validation: { diagnostics: [] }, rendered_yaml: preview }) })
  await flushPromises()
  expect(wrapper.findAll('.compose-code-gutter span').map(line => line.text())).toEqual(['1', '2', '3', '4'])
  expect(wrapper.find('.compose-code-input').classes()).toContain('compose-source')
  await wrapper.findAll('.y-tabs button')[2].trigger('click')
  expect(wrapper.find('.compose-preview pre').text()).toContain('nginx:2')
  await wrapper.findAll('.y-tabs button')[0].trigger('click')
  const input = wrapper.find('#y-compose-code')
  input.element.setSelectionRange(0, 0)
  await input.trigger('keydown', { key: 'Tab' })
  expect(input.element.value.startsWith('  services:')).toBe(true)
  expect(wrapper.emitted('edit')).toBeTruthy()
  expect(props.save).not.toHaveBeenCalled()
  expect(props.review).not.toHaveBeenCalled()
 })
 it('reports syntax and schema errors immediately and clears stale preview', async () => {
  const { wrapper, props } = fixture()
  await flushPromises()
  await wrapper.findAll('.y-tabs button')[2].trigger('click')
  expect(wrapper.find('.compose-preview pre').text()).toContain('nginx:1')
  await wrapper.findAll('.y-tabs button')[0].trigger('click')
  await wrapper.find('#y-compose-code').setValue('services:\n  web: [')
  expect(wrapper.text()).toContain('YAML_SYNTAX_ERROR')
  expect(wrapper.find('.compose-code-gutter .invalid').exists()).toBe(true)
  expect(props.analyze).toHaveBeenCalledTimes(1)
  await wrapper.find('#y-compose-code').setValue('services:\n  web:\n    image: [wrong]\n')
  expect(wrapper.text()).toContain('COMPOSE_SCHEMA_ERROR')
  await wrapper.findAll('.y-tabs button')[2].trigger('click')
  expect(wrapper.find('.compose-preview pre').text()).toBe('composeEditor.previewUnavailable')
 })
 it('ignores an earlier server response after rapid edits', async () => {
  vi.useFakeTimers()
  let first
  const analyze = vi.fn().mockImplementationOnce(() => new Promise(resolve => { first = resolve })).mockResolvedValue({ validation: { diagnostics: [] }, rendered_yaml: 'new preview' })
  const { wrapper } = fixture({ analyze })
  await wrapper.find('#y-compose-code').setValue(source.replace('nginx:1', 'nginx:2'))
  await vi.advanceTimersByTimeAsync(400)
  first({ validation: { diagnostics: [] }, rendered_yaml: 'old preview' })
  await flushPromises()
  await wrapper.findAll('.y-tabs button')[2].trigger('click')
  expect(wrapper.find('.compose-preview pre').text()).toBe('new preview')
 })
 it('locates a server diagnostic on the current source line', async () => {
  const diagnostic = { id: 'policy', code: 'NSC_EXECUTION_POLICY', severity: 'error', pointer: '/services/web/image', line: 3, column: 5 }
  const { wrapper } = fixture({ analyze: vi.fn().mockResolvedValue({ validation: { diagnostics: [diagnostic] }, rendered_yaml: source }) })
  await flushPromises()
  expect(wrapper.findAll('.compose-code-gutter .invalid')).toHaveLength(1)
  await wrapper.find('.callout.amber .row-link').trigger('click')
  expect(wrapper.find('#y-compose-code').element.selectionStart).toBe(source.indexOf('    image') + 4)
 })
 it('locates a server policy diagnostic by YAML path when no line was reported', async () => {
  const diagnostic = { id: 'policy', code: 'NSC_EXECUTION_POLICY', severity: 'error', path: ['services', 'web', 'image'], pointer: '/services/web/image' }
  const { wrapper } = fixture({ analyze: vi.fn().mockResolvedValue({ validation: { diagnostics: [diagnostic] }, rendered_yaml: source }) })
  await flushPromises()
  expect(wrapper.findAll('.compose-code-gutter .invalid').map(line => line.text())).toEqual(['3'])
  await wrapper.find('.callout.amber .row-link').trigger('click')
  expect(wrapper.find('#y-compose-code').element.selectionStart).toBe(source.indexOf('nginx:1'))
 })
})
