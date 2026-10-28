import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ProjectSettings from '../../src/components/ProjectSettings.vue'
import { setLocale } from '../../src/shared/i18n'
import { ApiRequestError } from '../../src/shared/api/client'
const { api, notify, navigate, db } = vi.hoisted(() => ({
  api: { writable: vi.fn(), project: vi.fn(), save: vi.fn(), preview: vi.fn(), remove: vi.fn(), resume: vi.fn(), task: vi.fn(), refresh: vi.fn() },
  notify: vi.fn(), navigate: vi.fn(), db: { runtimes: [{ id: 'rt', name: 'Docker', canWrite: true }], environments: [{ id: 'env', name: 'Production' }] },
}))
vi.mock('@project-lifecycle', () => ({ projectLifecycle: api }))
vi.mock('vue-router', () => ({ useRouter: () => ({ replace: navigate }) }))
vi.mock('../../src/stores/workspace', () => ({ useWorkspaceStore: () => ({ state: () => db, notify }) }))
const project = { id: 'project-id', name: 'Literal <name>', slug: 'literal', description: 'kept', default_runtime_id: 'rt', revision: 4 }
const preview = { id: 'preview-id', project_id: project.id, project_slug: project.slug, project_revision: 4, force: false, resources: [{ id: 'service-id', name: '<b>api & db</b>', kind: 'service', environment_id: 'env' }], pools: [], blockers: [] }
let wrapper: VueWrapper
beforeEach(() => {
  vi.clearAllMocks(); setLocale('en');api.writable.mockReturnValue(true)
  api.project.mockResolvedValue({ ...project });api.preview.mockResolvedValue(structuredClone(preview));api.refresh.mockResolvedValue(undefined)
  document.body.innerHTML = '<button data-action="p-project-settings" data-id="project-id">Settings</button>'
  wrapper = mount(ProjectSettings, { attachTo: document.body })
})
afterEach(() => { wrapper.unmount();document.body.innerHTML = '';vi.useRealTimers() })
async function open() { document.querySelector<HTMLButtonElement>('[data-action=p-project-settings]')!.click();await flushPromises() }
async function review() { await wrapper.findAll('button').find(b => b.text() === 'Preview deletion')!.trigger('click');await flushPromises() }
it('saves literal metadata with ETag and retains failed edits and leave confirmation', async () => {
  await open();await wrapper.get('#project-settings-name').setValue('edited <literal>')
  api.save.mockRejectedValue(new ApiRequestError(412, 'conflict'))
  await wrapper.get('form').trigger('submit');await flushPromises()
  expect(api.save).toHaveBeenCalledWith(project.id, expect.objectContaining({ name: 'edited <literal>' }), 4)
  expect(wrapper.get('#project-settings-name').element).toHaveProperty('value', 'edited <literal>')
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));await flushPromises()
  expect(wrapper.text()).toContain('Discard')
  await wrapper.findAll('button').find(b => b.text() === 'Keep editing')!.trigger('click')
  api.save.mockResolvedValue({ ...project, name: 'edited <literal>', revision: 5 })
  await wrapper.get('form').trigger('submit');await flushPromises();expect(api.refresh).toHaveBeenCalledOnce()
})
it('previews names safely, requires exact confirmation, and cannot delete unsaved settings', async () => {
  await open();expect(wrapper.get('input[type=checkbox]').element).toHaveProperty('checked', false)
  await review();expect(api.preview).toHaveBeenCalledWith(project.id, false)
  expect(wrapper.text()).toContain('<b>api & db</b>');expect(wrapper.find('b').exists()).toBe(false)
  await wrapper.get('#project-delete-confirm').setValue(project.slug)
  await wrapper.get('#project-settings-name').setValue('unsaved')
  expect(wrapper.get('.modal-foot .danger').attributes('disabled')).toBeDefined()
  expect(api.remove).not.toHaveBeenCalled()
})
it('shows actual task progress, restores a saved task after reopen and only leaves on completion', async () => {
  const task = { id: 'task', project_id: project.id, status: 'RUNNING', preview: { ...preview, applications: [{ id: 'app', name: 'app-name', status: 'PENDING', observation: { objects: [] } }] } }
  api.project.mockResolvedValue({ ...project, deletion_id: 'task' });api.task.mockResolvedValue(task)
  await open();expect(api.task).toHaveBeenCalledWith('task');expect(wrapper.text()).toContain('app-name')
  expect(navigate).not.toHaveBeenCalled()
  api.task.mockResolvedValue({ ...task, status: 'SUCCEEDED' })
  await wrapper.findAll('button').find(b => b.text() === 'Refresh progress')!.trigger('click');await flushPromises()
  expect(navigate).toHaveBeenCalledWith('/projects');expect(api.refresh).toHaveBeenCalled();expect(wrapper.find('[role=dialog]').exists()).toBe(false)
})
it('viewer settings are readable but every write and destructive control is disabled', async () => {
  api.writable.mockReturnValue(false);await open()
  expect(wrapper.get('#project-settings-name').attributes('disabled')).toBeDefined()
  expect(wrapper.findAll('button.danger').every(b => b.attributes('disabled') !== undefined)).toBe(true)
  expect(api.preview).not.toHaveBeenCalled()
})
it('failed project loading uses the existing error toast', async () => {
  api.project.mockRejectedValue(new ApiRequestError(404, 'resource.not_found'));await open()
  expect(notify).toHaveBeenCalledWith(expect.any(String), true)
})

it('system settings stay protected and display the registered read-only runtime', async () => {
  api.project.mockResolvedValue({ ...project, name: 'Gateway', system: true })
  db.runtimes[0]!.canWrite = false
  try {
    await open()
    expect(wrapper.get('#project-settings-runtime').element).toHaveProperty('value', 'rt')
    expect(wrapper.get('#project-settings-runtime').text()).toContain('Docker')
    expect(wrapper.get('#project-settings-name').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button.danger').every(b => b.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.text()).toContain('protected')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.save).not.toHaveBeenCalled(); expect(api.preview).not.toHaveBeenCalled()
  } finally { db.runtimes[0]!.canWrite = true }
})
