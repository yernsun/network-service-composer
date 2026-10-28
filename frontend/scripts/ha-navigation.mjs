import assert from 'node:assert/strict'

// Extend the archived route contract for the approved HA view while preserving
// every other navigation, scope and unsaved-edit comparison.
export function adaptHANavigation(source) {
  const edits = [
    ["} else if (ui.page === 'runtimes') path", "} else if (ui.page === 'ha') path = '/infrastructure/ha';else if (ui.page === 'runtimes') path"],
    ["if (a[0] === 'infrastructure') {", "if (a[0] === 'infrastructure') { if (a[1] === 'ha' && a.length === 2) { ui.page = 'ha'; return; }"],
  ]
  for (const [before, after] of edits) {
    assert.ok(source.includes(before), `HA navigation anchor changed: ${before}`)
    source = source.replace(before, after)
  }
  return source
}
