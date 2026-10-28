import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import WorkspacePage from '../views/WorkspacePage.vue'
import type { useWorkspaceStore } from '../stores/workspace'

const paths = [
  '/projects', '/projects/:project', '/projects/:project/environments', '/projects/:project/variables',
  '/projects/:project/environments/:environment/:section',
  '/projects/:project/environments/:environment/applications/:application/compose',
  '/projects/:project/environments/:environment/:section/:resource',
  '/services/:service', '/market', '/operations/:operation',
  '/infrastructure/runtimes', '/infrastructure/runtimes/:runtime',
  '/infrastructure/runtimes/:runtime/nodes/:node', '/infrastructure/runtimes/:runtime/instances/:instance',
  '/infrastructure/events', '/:pathMatch(.*)*',
]
const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/projects/portal' },
  ...paths.map((path, index) => ({ path, name: `workspace-${index}`, component: WorkspacePage })),
]
export const router = createRouter({ history: createWebHistory(), routes, scrollBehavior(to, from, saved) {
  if (saved) return saved
  if (to.path === from.path) return false
  return { top: 0 }
} })

export function connectRouter(store: ReturnType<typeof useWorkspaceStore>) {
  let syncTarget: string | null = null
  router.beforeEach((to, from) => {
    const service = store.service
    if (perspectiveOnly(to.fullPath, from.fullPath)) return true
    if (!service || syncTarget === to.fullPath || service.sameEditor(from.fullPath, to.fullPath) || !service.dirty) return true
    return new Promise<boolean>(resolve => service.guard(() => resolve(true), () => resolve(false)))
  })
  router.afterEach((to, _from, failure) => {
    if (!failure) {
      if (perspectiveOnly(to.fullPath, _from.fullPath)) store.service?.acceptPerspective(to.fullPath)
      else store.service?.acceptRoute(to.fullPath)
    }
  })
  return {
    navigate(path: string) { return router.push(path) },
    syncRoute(path: string, replace: boolean) {
      if (path === router.currentRoute.value.fullPath || path === syncTarget) return
      syncTarget = path
      void router[replace ? 'replace' : 'push'](path).finally(() => { if (syncTarget === path) syncTarget = null })
    },
  }
}

function perspectiveOnly(to: string, from: string) {
  if (to === from) return false
  const a = new URL(to, 'https://nsc.invalid'), b = new URL(from, 'https://nsc.invalid')
  a.searchParams.delete('perspective'); b.searchParams.delete('perspective')
  a.searchParams.sort(); b.searchParams.sort()
  return a.href === b.href
}
