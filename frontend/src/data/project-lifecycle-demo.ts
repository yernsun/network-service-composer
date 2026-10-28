import { useWorkspaceStore } from '../stores/workspace'
import { randomID } from '../shared/browser/random'
import { ApiRequestError } from '../shared/api/client'
import type { DeletionPreview, LifecycleProject, ProjectDeletion, ProjectLifecycleAdapter } from './project-lifecycle'
const state = () => useWorkspaceStore().state()
function persist() { localStorage.setItem('network-service-composer-vue-v1.3', JSON.stringify(state())); useWorkspaceStore().service?.refresh() }
function project(id: string): LifecycleProject {
  const p = state().projects.find(p => p.id === id)
  if (!p) throw new ApiRequestError(404, 'resource.not_found')
  return { id: p.id, name: p.name, slug: p.key, description: p.description, default_runtime_id: p.default_runtime_id || '', revision: p.revision || 1, ...(p.deletion_id ? { deletion_id: p.deletion_id } : {}) }
}
const previews = new Map<string, DeletionPreview>()
export const projectLifecycle: ProjectLifecycleAdapter = {
  writable: () => true,
  async project(id) { return project(id) },
  async save(id, input, revision) {
    const current = project(id); if (current.revision !== revision) throw new ApiRequestError(412, 'conflict')
    if (!input.name.trim()) throw new ApiRequestError(422, 'validation.project_name')
    const p = state().projects.find(p => p.id === id)!
    Object.assign(p, input, { revision: revision + 1 }); persist(); return project(id)
  },
  async preview(id, force) {
    const p = project(id), db = state()
    const resources: DeletionPreview['resources'] = []
    const blockers: DeletionPreview['blockers'] = []
    const envs = db.environments.filter(e => e.project === id)
    const apps = db.apps.filter(a => a.project === id)
    const services = db.services.filter(s => s.project === id)
    const serviceIDs = new Set(services.map(s => s.id))
    for (const e of envs) resources.push({ id: e.id, name: e.name, kind: 'environment', revision: 1, environment_id: e.id })
    for (const a of apps) resources.push({ id: a.id, name: a.name, kind: 'application', revision: 1, environment_id: a.env })
    for (const s of services) {
      resources.push({ id: s.id, name: s.name, kind: 'service', revision: 1, environment_id: s.env })
      if (!s.managed) blockers.push({ code: 'project.deletion.ownership', resource_id: s.id })
      if (!force && db.instances.some(i => i.service === s.id && i.current && ['Running', 'Starting', 'Pending'].includes(i.state))) blockers.push({ code: 'project.deletion.running', resource_id: s.id })
    }
    for (const d of db.domains.filter(d => d.project === id || d.active.routes.some(r => serviceIDs.has(r.service)))) {
      resources.push({ id: d.id, name: d.name, kind: 'domain', revision: d.rev })
      if (d.project !== id || d.active.routes.some(r => r.service && !serviceIDs.has(r.service))) blockers.push({ code: 'project.deletion.shared_reference', resource_id: d.id })
    }
    const v: DeletionPreview = { id: 'preview-' + randomID(), revision: 1, project_id: id, project_name: p.name, project_slug: p.slug, project_revision: p.revision, force,
      resources, applications: apps.map(a => ({ id: a.id, name: a.name, environment_id: a.env, runtime_id: a.runtime, status: 'PENDING', observation: { objects: [], preserved: [], observed_at: new Date().toISOString() } })),
      pools: [], blockers, preserved: ['volumes', 'bind_mounts', 'images', 'networks'], fingerprint: JSON.stringify(resources), expires_at: new Date(Date.now() + 300000).toISOString(),
    }
    previews.set(v.id, v); return v
  },
  async remove(v, confirmation) {
    if (confirmation !== project(v.project_id).slug) throw new ApiRequestError(422, 'project.deletion.confirm_slug')
    if (!previews.has(v.id) || Date.parse(v.expires_at) <= Date.now() || v.project_revision !== project(v.project_id).revision) throw new ApiRequestError(412, 'conflict')
    const fresh = await this.preview(v.project_id, v.force)
    if (fresh.fingerprint !== v.fingerprint) throw new ApiRequestError(412, 'project.deletion.scope_changed')
    if (fresh.blockers.length) throw new ApiRequestError(422, fresh.blockers[0]!.code)
    const db = state(), ids = new Set(v.resources.map(r => r.id)), now = new Date().toISOString()
    const task: ProjectDeletion = { id: 'deletion-' + randomID(), revision: 1, project_id: v.project_id, preview: v, status: 'SUCCEEDED', phase: 'COMPLETE', actor: 'demo', attempts: [{ number: 1, started_at: now, finished_at: now, status: 'SUCCEEDED' }], finished_at: now }
    db.projectDeletionTasks ||= {}; db.projectDeletionTasks[task.id] = task
    db.services = db.services.filter(s => !ids.has(s.id)); db.instances = db.instances.filter(i => !ids.has(i.service)); db.apps = db.apps.filter(a => !ids.has(a.id)); db.domains = db.domains.filter(d => !ids.has(d.id)); db.environments = db.environments.filter(e => !ids.has(e.id)); db.projects = db.projects.filter(p => p.id !== v.project_id)
    persist(); return task
  },
  async task(id) { const task = state().projectDeletionTasks?.[id]; if (!task) throw new ApiRequestError(404, 'resource.not_found'); return task },
  async resume(task) { return this.task(task.id) },
  async refresh() { useWorkspaceStore().service?.refresh() },
}
