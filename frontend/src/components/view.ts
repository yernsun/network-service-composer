import { cloneVNode, Fragment, h, isVNode, type VNode, type VNodeChild, type VNodeArrayChildren } from 'vue'
import { decodeHTML } from 'entities'
import { translate } from '../i18n'

// Templates are parsed at migration time. Runtime rendering uses Vue VNodes only.
export type Blueprint = string | { text: string } | { i18n: string; variables: number[] } | { tag: string; attrs: [string, string][]; children: Blueprint[] }
export class View {
  constructor(public nodes: VNodeChild[]) {}
  replace(search: string | RegExp, replacement: string): View {
    function walk(items: VNodeChild[]): VNodeChild[] {
      const result: VNodeChild[] = []; let text = ''
      const flush = () => { if (text) { result.push(text.replace(search, replacement)); text = '' } }
      for (const item of items) {
        if (typeof item === 'string' || typeof item === 'number') { text += String(item); continue }
        flush()
        if (isVNode(item)) {
          const copy = cloneVNode(item)
          if (Array.isArray(copy.children)) copy.children = walk(copy.children as VNodeChild[]) as VNodeArrayChildren
          else if (typeof copy.children === 'string') copy.children = copy.children.replace(search, replacement)
          result.push(copy)
        } else result.push(item)
      }
      flush(); return result
    }
    return new View(walk(this.nodes))
  }
}
export const isView = (v: unknown): v is View => v instanceof View
export function nodes(value: unknown): VNodeChild[] {
  if (isView(value)) return value.nodes
  if (Array.isArray(value)) return value.flatMap(nodes)
  if (isVNode(value)) return [value]
  if (value == null || value === false) return []
  return [typeof value === 'string' ? decodeHTML(value) : String(value)]
}
export function add(a: unknown, b: unknown): View | string | number {
  if (isView(a) || isView(b)) return new View([...nodes(a), ...nodes(b)])
  // Keep the archive's native JavaScript + coercion for generated expressions.
  // These compile-time assertions do not coerce objects, null or booleans.
  return (a as number) + (b as number)
}
export function join(list: unknown[], separator: string | View = ','): string | View {
  if (!list.some(isView) && !isView(separator)) return list.join(String(separator))
  return new View(list.flatMap((v, i) => [...(i ? nodes(separator) : []), ...nodes(v)]))
}
export function interpolateView(strings: string[], values: unknown[]): string | View {
  if (!values.some(isView)) return strings.map((s, i) => s + (i < values.length ? String(values[i]) : '')).join('')
  return new View(strings.flatMap((s, i) => [...nodes(s), ...(i < values.length ? nodes(values[i]) : [])]))
}
const marker = /NSCEXPR(\d+)END/g
function parts(s: string, values: unknown[]): unknown[] {
  const out: unknown[] = []; let last = 0
  for (const m of s.matchAll(marker)) {
    if (m.index! > last) out.push(s.slice(last, m.index))
    out.push(values[Number(m[1])]); last = m.index! + m[0].length
  }
  if (last < s.length) out.push(s.slice(last))
  return out
}
function stringValue(s: string, values: unknown[]): string {
  if (s.startsWith('@i18n:')) s = translate(s.slice(6))
  return parts(s, values).map(v => v == null ? '' : String(v)).join('')
}
function extraAttrs(value: unknown): Record<string, unknown> {
  const result: Record<string, unknown> = {}
  const pattern = /([^\s=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s]+)))?/g
  for (const m of String(value ?? '').matchAll(pattern)) {
    const key = m[1]!
    if (!/^on/i.test(key)) result[key] = decodeHTML(m[2] ?? m[3] ?? m[4] ?? '')
  }
  return result
}
function primaryCardDestination(items: VNodeChild[]): VNode | undefined {
  for (const item of items) {
    if (!isVNode(item)) continue
    if (item.props?.['data-action'] && Object.prototype.hasOwnProperty.call(item.props, 'data-card-primary')) return item
    if (Array.isArray(item.children)) {
      const nested = primaryCardDestination(item.children as VNodeChild[])
      if (nested) return nested
    }
  }
}
function makeCardActionable(attrs: Record<string, unknown>, children: VNodeChild[]) {
  if (!String(attrs.class || '').split(/\s+/).includes('actionable-card') || attrs['data-action']) return
  const primary = primaryCardDestination(children)
  if (!primary?.props?.['data-action']) return
  for (const [key, value] of Object.entries(primary.props)) {
    if (key.startsWith('data-') && key !== 'data-card-primary') attrs[key] = value
  }
  attrs.role = attrs.role || 'link'
  attrs.tabindex = attrs.tabindex ?? 0
  attrs.onKeydown = (event: KeyboardEvent) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    ;(event.currentTarget as HTMLElement).click()
  }
}
let resolveLink: (attrs: Record<string, unknown>) => string | null = () => null
export function configureLinks(resolver: typeof resolveLink) { resolveLink = resolver }
const overrides = new Map<string, unknown>()
export function setRegion(id: string, value: unknown) { overrides.set(id, value) }
export function clearRegions() { overrides.clear() }
export function resolveRegions(items: VNodeChild[]): VNodeChild[] {
  return items.map(item => {
    if (!isVNode(item)) return item
    const copy = cloneVNode(item)
    if (copy.props?.id && overrides.has(copy.props.id)) copy.children = nodes(overrides.get(copy.props.id)) as VNodeArrayChildren
    else if (Array.isArray(copy.children)) copy.children = resolveRegions(copy.children as VNodeChild[]) as VNodeArrayChildren
    return copy
  })
}
function create(spec: Blueprint, values: unknown[]): VNodeChild[] {
  if (typeof spec === 'string') return nodes(parts(spec, values))
  if ('i18n' in spec) return nodes(parts(translate(spec.i18n, Object.fromEntries(spec.variables.map(n => [`v${n}`, `NSCEXPR${n}END`]))), values))
  if ('text' in spec) return nodes(parts(spec.text, values))
  const attrs: Record<string, unknown> = {}
  for (const [key, raw] of spec.attrs) {
    const spread = /^nscexpr(\d+)end$/i.exec(key)
    if (spread) Object.assign(attrs, extraAttrs(values[Number(spread[1])]))
    else attrs[key] = decodeHTML(stringValue(raw, values))
  }
  let tag = spec.tag
  if (tag === 'button' && attrs['data-action'] && !('disabled' in attrs)) {
    const href = resolveLink(attrs)
    if (href) { tag = 'a'; attrs.href = href }
  }
  let children = overrides.has(String(attrs.id)) ? nodes(overrides.get(String(attrs.id))) : spec.children.flatMap(c => create(c, values))
  if (tag === 'textarea') { attrs.value = children.join(''); children = [] }
  if (tag === 'select') {
    const options = children.flatMap(c => isVNode(c) && c.type === Fragment ? c.children as VNode[] : [c])
    const selected = options.find(c => isVNode(c) && c.props && 'selected' in c.props) as VNode | undefined
    if (selected) attrs.value = selected.props?.value
  }
  if (tag === 'option' && 'selected' in attrs) attrs.selected = true
  makeCardActionable(attrs, children)
  return [h(tag, attrs, children)]
}
export function view(template: Blueprint[], values: unknown[] = []): View {
  return new View(template.flatMap(spec => create(spec, values)))
}
export function insertView(source: View, selector: string, value: unknown, mode: 'before' | 'prepend' | 'after' | 'replace' = 'prepend'): View {
  let found = false
  function walk(items: VNodeChild[]): VNodeChild[] {
    return items.flatMap(item => {
      if (!isVNode(item)) return [item]
      const cls = item.props?.class || ''
      const action = /^\[data-action=(.+)\]$/.exec(selector)
      const match = action ? item.props?.['data-action'] === action[1] : selector.startsWith('.') ? cls.split(' ').includes(selector.slice(1)) : item.type === selector
      if (match && !found) {
        found = true
        if (mode === 'replace') return nodes(value)
        if (mode === 'before') return [...nodes(value), item]
        if (mode === 'after') return [item, ...nodes(value)]
        item.children = [...nodes(value), ...(Array.isArray(item.children) ? item.children : [])] as VNodeArrayChildren
      } else if (Array.isArray(item.children)) item.children = walk(item.children as VNodeChild[]) as VNodeArrayChildren
      return [item]
    })
  }
  return new View(walk(source.nodes))
}
export function highlightLine(line: string): View {
  if (line.trim().startsWith('#')) return new View([line])
  const directive = /^(\s*)([a-z_]+)(?=\s)/.exec(line)
  const result: VNodeChild[] = []
  let tail = line
  if (directive) { result.push(directive[1], h('span', { class: 'code-directive' }, directive[2])); tail = line.slice(directive[0].length) }
  let last = 0
  for (const m of tail.matchAll(/\$[a-z_]+/g)) { result.push(tail.slice(last, m.index), h('span', { class: 'code-var' }, m[0])); last = m.index! + m[0].length }
  result.push(tail.slice(last))
  return new View(result)
}
