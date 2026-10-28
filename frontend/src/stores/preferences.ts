import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import { i18n, isLocale } from '../shared/i18n'
import { changeLocale, errorText } from '../shared/api/client'
import { useWorkspaceStore } from './workspace'
export type Theme = 'light' | 'dark' | 'system'
const STORAGE_KEY = 'nsc.preferences'
export const usePreferencesStore = defineStore('preferences', () => {
  let pendingLocale = Promise.resolve()
  const language = computed({ get: () => i18n.global.locale.value, set: value => {
    if (!isLocale(value)) return
    pendingLocale = pendingLocale.then(() => changeLocale(value)).catch(error => { useWorkspaceStore().notify(errorText(error)) })
  } })
  const theme = ref<Theme>('system')
  const systemDark = ref(false)
  const resolvedTheme = computed(() => theme.value === 'system' ? (systemDark.value ? 'dark' : 'light') : theme.value)
  const storageUnavailable = ref(false)
  function read(raw: string | null) {
    try {
      const value = JSON.parse(raw || '{}') as { language?: string; theme?: Theme }
      theme.value = value.theme && ['light', 'dark', 'system'].includes(value.theme) ? value.theme : 'system'
    } catch { storageUnavailable.value = true }
  }
  function initialize() {
    try { read(localStorage.getItem(STORAGE_KEY)) } catch { storageUnavailable.value = true }
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    systemDark.value = media.matches
    const systemChanged = (event: MediaQueryListEvent) => { systemDark.value = event.matches }
    media.addEventListener('change', systemChanged)
    const stopAppearance = watch([resolvedTheme, language], () => {
      document.documentElement.dataset.theme = resolvedTheme.value
      document.documentElement.lang = language.value
      document.documentElement.style.colorScheme = resolvedTheme.value
      document.documentElement.style.setProperty('--workspace-unsaved-indicator', JSON.stringify(i18n.global.t('archive.unsavedIndicator')))
      document.querySelector('meta[name="color-scheme"]')?.setAttribute('content', resolvedTheme.value)
    }, { immediate: true, flush: 'sync' })
    const stopStorage = watch([theme, language], () => {
      try { localStorage.setItem(STORAGE_KEY, JSON.stringify({ theme: theme.value })) }
      catch { storageUnavailable.value = true }
    }, { flush: 'sync' })
    const storageChanged = (event: StorageEvent) => { if (event.key === STORAGE_KEY && event.storageArea === localStorage) read(event.newValue) }
    window.addEventListener('storage', storageChanged)
    return () => { media.removeEventListener('change', systemChanged); window.removeEventListener('storage', storageChanged); stopAppearance(); stopStorage() }
  }
  return { language, theme, resolvedTheme, storageUnavailable, initialize }
})
