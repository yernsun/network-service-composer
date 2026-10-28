import { LineCounter, parseDocument, isAlias, isMap, isSeq, visit } from 'yaml'
import validateSchema from './schema.generated'

const pointer = path => '/' + path.map(p => String(p).replaceAll('~', '~0').replaceAll('/', '~1')).join('/')
export function diagnostic(code, severity, path = [], extra = {}) {
  return { id: `${code}:${pointer(path)}`, code, severity, path, pointer: pointer(path), service: path[0] === 'services' ? path[1] : '', actions: ['LOCATE'], ...extra }
}

export function parseCompose(source) {
  const lines = new LineCounter()
  const doc = parseDocument(source, { lineCounter: lines, uniqueKeys: true, merge: true, keepSourceTokens: true })
  const diagnostics = doc.errors.map(e => diagnostic('YAML_SYNTAX_ERROR', 'error', [], { ...lines.linePos(e.pos[0]), offset: e.pos[0], end: e.pos[1], reason: e.code }))
  if (diagnostics.length) return { doc, tree: null, diagnostics, syntax_valid: false, schema_valid: false }
  let tree
  try { tree = doc.toJS({ maxAliasCount: 100 }) } catch { return { doc, tree: null, diagnostics: [diagnostic('YAML_SYNTAX_ERROR', 'error')], syntax_valid: false, schema_valid: false } }
  if (!validateSchema(tree)) {
    for (const error of validateSchema.errors || []) {
      const path = error.instancePath.split('/').slice(1).map(p => p.replaceAll('~1', '/').replaceAll('~0', '~'))
      if (error.keyword === 'additionalProperties') path.push(error.params.additionalProperty)
      if (error.keyword === 'required') path.push(error.params.missingProperty)
      let node = doc.getIn(path, true)
      if (!node) node = doc.getIn(path.slice(0, -1), true)
      const offset = node?.range?.[0] || 0
      diagnostics.push(diagnostic('COMPOSE_SCHEMA_ERROR', 'error', path, { ...lines.linePos(offset), offset, end: node?.range?.[1] || offset, reason: error.keyword }))
    }
  }
  return { doc, tree, diagnostics, syntax_valid: true, schema_valid: diagnostics.length === 0 }
}

export function analyzeVolumes(tree, name) {
  return (tree.services[name].volumes || []).map((value, index) => {
    let type = 'volume', source = '', target
    if (typeof value === 'string') {
      const windows = /^[A-Za-z]:[\\/]/.test(value)
      const parts = value.split(':')
      if (windows) { source = parts.shift() + ':' + parts.shift(); target = parts.shift() || ''; type = 'bind' }
      else if (parts.length === 1) target = parts[0]
      else { [source, target] = parts; if (/^(\/|\.|~)/.test(source)) type = 'bind' }
    } else { ({ type, source = '', target } = value) }
    const definition = tree.volumes?.[source]
    const capability = type === 'tmpfs' ? 'ephemeral' : type === 'volume' && (definition?.external || definition?.driver && definition.driver !== 'local') ? 'unknown' : 'local'
    return { type, source, target, capability, declared: false, index, key: type === 'volume' && source ? `volume:${source}` : `mount:${name}:${index}` }
  })
}

// Local analysis is provisional. The server resolves environment variables and
// storage declarations, then checks fresh scheduling evidence before admission.
export function validateCompose(source, { swarm = false } = {}) {
  const result = parseCompose(source)
  result.services = []
  if (!result.schema_valid) return result
  for (const [name, value] of Object.entries(result.tree.services || {})) {
    const constraints = value.deploy?.placement?.constraints || []
    const mounts = analyzeVolumes(result.tree, name)
    const replicas = value.deploy?.replicas ?? 1
    const service = { name, mounts, constraints, replicas, matched_nodes: [] }
    result.services.push(service)
    if (!String(replicas).includes('$') && (!/^\d+$/.test(String(replicas)) || !Number.isSafeInteger(Number(replicas)))) result.diagnostics.push(diagnostic('COMPOSE_REPLICAS_INVALID', 'error', ['services', name, 'deploy', 'replicas']))
    if (swarm && mounts.some(m => m.capability === 'local') && !constraints.length) result.diagnostics.push(diagnostic(mounts.some(m => m.type === 'bind') ? 'SWARM_BIND_MOUNT_WITHOUT_PLACEMENT' : 'SWARM_LOCAL_VOLUME_WITHOUT_PLACEMENT', 'required', ['services', name, 'deploy', 'placement', 'constraints']))
  }
  return result
}

