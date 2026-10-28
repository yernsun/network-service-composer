import { i18n } from '../shared/i18n'

/** Re-evaluate presentation only when the language changes. Original modal
 * assignments (for example diagnostics and continuation callbacks) remain valid. */
export function localizeModal<T extends object, A>(factory: (initial: A) => T, initial: A): T
export function localizeModal<T extends object>(factory: () => T): T
export function localizeModal<T extends object, A>(factory: (initial?: A) => T, initial?: A): T {
  const target = factory(initial)
  let current = target
  let language = i18n.global.locale.value
  const overrides = new Map<PropertyKey, unknown>()
  return new Proxy(target, {
    get(_target, property) {
      if (language !== i18n.global.locale.value) {
        language = i18n.global.locale.value
        current = factory(initial)
      }
      return overrides.has(property) ? overrides.get(property) : Reflect.get(current, property)
    },
    set(_target, property, value) {
      overrides.set(property, value)
      return Reflect.set(target, property, value)
    },
  })
}

type Control = HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement | HTMLButtonElement
export function retainModalControls() {
  const host = document.getElementById('overlay')
  const entries = [...(host?.querySelectorAll<Control>('input,textarea,select,button') || [])].map(element => ({
    element, value: element.value, disabled: element.disabled,
    checked: element instanceof HTMLInputElement ? element.checked : undefined,
    selection: element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement
      ? [element.selectionStart, element.selectionEnd, element.selectionDirection] as const : null,
    scrollTop: element.scrollTop, scrollLeft: element.scrollLeft,
  }))
  const focus = host?.contains(document.activeElement) ? document.activeElement as HTMLElement : null
  return () => {
    for (const entry of entries) {
      const element = entry.element.isConnected ? entry.element : document.getElementById(entry.element.id) as Control | null
      if (!element) continue
      if (!(element instanceof HTMLInputElement && element.type === 'file')) element.value = entry.value
      element.disabled = entry.disabled
      if (element instanceof HTMLInputElement && entry.checked !== undefined) element.checked = entry.checked
      if ((element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement) && entry.selection?.[0] != null && entry.selection[1] != null)
        element.setSelectionRange(entry.selection[0], entry.selection[1], entry.selection[2] || undefined)
      element.scrollTop = entry.scrollTop; element.scrollLeft = entry.scrollLeft
    }
    if (focus?.isConnected) focus.focus({ preventScroll: true })
  }
}
