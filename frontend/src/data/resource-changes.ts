import { ref, shallowRef } from 'vue'

export type ResourceChange = {
  reason: 'mutation' | 'operation'
  method?: string
  path?: string
  params?: Record<string, string>
  operationID?: string
}

// Notifications identify resources only; configuration and credentials stay out.
export function resourcesChanged(change: ResourceChange) {
  window.dispatchEvent(new CustomEvent<ResourceChange>('nsc:resources-changed', { detail: change }))
}

export const navigationLoading = ref(false)
export const refreshFailure = shallowRef<unknown>()
export const retryResourceRefresh = shallowRef<(() => Promise<unknown>)>()
