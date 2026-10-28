import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises } from '@vue/test-utils'
import { usePreferencesStore } from '../../src/stores/preferences'
import { useWorkspaceStore } from '../../src/stores/workspace'
import { i18n, setLocale } from '../../src/shared/i18n'
import * as client from '../../src/shared/api/client'
let dispose: () => void
beforeEach(() => {
  localStorage.clear(); setLocale('en'); setActivePinia(createPinia())
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }))
})
afterEach(() => { dispose?.(); useWorkspaceStore().reset(); document.body.innerHTML = '' })
it('uses the accepted server locale, persists only the real theme and leaves form values literal', async () => {
  localStorage.setItem('nsc.preferences', JSON.stringify({ language: 'de', theme: 'dark' }))
  const preferences = usePreferencesStore(); dispose = preferences.initialize()
  expect(preferences.language).toBe('en')
  expect(document.documentElement.dataset.theme).toBe('dark')
  document.body.innerHTML = '<textarea>删除 {name} $HOME</textarea>'
  vi.spyOn(client, 'changeLocale').mockImplementation(async lang => { setLocale(lang) })
  for (const locale of ['ja', 'de', 'fr', 'ru', 'es', 'zh-CN', 'zh-TW', 'en'] as const) {
    preferences.language = locale
    await flushPromises()
    expect(preferences.language).toBe(locale)
    expect(document.documentElement.style.getPropertyValue('--workspace-unsaved-indicator')).toBe(JSON.stringify(i18n.global.t('archive.unsavedIndicator')))
    expect(document.querySelector('textarea')!.value).toBe('删除 {name} $HOME')
    expect(JSON.parse(localStorage.getItem('nsc.preferences')!)).toEqual({ theme: 'dark' })
  }
})
it('keeps the current language when the server rejects the preference and reports the failure', async () => {
  const preferences = usePreferencesStore(); dispose = preferences.initialize()
  vi.spyOn(client, 'changeLocale').mockRejectedValue(new client.ApiRequestError(0, 'network'))
  preferences.language = 'fr'
  await flushPromises()
  expect(preferences.language).toBe('en')
  expect(useWorkspaceStore().toasts.at(-1)?.message).toBe(i18n.global.t('console.network'))
  expect(localStorage.getItem('nsc.preferences')).toBeNull()
})
