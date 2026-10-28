import type { IPAccessEditorAdapter } from '../data/ip-access-types'
import { defineStore } from 'pinia'
import { markRaw, ref, shallowRef } from 'vue'
import { createMockService } from '@workspace-runtime'
import { clearRegions, configureLinks } from '../components/view'
import type { Database, ModalSpec, ToastMessage } from '../types/domain'

export const useWorkspaceStore = defineStore('workspace', () => {
  const revision = ref(0)
  const ipAccess = shallowRef<IPAccessEditorAdapter>()
  function configureIPAccess(adapter: IPAccessEditorAdapter) { ipAccess.value = markRaw(adapter) }
  const service = shallowRef<ReturnType<typeof createMockService>>()
  const toasts = ref<ToastMessage[]>([])
  let flush: (() => void) | undefined
  let toastId = 0
  const timers = new Set<ReturnType<typeof setTimeout>>()
  function changed() { revision.value++; flush?.() }
  function notify(message: string, error = true) {
    const id = ++toastId
    toasts.value.push({ id, message, error }); changed()
    const timer = setTimeout(() => { toasts.value = toasts.value.filter(t => t.id !== id); timers.delete(timer); changed() }, 4300)
    timers.add(timer)
  }
  function initialize(hooks: { navigate: (path: string) => Promise<unknown>; syncRoute: (path: string, replace: boolean) => void }, data?: unknown) {
    service.value = markRaw(createMockService({
      ...hooks,
      data,
      render: changed,
      toast: notify,
    }))
    configureLinks(attrs => service.value?.link(attrs) ?? null)
  }
  function content(name: string): unknown { void revision.value; return service.value?.view(name) }
  function state(): Database { void revision.value; return service.value!.state as Database }
  function modal(): ModalSpec | null { void revision.value; return service.value?.modal as ModalSpec | null }
  function connectRenderer(renderer: () => void) { flush = renderer }
  function dispose() { timers.forEach(clearTimeout); service.value?.dispose(); flush = undefined }
  function reset() {
    timers.forEach(clearTimeout); timers.clear(); service.value?.dispose()
    ipAccess.value = undefined; service.value = undefined; toasts.value = []; clearRegions(); changed()
  }
  return { ipAccess, configureIPAccess, revision, service, toasts, initialize, content, state, modal, connectRenderer, dispose, reset, notify }
})
