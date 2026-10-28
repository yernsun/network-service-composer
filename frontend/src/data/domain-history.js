const chronological = (a, b) => (a.created_at || '').localeCompare(b.created_at || '') || a.id.localeCompare(b.id)
const sameSavedValue = (a, b) => JSON.stringify(a) === JSON.stringify(b)

const routeFields = ['id', 'domain_id', 'path', 'match', 'kind', 'service_id', 'external_key', 'external_url', 'response_code', 'response_value',
  'priority', 'enabled', 'strip_path', 'websocket_enabled', 'read_timeout_seconds', 'connect_timeout_seconds',
  'target_port', 'upstream_protocol', 'upstream_server_name']

function routeRecord(write) {
  const record = { delete: !!write.delete, expected_revision: write.expected_revision || 0 }
  for (const field of routeFields) if (write.route?.[field] !== undefined) record[field] = write.route[field]
  return record
}

function routeText(write) { return write ? JSON.stringify(routeRecord(write), null, 2) : '' }
function snippetKey(snippet) { return `${snippet.scope}\u0000${snippet.resource_id || ''}` }

function routeOwners(versions, domainId, currentRouteIds) {
  const owners = new Map()
  const add = (id, owner) => {
    if (!id || !owner) return
    if (!owners.has(id)) owners.set(id, new Set())
    owners.get(id).add(owner)
  }
  for (const id of currentRouteIds) add(id, domainId)
  for (const version of versions) for (const change of version.routes || []) add(change.route?.id, change.route?.domain_id)
  return owners
}

function snippetScope(snippet, domainId, owners) {
  if (snippet.scope === 'http') return 'global'
  if (snippet.scope === 'server') return snippet.resource_id === domainId ? 'domain' : 'pool'
  if (snippet.scope === 'location') {
    const known = owners.get(snippet.resource_id)
    if (!known || known.size !== 1) return 'unknown'
    return known.has(domainId) ? 'domain' : 'pool'
  }
  return 'unknown'
}

function routeScope(write, domainId, owners) {
  if (write?.route?.domain_id) return write.route.domain_id === domainId ? 'domain' : 'pool'
  const known = owners.get(write?.route?.id)
  if (!known || known.size !== 1) return 'unknown'
  return known.has(domainId) ? 'domain' : 'pool'
}

function snippetChanges(version, baseline, domainId, owners) {
  const before = new Map((baseline?.snippets || []).map(snippet => [snippetKey(snippet), snippet]))
  const after = new Map((version.snippets || []).map(snippet => [snippetKey(snippet), snippet]))
  const keys = [...new Set([...before.keys(), ...after.keys()])].sort()
  return keys.flatMap(key => {
    const old = before.get(key), current = after.get(key)
    if (baseline && old?.content === current?.content) return []
    if (!baseline && !current) return []
    const snippet = current || old
    return [{ type: 'snippet', key, scope: snippetScope(snippet, domainId, owners),
      snippetScope: snippet.scope, resourceId: snippet.resource_id || '',
      action: !baseline ? 'snapshot' : !old ? 'added' : !current ? 'removed' : 'updated',
      before: old?.content ?? null, after: current?.content ?? null }]
  })
}

function accessChanges(version, baseline, domainId, owners) {
  const before = new Map((baseline?.access_controls || []).map(entry => [snippetKey(entry), entry]))
  const after = new Map((version.access_controls || []).map(entry => [snippetKey(entry), entry]))
  return [...new Set([...before.keys(), ...after.keys()])].sort().flatMap(key => {
    const old = before.get(key), current = after.get(key)
    if (baseline && sameSavedValue(old, current)) return []
    const entry = current || old
    return [{ type: 'access', key, scope: snippetScope(entry, domainId, owners), resourceId: entry.resource_id,
      action: !baseline ? 'snapshot' : !old ? 'added' : !current ? 'removed' : 'updated',
      before: old ? JSON.stringify(old, null, 2) : null, after: current ? JSON.stringify(current, null, 2) : null }]
  })
}

function routeChanges(version, baseline, type, domainId, owners) {
  if (type === 'published') return (version.routes || []).map(write => ({
    type: 'route', key: write.route?.id || '', scope: routeScope(write, domainId, owners),
    path: write.route?.path || '', action: write.delete ? 'publishedDelete' : write.expected_revision ? 'publishedUpdate' : 'publishedCreate',
    before: null, after: routeText(write), beforeUnavailable: !!(write.delete || write.expected_revision),
  }))
  const before = new Map((baseline?.routes || []).map(write => [write.route?.id, write]))
  const after = new Map((version.routes || []).map(write => [write.route?.id, write]))
  const keys = [...new Set([...before.keys(), ...after.keys()])].filter(Boolean).sort()
  return keys.flatMap(key => {
    const old = before.get(key), current = after.get(key)
    if (baseline && sameSavedValue(old && routeRecord(old), current && routeRecord(current))) return []
    const write = current || old
    return [{ type: 'route', key, scope: routeScope(write, domainId, owners), path: write.route?.path || '',
      action: !baseline ? 'snapshot' : !old ? 'pendingAdded' : !current ? 'pendingRemoved' : 'pendingUpdated',
      before: old ? routeText(old) : null, after: current ? routeText(current) : null,
      beforeUnavailable: !!current && !old && !!(current.delete || current.expected_revision),
    }]
  })
}