function resolved(node, doc) { return isAlias(node) ? node.resolve(doc) : node }
function inherited(map, key, doc, seen = new Set()) {
  map = resolved(map, doc)
  if (!isMap(map) || seen.has(map)) return undefined
  seen.add(map)
  if (map.has(key)) return map.get(key, true)
  const merge = map.items.find(p => (p.key?.source === '<<' || String(p.key?.value ?? p.key) === '<<'))?.value
  for (const parent of isSeq(merge) ? merge.items : merge ? [merge] : []) {
    const found = inherited(parent, key, doc, seen)
    if (found) return found
  }
}
function localMap(parent, key, doc) {
  const own = parent.get(key, true)
  if (isMap(own)) {
    let anchors = false
    visit(own, (_, n) => { if (n?.anchor) anchors = true })
    if (!anchors) return own
    // Keep anchor definitions at their original position in a merge base.
    // Local overrides can then change this service without mutating consumers.
    const local = doc.createNode({ '<<': null })
    local.items[0].key.type = 'PLAIN'
    local.items[0].value = own
    parent.set(key, local)
    return local
  }
  const node = resolved(inherited(parent, key, doc), doc)
  const copy = isMap(node) ? node.clone() : doc.createNode({})
  const anchors = new Map(), used = new Set()
  visit(doc, (_, n) => { if (n?.anchor) used.add(n.anchor) })
  visit(copy, (_, n) => {
    if (!n?.anchor) return
    const old = n.anchor
    let name = `${old}_local`, suffix = 1
    while (used.has(name)) name = `${old}_local${suffix++}`
    anchors.set(old, name); used.add(name); n.anchor = name
  })
  visit(copy, (_, n) => { if (isAlias(n) && anchors.has(n.source)) n.source = anchors.get(n.source) })
  copy.anchor = undefined
  parent.set(key, copy)
  return copy
}
export function constraintParts(raw) {
  const m = raw.match(/^\s*([A-Za-z_][\w.-]*)\s*(==|!=)\s*([^=<>!\r\n]+?)\s*$/)
  return m ? { key: m[1], op: m[2], value: m[3].trim() } : null
}
export function editPlacement(source, service, addition, { replace = false } = {}) {
  const parsed = parseCompose(source)
  if (!parsed.schema_valid) return { source, error: 'COMPOSE_SCHEMA_ERROR', conflicts: [] }
  const candidate = constraintParts(addition)
  if (!candidate) return { source, error: 'PLACEMENT_CONSTRAINT_INVALID', conflicts: [] }
  const original = parsed.tree.services?.[service]?.deploy?.placement?.constraints || []
  const equal = raw => { const p = constraintParts(raw); return p && p.key === candidate.key && p.op === candidate.op && p.value.toLowerCase() === candidate.value.toLowerCase() }
  const conflicts = original.filter(raw => { const p = constraintParts(raw); return p && p.key === candidate.key && (p.op === '==' && candidate.op === '==' && p.value.toLowerCase() !== candidate.value.toLowerCase() || p.op !== candidate.op && p.value.toLowerCase() === candidate.value.toLowerCase()) })
  if (conflicts.length && !replace) return { source, conflicts, error: 'PLACEMENT_CONSTRAINT_CONFLICT' }
  if (!conflicts.length && original.some(equal)) return { source, conflicts: [] }
  const doc = parsed.doc
  const services = localMap(doc.contents, 'services', doc)
  if (!parsed.tree.services?.[service]) return { source, conflicts: [], error: 'COMPOSE_SERVICES_REQUIRED' }
  const selected = localMap(services, service, doc)
  const deploy = localMap(selected, 'deploy', doc)
  const placement = localMap(deploy, 'placement', doc)
  const effective = resolved(inherited(placement, 'constraints', doc), doc)
  const seq = isSeq(effective) ? effective.clone() : doc.createNode([])
  seq.anchor = undefined
  seq.items = seq.items.filter(item => !conflicts.includes(String(item.value ?? item)))
  if (!original.some(equal)) seq.add(addition.trim())
  placement.set('constraints', seq)
  return { source: doc.toString(), conflicts: [] }
}

// Copy only the selected service's effective sequence. Shared anchors remain
// definitions for other consumers, while this service gets a local override.
export function editServicePort(source, service, index, value) {
  const parsed = parseCompose(source)
  if (!parsed.syntax_valid || !parsed.tree?.services?.[service]) return { source, error: 'COMPOSE_SCHEMA_ERROR' }
  const doc = parsed.doc
  const selected = localMap(localMap(doc.contents, 'services', doc), service, doc)
  const effective = resolved(inherited(selected, 'ports', doc), doc)
  const seq = isSeq(effective) ? effective.clone() : doc.createNode([])
  seq.anchor = undefined
  if (index < 0 || index > seq.items.length) return { source, error: 'COMPOSE_PORT_INVALID' }
  if (value === null) seq.items.splice(index, 1)
  else {
    const previous = resolved(seq.items[index], doc)
    const next = isMap(previous) ? previous.clone() : doc.createNode(value)
    if (isMap(previous)) {
      const before = parsed.tree.services[service].ports[index]
      for (const key of Object.keys(before)) if (!(key in value)) next.delete(key)
      for (const [key, entry] of Object.entries(value)) if (JSON.stringify(before[key]) !== JSON.stringify(entry)) next.set(key, entry)
    }
    if (previous) { next.comment = previous.comment; next.commentBefore = previous.commentBefore }
    seq.items[index] = next
  }
  // Local copies must not redefine anchors still used by other services.
  const anchors = new Map(), used = new Set()
  visit(doc, (_, node) => { if (typeof node?.anchor === 'string') used.add(node.anchor) })
  visit(seq, (_, node) => {
    if (typeof node?.anchor !== 'string') return
    const original = node.anchor
    let name = `${original}_port`, n = 1
    while (used.has(name)) name = `${original}_port${n++}`
    anchors.set(original, name); used.add(name); node.anchor = name
  })
  visit(seq, (_, node) => { if (isAlias(node) && anchors.has(node.source)) node.source = anchors.get(node.source) })
  selected.set('ports', seq)
  return { source: doc.toString() }
}
