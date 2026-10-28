import assert from 'node:assert/strict'

// The activity route must be recognized before the generic operation ID route.
// Keep the archive's other editor, scope and unsaved-input comparisons intact.
export function adaptActivityNavigation(source) {
  const edits = [
    ["} else if (ui.page === 'ha') path", "} else if (ui.page === 'activity') { path = '/operations/activity'; q = Y.activityQuery || {}; } else if (ui.page === 'ha') path"],
    ["if (a[0] === 'operations' && a.length === 2) {", "if (a[0] === 'operations' && a[1] === 'activity' && a.length === 2) { ui.page = 'activity'; Y.activityQuery = Object.fromEntries(['resource_type', 'resource_id', 'project_id', 'environment_id', 'state'].map(key => [key, q.get(key) || ''])); return; }\n    if (a[0] === 'operations' && a.length === 2) {"],
  ]
  for (const [before, after] of edits) {
    assert.ok(source.includes(before), `Activity navigation anchor changed: ${before}`)
    source = source.replace(before, after)
  }
  return source
}
