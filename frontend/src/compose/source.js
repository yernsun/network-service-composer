import { isCollection, parseDocument, visit } from 'yaml'
import { parseCompose } from './validator'

// Formatting is a view of the source, never a mutation of its persisted bytes.
export function readableCompose(source = '') {
  if (!source.trimStart().startsWith('{')) return source
  try { JSON.parse(source) } catch { return source }
  const doc = parseDocument(source, { uniqueKeys: true, merge: true })
  if (doc.errors.length) return source
  visit(doc, { Node(_, node) { if (isCollection(node)) node.flow = false }, Pair(_, pair) { if (pair.key) pair.key.type = 'PLAIN' } })
  return doc.toString({ indent: 2, lineWidth: 0 })
}

export function serviceChanges(before, after) {
  const a = parseCompose(before), b = parseCompose(after)
  if (!b.schema_valid || before.trim() && !a.schema_valid) return []
  const stable = value => JSON.stringify(sortObject(value))
  const resources = tree => Object.fromEntries(Object.entries(tree || {}).filter(([key]) => key !== 'services' && !key.startsWith('x-')))
  const sharedChanged = stable(resources(a.tree)) !== stable(resources(b.tree))
  return [...new Set([...Object.keys(a.tree?.services || {}), ...Object.keys(b.tree?.services || {})])].sort().map(name => ({ name,
    kind: !b.tree?.services?.[name] ? 'retained' : !a.tree?.services?.[name] ? 'added' : stable(a.tree.services[name]) !== stable(b.tree.services[name]) || sharedChanged ? 'updated' : 'unchanged',
  }))
}
function sortObject(value) {
  if (Array.isArray(value)) return value.map(sortObject)
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map(k => [k, sortObject(value[k])]))
  return value
}

// Only presentation data is masked. Originals remain in the permission-protected
// editor; masking must never be fed back into saved or executable configuration.
export function diffSource(source) {
  const doc = parseDocument(readableCompose(source), { uniqueKeys: true, merge: true })
  if (doc.errors.length) return '[Invalid YAML: inspect the source editor]'
  const secret = /password|passwd|secret|token|credential|private.?key|api.?key/i
  visit(doc, {
    Node(_, node) { node.comment = null; node.commentBefore = null },
    Pair(_, pair) {
      if (['environment', 'command', 'entrypoint'].includes(String(pair.key?.value || '')) || String(pair.key?.value || '').startsWith('x-') || secret.test(String(pair.key?.value || ''))) pair.value = doc.createNode('[REDACTED]')
    },
    Scalar(_, node) {
      if (typeof node.value !== 'string') return
      node.value = node.value.replace(/(^|\n)([^=\n]*(?:PASSWORD|PASSWD|SECRET|TOKEN|CREDENTIAL|PRIVATE_KEY|API_KEY)[^=\n]*=)[^\n]*/gi, '$1$2[REDACTED]').replace(/(\w+:\/\/[^\s/:]+:)[^@\s]+@/g, '$1[REDACTED]@')
    },
  })
  return doc.toString({ lineWidth: 0 })
}

// Myers diff avoids the old quadratic matrix and the 600-line whole-file fallback.
export function lineDiff(before, after) {
  const original = before.split('\n'), candidate = after.split('\n')
  let prefix = 0, suffix = 0
  while (prefix < original.length && prefix < candidate.length && original[prefix] === candidate[prefix]) prefix++
  while (suffix < original.length - prefix && suffix < candidate.length - prefix && original[original.length - 1 - suffix] === candidate[candidate.length - 1 - suffix]) suffix++
  const a = original.slice(prefix, original.length - suffix), b = candidate.slice(prefix, candidate.length - suffix)
  const common = new Set(a)
  const middle = b.some(line => common.has(line)) ? myersRows(a, b) : [
    ...a.map((text, i) => ({ type: 'remove', text, old: i + 1, next: null })),
    ...b.map((text, i) => ({ type: 'add', text, old: null, next: i + 1 })),
  ]
  return [
    ...original.slice(0, prefix).map((text, i) => ({ type: 'same', text, old: i + 1, next: i + 1 })),
    ...middle.map(row => ({ ...row, old: row.old === null ? null : row.old + prefix, next: row.next === null ? null : row.next + prefix })),
    ...original.slice(original.length - suffix).map((text, i) => ({ type: 'same', text, old: original.length - suffix + i + 1, next: candidate.length - suffix + i + 1 })),
  ]
}
function myersRows(a, b) {
  const trace = []
  let frontier = new Map([[1, 0]])
  for (let d = 0; d <= a.length + b.length; d++) {
    // Bound trace memory for a large rewrite. Retain real matching lines with
    // an ordered index instead of replacing the entire file or freezing the UI.
    if (d > 256) return indexedRows(a, b)
    trace.push(new Map(frontier))
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || k !== d && (frontier.get(k - 1) ?? -1) < (frontier.get(k + 1) ?? -1) ? frontier.get(k + 1) || 0 : (frontier.get(k - 1) || 0) + 1
      let y = x - k
      while (x < a.length && y < b.length && a[x] === b[y]) { x++; y++ }
      frontier.set(k, x)
      if (x >= a.length && y >= b.length) return backtrack(trace, a, b)
    }
  }
  return []
}
function indexedRows(a, b) {
  const positions = new Map(), cursors = new Map(), rows = []
  b.forEach((text, index) => { if (!positions.has(text)) positions.set(text, []); positions.get(text).push(index) })
  let next = 0
  a.forEach((text, old) => {
    const matches = positions.get(text) || []
    let cursor = cursors.get(text) || 0
    while (cursor < matches.length && matches[cursor] < next) cursor++
    cursors.set(text, cursor)
    if (cursor === matches.length) { rows.push({ type: 'remove', text, old: old + 1, next: null }); return }
    const match = matches[cursor]
    while (next < match) { rows.push({ type: 'add', text: b[next], old: null, next: next + 1 }); next++ }
    rows.push({ type: 'same', text, old: old + 1, next: ++next })
  })
  while (next < b.length) { rows.push({ type: 'add', text: b[next], old: null, next: next + 1 }); next++ }
  return rows
}
function backtrack(trace, a, b) {
  let x = a.length, y = b.length
  const rows = []
  for (let d = trace.length - 1; d >= 0; d--) {
    const v = trace[d], k = x - y
    const previousK = k === -d || k !== d && (v.get(k - 1) ?? -1) < (v.get(k + 1) ?? -1) ? k + 1 : k - 1
    const px = v.get(previousK) || 0, py = px - previousK
    while (x > px && y > py) { rows.push({ type: 'same', text: a[x - 1], old: x--, next: y-- }) }
    if (!d) break
    if (x === px) rows.push({ type: 'add', text: b[y - 1], old: null, next: y-- })
    else rows.push({ type: 'remove', text: a[x - 1], old: x--, next: null })
  }
  return rows.reverse()
}