function draftBaseline(version, drafts, published, publishedComplete) {
  if (!publishedComplete) return null
  const earlier = drafts.filter(item => chronological(item, version) < 0)
  const previousDraft = earlier.at(-1)
  const previousRelease = published.filter(item => (item.created_at || '') < (version.created_at || '')).at(-1)
  if (published.some(item => item.created_at === version.created_at)) return null
  if (previousDraft && previousRelease && previousDraft.created_at === previousRelease.created_at) return null
  // The first retained draft may have had a predecessor removed by the 100-row limit.
  if (!previousDraft && drafts.length >= 100) return null
  if (previousRelease && (!previousDraft || chronological(previousDraft, previousRelease) < 0)) {
    return { version: { ...previousRelease, routes: [] }, id: previousRelease.id, type: 'published' }
  }
  if (previousDraft) {
    // Older publications may have been pruned independently of draft history.
    if (published.length && chronological(previousDraft, published[0]) < 0) return null
    return { version: previousDraft, id: previousDraft.id, type: 'draft' }
  }
  return previousRelease ? { version: { ...previousRelease, routes: [] }, id: previousRelease.id, type: 'published' } : null
}

function entryScope(changes, type, baseline) {
  const evidenced = baseline ? changes : type === 'published' ? changes.filter(change => change.type === 'route') : []
  if (evidenced.some(change => change.scope === 'domain')) return 'domain'
  if (evidenced.some(change => change.scope === 'global')) return 'global'
  if (!baseline || evidenced.some(change => change.scope === 'unknown')) return 'unknown'
  return evidenced.length ? 'pool' : 'none'
}

export function domainHistoryEntries(published, drafts, domainId, currentRouteIds = [], options = {}) {
  const publishedComplete = options.publishedComplete !== false
  const releases = [...published].sort(chronological), savedDrafts = [...drafts].sort(chronological)
  const owners = routeOwners([...releases, ...savedDrafts], domainId, currentRouteIds)
  const releaseById = new Map(releases.map(version => [version.id, version]))
  return [...releases.map(version => ({ version, type: 'published' })), ...savedDrafts.map(version => ({ version, type: 'draft' }))]
    .map(({ version, type }) => {
      const baseline = type === 'published'
        ? version.parent_version && releaseById.has(version.parent_version)
          ? { version: releaseById.get(version.parent_version), id: version.parent_version, type: 'published' } : null
        : draftBaseline(version, savedDrafts, releases, publishedComplete)
      const changes = [...snippetChanges(version, baseline?.version, domainId, owners),
        ...accessChanges(version, baseline?.version, domainId, owners),
        ...routeChanges(version, baseline?.version, type, domainId, owners)]
      const routePaths = [...new Set(changes.filter(change => change.type === 'route' && change.scope === 'domain' && (baseline || type === 'published'))
        .map(change => change.path).filter(Boolean))]
      return { id: version.id, type, time: version.created_at, actor: version.actor || '', description: version.description || '',
        baselineId: baseline?.id || '', baselineType: baseline?.type || '', baselineAvailable: !!baseline,
        scope: entryScope(changes, type, baseline), routePaths,
        snippetChanged: !!baseline && changes.some(change => change.type === 'snippet' && change.scope === 'domain'), changes }
    }).sort((a, b) => (b.time || '').localeCompare(a.time || '') || b.id.localeCompare(a.id))
}

export function domainHistoryDetail(entry, scope = 'domain') {
  const changes = entry.changes.filter(change => scope === 'pool' || change.scope === 'domain' || change.scope === 'global')
  return changes.map(change => {
    const canDiff = entry.baselineAvailable && (change.type === 'snippet' || change.type === 'access' || change.type === 'route' && change.action !== 'publishedUpdate' && change.action !== 'publishedDelete')
    const lines = canDiff ? diffLines(change.before || '', change.after || '') : null
    return { ...change, lines, tooLarge: canDiff && lines === null }
  })
}

export function diffLines(before, after) {
  const oldLines = before ? before.split('\n') : [], newLines = after ? after.split('\n') : []
  if (oldLines.length > 600 || newLines.length > 600) return null
  const rows = Array.from({ length: oldLines.length + 1 }, () => new Uint16Array(newLines.length + 1))
  for (let i = oldLines.length - 1; i >= 0; i--) for (let j = newLines.length - 1; j >= 0; j--) {
    rows[i][j] = oldLines[i] === newLines[j] ? rows[i + 1][j + 1] + 1 : Math.max(rows[i + 1][j], rows[i][j + 1])
  }
  const result = []
  let i = 0, j = 0
  while (i < oldLines.length || j < newLines.length) {
    if (i < oldLines.length && j < newLines.length && oldLines[i] === newLines[j]) {
      result.push({ type: 'same', text: oldLines[i], beforeLine: ++i, afterLine: ++j })
    } else if (j < newLines.length && (i === oldLines.length || rows[i][j + 1] > rows[i + 1][j])) {
      result.push({ type: 'add', text: newLines[j], beforeLine: null, afterLine: ++j })
    } else {
      result.push({ type: 'remove', text: oldLines[i], beforeLine: ++i, afterLine: null })
    }
  }
  return result
}
