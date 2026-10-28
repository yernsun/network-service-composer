import { request, session } from '../shared/api/client'
import { idempotencyKey } from '../shared/browser/random'
import { refreshProjectData } from './api.js'
import type { DeletionPreview, LifecycleProject, ProjectDeletion, ProjectLifecycleAdapter } from './project-lifecycle'
const keys = idempotencyKey()
const etag = (revision: number) => ({ 'If-Match': `"rev-${revision}"` })
export const projectLifecycle: ProjectLifecycleAdapter = {
  writable: () => session.value.role === 'administrator',
  async project(id) { return (await request('get', '/projects/{id}', { params: { path: { id } } })).data as LifecycleProject },
  async save(id, input, revision) {
    return (await request('patch', '/projects/{id}', { params: { path: { id } }, body: input, headers: etag(revision) })).data as LifecycleProject
  },
  async preview(id, force) { return (await request('post', '/projects/{id}/deletion-previews', { params: { path: { id } }, body: { force } })).data as DeletionPreview },
  async remove(preview, confirmation) {
    const body = { preview_id: preview.id, force: preview.force, confirm_slug: confirmation }
    return (await request('delete', '/projects/{id}', { params: { path: { id: preview.project_id } }, body, headers: {
      ...etag(preview.project_revision), 'Idempotency-Key': keys.forRequest('delete', '/projects/{id}', { id: preview.project_id }, body),
    } })).data as ProjectDeletion
  },
  async task(id) { return (await request('get', '/project-deletions/{id}', { params: { path: { id } } })).data as ProjectDeletion },
  async resume(task, preview, confirmation) {
    return (await request('post', '/project-deletions/{id}/resume', { params: { path: { id: task.id } }, body: { preview_id: preview.id, force: preview.force, confirm_slug: confirmation }, headers: etag(task.revision) })).data as ProjectDeletion
  },
  refresh: refreshProjectData,
}
