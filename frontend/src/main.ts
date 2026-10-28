import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import { connectRouter, router } from './router'
import { useWorkspaceStore } from './stores/workspace'
import { usePreferencesStore } from './stores/preferences'
import { i18n } from './shared/i18n'
import { errorText, restoreSession, restored, session } from './shared/api/client'
import { initializeData } from '@workspace-data'
import { bootstrapError } from './auth/bootstrap'
import { queryClient } from './data/inventory-cache'
import { navigationLoading } from './data/resource-changes'
import './styles/original.css'
import './styles/preferences.css'
import './auth/auth.css'
import './styles/forms.css'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia).use(i18n).use(VueQueryPlugin, { queryClient })
const disposePreferences = usePreferencesStore(pinia).initialize()
const store = useWorkspaceStore(pinia)
let data: { dispose?(): void; prepareRoute?(path: string): Promise<boolean>; refresh?(): Promise<unknown> } | undefined
let bootstrapPath: string | undefined
let navigationSequence = 0
router.beforeEach(async to => {
  if (!restored.value) await restoreSession()
  if (!to.meta.public && !session.value.authenticated) return { path: '/login', query: { next: to.fullPath } }
  if (to.meta.public) return true
  if (!store.service) {
    const initialized = await initializeData(to.fullPath)
    data = initialized
    bootstrapPath = to.fullPath
    store.initialize(hooks, data)
    if (typeof initialized.ipAccessEditorState === 'function') store.configureIPAccess(initialized)
  }
  return true
})
const originalHooks = connectRouter(store)
const hooks = { ...originalHooks, navigate(path: string) {
  if (router.resolve(path).fullPath === router.currentRoute.value.fullPath) return refreshCurrentRoute()
  return originalHooks.navigate(path)
} }
async function refreshCurrentRoute() {
  const sequence = ++navigationSequence
  navigationLoading.value = true
  try { if (data?.refresh) await data.refresh(); else store.service?.refresh() }
  finally { if (sequence === navigationSequence) navigationLoading.value = false }
}
function refreshSameLink(event: MouseEvent) {
  if (event.button || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey || event.defaultPrevented) return
  const link = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null
  if (!link || link.hasAttribute('download') || link.target && link.target !== '_self') return
  const url = new URL(link.href)
  if (url.origin !== window.location.origin || url.pathname + url.search + url.hash !== router.currentRoute.value.fullPath) return
  event.preventDefault(); event.stopImmediatePropagation()
  void refreshCurrentRoute()
}
document.addEventListener('click', refreshSameLink, true)
function localRouteUpdate(target: URL, previous: URL, path: string) {
  if (target.pathname !== previous.pathname || store.service?.route() !== path) return false
  for (const field of ['q', 'filter', 'layout', 'health', 'runtime', 'page', 'tab', 'view', 'editor', 'file', 'perspective']) {
    target.searchParams.delete(field); previous.searchParams.delete(field)
  }
  // The editor includes its default layer when synchronizing its first edit.
  const layer = /\/projects\/[^/]+\/variables$/.test(target.pathname) ? 'project' : 'environment'
  for (const url of [target, previous]) {
    if (target.pathname.endsWith('/variables') && url.searchParams.get('layer') === layer) url.searchParams.delete('layer')
    if (/\/projects\/[^/]+\/environments\/[^/]+\/services$/.test(target.pathname)) {
      const scope = target.pathname.startsWith('/projects/all/') ? 'all' : target.pathname.includes('/environments/all/') ? 'project' : 'environment'
      if (url.searchParams.get('scope') === scope) url.searchParams.delete('scope')
    }
  }
  target.searchParams.sort(); previous.searchParams.sort()
  return target.href === previous.href
}
// Resolve details after the original dirty-editor guard admits navigation.
// A failed or superseded read keeps the current route and its form intact.
router.beforeResolve(async (to, from) => {
  if (to.meta.public) return true
  if (bootstrapPath === to.fullPath) { bootstrapPath = undefined; return true }
  const target = new URL(to.fullPath, window.location.origin), previous = new URL(from.fullPath, window.location.origin)
  target.searchParams.delete('perspective'); previous.searchParams.delete('perspective')
  if (target.href === previous.href) return true
  if (localRouteUpdate(target, previous, to.fullPath)) return true
  const sequence = ++navigationSequence
  navigationLoading.value = true
  try { return await data?.prepareRoute?.(to.fullPath) }
  catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) store.notify(errorText(error))
    return false
  }
  finally { if (sequence === navigationSequence) navigationLoading.value = false }
})
const expired = () => {
  navigationSequence++; navigationLoading.value = false; bootstrapPath = undefined
  data?.dispose?.(); data = undefined; store.reset()
  const path = router.currentRoute.value.fullPath
  void router.replace({ path: '/login', query: { next: path.startsWith('/logout') ? '/projects' : path } })
}
window.addEventListener('nsc:unauthorized', expired)
app.use(router)
// Do not hold the entry module with top-level await: production lazy auth
// chunks import the shared client from this chunk and must be able to evaluate.
void router.isReady().then(() => {
  app.mount('#app')
  if (import.meta.env.DEV) Object.assign(window, { NSCv13: {
    version: 'archive', ready: true,
    state: () => structuredClone(store.state()),
    view: () => ({ ...store.service?.workload, ...store.service?.workspace }),
    route: () => router.currentRoute.value.fullPath,
  } })
}).catch(error => { bootstrapError.value = error; app.mount('#app') })
if (import.meta.hot) import.meta.hot.dispose(() => {
  document.removeEventListener('click', refreshSameLink, true)
  window.removeEventListener('nsc:unauthorized', expired)
  data?.dispose?.(); disposePreferences(); app.unmount()
})
