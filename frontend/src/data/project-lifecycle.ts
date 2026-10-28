import type { components } from '../shared/api/schema'
export type LifecycleProject = components['schemas']['LogicalProject']
export type ProjectSettings = components['schemas']['ProjectSettings']
export type DeletionPreview = components['schemas']['ProjectDeletionPreview']
export type ProjectDeletion = components['schemas']['ProjectDeletion']
export interface ProjectLifecycleAdapter {
  writable(): boolean
  project(id: string): Promise<LifecycleProject>
  save(id: string, input: ProjectSettings, revision: number): Promise<LifecycleProject>
  preview(id: string, force: boolean): Promise<DeletionPreview>
  remove(preview: DeletionPreview, confirmation: string): Promise<ProjectDeletion>
  task(id: string): Promise<ProjectDeletion>
  resume(task: ProjectDeletion, preview: DeletionPreview, confirmation: string): Promise<ProjectDeletion>
  refresh(): Promise<void>
}
