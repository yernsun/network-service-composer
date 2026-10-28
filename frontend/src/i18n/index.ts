import { realMessageKeys } from './mode-keys'
import { i18n } from '../shared/i18n'
export { localizeModal } from './modal'
export function translate(key: string, params: Record<string, string | number> = {}): string {
  return i18n.global.t(realMessageKeys[key] || key, params)
}

/** A pool snapshot cannot establish an independently loaded domain version. */
export function observedVersion(value: unknown): string {
  return value == null ? translate('community.evidence_state.UNKNOWN') : `v${value}`
}

/** Resolve static interface labels when read, without touching workspace data. */
export function labels(values: Record<string, string | (() => string)>): Record<string, string> {
  return new Proxy(values, {
    get(target, property, receiver) {
      const value = Reflect.get(target, property, receiver)
      return typeof value === 'function' ? value() : value
    },
  }) as Record<string, string>
}

/** Preserve attribute syntax when a translated tooltip contains quotes. */
export function translatedAttribute(prefix: string, key: string, suffix: string): string {
  const value = translate(key).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]!)
  return prefix + value + suffix
}
