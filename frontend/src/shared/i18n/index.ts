import { createI18n } from 'vue-i18n'
import localeDefinitions from '../../../../internal/i18n/locales.json'
import ruMessages from './generated/ru.json'
import esMessages from './generated/es.json'
import enMessages from './generated/en.json'
import jaMessages from './generated/ja.json'
import deMessages from './generated/de.json'
import frMessages from './generated/fr.json'
import zhCNMessages from './generated/zh-CN.json'
import zhTWMessages from './generated/zh-TW.json'
export type AppLocale = 'en' | 'ja' | 'de' | 'zh-CN' | 'zh-TW' | 'fr' | 'ru' | 'es'
export const supportedLocales = localeDefinitions.map(({ code }) => code as AppLocale)
export const languages = localeDefinitions
export const isLocale = (locale: string): locale is AppLocale =>
  supportedLocales.includes(locale as AppLocale)
const htmlLocale = document.documentElement.lang
export const initialLocale: AppLocale = isLocale(htmlLocale) ? htmlLocale : 'en'
export const i18n = createI18n({
  legacy: false,
  locale: initialLocale,
  fallbackLocale: 'en',
  messages: {
    en: enMessages,
    ja: jaMessages,
    de: deMessages,
    fr: frMessages,
    ru: ruMessages,
    es: esMessages,
    'zh-CN': zhCNMessages,
    'zh-TW': zhTWMessages,
  },
})
export function setLocale(locale: string) {
  if (!isLocale(locale)) return
  i18n.global.locale.value = locale
  document.documentElement.lang = locale
}
/** Format user-controlled resource data without translating string contents. */
export function literalValue(value: unknown): string {
  if (value == null || value === '') return '—'
  if (typeof value === 'boolean') return i18n.global.t(value ? 'common.yes' : 'common.no')
  if (Array.isArray(value)) return value.map(literalValue).join(', ')
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}
export function textValue(value: unknown): string {
  if (value == null || value === '') return '—'
  if (typeof value === 'boolean') return i18n.global.t(value ? 'common.yes' : 'common.no')
  if (Array.isArray(value)) return value.map(textValue).join(', ')
  if (typeof value === 'object') return JSON.stringify(value)
  const key = `status.${value}`
  return i18n.global.te(key) ? i18n.global.t(key) : String(value)
}
export function formatDate(value: unknown) {
  if (!value) return '—'
  const date = new Date(String(value))
  return Number.isNaN(date.getTime())
    ? textValue(value)
    : new Intl.DateTimeFormat(i18n.global.locale.value, {
        dateStyle: 'medium',
        timeStyle: 'short',
      }).format(date)
}
